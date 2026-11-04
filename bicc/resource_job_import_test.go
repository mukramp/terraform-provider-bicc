package bicc

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Resource-level tests for import (PLAN task 2, KI-2, KI-8). ImportState and Read are
// called the way the plugin framework calls them (import: ImportState, then Read with the
// state ImportState produced), against an httptest mock. No live BICC call is made (DR-8).
//
// The mock job has the shape of the live job GET/list (keys per ASSUMPTIONS A3 and the
// client structs): dataStores[].dataStoreMeta{...}, groupItemPriority, groupNumber,
// lastExtractDate. The values are made up; only the shape is live.

// importJobBody has three data stores:
//   - CrmAnalyticsAM.PartiesAnalyticsAM.Person: two columns, chunked by creation date,
//     with filters, initial extract date, silent error and union flags set
//   - FscmTopModelAM.FinFunAM.BusinessUnitPVO: one column, everything else default
//     (empty filters, no initial date, no chunk type: the live shape for "unset")
//   - FscmTopModelAM.PayablesAM.InvoicePVO: zero columns
const importJobBody = `{
  "name": "SIT_TEST_JOB",
  "description": "imported job",
  "schedules": null,
  "dataStores": [
    {
      "dataStoreMeta": {
        "dataStoreKey": "CrmAnalyticsAM.PartiesAnalyticsAM.Person",
        "filters": "PersonId > 0",
        "isEffectiveDateDisabled": true,
        "isFlexDataStore": false,
        "isSilentError": true,
        "initialExtractDate": "2024-01-31T00:00:00.000Z",
        "chunkType": "DateSeqIncr",
        "chunkDateSeqIncr": 30,
        "chunkDateSeqMin": 2,
        "chunkPkSeqIncr": 0,
        "useUnionForIncremental": true,
        "columns": [
          {"name":"PersonId","isPrimaryKey":true,"isPopulate":true,"isLastUpdateDate":false,"isNaturalKey":false,"isEffectiveStartDate":false,"isCreationDate":false,"colConversion":null},
          {"name":"CreationDate","isPrimaryKey":false,"isPopulate":true,"isLastUpdateDate":false,"isNaturalKey":false,"isEffectiveStartDate":false,"isCreationDate":true,"colConversion":null}
        ]
      },
      "groupItemPriority": 0,
      "groupNumber": 0,
      "lastExtractDate": "2024-02-01T10:00:00.000Z"
    },
    {
      "dataStoreMeta": {
        "dataStoreKey": "FscmTopModelAM.FinFunAM.BusinessUnitPVO",
        "filters": "",
        "isEffectiveDateDisabled": false,
        "isFlexDataStore": false,
        "isSilentError": false,
        "chunkDateSeqIncr": 0,
        "chunkDateSeqMin": 0,
        "chunkPkSeqIncr": 0,
        "useUnionForIncremental": false,
        "columns": [
          {"name":"BusinessUnitId","isPrimaryKey":true,"isPopulate":true,"isLastUpdateDate":false,"isNaturalKey":true,"isEffectiveStartDate":false,"isCreationDate":false,"colConversion":null}
        ]
      },
      "groupItemPriority": 0,
      "groupNumber": 0
    },
    {
      "dataStoreMeta": {
        "dataStoreKey": "FscmTopModelAM.PayablesAM.InvoicePVO",
        "isEffectiveDateDisabled": false,
        "isFlexDataStore": false,
        "isSilentError": false,
        "chunkDateSeqIncr": 0,
        "chunkDateSeqMin": 0,
        "chunkPkSeqIncr": 0,
        "useUnionForIncremental": false,
        "columns": []
      },
      "groupItemPriority": 0,
      "groupNumber": 0
    }
  ]
}`

const (
	keyPerson  = "CrmAnalyticsAM.PartiesAnalyticsAM.Person"
	keyBU      = "FscmTopModelAM.FinFunAM.BusinessUnitPVO"
	keyInvoice = "FscmTopModelAM.PayablesAM.InvoicePVO"
)

func jobGetHandler(t *testing.T, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s (import and read must only GET)", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func nullState(t *testing.T) tfsdk.State {
	t.Helper()
	sr := resourceSchema(t)
	return tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
}

