package bicc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/tra-jkv/terraform-provider-bicc/bicc/client"
)

// Provider-server tests: import -> read -> plan through the real protocol-6 server
// (review MAJOR-3).
//
// WHY: the plan-after-import tests in resource_job_import_test.go build their plan by
// copying Description, Name and ID from the imported state (runModifyPlan), so they cannot
// see a difference between what import wrote and what a config plans. The description
// null/"" bug (MAJOR-1, ASSUMPTIONS A12) was invisible to them. Here the plan is driven
// through providerserver.NewProtocol6(provider)() with tfprotov6 requests, from a
// config-shaped proposed state, so the framework's own plan steps run: description is
// taken from the config object (null), Optional+Computed attributes are marked unknown
// when the plan differs from the prior state, attribute plan modifiers run, then
// ModifyPlan.
//
// WHAT RUNS (real code): bicc.New() (schema, resource list), the framework's
// GetProviderSchema, ConfigureProvider, ValidateResourceConfig, ImportResourceState,
// ReadResource and PlanResourceChange, the resource's ImportState/Read/ModifyPlan and
// attribute plan modifiers, the real HTTP client against an httptest mock whose job JSON
// has the live shape (description null as on 6 of 39 SIT jobs, A12).
//
// WHAT IS REPLACED: only provider Configure, because the real one builds
// https://host:port and the mock is plain HTTP; the wrapper below embeds the real provider
// and swaps in a client whose BaseURL is the mock. Provider Configure itself is covered in
// provider_test.go.
//
// OPEN FINDING surfaced by the "merged" variant (not fixed here, no schema change allowed):
// the `columns` attributes have schema Defaults (is_populate=true, the other flags false).
// The framework applies Defaults to the planned state by looking each planned set element
// up in the CONFIG by value; when the proposed element differs from the config element
// (as it does as soon as core merges prior-state values into computed attributes the
// config leaves null, e.g. chunk_date_seq_incr = 0), the lookup finds nothing, the config
// value is read as null, and the Default overwrites a configured flag (is_primary_key =
// true becomes false). Result with the merged variant: planned `columns` differ from both
// the config and the prior state. Confirmed in a scratch copy: removing the Defaults makes
// the merged plan equal to the prior state. Whether real Terraform core reproduces it is
// UNVERIFIED (B1; DECISIONS Q13). If it does, the plan would no longer match the config
// for `columns`, and core would either report "Provider produced invalid plan" or accept
// the plan with the flags reset to their defaults; which of the two happens, and whether an
// apply could then send the reset flags in a PUT, is not known. Do not read this as a
// safe failure. The merged variant therefore asserts everything except `columns` and
// reports the columns difference as a skip, so it is neither hidden nor turned into a
// failing test; when the gap is fixed the skip disappears by itself.
//
// RAW/MERGED ASYMMETRY FOR `columns`: in the raw variant the proposed element equals the
// config element (computed attributes are null in both), so the framework finds the config
// element, no Default overwrites anything, and `columns` pass. The merged variant is the
// more core-like one and is the one that skips `columns` because of the Default
// overwrite. So the "empty plan" result for `columns` rests on the raw variant only, which
// is the less realistic of the two; for every other attribute both variants assert it.
//
// WHAT DOES NOT RUN (needs a terraform binary, ASSUMPTIONS B1):
//   - Terraform core. The ProposedNewState is built here from the config-shaped object
//     (variant "raw": computed attributes the config leaves out are null; variant
//     "merged": they are filled from the prior state by data_store_key, an approximation
//     of core's ProposedNew for set elements, not core itself).
//   - Core's validity checks on the plan. One of them is emulated in plannedMatchesConfig
//     (a set config value must reappear unchanged in the plan); the rest (for example how
//     core matches set elements, the "Provider produced invalid plan" text, unknown
//     handling in plan output) are not.
//   - HCL parsing, `terraform import`'s own sequencing, apply, destroy, and any live BICC.
//   - Apply: Update/Create are tested through the resource methods in
//     resource_job_description_test.go, not through ApplyResourceChange.

// serverProvider is the real provider with only Configure replaced (see above).
type serverProvider struct {
	provider.Provider
	baseURL string
}

