package bicc

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Tests for the `description` null/"" handling (review MAJOR-1).
//
// OBSERVED (ASSUMPTIONS A12, SIT 2026-11-04, read-only counts): of 39 jobs, description is
// a non-empty string for 33 and JSON null for 6 (none "" and none missing). The client
// decodes description into a plain Go string, so null arrives as "". The schema has
// description Optional and NOT Computed, so a config that leaves it out plans null. A state
// holding "" would then plan "" -> null on every run, and an Update would write "" over a
// planned null (Terraform core: "Provider produced inconsistent result after apply").
// The rule under test: an API "" never replaces a null the plan/state already holds, and
// ImportState writes null for an empty API description.

const nullDescJobBody = `{"name":"SIT_TEST_JOB","description":null,"schedules":null,"dataStores":[]}`

func jobBodyWithDescription(desc string) string {
	return `{"name":"SIT_TEST_JOB","description":"` + desc + `","schedules":null,"dataStores":[]}`
}

func emptyStores() types.Set {
	return types.SetValueMust(types.ObjectType{AttrTypes: dataStoreAttrTypes}, []attr.Value{})
}

func stateWithDescription(t *testing.T, desc types.String) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t).Schema}
	m := biccJobModel{
		ID:          types.StringValue(testJobID),
		Name:        types.StringValue("SIT_TEST_JOB"),
		Description: desc,
		DataStores:  emptyStores(),
	}
	if diags := state.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return state
}

func descOf(t *testing.T, s tfsdk.State) types.String {
	t.Helper()
	if s.Raw.IsNull() {
		t.Fatal("state is empty")
	}
	return modelOf(t, s).Description
}

func TestImportMapsEmptyAPIDescriptionToNull(t *testing.T) {
	cases := []struct {
		name string
		body string
		want types.String
	}{
		{"JSON null (6 of 39 SIT jobs, A12)", nullDescJobBody, types.StringNull()},
		{"key missing", `{"name":"SIT_TEST_JOB","schedules":null,"dataStores":[]}`, types.StringNull()},
		{"empty string", jobBodyWithDescription(""), types.StringNull()},
		{"non-empty string (33 of 39 SIT jobs, A12)", jobBodyWithDescription("imported job"), types.StringValue("imported job")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMockedResource(t, jobGetHandler(t, tc.body))
			imp, rd := importThenRead(t, r, testJobID)
			if imp.Diagnostics.HasError() || rd.Diagnostics.HasError() {
				t.Fatalf("import/read: %v %v", imp.Diagnostics, rd.Diagnostics)
			}
			if got := descOf(t, imp.State); !got.Equal(tc.want) {
				t.Errorf("description after ImportState = %#v, want %#v", got, tc.want)
			}
			// The Read that follows import must not turn null back into "".
			if got := descOf(t, rd.State); !got.Equal(tc.want) {
				t.Errorf("description after the Read that follows import = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestReadKeepsNullDescriptionOnlyWhenAPIIsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		prior types.String
		body  string
		want  types.String
	}{
		{"null state, API null: stays null", types.StringNull(), nullDescJobBody, types.StringNull()},
		{"null state, API \"\": stays null", types.StringNull(), jobBodyWithDescription(""), types.StringNull()},
		{"null state, API set: drift is shown", types.StringNull(), jobBodyWithDescription("added in BICC"), types.StringValue("added in BICC")},
		{"set state, API set: API value", types.StringValue("old"), jobBodyWithDescription("new"), types.StringValue("new")},
		{"set state, API null: API value (\"\"), so drift from config is shown", types.StringValue("old"), nullDescJobBody, types.StringValue("")},
		{"\"\" state, API null: stays \"\"", types.StringValue(""), nullDescJobBody, types.StringValue("")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMockedResource(t, jobGetHandler(t, tc.body))
			resp := runRead(t, r, stateWithDescription(t, tc.prior))
			if resp.Diagnostics.HasError() {
				t.Fatalf("read: %v", resp.Diagnostics)
			}
			if got := descOf(t, resp.State); !got.Equal(tc.want) {
				t.Errorf("description = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func createWithDescription(t *testing.T, r *biccJobResource, desc types.String) *resource.CreateResponse {
	t.Helper()
	req, resp := newCreateReq(t)
	var m biccJobModel
	if diags := req.Plan.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("reading plan: %v", diags)
	}
	m.Description = desc
	if diags := req.Plan.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("setting plan: %v", diags)
	}
	r.Create(context.Background(), req, resp)
	return resp
}

func createUpdateHandler(t *testing.T, getBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPut {
			_, _ = w.Write([]byte(`{"status":"success","id":"4242"}`))
			return
		}
		if req.Method != http.MethodGet {
			t.Errorf("unexpected %s", req.Method)
		}
		_, _ = w.Write([]byte(getBody))
	}
}

func TestCreateKeepsPlannedNullDescription(t *testing.T) {
	cases := []struct {
		name string
		plan types.String
		body string
		want types.String
	}{
		{"null plan, API null: state null (matches plan, no inconsistent result)", types.StringNull(), nullDescJobBody, types.StringNull()},
		{"null plan, API \"\": state null", types.StringNull(), jobBodyWithDescription(""), types.StringNull()},
		{"set plan, API same: state set", types.StringValue("desc"), jobBodyWithDescription("desc"), types.StringValue("desc")},
		{"\"\" plan, API null: state \"\" (matches plan)", types.StringValue(""), nullDescJobBody, types.StringValue("")},
		{"null plan, API set: API value (BICC invented it; core will report it)", types.StringNull(), jobBodyWithDescription("x"), types.StringValue("x")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMockedResource(t, createUpdateHandler(t, tc.body))
			resp := createWithDescription(t, r, tc.plan)
			if resp.Diagnostics.HasError() {
				t.Fatalf("create: %v", resp.Diagnostics)
			}
			if got := descOf(t, resp.State); !got.Equal(tc.want) {
				t.Errorf("description = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func updateWithDescription(t *testing.T, r *biccJobResource, planDesc, stateDesc types.String) *resource.UpdateResponse {
	t.Helper()
	state := stateWithDescription(t, stateDesc)
	plan := tfsdk.Plan{Schema: state.Schema}
	m := modelOf(t, state)
	m.Description = planDesc
	if diags := plan.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("building plan: %v", diags)
	}
	resp := &resource.UpdateResponse{State: tfsdk.State{Schema: state.Schema, Raw: plan.Raw.Copy()}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, resp)
	return resp
}

func TestUpdateKeepsPlannedNullDescription(t *testing.T) {
	cases := []struct {
		name  string
		plan  types.String
		state types.String
		body  string
		want  types.String
	}{
		{"null plan, API null: state null", types.StringNull(), types.StringValue("old"), nullDescJobBody, types.StringNull()},
		{"null plan, API \"\": state null", types.StringNull(), types.StringNull(), jobBodyWithDescription(""), types.StringNull()},
		{"set plan, API same: state set", types.StringValue("new"), types.StringNull(), jobBodyWithDescription("new"), types.StringValue("new")},
		{"null plan, API set: API value", types.StringNull(), types.StringNull(), jobBodyWithDescription("x"), types.StringValue("x")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMockedResource(t, createUpdateHandler(t, tc.body))
			resp := updateWithDescription(t, r, tc.plan, tc.state)
			if resp.Diagnostics.HasError() {
				t.Fatalf("update: %v", resp.Diagnostics)
			}
			if got := descOf(t, resp.State); !got.Equal(tc.want) {
				t.Errorf("description = %#v, want %#v", got, tc.want)
			}
		})
	}
}