func runImport(t *testing.T, r *biccJobResource, id string) *resource.ImportStateResponse {
	t.Helper()
	// The framework hands ImportState a null state of the schema's type.
	resp := &resource.ImportStateResponse{State: nullState(t)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, resp)
	return resp
}

// importThenRead does what Terraform does for `terraform import`: ImportState, then Read
// with the state ImportState produced.
func importThenRead(t *testing.T, r *biccJobResource, id string) (*resource.ImportStateResponse, *resource.ReadResponse) {
	t.Helper()
	imp := runImport(t, r, id)
	if imp.Diagnostics.HasError() || imp.State.Raw.IsNull() {
		return imp, nil
	}
	return imp, runRead(t, r, imp.State)
}

func modelOf(t *testing.T, s tfsdk.State) biccJobModel {
	t.Helper()
	var m biccJobModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("reading state: %v", diags)
	}
	return m
}

func storesByKey(t *testing.T, set types.Set) map[string]dataStoreModel {
	t.Helper()
	var list []dataStoreModel
	if diags := set.ElementsAs(context.Background(), &list, false); diags.HasError() {
		t.Fatalf("reading data stores: %v", diags)
	}
	m := make(map[string]dataStoreModel, len(list))
	for _, ds := range list {
		m[ds.DataStoreKey.ValueString()] = ds
	}
	return m
}

func sortedKeys(m map[string]dataStoreModel) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func columnsByName(t *testing.T, set types.Set) map[string]columnModel {
	t.Helper()
	var list []columnModel
	if diags := set.ElementsAs(context.Background(), &list, false); diags.HasError() {
		t.Fatalf("reading columns: %v", diags)
	}
	m := make(map[string]columnModel, len(list))
	for _, c := range list {
		m[c.Name.ValueString()] = c
	}
	return m
}

func emptyColumns() types.Set {
	return types.SetValueMust(types.ObjectType{AttrTypes: columnAttrTypes}, []attr.Value{})
}

func emptyOverrides() types.List {
	return types.ListValueMust(types.ObjectType{AttrTypes: columnOverrideAttrTypes}, []attr.Value{})
}

// (a) Import returns ALL API data stores, with explicit columns and auto_populate false.
func TestImportReturnsAllDataStoresWithExplicitColumns(t *testing.T) {
	r, calls := newMockedResource(t, jobGetHandler(t, importJobBody))
	imp, rd := importThenRead(t, r, "4242")

	if imp.Diagnostics.HasError() {
		t.Fatalf("import: %v", imp.Diagnostics)
	}
	if rd.Diagnostics.HasError() {
		t.Fatalf("read after import: %v", rd.Diagnostics)
	}

	check := func(t *testing.T, s tfsdk.State) {
		t.Helper()
		m := modelOf(t, s)
		if m.ID.ValueString() != "4242" || m.Name.ValueString() != "SIT_TEST_JOB" || m.Description.ValueString() != "imported job" {
			t.Errorf("unexpected id/name/description: %q %q %q", m.ID.ValueString(), m.Name.ValueString(), m.Description.ValueString())
		}
		stores := storesByKey(t, m.DataStores)
		if got, want := sortedKeys(stores), []string{keyPerson, keyBU, keyInvoice}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("data stores = %v, want %v", got, want)
		}
		for key, ds := range stores {
			if ds.AutoPopulateAllColumns.IsNull() || ds.AutoPopulateAllColumns.ValueBool() {
				t.Errorf("%s: auto_populate_all_columns = %v, want false", key, ds.AutoPopulateAllColumns)
			}
			if ds.ColumnOverrides.IsNull() || ds.ColumnOverrides.IsUnknown() || len(ds.ColumnOverrides.Elements()) != 0 {
				t.Errorf("%s: column_overrides = %v, want empty list", key, ds.ColumnOverrides)
			}
			if ds.Columns.IsNull() || ds.Columns.IsUnknown() {
				t.Errorf("%s: columns must be a known set, got %v", key, ds.Columns)
			}
		}

		p := stores[keyPerson]
		cols := columnsByName(t, p.Columns)
		if len(cols) != 2 || !cols["PersonId"].IsPrimaryKey.ValueBool() || !cols["PersonId"].IsPopulate.ValueBool() ||
			!cols["CreationDate"].IsCreationDate.ValueBool() || cols["CreationDate"].IsPrimaryKey.ValueBool() {
			t.Errorf("Person columns not mapped from the API: %+v", cols)
		}
		if p.Filters.ValueString() != "PersonId > 0" || !p.IsSilentError.ValueBool() || !p.IsEffectiveDateDisabled.ValueBool() ||
			!p.UseUnionForIncremental.ValueBool() || p.InitialExtractDate.ValueString() != "2024-01-31" ||
			p.ChunkType.ValueString() != "DateSeqIncr" || p.ChunkDateSeqIncr.ValueInt64() != 30 ||
			p.ChunkDateSeqMin.ValueInt64() != 2 || p.ChunkPkSeqIncr.ValueInt64() != 0 {
			t.Errorf("Person scalar fields not mapped from the API: %+v", p)
		}

		bu := stores[keyBU]
		if !bu.Filters.IsNull() || !bu.InitialExtractDate.IsNull() || !bu.ChunkType.IsNull() || len(bu.Columns.Elements()) != 1 {
			t.Errorf("BusinessUnitPVO unset fields should be null with 1 column: %+v", bu)
		}
		if n := len(stores[keyInvoice].Columns.Elements()); n != 0 {
			t.Errorf("InvoicePVO columns = %d, want 0", n)
		}
	}

	t.Run("state written by ImportState", func(t *testing.T) { check(t, imp.State) })
	t.Run("state after the Read that follows import", func(t *testing.T) { check(t, rd.State) })
	t.Run("Read after import does not change what ImportState wrote", func(t *testing.T) {
		if !modelOf(t, imp.State).DataStores.Equal(modelOf(t, rd.State).DataStores) {
			t.Error("Read after import changed data_stores")
		}
	})

	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("expected 2 GETs (import + read), got %d", got)
	}
}

