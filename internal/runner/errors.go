// Package-level error helpers for runner. These classify ECS RunTask errors
// into "transient capacity" (worth backing off and retrying) vs everything
// else (config, IAM, quota -- surface unchanged).
package runner

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

// ErrTransientCapacity wraps RunTask failures attributed to ECS capacity
// exhaustion or upstream throttling. Callers detect it with errors.Is and
// engage a backoff before the next attempt.
var ErrTransientCapacity = errors.New("ecs: transient capacity unavailable")

// transientAPIErrorCodes lists ECS API error codes that mean "try again
// later". Matched case-insensitively against smithy.APIError.ErrorCode().
//
// Sources:
//   - ECS Common Errors page documents ThrottlingException.
//   - RunTask API Errors documents ServerException as 5xx server-side error.
//
// RequestLimitExceeded is intentionally absent (EC2 idiom, not ECS).
// ClusterContainsNoContainerInstancesException is intentionally absent
// (not a real ECS exception; empty-cluster surfaces as a Failure.Reason).
var transientAPIErrorCodes = map[string]struct{}{
	"throttlingexception": {},
	"serverexception":     {},
}

// isTransientAPIError reports whether err is an ECS API error whose code
// indicates a transient condition. Returns false for nil, plain errors,
// and any code not on the allow-list.
func isTransientAPIError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	_, ok := transientAPIErrorCodes[strings.ToLower(apiErr.ErrorCode())]
	return ok
}
