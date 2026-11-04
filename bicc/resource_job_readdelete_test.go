package bicc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/tra-jkv/terraform-provider-bicc/bicc/client"
)

// Resource-level tests for Read and Delete (PLAN task 5, KI-5). They call the
// resource methods the way the plugin framework does, against an httptest mock.
// No live BICC call is made (DR-8).

const testJobID = "4242"

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
	t.Run("404 removes the resource from state without error", func(t *testing.T) {
		r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
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
	t.Run("404 counts as success", func(t *testing.T) {
		r, calls := newMockedResource(t, func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodDelete || !strings.HasSuffix(req.URL.Path, "/"+testJobID) {
				t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			}
			w.WriteHeader(http.StatusNotFound)
		})
		resp := runDelete(t, r, existingState(t))
		if resp.Diagnostics.HasError() {
			t.Fatalf("delete of an already-missing job must succeed, got: %v", resp.Diagnostics)
		}
		if got := atomic.LoadInt32(calls); got != 1 {
			t.Errorf("expected exactly 1 request (no retries), got %d", got)
		}
	})

	t.Run("200 and 204 succeed", func(t *testing.T) {
		for _, status := range []int{http.StatusOK, http.StatusNoContent} {
			r, _ := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			if resp := runDelete(t, r, existingState(t)); resp.Diagnostics.HasError() {
				t.Errorf("status %d: unexpected error: %v", status, resp.Diagnostics)
			}
		}
	})

	for _, status := range []int{http.StatusInternalServerError, http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest} {
		status := status
		t.Run(http.StatusText(status)+" fails without retry", func(t *testing.T) {
			r, calls := newMockedResource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			resp := runDelete(t, r, existingState(t))
			if !resp.Diagnostics.HasError() {
				t.Fatalf("status %d: expected an error diagnostic", status)
			}
			if got := atomic.LoadInt32(calls); got != 1 {
				t.Errorf("expected exactly 1 request (no retries), got %d", got)
			}
		})
	}
}
