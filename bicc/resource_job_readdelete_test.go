package bicc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/tra-jkv/terraform-provider-bicc/bicc/client"
)

// Resource-level tests for Read and Delete (PLAN task 5, KI-5). They call the
// resource methods the way the plugin framework does, against an httptest mock.
// No live BICC call is made (DR-8).
//
// The mock shapes are the ones OBSERVED on SIT on 2026-11-04 (ASSUMPTIONS A5, A6):
//   - GET of a numeric id that does not exist: HTTP 400 {"code":"Bad Request","message":"Invalid Job Id provided"}
//   - GET of a malformed id: HTTP 404, plain text "Not Found" (routing level, NOT a missing job)
//   - DELETE of a missing id: HTTP 200
//
// Open risk, not covered because it was not observed: the same 400 may be returned for an
// existing job the account cannot see (permissions); Read would then drop it from state.

const (
	testJobID          = "4242"
	liveMissingJobBody = `{"code":"Bad Request","message":"Invalid Job Id provided"}`
)

func newMockedResource(t *testing.T, handler http.HandlerFunc) (*biccJobResource, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	c := client.NewClient(&client.Config{Host: "unused", Username: "u", Password: "p"})
	c.BaseURL = srv.URL

	r := &biccJobResource{}
	cfgResp := &resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: c}, cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("configure: %v", cfgResp.Diagnostics)
	}
	return r, &calls
}

func resourceSchema(t *testing.T) *resource.SchemaResponse {
	t.Helper()
	r := &biccJobResource{}
	sr := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, sr)
	return sr
}

// existingState builds a state holding a job with the given ID, name and description.
func existingState(t *testing.T) tfsdk.State {
	t.Helper()
	sr := resourceSchema(t)
	state := tfsdk.State{Schema: sr.Schema}
	model := biccJobModel{
		ID:          types.StringValue(testJobID),
		Name:        types.StringValue("SIT_TEST_JOB"),
		Description: types.StringValue("old description"),
		DataStores:  types.SetValueMust(types.ObjectType{AttrTypes: dataStoreAttrTypes}, []attr.Value{}),
	}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return state
}

func runRead(t *testing.T, r *biccJobResource, state tfsdk.State) *resource.ReadResponse {
	t.Helper()
	// The framework seeds resp.State with the prior state; mimic that.
	resp := &resource.ReadResponse{State: tfsdk.State{Schema: state.Schema, Raw: state.Raw.Copy()}}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	return resp
}

func runDelete(t *testing.T, r *biccJobResource, state tfsdk.State) *resource.DeleteResponse {
	t.Helper()
	resp := &resource.DeleteResponse{State: tfsdk.State{Schema: state.Schema, Raw: state.Raw.Copy()}}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	return resp
}

func stateDescription(t *testing.T, s tfsdk.State) (types.String, bool) {
	t.Helper()
	if s.Raw.IsNull() {
		return types.String{}, false
	}
	var m biccJobModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("reading state: %v", diags)
	}
	return m.Description, true
}