// stateWith builds a state for testJobID holding the given data stores.
func stateWith(t *testing.T, stores ...dataStoreModel) tfsdk.State {
	t.Helper()
	objs := make([]attr.Value, 0, len(stores))
	for _, ds := range stores {
		obj, diags := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, ds)
		if diags.HasError() {
			t.Fatalf("building data store: %v", diags)
		}
		objs = append(objs, obj)
	}
	state := tfsdk.State{Schema: resourceSchema(t).Schema}
	model := biccJobModel{
		ID:          types.StringValue(testJobID),
		Name:        types.StringValue("SIT_TEST_JOB"),
		Description: types.StringValue("old"),
		DataStores:  types.SetValueMust(types.ObjectType{AttrTypes: dataStoreAttrTypes}, objs),
	}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return state
}

func autoPopulateStore(key string) dataStoreModel {
	return dataStoreModel{
		DataStoreKey:            types.StringValue(key),
		Filters:                 types.StringNull(),
		IsSilentError:           types.BoolValue(false),
		IsEffectiveDateDisabled: types.BoolValue(false),
		UseUnionForIncremental:  types.BoolValue(false),
		InitialExtractDate:      types.StringNull(),
		ChunkType:               types.StringNull(),
		ChunkDateSeqIncr:        types.Int64Value(0),
		ChunkDateSeqMin:         types.Int64Value(0),
		ChunkPkSeqIncr:          types.Int64Value(0),
		AutoPopulateAllColumns:  types.BoolValue(true),
		ColumnOverrides:         emptyOverrides(),
		Columns:                 emptyColumns(),
	}
}

// (b) KI-8 regression: a normal Read still drops API data stores that are not in state.
func TestReadStillFiltersPhantomDataStores(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	resp := runRead(t, r, stateWith(t, autoPopulateStore(keyPerson)))
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	stores := storesByKey(t, modelOf(t, resp.State).DataStores)
	if got := sortedKeys(stores); len(got) != 1 || got[0] != keyPerson {
		t.Fatalf("data stores after Read = %v, want only [%s] (API returned 3)", got, keyPerson)
	}
	p := stores[keyPerson]
	if !p.AutoPopulateAllColumns.ValueBool() {
		t.Error("auto_populate_all_columns must be carried from state")
	}
	if len(p.Columns.Elements()) != 0 {
		t.Error("auto mode keeps columns out of state")
	}
}

