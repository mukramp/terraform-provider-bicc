package bicc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/tra-jkv/terraform-provider-bicc/bicc/client"
)

// Tests for PLAN.md task 6 / KI-6: a failed or empty column lookup for an
// auto_populate_all_columns data store must fail Create/Update and must never
// lead to a PUT. Mocks only; no live BICC access.

// A9 (observed 2026-11-03, live SIT): a missing key returns 500 with this shape.
const missingKeyBody = `{"code":"Internal Server Error","message":"Data store not found"}`

// mockBICC records every request and answers datastore lookups from `lookups`
// (key -> status, body). PUT /jobs/ answers a valid job response.
type mockBICC struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	puts    int
	lookups map[string]mockReply
}

type mockReply struct {
	status int
	body   string
}

func newMockBICC(t *testing.T, lookups map[string]mockReply) *mockBICC {
	t.Helper()
	m := &mockBICC{t: t, lookups: lookups}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut:
			m.puts++
			w.Write([]byte(`{"id":"7","status":"SUCCESS"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/biacm/rest/meta/datastores/"):
			key := strings.TrimPrefix(r.URL.Path, "/biacm/rest/meta/datastores/")
			rep, ok := m.lookups[key]
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(missingKeyBody))
				return
			}
			w.WriteHeader(rep.status)
			w.Write([]byte(rep.body))
		default:
			// Create/Update re-read the job after a PUT; those paths are not
			// reached in the failure tests.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockBICC) putCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.puts
}

func (m *mockBICC) resource() *biccJobResource {
	c := client.NewClient(&client.Config{Host: "unused", Username: "u", Password: "p", Port: 0})
	c.BaseURL = m.srv.URL
	return &biccJobResource{client: c}
}

func okColumnsBody() string {
	return `{"columns":[` +
		`{"name":"ID","isPrimaryKey":true,"isPopulate":true},` +
		`{"name":"NAME","isPopulate":true},` +
		`{"name":"HIDDEN","isPopulate":false}]}`
}

// autoDataStoreSet builds data_stores with auto_populate_all_columns = true for
// the given keys.
func autoDataStoreSet(t *testing.T, overrides map[string][]attr.Value, keys ...string) types.Set {
	t.Helper()
	objs := make([]attr.Value, len(keys))
	for i, k := range keys {
		ov := overrides[k]
		if ov == nil {
			ov = []attr.Value{}
		}
		obj, d := types.ObjectValue(dataStoreAttrTypes, map[string]attr.Value{
			"data_store_key":             types.StringValue(k),
			"filters":                    types.StringValue(""),
			"is_silent_error":            types.BoolValue(false),
			"is_effective_date_disabled": types.BoolValue(false),
			"use_union_for_incremental":  types.BoolValue(false),
			"initial_extract_date":       types.StringNull(),
			"chunk_type":                 types.StringNull(),
			"chunk_date_seq_incr":        types.Int64Value(0),
			"chunk_date_seq_min":         types.Int64Value(0),
			"chunk_pk_seq_incr":          types.Int64Value(0),
			"auto_populate_all_columns":  types.BoolValue(true),
			"column_overrides":           types.ListValueMust(types.ObjectType{AttrTypes: columnOverrideAttrTypes}, ov),
			"columns":                    types.SetValueMust(types.ObjectType{AttrTypes: columnAttrTypes}, []attr.Value{}),
		})
		if d.HasError() {
			t.Fatalf("building test object: %v", d)
		}
		objs[i] = obj
	}
	s, d := types.SetValue(types.ObjectType{AttrTypes: dataStoreAttrTypes}, objs)
	if d.HasError() {
		t.Fatalf("building test set: %v", d)
	}
	return s
}

func overrideDeselect(t *testing.T, name string) attr.Value {
	t.Helper()
	return overrideSetPopulate(t, name, false)
}

func overrideSetPopulate(t *testing.T, name string, populate bool) attr.Value {
	t.Helper()
	v, d := types.ObjectValue(columnOverrideAttrTypes, map[string]attr.Value{
		"name":                    types.StringValue(name),
		"is_populate":             types.BoolValue(populate),
		"is_primary_key":          types.BoolNull(),
		"is_last_update_date":     types.BoolNull(),
		"is_creation_date":        types.BoolNull(),
		"is_effective_start_date": types.BoolNull(),
		"is_natural_key":          types.BoolNull(),
	})
	if d.HasError() {
		t.Fatalf("building override: %v", d)
	}
	return v
}

func jobSchema(t *testing.T, r *biccJobResource) resource.SchemaResponse {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	if sr.Diagnostics.HasError() {
		t.Fatalf("schema: %v", sr.Diagnostics)
	}
	return sr
}

func planFor(t *testing.T, r *biccJobResource, model biccJobModel) tfsdk.Plan {
	t.Helper()
	sr := jobSchema(t, r)
	p := tfsdk.Plan{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
	if d := p.Set(context.Background(), model); d.HasError() {
		t.Fatalf("setting plan: %v", d)
	}
	return p
}

func stateFor(t *testing.T, r *biccJobResource, model biccJobModel) tfsdk.State {
	t.Helper()
	sr := jobSchema(t, r)
	s := tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
	if d := s.Set(context.Background(), model); d.HasError() {
		t.Fatalf("setting state: %v", d)
	}
	return s
}

func errorText(d diag.Diagnostics) string {
	var b strings.Builder
	for _, e := range d.Errors() {
		b.WriteString(e.Summary())
		b.WriteString(" | ")
		b.WriteString(e.Detail())
		b.WriteString("\n")
	}
	return b.String()
}

// Create with a failing lookup (HTTP 500 body, as observed in A9): error
// diagnostic naming the key and the underlying error, and no PUT.
func TestCreate_FailedColumnLookup_NoPUT(t *testing.T) {
	const key = "FscmTopModelAM.Nope.DoesNotExistPVO"
	m := newMockBICC(t, nil) // every lookup -> 500 + missingKeyBody
	r := m.resource()

	model := biccJobModel{
		ID:          types.StringUnknown(),
		Name:        types.StringValue("tf-task6-create"),
		Description: types.StringValue(""),
		DataStores:  autoDataStoreSet(t, nil, key),
	}
	req := resource.CreateRequest{Plan: planFor(t, r, model)}
	resp := &resource.CreateResponse{State: tfsdk.State{Schema: req.Plan.Schema, Raw: tftypes.NewValue(req.Plan.Schema.Type().TerraformType(context.Background()), nil)}}

	r.Create(context.Background(), req, resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for the failed column lookup")
	}
	txt := errorText(resp.Diagnostics)
	for _, want := range []string{key, "500", "Internal Server Error"} {
		if !strings.Contains(txt, want) {
			t.Errorf("diagnostic should contain %q, got:\n%s", want, txt)
		}
	}
	if n := m.putCount(); n != 0 {
		t.Errorf("a job was PUT %d time(s) despite the failed lookup; want 0", n)
	}
}

// Update with a failing lookup: same guarantees.
func TestUpdate_FailedColumnLookup_NoPUT(t *testing.T) {
	const key = "FscmTopModelAM.Nope.DoesNotExistPVO"
	m := newMockBICC(t, nil)
	r := m.resource()

	model := biccJobModel{
		ID:          types.StringValue("7"),
		Name:        types.StringValue("tf-task6-update"),
		Description: types.StringValue(""),
		DataStores:  autoDataStoreSet(t, nil, key),
	}
	req := resource.UpdateRequest{Plan: planFor(t, r, model), State: stateFor(t, r, model)}
	resp := &resource.UpdateResponse{State: stateFor(t, r, model)}

	r.Update(context.Background(), req, resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for the failed column lookup")
	}
	txt := errorText(resp.Diagnostics)
	for _, want := range []string{key, "500", "Internal Server Error"} {
		if !strings.Contains(txt, want) {
			t.Errorf("diagnostic should contain %q, got:\n%s", want, txt)
		}
	}
	if n := m.putCount(); n != 0 {
		t.Errorf("a job was PUT %d time(s) despite the failed lookup; want 0", n)
	}
}

// One failing store among several: the whole apply fails, the diagnostic names
// only the failing key, and nothing is PUT (no partial job).
func TestBuildJob_OneOfManyLookupsFails(t *testing.T) {
	m := newMockBICC(t, map[string]mockReply{
		"Good.One": {http.StatusOK, okColumnsBody()},
		"Good.Two": {http.StatusOK, okColumnsBody()},
	})
	r := m.resource()

	model := biccJobModel{
		Name:       types.StringValue("j"),
		DataStores: autoDataStoreSet(t, nil, "Good.One", "Bad.Key", "Good.Two"),
	}
	job, diags := r.buildJobFromModel(context.Background(), model)
	if !diags.HasError() {
		t.Fatal("expected an error diagnostic")
	}
	if job != nil {
		t.Errorf("no job may be returned when a lookup failed, got %+v", job)
	}
	txt := errorText(diags)
	if !strings.Contains(txt, "Bad.Key") {
		t.Errorf("diagnostic should name Bad.Key, got:\n%s", txt)
	}
	if strings.Contains(txt, "Good.One") || strings.Contains(txt, "Good.Two") {
		t.Errorf("diagnostic should not name stores whose lookup succeeded, got:\n%s", txt)
	}
}

// Decision (task 6): a lookup that succeeds but leaves zero populated columns
// is an error as well, because it would PUT a job with no columns.
func TestBuildJob_LookupSucceedsButZeroColumns_Errors(t *testing.T) {
	t.Run("API returns no columns", func(t *testing.T) {
		m := newMockBICC(t, map[string]mockReply{"Empty.Key": {http.StatusOK, `{"columns":[]}`}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"), DataStores: autoDataStoreSet(t, nil, "Empty.Key"),
		})
		if !diags.HasError() || job != nil {
			t.Fatalf("expected error and nil job, got job=%v diags=%v", job, diags)
		}
		if !strings.Contains(errorText(diags), "Empty.Key") {
			t.Errorf("diagnostic should name the key, got:\n%s", errorText(diags))
		}
	})

	t.Run("only a primary key with isPopulate=false (dropped by the later is_populate filter)", func(t *testing.T) {
		body := `{"columns":[{"name":"ID","isPrimaryKey":true,"isPopulate":false}]}`
		m := newMockBICC(t, map[string]mockReply{"Pk.Only": {http.StatusOK, body}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"), DataStores: autoDataStoreSet(t, nil, "Pk.Only"),
		})
		if !diags.HasError() || job != nil {
			t.Fatalf("expected error and nil job, got job=%v diags=%v", job, diags)
		}
		if !strings.Contains(errorText(diags), "no column remains populated") {
			t.Errorf("message should state the real condition, got:\n%s", errorText(diags))
		}
	})

	t.Run("no column is default-selected or primary key", func(t *testing.T) {
		body := `{"columns":[{"name":"A","isPopulate":false},{"name":"B","isPopulate":false}]}`
		m := newMockBICC(t, map[string]mockReply{"None.Selected": {http.StatusOK, body}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"), DataStores: autoDataStoreSet(t, nil, "None.Selected"),
		})
		if !diags.HasError() || job != nil {
			t.Fatalf("expected error and nil job, got job=%v diags=%v", job, diags)
		}
	})

	t.Run("column_overrides deselect every column", func(t *testing.T) {
		body := `{"columns":[{"name":"A","isPopulate":true}]}`
		m := newMockBICC(t, map[string]mockReply{"All.Deselected": {http.StatusOK, body}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"),
			DataStores: autoDataStoreSet(t,
				map[string][]attr.Value{"All.Deselected": {overrideDeselect(t, "A")}},
				"All.Deselected"),
		})
		if !diags.HasError() || job != nil {
			t.Fatalf("expected error and nil job, got job=%v diags=%v", job, diags)
		}
	})
}

// Guard against over-fixing: a successful lookup still builds the job, keeping
// isPopulate || isPrimaryKey columns and honouring overrides.
func TestBuildJob_LookupSucceeds_BuildsColumns(t *testing.T) {
	m := newMockBICC(t, map[string]mockReply{"Good.Key": {http.StatusOK, okColumnsBody()}})
	job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
		Name: types.StringValue("j"), DataStores: autoDataStoreSet(t, nil, "Good.Key"),
	})
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if len(job.DataStores) != 1 {
		t.Fatalf("want 1 data store, got %d", len(job.DataStores))
	}
	cols := job.DataStores[0].DataStoreMeta.Columns
	if len(cols) != 2 || cols[0].Name != "ID" || cols[1].Name != "NAME" {
		t.Errorf("unexpected columns: %+v", cols)
	}
	if m.putCount() != 0 {
		t.Error("buildJobFromModel must not PUT")
	}
}

// MINOR-3: every failing store is reported, not only the first one. A later
// store's ElementsAs must not trip on an earlier store's lookup error.
func TestBuildJob_TwoBadKeysAmongGood_BothReported(t *testing.T) {
	m := newMockBICC(t, map[string]mockReply{
		"Good.One": {http.StatusOK, okColumnsBody()},
		"Good.Two": {http.StatusOK, okColumnsBody()},
	})
	r := m.resource()
	ds := autoDataStoreSet(t, nil, "Good.One", "Bad.Key", "Good.Two", "Bad.Two")

	t.Run("buildJobFromModel", func(t *testing.T) {
		job, diags := r.buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"), DataStores: ds,
		})
		if !diags.HasError() || job != nil {
			t.Fatalf("expected error and nil job, got job=%v diags=%v", job, diags)
		}
		txt := errorText(diags)
		for _, want := range []string{"Bad.Key", "Bad.Two"} {
			if !strings.Contains(txt, want) {
				t.Errorf("diagnostic should name %q, got:\n%s", want, txt)
			}
		}
		for _, bad := range []string{"Good.One", "Good.Two"} {
			if strings.Contains(txt, bad) {
				t.Errorf("diagnostic should not name %q, got:\n%s", bad, txt)
			}
		}
	})

	t.Run("Create reports both and sends no PUT", func(t *testing.T) {
		model := biccJobModel{
			ID: types.StringUnknown(), Name: types.StringValue("tf-task6-two-bad"),
			Description: types.StringValue(""), DataStores: ds,
		}
		req := resource.CreateRequest{Plan: planFor(t, r, model)}
		resp := &resource.CreateResponse{State: tfsdk.State{Schema: req.Plan.Schema, Raw: tftypes.NewValue(req.Plan.Schema.Type().TerraformType(context.Background()), nil)}}
		r.Create(context.Background(), req, resp)
		txt := errorText(resp.Diagnostics)
		for _, want := range []string{"Bad.Key", "Bad.Two"} {
			if !strings.Contains(txt, want) {
				t.Errorf("Create diagnostic should name %q, got:\n%s", want, txt)
			}
		}
		if n := m.putCount(); n != 0 {
			t.Errorf("a job was PUT %d time(s); want 0", n)
		}
	})
}

// PRE-EXISTING behaviour, documented, not endorsed (KI-12). The comment in
// buildJobFromModel says primary key columns are "always included regardless of
// isPopulate", but the later "Remove columns deselected via overrides" loop
// drops every column whose IsPopulate is false, including a primary key with
// isPopulate=false, unless a column_override sets is_populate=true on it. These
// tests pin the current behaviour so a future change is a conscious one.
func TestBuildJob_PreExisting_PrimaryKeyWithIsPopulateFalse(t *testing.T) {
	const body = `{"columns":[` +
		`{"name":"ID","isPrimaryKey":true,"isPopulate":false},` +
		`{"name":"NAME","isPopulate":true},` +
		`{"name":"OTHER","isPopulate":false}]}`

	names := func(job *client.Job) []string {
		var out []string
		for _, c := range job.DataStores[0].DataStoreMeta.Columns {
			out = append(out, c.Name)
		}
		return out
	}

	t.Run("no override: pk with isPopulate=false is dropped", func(t *testing.T) {
		m := newMockBICC(t, map[string]mockReply{"K": {http.StatusOK, body}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"), DataStores: autoDataStoreSet(t, nil, "K"),
		})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if got := names(job); len(got) != 1 || got[0] != "NAME" {
			t.Errorf("current behaviour: only NAME is sent, got %v", got)
		}
	})

	t.Run("override is_populate=true on the pk keeps it", func(t *testing.T) {
		m := newMockBICC(t, map[string]mockReply{"K": {http.StatusOK, body}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"),
			DataStores: autoDataStoreSet(t,
				map[string][]attr.Value{"K": {overrideSetPopulate(t, "ID", true)}}, "K"),
		})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if got := names(job); len(got) != 2 || got[0] != "ID" || got[1] != "NAME" {
			t.Errorf("want [ID NAME], got %v", got)
		}
	})

	t.Run("override is_populate=true on a non-default, non-pk column has no effect", func(t *testing.T) {
		m := newMockBICC(t, map[string]mockReply{"K": {http.StatusOK, body}})
		job, diags := m.resource().buildJobFromModel(context.Background(), biccJobModel{
			Name: types.StringValue("j"),
			DataStores: autoDataStoreSet(t,
				map[string][]attr.Value{"K": {overrideSetPopulate(t, "OTHER", true)}}, "K"),
		})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if got := names(job); len(got) != 1 || got[0] != "NAME" {
			t.Errorf("current behaviour: OTHER is not added, got %v", got)
		}
	})
}
