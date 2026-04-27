// Package-level error helpers for runner. These classify ECS RunTask errors
// into "transient capacity" (worth backing off and retrying) vs everything
// else (config, IAM, quota -- surface unchanged).
package runner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
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

// transientFailureReasonSubstrings lists case-insensitive substrings that
// match documented transient ECS failure reasons.
//
// Sources:
//   - AWS ECS dev guide "API failure reason messages" page documents
//     RESOURCE:CPU, RESOURCE:MEMORY, RESOURCE:ENI, AGENT,
//     EMPTY CAPACITY PROVIDER, NO ACTIVE INSTANCES.
//   - The ECS agent emits RESOURCE:PORTS / RESOURCE:PORTS_TCP /
//     RESOURCE:PORTS_UDP for bridge-mode port collisions; matched via the
//     RESOURCE:PORTS prefix.
//   - AWS Knowledge Center documents the Fargate "Capacity is unavailable"
//     wording.
var transientFailureReasonSubstrings = []string{
	"resource:cpu",
	"resource:memory",
	"resource:eni",
	"resource:ports",
	"capacity is unavailable",
	"agent",
	"empty capacity provider",
	"no active instances",
}

// isTransientFailureReason reports whether any element of failures has a
// Reason matching one of the transient substrings (case-insensitive).
func isTransientFailureReason(failures []ecsTypes.Failure) bool {
	for _, f := range failures {
		if f.Reason == nil {
			continue
		}
		reason := strings.ToLower(aws.ToString(f.Reason))
		for _, sub := range transientFailureReasonSubstrings {
			if strings.Contains(reason, sub) {
				return true
			}
		}
	}
	return false
}

// joinFailureReasons formats a list of ECS failures as "<arn>: <reason>; "
// concatenated, preserving the format the previous inline code produced.
func joinFailureReasons(failures []ecsTypes.Failure) string {
	var b strings.Builder
	for _, f := range failures {
		fmt.Fprintf(&b, "%s: %s; ", aws.ToString(f.Arn), aws.ToString(f.Reason))
	}
	return b.String()
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
