package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient returns a Client pointed at the given test server.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	cfg := &Config{
		Host:     srv.Listener.Addr().String(),
		Username: "user",
		Password: "pass",
		Port:     0, // unused; BaseURL is overridden below
	}
	c := NewClient(cfg)
	c.BaseURL = srv.URL
	return c
}

// -------------------------------------------------------------------------
// JobResponse.UnmarshalJSON
// -------------------------------------------------------------------------

func TestJobResponseUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantID  int64
		wantErr bool
	}{
		{"id as string", `{"id":"42","status":"success"}`, 42, false},
		{"id as float", `{"id":42,"status":"success"}`, 42, false},
		{"id as large string", `{"id":"999999"}`, 999999, false},
		{"id missing", `{"status":"success"}`, 0, true},
		{"id as invalid string", `{"id":"notanumber"}`, 0, true},
		{"id as null", `{"id":null}`, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var jr JobResponse
			err := json.Unmarshal([]byte(tc.input), &jr)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (ID=%d)", jr.ID)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if jr.ID != tc.wantID {
				t.Errorf("ID: got %d, want %d", jr.ID, tc.wantID)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Column.ToJobColumn
// -------------------------------------------------------------------------

func TestColumnToJobColumn(t *testing.T) {
	src := Column{
		Name:                 "COL1",
		Label:                "Label",
		DataType:             "VARCHAR2",
		Size:                 "100",
		Precision:            "0",
		Scale:                "0",
		IsPrimaryKey:         true,
		IsPopulate:           true,
		IsLastUpdateDate:     false,
		IsNaturalKey:         false,
		IsEffectiveStartDate: false,
		IsCreationDate:       false,
		ColConversion:        nil,
	}

	got := src.ToJobColumn()

	if got.Label != "" || got.DataType != "" || got.Size != "" || got.Precision != "" || got.Scale != "" {
		t.Error("ToJobColumn should strip metadata fields (Label, DataType, Size, Precision, Scale)")
	}
	if got.Name != src.Name || got.IsPrimaryKey != src.IsPrimaryKey || got.IsPopulate != src.IsPopulate {
		t.Error("ToJobColumn should preserve functional fields")
	}
}

// -------------------------------------------------------------------------
// CreateOrUpdateJob — HTTP behaviour
// -------------------------------------------------------------------------

func TestCreateOrUpdateJob(t *testing.T) {
	t.Run("success with string id", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("expected PUT, got %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"123","status":"success"}`))
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		resp, err := c.CreateOrUpdateJob(context.Background(), &Job{Name: "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.ID != 123 {
			t.Errorf("ID: got %d, want 123", resp.ID)
		}
	})

	t.Run("non-200 status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`internal error`))
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		_, err := c.CreateOrUpdateJob(context.Background(), &Job{Name: "test"})
		if err == nil {
			t.Fatal("expected error for non-200 status")
		}
	})
}

// -------------------------------------------------------------------------
// GetJob — HTTP behaviour
// -------------------------------------------------------------------------

func TestGetJob(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"name":"myjob","description":"desc","dataStores":[],"schedules":null}`))
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		job, err := c.GetJob(context.Background(), 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if job.Name != "myjob" {
			t.Errorf("Name: got %q, want %q", job.Name, "myjob")
		}
	})

	t.Run("404 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		_, err := c.GetJob(context.Background(), 999)
		if err == nil {
			t.Fatal("expected error for 404")
		}
	})
}

// -------------------------------------------------------------------------
// GetDataStoreColumns — HTTP behaviour
// -------------------------------------------------------------------------

func TestGetDataStoreColumns(t *testing.T) {
	t.Run("returns columns", func(t *testing.T) {
		body := `{"columns":[{"name":"COL_A","isPrimaryKey":true,"isPopulate":true,"isLastUpdateDate":false,"isNaturalKey":false,"isEffectiveStartDate":false,"isCreationDate":false,"colConversion":null}]}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(body))
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		cols, err := c.GetDataStoreColumns(context.Background(), "SomeDS.Key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cols) != 1 || cols[0].Name != "COL_A" {
			t.Errorf("unexpected columns: %+v", cols)
		}
	})

	t.Run("non-200 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		_, err := c.GetDataStoreColumns(context.Background(), "SomeDS.Key")
		if err == nil {
			t.Fatal("expected error for non-200 status")
		}
	})
}