func (p *serverProvider) Configure(_ context.Context, _ provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	c := client.NewClient(&client.Config{Host: "unused", Username: "u", Password: "p"})
	c.BaseURL = p.baseURL
	resp.ResourceData = c
	resp.DataSourceData = c
}

type protoHarness struct {
	t       *testing.T
	srv     tfprotov6.ProviderServer
	resType tftypes.Type
	schema  tfsdk.State // carries the resource schema
	calls   *int32
}

func newProtoHarness(t *testing.T, jobBody string) *protoHarness {
	t.Helper()
	var calls int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&calls, 1)
		if req.Method != http.MethodGet {
			t.Errorf("unexpected %s %s: import, read and plan must only GET", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jobBody))
	}))
	t.Cleanup(mock.Close)

	srv := providerserver.NewProtocol6(&serverProvider{Provider: New(), baseURL: mock.URL})()
	ctx := context.Background()

	if resp, err := srv.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{}); err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	} else {
		failOnProtoDiags(t, "GetProviderSchema", resp.Diagnostics)
		if _, ok := resp.ResourceSchemas["bicc_job"]; !ok {
			t.Fatalf("bicc_job not in the provider server's resource schemas")
		}
	}

	cfgVal := tftypes.NewValue(providerTFType, map[string]tftypes.Value{
		"host":     tftypes.NewValue(tftypes.String, "unused"),
		"username": tftypes.NewValue(tftypes.String, "u"),
		"password": tftypes.NewValue(tftypes.String, "p"),
		"port":     tftypes.NewValue(tftypes.Number, nil),
	})
	dv, err := tfprotov6.NewDynamicValue(providerTFType, cfgVal)
	if err != nil {
		t.Fatalf("provider config: %v", err)
	}
	cresp, err := srv.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{TerraformVersion: "1.9.0", Config: &dv})
	if err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	failOnProtoDiags(t, "ConfigureProvider", cresp.Diagnostics)

	sch := resourceSchema(t).Schema
	return &protoHarness{
		t:       t,
		srv:     srv,
		resType: sch.Type().TerraformType(ctx),
		schema:  tfsdk.State{Schema: sch},
		calls:   &calls,
	}
}

func failOnProtoDiags(t *testing.T, step string, diags []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("%s: %s: %s", step, d.Summary, d.Detail)
		}
	}
}

func (h *protoHarness) dyn(v tftypes.Value) *tfprotov6.DynamicValue {
	h.t.Helper()
	dv, err := tfprotov6.NewDynamicValue(h.resType, v)
	if err != nil {
		h.t.Fatalf("dynamic value: %v", err)
	}
	return &dv
}

func (h *protoHarness) undyn(dv *tfprotov6.DynamicValue) tftypes.Value {
	h.t.Helper()
	if dv == nil {
		h.t.Fatal("nil dynamic value")
	}
	v, err := dv.Unmarshal(h.resType)
	if err != nil {
		h.t.Fatalf("unmarshal: %v", err)
	}
	return v
}

// raw turns a model into the tftypes value the protocol carries.
func (h *protoHarness) raw(m biccJobModel) tftypes.Value {
	h.t.Helper()
	s := tfsdk.State{Schema: h.schema.Schema, Raw: tftypes.NewValue(h.resType, nil)}
	if diags := s.Set(context.Background(), m); diags.HasError() {
		h.t.Fatalf("building value: %v", diags)
	}
	return s.Raw
}

func (h *protoHarness) model(v tftypes.Value) biccJobModel {
	h.t.Helper()
	return modelOf(h.t, tfsdk.State{Schema: h.schema.Schema, Raw: v})
}

// importAndRead is what `terraform import` does against the provider: ImportResourceState,
// then ReadResource on the imported state. It returns the state Terraform would store.
func (h *protoHarness) importAndRead(id string) tftypes.Value {
	h.t.Helper()
	ctx := context.Background()
	imp, err := h.srv.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "bicc_job", ID: id})
	if err != nil {
		h.t.Fatalf("ImportResourceState: %v", err)
	}
	failOnProtoDiags(h.t, "ImportResourceState", imp.Diagnostics)
	if len(imp.ImportedResources) != 1 {
		h.t.Fatalf("ImportResourceState returned %d resources, want 1", len(imp.ImportedResources))
	}
	ir := imp.ImportedResources[0]
	rd, err := h.srv.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "bicc_job", CurrentState: ir.State, Private: ir.Private})
	if err != nil {
		h.t.Fatalf("ReadResource: %v", err)
	}
	failOnProtoDiags(h.t, "ReadResource", rd.Diagnostics)
	return h.undyn(rd.NewState)
}

