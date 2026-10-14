package s3compat

import (
	"github.com/aws/smithy-go"
)

// apiErrorStub is a minimal smithy.APIError used to drive the classifier without
// a network call.
//
// It is defined here rather than constructing a real smithy error because the
// classifier reads only the code and the status, and a stub keeps the test honest
// about that dependency.
type apiErrorStub struct {
	status int
	code   string
	msg    string
}

func (e *apiErrorStub) ErrorCode() string             { return e.code }
func (e *apiErrorStub) ErrorMessage() string          { return e.msg }
func (e *apiErrorStub) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }
func (e *apiErrorStub) Error() string                 { return e.code + ": " + e.msg }

// statusError adds an HTTP status so classifySDKError can read it through the
// same interface a real smithy response error satisfies.
type statusError struct {
	*apiErrorStub
}

func (e *statusError) HTTPStatusCode() int { return e.status }