// A9 (observed 2026-11-03, live SIT, read-only): a data store key that does not
// exist returns HTTP 500 with a JSON body whose "code" is "Internal Server
// Error", not 404. Only the status and that code were observed; the "message"
// text below is invented for the test and is deliberately not asserted on.
// The error must carry the status and the body, and 500 must not be treated as
// "not found".
func TestGetDataStoreColumns_Error500IncludesStatusAndBody(t *testing.T) {
	const body = `{"code":"Internal Server Error","message":"synthetic test message"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	cols, err := c.GetDataStoreColumns(context.Background(), "FscmTopModelAM.Nope.DoesNotExistPVO")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if cols != nil {
		t.Errorf("expected nil columns on error, got %+v", cols)
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") {
		t.Errorf("error should include the status code, got: %s", msg)
	}
	if !strings.Contains(msg, "Internal Server Error") {
		t.Errorf("error should include the response body (code), got: %s", msg)
	}
}

// The key is part of the URL path and must be escaped (KI-6, A8 unverified on
// the live API; escaping is standard URL hygiene). A normal key is unchanged.
func TestGetDataStoreColumns_PathEscaping(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantURI string
	}{
		{
			name:    "normal key unchanged on the wire",
			key:     "HcmTopModelAnalyticsGlobalAM.HcmProfileCoreAM.CountryPVO",
			wantURI: "/biacm/rest/meta/datastores/HcmTopModelAnalyticsGlobalAM.HcmProfileCoreAM.CountryPVO",
		},
		{
			// Not a PathEscape discriminator: net/http escapes a space itself, so
			// this passes with or without url.PathEscape. Kept as a wire-format check.
			name:    "space on the wire (passes without PathEscape too)",
			key:     "My DS.Key",
			wantURI: "/biacm/rest/meta/datastores/My%20DS.Key",
		},
		{
			name:    "percent sign is escaped, not decoded",
			key:     "A%41.Key",
			wantURI: "/biacm/rest/meta/datastores/A%2541.Key",
		},
		{
			name:    "slash is escaped, not a path separator",
			key:     "A/B.Key",
			wantURI: "/biacm/rest/meta/datastores/A%2FB.Key",
		},
		{
			name:    "query and fragment characters are escaped",
			key:     "A?x=1#frag",
			wantURI: "/biacm/rest/meta/datastores/A%3Fx=1%23frag",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotURI, gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotURI = r.RequestURI // raw request target as sent on the wire
				gotQuery = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"columns":[{"name":"C","isPopulate":true}]}`))
			}))
			defer srv.Close()

			c := newTestClient(t, srv)
			if _, err := c.GetDataStoreColumns(context.Background(), tc.key); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotURI != tc.wantURI {
				t.Errorf("request URI: got %q, want %q", gotURI, tc.wantURI)
			}
			if gotQuery != "" {
				t.Errorf("key leaked into the query string: %q", gotQuery)
			}
		})
	}
}

// -------------------------------------------------------------------------
// DeleteJob — HTTP behaviour
// -------------------------------------------------------------------------

func TestDeleteJob(t *testing.T) {
	t.Run("success 200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("expected DELETE, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		if err := c.DeleteJob(context.Background(), 1); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("success 204", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		if err := c.DeleteJob(context.Background(), 1); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("non-200/204 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`bad request`))
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		if err := c.DeleteJob(context.Background(), 1); err == nil {
			t.Fatal("expected error for non-200/204 status")
		}
	})
}

// -------------------------------------------------------------------------
// doRequest — auth headers
// -------------------------------------------------------------------------

func TestDoRequestSetsAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		gotAuth = user + ":" + pass
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"name":"x","dataStores":[],"schedules":null}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.Config.Username = "myuser"
	c.Config.Password = "mypass"

	_, _ = c.GetJob(context.Background(), 1)

	if gotAuth != "myuser:mypass" {
		t.Errorf("basic auth: got %q, want %q", gotAuth, "myuser:mypass")
	}
}
