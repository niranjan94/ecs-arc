package runner

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/smithy-go"
)

// fakeAPIError is a minimal smithy.APIError for testing. The smithy.APIError
// interface is what aws-sdk-go-v2 errors implement; matching against it lets
// the classifier work without depending on each ecs/types.* concrete error.
type fakeAPIError struct{ code string }

func (e *fakeAPIError) Error() string                 { return e.code }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.code }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestIsTransientFailureReason(t *testing.T) {
	tests := []struct {
		name     string
		failures []ecsTypes.Failure
		want     bool
	}{
		{"RESOURCE:CPU", failureWith("RESOURCE:CPU"), true},
		{"RESOURCE:MEMORY", failureWith("RESOURCE:MEMORY"), true},
		{"RESOURCE:ENI", failureWith("RESOURCE:ENI"), true},
		{"RESOURCE:PORTS_TCP", failureWith("RESOURCE:PORTS_TCP"), true},
		{"RESOURCE:PORTS_UDP", failureWith("RESOURCE:PORTS_UDP"), true},
		{"RESOURCE:PORTS bare", failureWith("RESOURCE:PORTS"), true},
		{"Capacity is unavailable (Fargate)", failureWith("Capacity is unavailable at this time. Please try again later or in a different availability zone."), true},
		{"AGENT", failureWith("AGENT"), true},
		{"EMPTY CAPACITY PROVIDER", failureWith("EMPTY CAPACITY PROVIDER"), true},
		{"NO ACTIVE INSTANCES", failureWith("NO ACTIVE INSTANCES"), true},
		{"case insensitive: resource:cpu", failureWith("resource:cpu"), true},
		{"second failure transient, first not", []ecsTypes.Failure{
			{Reason: aws.String("MISSING")},
			{Reason: aws.String("RESOURCE:CPU")},
		}, true},
		{"missing role -- not transient", failureWith("Task failed ELB health checks"), false},
		{"empty list -- not transient", nil, false},
		{"nil reason -- not transient", []ecsTypes.Failure{{Reason: nil}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientFailureReason(tc.failures); got != tc.want {
				t.Errorf("isTransientFailureReason(%v) = %v, want %v", tc.failures, got, tc.want)
			}
		})
	}
}

func failureWith(reason string) []ecsTypes.Failure {
	return []ecsTypes.Failure{{
		Arn:    aws.String("arn:aws:ecs:us-east-1:123:task/x"),
		Reason: aws.String(reason),
	}}
}

func TestJoinFailureReasons(t *testing.T) {
	got := joinFailureReasons([]ecsTypes.Failure{
		{Arn: aws.String("a"), Reason: aws.String("RESOURCE:CPU")},
		{Arn: aws.String("b"), Reason: aws.String("AGENT")},
	})
	want := "a: RESOURCE:CPU; b: AGENT; "
	if got != want {
		t.Errorf("joinFailureReasons = %q, want %q", got, want)
	}
}

func TestIsTransientAPIError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"ThrottlingException", &fakeAPIError{code: "ThrottlingException"}, true},
		{"ServerException", &fakeAPIError{code: "ServerException"}, true},
		{"throttlingexception lower-case", &fakeAPIError{code: "throttlingexception"}, true},
		{"ClientException is not transient", &fakeAPIError{code: "ClientException"}, false},
		{"InvalidParameterException is not transient", &fakeAPIError{code: "InvalidParameterException"}, false},
		{"AccessDeniedException is not transient", &fakeAPIError{code: "AccessDeniedException"}, false},
		{"ClusterNotFoundException is not transient", &fakeAPIError{code: "ClusterNotFoundException"}, false},
		{"BlockedException is not transient", &fakeAPIError{code: "BlockedException"}, false},
		{"plain error is not transient", errors.New("network timeout"), false},
		{"nil error is not transient", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientAPIError(tc.err); got != tc.want {
				t.Errorf("isTransientAPIError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