// (c) A normal Read of a job whose state has data_stores = [] must not pull in API stores.
// This is the case an "empty means import" shortcut would break.
func TestReadWithEmptyDataStoresDoesNotPullInAPIStores(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	resp := runRead(t, r, stateWith(t)) // data_stores = []
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	ds := modelOf(t, resp.State).DataStores
	if ds.IsNull() || len(ds.Elements()) != 0 {
		t.Fatalf("data_stores after Read = %v, want an empty set (API returned 3 stores)", ds)
	}
}

// A Read with data_stores NULL (only the partial state a failed Create leaves, see
// TestCreateRecordsIDWhenReReadFails) is not an import either. Import never goes through
// Read with a null set because ImportState itself populates all stores. Pinned so that
// nobody changes Read to treat null as import without noticing.
func TestReadWithNullDataStoresDoesNotPullInAPIStores(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	state := stateWith(t)
	m := modelOf(t, state)
	m.DataStores = types.SetNull(types.ObjectType{AttrTypes: dataStoreAttrTypes})
	if diags := state.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	resp := runRead(t, r, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	if n := len(modelOf(t, resp.State).DataStores.Elements()); n != 0 {
		t.Fatalf("Read with null data_stores pulled in %d API stores", n)
	}
}

// (d) Bad ids fail with a clear error, make no HTTP call, and leave no state.
func TestImportRejectsBadIDs(t *testing.T) {
	bad := []string{"", " ", "abc", "12abc", "0", "-1", "-4242", "+0", "1.5", "1e3", "0x10", " 4242", "4242 ", "99999999999999999999", "4242/../1"}
	for _, id := range bad {
		id := id
		t.Run("id="+id, func(t *testing.T) {
			r, calls := newMockedResource(t, jobGetHandler(t, importJobBody))
			resp := runImport(t, r, id)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("id %q: expected an error diagnostic", id)
			}
			if !resp.State.Raw.IsNull() {
				t.Errorf("id %q: state must stay empty", id)
			}
			if got := atomic.LoadInt32(calls); got != 0 {
				t.Errorf("id %q: made %d HTTP calls for an invalid id", id, got)
			}
		})
	}
}

func TestImportNormalisesNumericID(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	resp := runImport(t, r, "007")
	if resp.Diagnostics.HasError() {
		t.Fatalf("import: %v", resp.Diagnostics)
	}
	if got := modelOf(t, resp.State).ID.ValueString(); got != "7" {
		t.Errorf("id = %q, want canonical %q", got, "7")
	}
}