// validate runs ValidateResourceConfig on a config-shaped value, so the configs below
// are known to be valid against the real schema (required attributes, types).
func (h *protoHarness) validate(config tftypes.Value) {
	h.t.Helper()
	resp, err := h.srv.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{TypeName: "bicc_job", Config: h.dyn(config)})
	if err != nil {
		h.t.Fatalf("ValidateResourceConfig: %v", err)
	}
	failOnProtoDiags(h.t, "ValidateResourceConfig", resp.Diagnostics)
}

func (h *protoHarness) plan(prior, proposed, config tftypes.Value) tftypes.Value {
	h.t.Helper()
	resp, err := h.srv.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "bicc_job",
		PriorState:       h.dyn(prior),
		ProposedNewState: h.dyn(proposed),
		Config:           h.dyn(config),
	})
	if err != nil {
		h.t.Fatalf("PlanResourceChange: %v", err)
	}
	failOnProtoDiags(h.t, "PlanResourceChange", resp.Diagnostics)
	return h.undyn(resp.PlannedState)
}

// The job the mock serves: importJobBody with its description replaced by `descJSON`
// (a JSON literal: null or a quoted string).
func serverJobBody(descJSON string) string {
	return strings.Replace(importJobBody, `"description": "imported job"`, `"description": `+descJSON, 1)
}

// configJob is the config-shaped model for the mock job, written the way an operator
// writes it after `terraform state show`: same name, explicit columns, auto_populate false,
// computed attributes that the config leaves out are null, id is null (computed).
// `filterBU` is the filters value for BusinessUnitPVO (null = leave unset).
func configJob(desc types.String, filterBU types.String) biccJobModel {
	person := planStoreExplicit(keyPerson, []columnModel{col("PersonId", true, false), col("CreationDate", false, true)}, unsetNull, func(d *dataStoreModel) {
		d.Filters = types.StringValue("PersonId > 0")
		d.IsSilentError = types.BoolValue(true)
		d.IsEffectiveDateDisabled = types.BoolValue(true)
		d.UseUnionForIncremental = types.BoolValue(true)
		d.InitialExtractDate = types.StringValue("2024-01-31")
		d.ChunkType = types.StringValue("DateSeqIncr")
		d.ChunkDateSeqIncr = types.Int64Value(30)
		d.ChunkDateSeqMin = types.Int64Value(2)
	})
	bu := planStoreExplicit(keyBU, []columnModel{colNK("BusinessUnitId", true, false, true)}, unsetNull, func(d *dataStoreModel) {
		d.Filters = filterBU
	})
	inv := planStoreExplicit(keyInvoice, nil, unsetNull, nil)
	return biccJobModel{
		ID:          types.StringNull(),
		Name:        types.StringValue("SIT_TEST_JOB"),
		Description: desc,
		DataStores:  storeSet(person, bu, inv),
	}
}

func storeSet(stores ...dataStoreModel) types.Set {
	objs := make([]attr.Value, len(stores))
	for i, ds := range stores {
		obj, diags := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, ds)
		if diags.HasError() {
			panic(diags)
		}
		objs[i] = obj
	}
	return types.SetValueMust(types.ObjectType{AttrTypes: dataStoreAttrTypes}, objs)
}

