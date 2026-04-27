package runner

import (
	"errors"
	"testing"

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