// Importing an id that does not exist (live shape, A6) or that fails any other way is an
// error and writes nothing to state.
func TestImportFailuresLeaveNoState(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"missing job (400 Invalid Job Id, live shape)", http.StatusBadRequest, liveMissingJobBody, "does not exist"},
		{"500", http.StatusInternalServerError, `boom`, "boom"},
		{"bare 404", http.StatusNotFound, `Not Found`, "404"},
		{"401", http.StatusUnauthorized, ``, "401"},
		{"200 with invalid JSON", http.StatusOK, `not json`, "unmarshal"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			resp := runImport(t, r, "4242")
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error diagnostic")
			}
			if !resp.State.Raw.IsNull() {
				t.Error("state must stay empty when import fails")
			}
			msg := resp.Diagnostics[0].Summary() + " " + resp.Diagnostics[0].Detail()
			if !strings.Contains(msg, tc.want) {
				t.Errorf("diagnostic %q does not mention %q", msg, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// (e) Plan after import. Unit level: the resource's ModifyPlan against the state that
// import produced and a plan shaped like the framework's plan for an equivalent config.
// This is NOT a terraform plan: terraform-plugin-testing is not in go.mod and Terraform
// core's own checks (valid-plan rules, set element matching) are not exercised.
// ---------------------------------------------------------------------------

// configStoreFor builds the plan element Terraform would hand ModifyPlan for a config that
// lists the data store with explicit columns taken from the mock job. `unset` is what the
// framework puts into Optional+Computed attributes the config leaves out (null or unknown).
type unsetKind int

const (
	unsetNull unsetKind = iota
	unsetUnknown
)

func planStoreExplicit(key string, cols []columnModel, unset unsetKind, set func(*dataStoreModel)) dataStoreModel {
	colObjs := make([]attr.Value, len(cols))
	for i, c := range cols {
		obj, diags := types.ObjectValueFrom(context.Background(), columnAttrTypes, c)
		if diags.HasError() {
			panic(diags)
		}
		colObjs[i] = obj
	}
	ds := dataStoreModel{
		DataStoreKey: types.StringValue(key),
		// config: auto_populate_all_columns = false, explicit columns (column defaults applied)
		AutoPopulateAllColumns: types.BoolValue(false),
		Columns:                types.SetValueMust(types.ObjectType{AttrTypes: columnAttrTypes}, colObjs),
	}
	if unset == unsetUnknown {
		ds.Filters = types.StringUnknown()
		ds.IsSilentError = types.BoolUnknown()
		ds.IsEffectiveDateDisabled = types.BoolUnknown()
		ds.UseUnionForIncremental = types.BoolUnknown()
		ds.InitialExtractDate = types.StringUnknown()
		ds.ChunkType = types.StringUnknown()
		ds.ChunkDateSeqIncr = types.Int64Unknown()
		ds.ChunkDateSeqMin = types.Int64Unknown()
		ds.ChunkPkSeqIncr = types.Int64Unknown()
		ds.ColumnOverrides = types.ListUnknown(types.ObjectType{AttrTypes: columnOverrideAttrTypes})
	} else {
		ds.Filters = types.StringNull()
		ds.IsSilentError = types.BoolNull()
		ds.IsEffectiveDateDisabled = types.BoolNull()
		ds.UseUnionForIncremental = types.BoolNull()
		ds.InitialExtractDate = types.StringNull()
		ds.ChunkType = types.StringNull()
		ds.ChunkDateSeqIncr = types.Int64Null()
		ds.ChunkDateSeqMin = types.Int64Null()
		ds.ChunkPkSeqIncr = types.Int64Null()
		ds.ColumnOverrides = types.ListNull(types.ObjectType{AttrTypes: columnOverrideAttrTypes})
	}
	if set != nil {
		set(&ds)
	}
	return ds
}

func col(name string, pk, creation bool) columnModel {
	return colNK(name, pk, creation, false)
}

func colNK(name string, pk, creation, natural bool) columnModel {
	return columnModel{
		Name:                 types.StringValue(name),
		IsPopulate:           types.BoolValue(true),
		IsPrimaryKey:         types.BoolValue(pk),
		IsLastUpdateDate:     types.BoolValue(false),
		IsCreationDate:       types.BoolValue(creation),
		IsEffectiveStartDate: types.BoolValue(false),
		IsNaturalKey:         types.BoolValue(natural),
	}
}

func runModifyPlan(t *testing.T, r *biccJobResource, state tfsdk.State, planStores []dataStoreModel) (tfsdk.Plan, *resource.ModifyPlanResponse) {
	t.Helper()
	objs := make([]attr.Value, len(planStores))
	for i, ds := range planStores {
		obj, diags := types.ObjectValueFrom(context.Background(), dataStoreAttrTypes, ds)
		if diags.HasError() {
			t.Fatalf("building plan element: %v", diags)
		}
		objs[i] = obj
	}
	sm := modelOf(t, state)
	plan := tfsdk.Plan{Schema: state.Schema}
	pm := biccJobModel{
		ID:          sm.ID, // UseStateForUnknown
		Name:        sm.Name,
		Description: sm.Description,
		DataStores:  types.SetValueMust(types.ObjectType{AttrTypes: dataStoreAttrTypes}, objs),
	}
	if diags := plan.Set(context.Background(), pm); diags.HasError() {
		t.Fatalf("building plan: %v", diags)
	}
	resp := &resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw.Copy()}}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: state, Plan: plan}, resp)
	return plan, resp
}

