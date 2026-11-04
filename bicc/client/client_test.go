package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

	t.Run("404 returns typed not-found error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		_, err := c.GetJob(context.Background(), 999)
		if err == nil {
			t.Fatal("expected error for 404")
		}
		assertNotFound(t, err, 999, http.MethodGet, http.StatusNotFound)
	})

	t.Run("non-404 failures return an untyped error", func(t *testing.T) {
		for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest} {
			status := status
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			c := newTestClient(t, srv)
			_, err := c.GetJob(context.Background(), 1)
			srv.Close()
			if err == nil {
				t.Fatalf("status %d: expected error", status)
			}
			assertNotTyped(t, err, status)
		}
	})

	t.Run("200 with invalid JSON returns an untyped error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not json`))
		}))
		defer srv.Close()
		_, err := newTestClient(t, srv).GetJob(context.Background(), 1)
		if err == nil {
			t.Fatal("expected error")
		}
		assertNotTyped(t, err, 200)
	})

	t.Run("network error returns an untyped error", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		c := newTestClient(t, srv)
		srv.Close() // connection refused from here on
		_, err := c.GetJob(context.Background(), 1)
		if err == nil {
			t.Fatal("expected error")
		}
		assertNotTyped(t, err, 0)
	})
}

// assertNotFound checks that err is the typed not-found error through both errors.Is and errors.As.
func assertNotFound(t *testing.T, err error, jobID int64, method string, status int) {
	t.Helper()
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false for %v", err)
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("errors.As(*NotFoundError) = false for %v", err)
	}
	if nf.JobID != jobID || nf.Method != method || nf.StatusCode != status {
		t.Errorf("NotFoundError = %+v, want job %d, %s, %d", *nf, jobID, method, status)
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound = false")
	}
}

func assertNotTyped(t *testing.T, err error, status int) {
	t.Helper()
	if errors.Is(err, ErrNotFound) || IsNotFound(err) {
		t.Errorf("status %d: error must not be a not-found error: %v", status, err)
	}
}

// -------------------------------------------------------------------------
// isJobNotFound — the single not-found predicate
// -------------------------------------------------------------------------

// UNVERIFIED (ASSUMPTIONS A5/A6): only 404 is treated as "job missing". When the live
// checks show what BICC really returns, change isJobNotFound and extend this table.
func TestIsJobNotFound(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"404", http.StatusNotFound, "", true},
		{"404 with body", http.StatusNotFound, `{"title":"Not Found"}`, true},
		{"200", http.StatusOK, "", false},
		{"204", http.StatusNoContent, "", false},
		{"400", http.StatusBadRequest, "", false},
		{"401", http.StatusUnauthorized, "", false},
		{"403", http.StatusForbidden, "", false},
		// A9: BICC answers 500 for a missing datastore key. A bare 500 must NOT be treated
		// as "job gone": it is indistinguishable from an outage until A6 is run.
		{"500", http.StatusInternalServerError, "", false},
		{"500 with not-found-looking body", http.StatusInternalServerError, `{"detail":"not found"}`, false},
		{"503", http.StatusServiceUnavailable, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isJobNotFound(tc.status, []byte(tc.body)); got != tc.want {
				t.Errorf("isJobNotFound(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
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

	t.Run("404 returns typed not-found error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		err := newTestClient(t, srv).DeleteJob(context.Background(), 7)
		if err == nil {
			t.Fatal("expected error for 404")
		}
		assertNotFound(t, err, 7, http.MethodDelete, http.StatusNotFound)
	})

	t.Run("500 returns an untyped error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := newTestClient(t, srv).DeleteJob(context.Background(), 7)
		if err == nil {
			t.Fatal("expected error for 500")
		}
		assertNotTyped(t, err, 500)
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
