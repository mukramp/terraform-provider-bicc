package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotFound is matched by errors.Is for every *NotFoundError.
var ErrNotFound = errors.New("not found")

// NotFoundError means the BICC API answered that the job does not exist, as decided
// by isJobNotFound. It is returned by GetJob and DeleteJob and by nothing else.
// Network errors, auth errors, 5xx and unparseable bodies are never NotFoundError.
type NotFoundError struct {
	JobID      int64
	Method     string // HTTP method of the request that got the not-found answer
	StatusCode int    // HTTP status that was classified as not-found
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("job %d not found (%s returned HTTP %d)", e.JobID, e.Method, e.StatusCode)
}

// Is makes errors.Is(err, ErrNotFound) true for a *NotFoundError.
func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// IsNotFound reports whether err (or an error it wraps) is a *NotFoundError.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// isJobNotFound is the ONE place that decides whether a response to GET/DELETE on
// /biacm/rest/meta/jobs/{id} means "the job does not exist".
//
// OBSERVED 2026-11-04 on SIT (docs/ASSUMPTIONS.md A6, read-only GETs): a numeric id that
// does not exist (1, -5, a deleted id) returns HTTP 400 with the JSON body
//
//	{"code":"Bad Request","message":"Invalid Job Id provided"}
//
// "Deleted" and "never existed" cannot be told apart. A non-numeric or overflowing id
// returns a plain-text HTTP 404 "Not Found" from the routing layer, which is NOT a
// missing job. A repeat DELETE of a missing id returns HTTP 200 (A5), so DELETE never
// reaches this predicate in the live case.
//
// The predicate is deliberately narrow and true only for: status 400, a JSON body whose
// code is exactly "Bad Request" and whose message contains "invalid job id"
// (case-insensitive). In particular:
//   - a bare 404 is never "job gone": a wrong host, port or proxy could answer 404 for
//     every path, and Read would then drop every job from state (the plan would
//     recreate them all);
//   - any other 400 (for example a JBO-26048 constraint violation), 5xx, 401 and 403
//     are errors, not "job gone".
//
// OPEN RISK (A6, unchecked): the same 400 may also be returned for an existing job the
// account cannot see (permissions). If so, Read would drop such a job from state. Do
// not widen this function; narrow it further if that is ever observed.
func isJobNotFound(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return false
	}
	return e.Code == "Bad Request" && strings.Contains(strings.ToLower(e.Message), "invalid job id")
}