func TestPlanAfterImportWithEquivalentExplicitConfigHasNoDataStoreDiff(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	imp, rd := importThenRead(t, r, "4242")
	if imp.Diagnostics.HasError() || rd.Diagnostics.HasError() {
		t.Fatalf("import/read: %v %v", imp.Diagnostics, rd.Diagnostics)
	}
	state := rd.State // the state Terraform stores after `terraform import`

	for name, kind := range map[string]unsetKind{"unset attributes null": unsetNull, "unset attributes unknown": unsetUnknown} {
		kind := kind
		t.Run(name, func(t *testing.T) {
			stores := []dataStoreModel{
				// Person: config sets the values that differ from the API defaults.
				planStoreExplicit(keyPerson, []columnModel{col("PersonId", true, false), col("CreationDate", false, true)}, kind, func(d *dataStoreModel) {
					d.Filters = types.StringValue("PersonId > 0")
					d.IsSilentError = types.BoolValue(true)
					d.IsEffectiveDateDisabled = types.BoolValue(true)
					d.UseUnionForIncremental = types.BoolValue(true)
					d.InitialExtractDate = types.StringValue("2024-01-31")
					d.ChunkType = types.StringValue("DateSeqIncr")
					d.ChunkDateSeqIncr = types.Int64Value(30)
					d.ChunkDateSeqMin = types.Int64Value(2)
				}),
				planStoreExplicit(keyBU, []columnModel{colNK("BusinessUnitId", true, false, true)}, kind, nil),
				planStoreExplicit(keyInvoice, nil, kind, nil),
			}
			_, resp := runModifyPlan(t, r, state, stores)
			if resp.Diagnostics.HasError() {
				t.Fatalf("ModifyPlan: %v", resp.Diagnostics)
			}
			var planned biccJobModel
			if diags := resp.Plan.Get(context.Background(), &planned); diags.HasError() {
				t.Fatalf("reading plan: %v", diags)
			}
			want := modelOf(t, state).DataStores
			if !planned.DataStores.Equal(want) {
				gotBy, wantBy := storesByKey(t, planned.DataStores), storesByKey(t, want)
				for k, w := range wantBy {
					g := gotBy[k]
					t.Errorf("%s differs:\n plan:  %+v\n state: %+v", k, g, w)
				}
			}
		})
	}
}

// The case the docs call out: config says auto_populate_all_columns = true (and no
// explicit columns), imported state says false with explicit columns. ModifyPlan pins the
// planned element to state (post-import suppression, pre-existing behaviour), so at unit
// level there is no data_stores diff. Whether Terraform core accepts a planned value that
// differs from a set config value is NOT covered here (unverified, ASSUMPTIONS B1).
func TestPlanAfterImportWithAutoPopulateConfigIsPinnedToState(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	_, rd := importThenRead(t, r, "4242")
	state := rd.State

	stores := []dataStoreModel{
		planStoreExplicit(keyPerson, nil, unsetUnknown, func(d *dataStoreModel) {
			d.AutoPopulateAllColumns = types.BoolValue(true)
			d.Columns = types.SetUnknown(types.ObjectType{AttrTypes: columnAttrTypes})
			d.Filters = types.StringValue("PersonId > 0")
			d.IsSilentError = types.BoolValue(true)
			d.IsEffectiveDateDisabled = types.BoolValue(true)
			d.UseUnionForIncremental = types.BoolValue(true)
			d.InitialExtractDate = types.StringValue("2024-01-31")
			d.ChunkType = types.StringValue("DateSeqIncr")
			d.ChunkDateSeqIncr = types.Int64Value(30)
			d.ChunkDateSeqMin = types.Int64Value(2)
		}),
	}
	_, resp := runModifyPlan(t, r, state, stores)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan: %v", resp.Diagnostics)
	}
	var planned biccJobModel
	if diags := resp.Plan.Get(context.Background(), &planned); diags.HasError() {
		t.Fatalf("reading plan: %v", diags)
	}
	got := storesByKey(t, planned.DataStores)[keyPerson]
	want := storesByKey(t, modelOf(t, state).DataStores)[keyPerson]
	if got.AutoPopulateAllColumns.ValueBool() || len(got.Columns.Elements()) != 2 {
		t.Errorf("expected the planned element pinned to state (auto=false, 2 columns), got auto=%v columns=%d",
			got.AutoPopulateAllColumns, len(got.Columns.Elements()))
	}
	if !got.Columns.Equal(want.Columns) {
		t.Error("planned columns differ from imported state")
	}
}