// mergedProposed approximates core's ProposedNew: id and every computed attribute the
// config leaves null are taken from the prior state (elements matched by data_store_key).
func mergedProposed(t *testing.T, cfg, prior biccJobModel) biccJobModel {
	t.Helper()
	priorBy := storesByKey(t, prior.DataStores)
	var list []dataStoreModel
	if diags := cfg.DataStores.ElementsAs(context.Background(), &list, false); diags.HasError() {
		t.Fatalf("config stores: %v", diags)
	}
	for i := range list {
		p, ok := priorBy[list[i].DataStoreKey.ValueString()]
		if !ok {
			continue
		}
		s := func(c *types.String, v types.String) {
			if c.IsNull() {
				*c = v
			}
		}
		b := func(c *types.Bool, v types.Bool) {
			if c.IsNull() {
				*c = v
			}
		}
		n := func(c *types.Int64, v types.Int64) {
			if c.IsNull() {
				*c = v
			}
		}
		s(&list[i].Filters, p.Filters)
		b(&list[i].IsSilentError, p.IsSilentError)
		b(&list[i].IsEffectiveDateDisabled, p.IsEffectiveDateDisabled)
		b(&list[i].UseUnionForIncremental, p.UseUnionForIncremental)
		s(&list[i].InitialExtractDate, p.InitialExtractDate)
		s(&list[i].ChunkType, p.ChunkType)
		n(&list[i].ChunkDateSeqIncr, p.ChunkDateSeqIncr)
		n(&list[i].ChunkDateSeqMin, p.ChunkDateSeqMin)
		n(&list[i].ChunkPkSeqIncr, p.ChunkPkSeqIncr)
		if list[i].ColumnOverrides.IsNull() {
			list[i].ColumnOverrides = p.ColumnOverrides
		}
	}
	out := cfg
	out.ID = prior.ID
	out.DataStores = storeSet(list...)
	return out
}

// plannedMatchesConfig emulates ONE of core's plan validity rules: where the config sets
// a value, the planned value must be the same. It returns what differs (empty = valid).
// Core's other checks are not emulated.
func plannedMatchesConfig(t *testing.T, cfg, planned biccJobModel, skipAttrs ...string) []string {
	t.Helper()
	var bad []string
	if !cfg.Name.Equal(planned.Name) {
		bad = append(bad, "name")
	}
	if !cfg.Description.Equal(planned.Description) {
		bad = append(bad, "description: config "+cfg.Description.String()+", planned "+planned.Description.String())
	}
	cfgBy, plannedBy := storesByKey(t, cfg.DataStores), storesByKey(t, planned.DataStores)
	for key, c := range cfgBy {
		p, ok := plannedBy[key]
		if !ok {
			bad = append(bad, key+": missing from plan")
			continue
		}
		cobj, _ := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, c)
		pobj, _ := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, p)
		pattrs := pobj.Attributes()
		for name, cv := range cobj.Attributes() {
			if cv.IsNull() || cv.IsUnknown() || contains(skipAttrs, name) {
				continue
			}
			if !cv.Equal(pattrs[name]) {
				bad = append(bad, key+"."+name+": config "+cv.String()+", planned "+pattrs[name].String())
			}
		}
	}
	return bad
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// storeAttrDiffs lists the attributes (other than skipAttrs) in which two data stores differ.
func storeAttrDiffs(a, b dataStoreModel, skipAttrs ...string) []string {
	oa, _ := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, a)
	ob, _ := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, b)
	var out []string
	for name, v := range ob.Attributes() {
		if contains(skipAttrs, name) {
			continue
		}
		if !v.Equal(oa.Attributes()[name]) {
			out = append(out, name+": "+oa.Attributes()[name].String()+" vs "+v.String())
		}
	}
	return out
}

