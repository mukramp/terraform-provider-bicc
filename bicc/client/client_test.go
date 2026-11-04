package client

import (
	"context"
	"encoding/json"
	"errors"
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

	// M-3a: 2xx with a body that has no usable id is typed (ErrUnusableResponse) so Create
	// can say the job may exist; the message text is unchanged for other callers.
	t.Run("2xx with unusable body is ErrUnusableResponse", func(t *testing.T) {
		for _, body := range []string{`{}`, `{"id":"x"}`, `not json`} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			_, err := newTestClient(t, srv).CreateOrUpdateJob(context.Background(), &Job{Name: "test"})
			srv.Close()
			if err == nil || !errors.Is(err, ErrUnusableResponse) {
				t.Errorf("body %q: err = %v, want ErrUnusableResponse", body, err)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "error unmarshaling response: ") {
				t.Errorf("body %q: message changed: %v", body, err)
			}
		}
	})

	t.Run("non-2xx and network errors are not ErrUnusableResponse", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{}`))
		}))
		_, err := newTestClient(t, srv).CreateOrUpdateJob(context.Background(), &Job{Name: "test"})
		srv.Close()
		if err == nil || errors.Is(err, ErrUnusableResponse) {
			t.Errorf("500: err = %v, must be an ordinary error", err)
		}
		c := newTestClient(t, srv) // closed server
		if _, err := c.CreateOrUpdateJob(context.Background(), &Job{Name: "test"}); err == nil || errors.Is(err, ErrUnusableResponse) {
			t.Errorf("network: err = %v, must be an ordinary error", err)
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

	// OBSERVED 2026-11-04 on SIT (ASSUMPTIONS A6): a missing numeric id is HTTP 400 with
	// liveMissingJobBody.
	t.Run("400 Invalid Job Id (live shape) returns typed not-found error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(liveMissingJobBody))
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		_, err := c.GetJob(context.Background(), 999)
		if err == nil {
			t.Fatal("expected error for 400 Invalid Job Id")
		}
		assertNotFound(t, err, 999, http.MethodGet, http.StatusBadRequest)
	})

	// OBSERVED 2026-11-04 (A6): a non-numeric or overflowing id gets a plain-text 404
	// from the routing layer. That is NOT a missing job.
	t.Run("404 Not Found (live routing shape) returns an untyped error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Not Found"))
		}))
		defer srv.Close()

		_, err := newTestClient(t, srv).GetJob(context.Background(), 999)
		if err == nil {
			t.Fatal("expected error for 404")
		}
		assertNotTyped(t, err, http.StatusNotFound)
	})

	t.Run("other failures return an untyped error", func(t *testing.T) {
		for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest, http.StatusNotFound} {
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

// liveMissingJobBody is the body BICC returned on SIT on 2026-11-04 for GET of a numeric
// job id that does not exist (ASSUMPTIONS A6, observed, read-only GET).
const liveMissingJobBody = `{"code":"Bad Request","message":"Invalid Job Id provided"}`

// The predicate is true ONLY for 400 + JSON {code:"Bad Request", message "Invalid Job Id
// provided"} (exact, case-sensitive, message compared after trimming whitespace).
// Everything else, in particular a bare 404, must be false: a wrong host/proxy answering
// 404 for every path would otherwise make Read drop every job from state.
func TestIsJobNotFound(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"400 exact live body", 400, liveMissingJobBody, true},
		{"400 message with surrounding whitespace is trimmed", 400, `{"code":"Bad Request","message":"  Invalid Job Id provided\n"}`, true},
		// Exact match only: the one observed text is "Invalid Job Id provided".
		{"400 message in upper case", 400, `{"code":"Bad Request","message":"INVALID JOB ID provided"}`, false},
		{"400 message in lower case", 400, `{"code":"Bad Request","message":"invalid job id provided"}`, false},
		{"400 message without 'provided'", 400, `{"code":"Bad Request","message":"Invalid Job Id"}`, false},
		{"400 message with extra text", 400, `{"code":"Bad Request","message":"Error: Invalid Job Id provided for request 42"}`, false},
		{"400 message with trailing text", 400, `{"code":"Bad Request","message":"Invalid Job Id provided."}`, false},
		{"400 near miss: Invalid column", 400, `{"code":"Bad Request","message":"Invalid column"}`, false},
		{"400 near miss: Invalid Job state", 400, `{"code":"Bad Request","message":"Invalid Job state"}`, false},
		{"400 near miss: Invalid Data Store Id provided", 400, `{"code":"Bad Request","message":"Invalid Data Store Id provided"}`, false},
		{"400 JSON with extra fields", 400, `{"code":"Bad Request","message":"Invalid Job Id provided","extra":1}`, true},
		{"400 empty body", 400, ``, false},
		{"400 non-JSON body", 400, `Invalid Job Id provided`, false},
		{"400 non-JSON html", 400, `<html>Bad Request</html>`, false},
		{"400 JBO-26048 constraint violation", 400, `{"code":"Bad Request","message":"JBO-26048: Constraint \"X_FK\" violated during post operation"}`, false},
		{"400 Invalid Job Id with a different code", 400, `{"code":"Conflict","message":"Invalid Job Id provided"}`, false},
		{"400 code in other case", 400, `{"code":"bad request","message":"Invalid Job Id provided"}`, false},
		{"400 JSON without message", 400, `{"code":"Bad Request"}`, false},
		{"400 JSON without code", 400, `{"message":"Invalid Job Id provided"}`, false},
		{"400 truncated JSON", 400, `{"code":"Bad Request","message":"Invalid Job Id provided"`, false},
		{"400 message of wrong type", 400, `{"code":"Bad Request","message":42}`, false},
		// Strict parse: a body that json.Unmarshal reports an error for is never "job gone",
		// even if the fields we look for were filled before the error (duplicate key).
		{"400 duplicate message key, second of wrong type", 400, `{"code":"Bad Request","message":"Invalid Job Id provided","message":5}`, false},
		{"400 JSON array", 400, `[{"code":"Bad Request","message":"Invalid Job Id provided"}]`, false},
		{"400 JSON null", 400, `null`, false},
		{"500 with the live body", 500, liveMissingJobBody, false},
		{"503 with the live body", 503, liveMissingJobBody, false},
		{"401 with the live body", 401, liveMissingJobBody, false},
		{"403 with the live body", 403, liveMissingJobBody, false},
		{"404 with the live body", 404, liveMissingJobBody, false},
		{"200 with the live body", 200, liveMissingJobBody, false},
		{"404 Not Found (live routing shape)", 404, `Not Found`, false},
		{"404 empty", 404, ``, false},
		{"404 JSON not-found-looking", 404, `{"title":"Not Found"}`, false},
		{"200 empty", 200, ``, false},
		{"204 empty", 204, ``, false},
		{"500 empty", 500, ``, false},
		// A9: BICC answers 500 for a missing datastore key; 5xx is never "job gone".
		{"500 with not-found-looking body", 500, `{"detail":"not found"}`, false},
		{"503 empty", 503, ``, false},
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

	// OBSERVED 2026-11-04 (A5): DELETE of an already-missing id returns HTTP 200, so the
	// live case is the success path above. DeleteJob has NO not-found mapping: any non-2xx
	// is an error, including the GET not-found shape (400 Invalid Job Id) and a bare 404.
	// Mapping them to success would report a delete that did not happen while the job
	// stays on BICC.
	t.Run("400 with the live GET not-found body is an untyped error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(liveMissingJobBody))
		}))
		defer srv.Close()

		err := newTestClient(t, srv).DeleteJob(context.Background(), 7)
		if err == nil {
			t.Fatal("expected error for 400 on DELETE")
		}
		assertNotTyped(t, err, 400)
	})

	t.Run("404 Not Found is an untyped error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Not Found"))
		}))
		defer srv.Close()

		err := newTestClient(t, srv).DeleteJob(context.Background(), 7)
		if err == nil {
			t.Fatal("expected error for 404 on DELETE")
		}
		assertNotTyped(t, err, 404)
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