// Sanity check on the test itself: a config that really differs (a changed filter) must
// still show a difference, so the equality assertions above are not vacuous.
func TestPlanAfterImportStillShowsRealChanges(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, importJobBody))
	_, rd := importThenRead(t, r, "4242")
	state := rd.State

	stores := []dataStoreModel{
		planStoreExplicit(keyBU, []columnModel{colNK("BusinessUnitId", true, false, true)}, unsetUnknown, func(d *dataStoreModel) {
			d.Filters = types.StringValue("Active = 'Y'")
		}),
	}
	_, resp := runModifyPlan(t, r, state, stores)
	var planned biccJobModel
	if diags := resp.Plan.Get(context.Background(), &planned); diags.HasError() {
		t.Fatalf("reading plan: %v", diags)
	}
	got := storesByKey(t, planned.DataStores)[keyBU]
	if got.Filters.ValueString() != "Active = 'Y'" {
		t.Errorf("a real config change was swallowed: filters = %v", got.Filters)
	}
	if planned.DataStores.Equal(modelOf(t, state).DataStores) {
		t.Error("plan equals state although the config changed")
	}
}

// ---------------------------------------------------------------------------
// Empty import (review MINOR-2). A 200 that carries no job (`{}`, `null`, or a body with
// no name) would otherwise import a nameless job: state with name "", no data stores.
// The next apply from a matching config would then PUT that job's data over whatever
// BICC holds. ImportState must refuse. A job WITH a name and no data stores is a legal
// job and must still import.
// ---------------------------------------------------------------------------

func TestImportRejectsAnAPIAnswerWithoutAJobName(t *testing.T) {
	bodies := map[string]string{
		"200 {}":                        `{}`,
		"200 null":                      `null`,
		"200 only description null":     `{"description":null}`,
		"200 empty name":                `{"name":"","dataStores":[]}`,
		"200 name null":                 `{"name":null,"description":"x","dataStores":[]}`,
		"200 data stores but no name":   `{"dataStores":[{"dataStoreMeta":{"dataStoreKey":"A.B.C","columns":[]}}]}`,
		"200 empty array (wrong shape)": `[]`,
	}
	for name, body := range bodies {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			r, _ := newMockedResource(t, jobGetHandler(t, body))
			resp := runImport(t, r, "4242")
			if name == "200 empty array (wrong shape)" {
				// Not a JSON object: fails to decode, which is an error too.
				if !resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
					t.Fatalf("expected an error and empty state, got %v / null=%v", resp.Diagnostics, resp.State.Raw.IsNull())
				}
				return
			}
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected an error diagnostic, state: %v", resp.State.Raw)
			}
			if !resp.State.Raw.IsNull() {
				t.Error("state must stay empty")
			}
			var msg string
			for _, d := range resp.Diagnostics.Errors() {
				msg += d.Summary() + " " + d.Detail()
			}
			if !strings.Contains(msg, "BICC returned a job without a name for id 4242; not importing") {
				t.Errorf("unexpected message: %s", msg)
			}
		})
	}
}

// A job with a name and zero data stores is legitimate (data_stores = [] is a valid
// config, KI-8): import succeeds with an empty, known data_stores set.
func TestImportAcceptsANamedJobWithoutDataStores(t *testing.T) {
	r, _ := newMockedResource(t, jobGetHandler(t, `{"name":"EMPTY_JOB","description":"d","schedules":null,"dataStores":[]}`))
	imp, rd := importThenRead(t, r, "4242")
	if imp.Diagnostics.HasError() || rd.Diagnostics.HasError() {
		t.Fatalf("import/read: %v %v", imp.Diagnostics, rd.Diagnostics)
	}
	for name, s := range map[string]tfsdk.State{"ImportState": imp.State, "Read after import": rd.State} {
		m := modelOf(t, s)
		if m.Name.ValueString() != "EMPTY_JOB" || m.ID.ValueString() != "4242" {
			t.Errorf("%s: unexpected id/name: %q %q", name, m.ID.ValueString(), m.Name.ValueString())
		}
		if m.DataStores.IsNull() || m.DataStores.IsUnknown() || len(m.DataStores.Elements()) != 0 {
			t.Errorf("%s: data_stores = %v, want a known empty set", name, m.DataStores)
		}
	}

	// Same with the dataStores key missing altogether (decodes to a nil slice).
	r2, _ := newMockedResource(t, jobGetHandler(t, `{"name":"EMPTY_JOB"}`))
	imp2 := runImport(t, r2, "4242")
	if imp2.Diagnostics.HasError() {
		t.Fatalf("import without dataStores key: %v", imp2.Diagnostics)
	}
	if ds := modelOf(t, imp2.State).DataStores; ds.IsNull() || len(ds.Elements()) != 0 {
		t.Errorf("data_stores = %v, want a known empty set", ds)
	}
}