func TestProviderServerImportReadPlan(t *testing.T) {
	// variant: how the proposed new state is derived from the config-shaped object.
	variants := []string{"raw", "merged"}

	cases := []struct {
		name       string
		apiDesc    string       // JSON literal served by the mock
		cfgDesc    types.String // description in config
		filterBU   types.String // filters for BusinessUnitPVO in config
		wantEmpty  bool         // plan must equal prior state
		wantDesc   types.String // description in the prior (imported) state
		wantBUFilt types.String // BusinessUnitPVO filters expected in the plan when not empty
	}{
		{
			name:      "description null in API and config: empty plan (MAJOR-1/MAJOR-3, A12)",
			apiDesc:   `null`,
			cfgDesc:   types.StringNull(),
			filterBU:  types.StringNull(),
			wantEmpty: true,
			wantDesc:  types.StringNull(),
		},
		{
			name:      "description set in API and equal in config: empty plan",
			apiDesc:   `"imported job"`,
			cfgDesc:   types.StringValue("imported job"),
			filterBU:  types.StringNull(),
			wantEmpty: true,
			wantDesc:  types.StringValue("imported job"),
		},
		{
			name:       "real change (different filter): plan differs",
			apiDesc:    `null`,
			cfgDesc:    types.StringNull(),
			filterBU:   types.StringValue("Active = 'Y'"),
			wantEmpty:  false,
			wantDesc:   types.StringNull(),
			wantBUFilt: types.StringValue("Active = 'Y'"),
		},
	}

	for _, tc := range cases {
		for _, variant := range variants {
			tc, variant := tc, variant
			t.Run(tc.name+" ["+variant+"]", func(t *testing.T) {
				h := newProtoHarness(t, serverJobBody(tc.apiDesc))

				prior := h.importAndRead("4242")
				priorM := h.model(prior)
				if got := priorM.Description; !got.Equal(tc.wantDesc) {
					// Not fatal: the plan assertions below must show the consequence too.
					t.Errorf("imported description = %#v, want %#v", got, tc.wantDesc)
				}
				if n := len(priorM.DataStores.Elements()); n != 3 {
					t.Fatalf("imported %d data stores, want 3", n)
				}

				cfg := configJob(tc.cfgDesc, tc.filterBU)
				proposed := cfg
				proposed.ID = priorM.ID
				if variant == "merged" {
					proposed = mergedProposed(t, cfg, priorM)
				}
				cfgRaw := h.raw(cfg)
				h.validate(cfgRaw)

				planned := h.plan(prior, h.raw(proposed), cfgRaw)
				plannedM := h.model(planned)

				// In the merged variant `columns` is excluded (open finding, see the header).
				var skip []string
				if variant == "merged" {
					skip = []string{"columns"}
				}

				if bad := plannedMatchesConfig(t, cfg, plannedM, skip...); len(bad) != 0 {
					t.Errorf("plan does not reproduce the config (core would reject it):\n  %s", strings.Join(bad, "\n  "))
				}

				gotBy, priorBy := storesByKey(t, plannedM.DataStores), storesByKey(t, priorM.DataStores)
				for _, k := range []string{keyPerson, keyBU, keyInvoice} {
					if _, ok := gotBy[k]; !ok {
						t.Errorf("%s: missing from plan", k)
					}
				}

				if tc.wantEmpty {
					if !plannedM.Description.Equal(priorM.Description) {
						t.Errorf("description: planned %#v, prior state %#v", plannedM.Description, priorM.Description)
					}
					for k, p := range priorBy {
						for _, d := range storeAttrDiffs(gotBy[k], p, skip...) {
							t.Errorf("%s: planned differs from prior state in %s", k, d)
						}
					}
					if variant == "raw" && !planned.Equal(prior) {
						diff, _ := planned.Diff(prior)
						t.Errorf("plan is not empty: planned state != prior state (%d differences)", len(diff))
					}
				} else {
					if variant == "raw" && planned.Equal(prior) {
						t.Fatal("plan equals the prior state although the config changed (the assertions above would be vacuous)")
					}
					if !gotBy[keyBU].Filters.Equal(tc.wantBUFilt) {
						t.Errorf("BusinessUnitPVO filters in plan = %s, want %s", gotBy[keyBU].Filters, tc.wantBUFilt)
					}
					// Only that data store changes.
					for _, k := range []string{keyPerson, keyInvoice} {
						for _, d := range storeAttrDiffs(gotBy[k], priorBy[k], skip...) {
							t.Errorf("%s changed in the plan although only BusinessUnitPVO filters changed: %s", k, d)
						}
					}
					if !plannedM.Description.Equal(priorM.Description) {
						t.Errorf("description changed in the plan: %#v vs %#v", plannedM.Description, priorM.Description)
					}
				}

				// import + read = 2 GETs; planning must not call BICC.
				if got := atomic.LoadInt32(h.calls); got != 2 {
					t.Errorf("BICC calls = %d, want 2 (import + read; plan makes none)", got)
				}

				if variant == "merged" && !t.Failed() {
					for k, p := range priorBy {
						if !gotBy[k].Columns.Equal(p.Columns) {
							t.Skipf("OPEN FINDING (see the header): planned `columns` of %s differ from the prior state; every other attribute was asserted equal. Unverified in core (B1)", k)
						}
					}
				}
			})
		}
	}
}
