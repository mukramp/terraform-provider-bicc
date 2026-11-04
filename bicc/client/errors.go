package client

import (
	"errors"
	"fmt"
	"net/http"
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
// UNVERIFIED (docs/ASSUMPTIONS.md A5, A6): what BICC returns for a missing job has not
// been observed. HTTP 404 is the assumption. BICC returned HTTP 500, not 404, for a
// missing data store key (A9, observed 2026-11-03), so a missing job may well be a 500
// with a recognisable body. Do NOT widen this to "any 5xx": that would make a transient
// outage look like a deleted job and let Read drop state (KI-5). Once A5/A6 are run, extend
// this function (status plus a specific body marker) and its test, and nothing else.
func isJobNotFound(statusCode int, body []byte) bool {
	_ = body // reserved for a body-marker check once A5/A6 show what BICC sends
	return statusCode == http.StatusNotFound
}
