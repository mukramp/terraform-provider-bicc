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
// by isJobNotFound (HTTP 400 "Invalid Job Id provided", observed on SIT 2026-11-04, A6).
// It is returned by GetJob and by nothing else (DeleteJob has no not-found mapping).
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

// ErrUnusableResponse is matched by errors.Is for an error returned by CreateOrUpdateJob
// when BICC answered the PUT with HTTP 200/201 but the body could not be decoded into a
// job id. The job may exist on BICC; the caller cannot track it. The error text is
// unchanged from before (the decoder's message); only the type is new.
var ErrUnusableResponse = errors.New("PUT acknowledged but response unusable")

type unusableResponseError struct{ err error }

func (e *unusableResponseError) Error() string        { return e.err.Error() }
func (e *unusableResponseError) Unwrap() error        { return e.err }
func (e *unusableResponseError) Is(target error) bool { return target == ErrUnusableResponse }

// IsNotFound reports whether err (or an error it wraps) is a *NotFoundError.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// isJobNotFound is the ONE place that decides whether a response to GET on
// /biacm/rest/meta/jobs/{id} means "the job does not exist".
//
// OBSERVED 2026-11-04 on SIT (docs/ASSUMPTIONS.md A6, read-only GETs): a numeric id that
// does not exist (1, -5, a deleted id) returns HTTP 400 with the JSON body
//
//	{"code":"Bad Request","message":"Invalid Job Id provided"}
//
// "Deleted" and "never existed" cannot be told apart. A non-numeric or overflowing id
// returns a plain-text HTTP 404 "Not Found" from the routing layer, which is NOT a
// missing job. A repeat DELETE of a missing id returns HTTP 200 (A5); DeleteJob does not use
// this predicate, and any non-2xx DELETE is an error.
//
// The predicate is deliberately narrow and true only for: status 400 and a JSON body whose
// code is exactly "Bad Request" and whose message, trimmed of surrounding whitespace, is
// exactly "Invalid Job Id provided" (the only observed text; case-sensitive, no substring
// match, so near misses such as "Invalid Job state" or "Invalid column" are errors). In
// particular:
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
	return e.Code == "Bad Request" && strings.TrimSpace(e.Message) == "Invalid Job Id provided"
}