func TestReadRemovesStateOnlyOnNotFound(t *testing.T) {
	t.Run("400 Invalid Job Id (live shape) removes the resource from state without error", func(t *testing.T) {
		r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(liveMissingJobBody))
		})
		resp := runRead(t, r, existingState(t))

		if resp.Diagnostics.HasError() {
			t.Fatalf("not-found must not be an error, got: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Fatal("state must be removed on a real not-found")
		}
	})

	errorCases := []struct {
		name   string
		status int
		body   string
	}{
		// A bare 404 is NOT "job gone" (A6): a wrong host/port/proxy could answer 404 to
		// everything, and Read would drop every job from state.
		{"404 Not Found (live routing shape) errors and keeps state", http.StatusNotFound, `Not Found`},
		{"404 empty errors and keeps state", http.StatusNotFound, ``},
		{"400 other message errors and keeps state", http.StatusBadRequest, `{"code":"Bad Request","message":"JBO-26048: Constraint \"X_FK\" violated during post operation"}`},
		{"400 empty body errors and keeps state", http.StatusBadRequest, ``},
		{"400 non-JSON errors and keeps state", http.StatusBadRequest, `Invalid Job Id provided`},
		{"400 live body with different code errors and keeps state", http.StatusBadRequest, `{"code":"Conflict","message":"Invalid Job Id provided"}`},
		{"500 with the live body errors and keeps state", http.StatusInternalServerError, liveMissingJobBody},
		{"500 errors and keeps state", http.StatusInternalServerError, `{"error":"boom"}`},
		{"503 errors and keeps state", http.StatusServiceUnavailable, ``},
		{"401 errors and keeps state", http.StatusUnauthorized, ``},
		{"403 errors and keeps state", http.StatusForbidden, `Blocked: non-whitelisted domain`},
		{"200 with invalid JSON errors and keeps state", http.StatusOK, `not json`},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			resp := runRead(t, r, existingState(t))

			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error diagnostic")
			}
			desc, ok := stateDescription(t, resp.State)
			if !ok {
				t.Fatal("state was removed on a non-not-found error (KI-5)")
			}
			if desc.ValueString() != "old description" {
				t.Errorf("state was modified: description = %q", desc.ValueString())
			}
		})
	}

	t.Run("network error errors and keeps state", func(t *testing.T) {
		r, _ := newMockedResource(t, func(http.ResponseWriter, *http.Request) {})
		// Point the client at a closed server: connection refused.
		dead := httptest.NewServer(http.NotFoundHandler())
		r.client.BaseURL = dead.URL
		dead.Close()

		resp := runRead(t, r, existingState(t))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic")
		}
		if _, ok := stateDescription(t, resp.State); !ok {
			t.Fatal("state was removed on a network error (KI-5)")
		}
	})

	t.Run("timeout errors and keeps state", func(t *testing.T) {
		release := make(chan struct{})
		r, _ := newMockedResource(t, func(http.ResponseWriter, *http.Request) {
			<-release // hang until the client gives up
		})
		t.Cleanup(func() { close(release) })
		r.client.HTTPClient.Timeout = 50 * time.Millisecond

		resp := runRead(t, r, existingState(t))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic")
		}
		if _, ok := stateDescription(t, resp.State); !ok {
			t.Fatal("state was removed on a timeout (KI-5)")
		}
	})

	t.Run("200 updates state (happy path unchanged)", func(t *testing.T) {
		r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"SIT_TEST_JOB","description":"new description","dataStores":[],"schedules":null}`))
		})
		resp := runRead(t, r, existingState(t))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", resp.Diagnostics)
		}
		desc, ok := stateDescription(t, resp.State)
		if !ok || desc.ValueString() != "new description" {
			t.Fatalf("description = %q (present=%v), want %q", desc.ValueString(), ok, "new description")
		}
	})
}

func TestDeleteIsIdempotentOnNotFound(t *testing.T) {
	// OBSERVED 2026-11-04 (A5): DELETE of an already-missing id returns HTTP 200, so a
	// repeated or racing delete succeeds through the ordinary success path.
	t.Run("200 for an already-missing job (live shape) succeeds", func(t *testing.T) {
		r, calls := newMockedResource(t, func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodDelete || !strings.HasSuffix(req.URL.Path, "/"+testJobID) {
				t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		})
		resp := runDelete(t, r, existingState(t))
		if resp.Diagnostics.HasError() {
			t.Fatalf("delete of an already-missing job must succeed, got: %v", resp.Diagnostics)
		}
		if got := atomic.LoadInt32(calls); got != 1 {
			t.Errorf("expected exactly 1 request (no retries), got %d", got)
		}
	})

	// DEFENSIVE: BICC has not been observed to answer a DELETE with the GET not-found
	// shape. If it ever does, Delete still succeeds.
	t.Run("defensive: 400 Invalid Job Id body counts as success", func(t *testing.T) {
		r, calls := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(liveMissingJobBody))
		})
		resp := runDelete(t, r, existingState(t))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", resp.Diagnostics)
		}
		if got := atomic.LoadInt32(calls); got != 1 {
			t.Errorf("expected exactly 1 request (no retries), got %d", got)
		}
	})

	t.Run("204 succeeds", func(t *testing.T) {
		r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		if resp := runDelete(t, r, existingState(t)); resp.Diagnostics.HasError() {
			t.Errorf("unexpected error: %v", resp.Diagnostics)
		}
	})

	failCases := []struct {
		name   string
		status int
		body   string
	}{
		{"500", http.StatusInternalServerError, ``},
		{"401", http.StatusUnauthorized, ``},
		{"403", http.StatusForbidden, ``},
		{"400 other message", http.StatusBadRequest, `{"code":"Bad Request","message":"JBO-26048: Constraint violated"}`},
		{"400 empty", http.StatusBadRequest, ``},
		// A bare 404 is not "job gone" (A6), so Delete reports it instead of hiding it.
		{"404 Not Found", http.StatusNotFound, `Not Found`},
		{"500 with the live body", http.StatusInternalServerError, liveMissingJobBody},
	}
	for _, tc := range failCases {
		tc := tc
		t.Run(tc.name+" fails without retry", func(t *testing.T) {
			r, calls := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			resp := runDelete(t, r, existingState(t))
			if !resp.Diagnostics.HasError() {
				t.Fatalf("status %d: expected an error diagnostic", tc.status)
			}
			if got := atomic.LoadInt32(calls); got != 1 {
				t.Errorf("expected exactly 1 request (no retries), got %d", got)
			}
		})
	}
}

// Create: PUT upserts by name. If the PUT succeeded and the re-read fails, Create must NOT
// write any state: state with an id would be saved tainted and the next apply would plan
// DELETE (about 150s on SIT) and create, which would delete a real job if the name matched
// one created outside Terraform. With no state the next apply re-sends the same PUT, which
// converges (A1 unverified for rename). The job is an orphan until then; the error says so.
func newCreateReq(t *testing.T) (resource.CreateRequest, *resource.CreateResponse) {
	t.Helper()
	sr := resourceSchema(t)
	plan := tfsdk.Plan{Schema: sr.Schema}
	model := biccJobModel{
		ID:          types.StringUnknown(),
		Name:        types.StringValue("SIT_TEST_JOB"),
		Description: types.StringValue("desc"),
		DataStores:  types.SetValueMust(types.ObjectType{AttrTypes: dataStoreAttrTypes}, []attr.Value{}),
	}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building plan: %v", diags)
	}
	// The framework hands Create a null state of the schema's type.
	resp := &resource.CreateResponse{State: tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}}
	return resource.CreateRequest{Plan: plan}, resp
}

func TestCreateLeavesStateEmptyWhenReReadFails(t *testing.T) {
	failures := []struct {
		name   string
		status int
		body   string
	}{
		{"re-read 500", http.StatusInternalServerError, `boom`},
		{"re-read 400 Invalid Job Id", http.StatusBadRequest, liveMissingJobBody},
		{"re-read bad JSON", http.StatusOK, `not json`},
	}
	for _, tc := range failures {
		tc := tc
		t.Run(tc.name+" leaves state empty and reports the orphan", func(t *testing.T) {
			var puts int32
			r, _ := newMockedResource(t, func(w http.ResponseWriter, req *http.Request) {
				switch req.Method {
				case http.MethodPut:
					atomic.AddInt32(&puts, 1)
					_, _ = w.Write([]byte(`{"status":"success","id":"555"}`))
				default:
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}
			})
			req, resp := newCreateReq(t)
			r.Create(context.Background(), req, resp)

			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error diagnostic")
			}
			if !resp.State.Raw.IsNull() {
				t.Fatal("state must stay EMPTY when the re-read fails: tainted state would make the next apply delete the job")
			}
			var msg string
			for _, d := range resp.Diagnostics.Errors() {
				msg += d.Summary() + " " + d.Detail()
			}
			for _, want := range []string{"555", "exists on BICC", "upserts by name", "re-sends the same job", "no data loss"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error message lacks %q: %s", want, msg)
				}
			}
			if got := atomic.LoadInt32(&puts); got != 1 {
				t.Errorf("expected exactly 1 PUT, got %d", got)
			}
		})
	}

	t.Run("PUT failure leaves state empty", func(t *testing.T) {
		r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		req, resp := newCreateReq(t)
		r.Create(context.Background(), req, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic")
		}
		if !resp.State.Raw.IsNull() {
			t.Fatal("state must stay empty when the PUT itself failed")
		}
	})

	t.Run("happy path still sets full state", func(t *testing.T) {
		r, _ := newMockedResource(t, func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPut {
				_, _ = w.Write([]byte(`{"status":"success","id":"555"}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"SIT_TEST_JOB","description":"desc","dataStores":[],"schedules":null}`))
		})
		req, resp := newCreateReq(t)
		r.Create(context.Background(), req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", resp.Diagnostics)
		}
		var m biccJobModel
		if diags := resp.State.Get(context.Background(), &m); diags.HasError() {
			t.Fatalf("reading state: %v", diags)
		}
		if m.ID.ValueString() != "555" || m.Name.ValueString() != "SIT_TEST_JOB" || m.DataStores.IsNull() {
			t.Errorf("unexpected state: %+v", m)
		}
	})
}
