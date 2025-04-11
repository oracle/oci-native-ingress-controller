package exception

import (
	"errors"
	"github.com/oracle/oci-go-sdk/v65/common"
)

// helper struct since the oci go sdk doesn't let you create errors directly
// since the struct is private...
type NotFoundServiceError struct {
	common.ServiceError
}

func (e *NotFoundServiceError) GetHTTPStatusCode() int {
	return 404
}

// The human-readable error string as sent by the service
func (e *NotFoundServiceError) GetMessage() string {
	return "NotFound"
}

// A short error code that defines the error, meant for programmatic parsing.
// See https://docs.cloud.oracle.com/Content/API/References/apierrors.htm
func (e *NotFoundServiceError) GetCode() string {
	return "NotFound"
}

// Unique Oracle-assigned identifier for the request.
// If you need to contact Oracle about a particular request, please provide the request ID.
func (e *NotFoundServiceError) GetOpcRequestID() string {
	return "fakeopcrequestid"
}

func (e *NotFoundServiceError) Error() string {
	return "NotFound"
}

type ConflictServiceError struct {
	common.ServiceError
}

func (e *ConflictServiceError) GetHTTPStatusCode() int {
	return 409
}

func (e *ConflictServiceError) GetMessage() string {
	return "Conflict"
}

func (e *ConflictServiceError) GetCode() string {
	return "Conflict"
}

func (e *ConflictServiceError) GetOpcRequestID() string {
	return "fakeopcrequestid"
}

func (e *ConflictServiceError) Error() string {
	return "Conflict"
}

// TransientError : We will not publish events for an expected transient errors
type TransientError struct {
	wrappedError error
}

func (e TransientError) Error() string {
	return e.wrappedError.Error()
}

func (e TransientError) GetWrappedError() error {
	return e.wrappedError
}

func NewTransientError(err error) TransientError {
	return TransientError{wrappedError: err}
}

// HasTransientError is true if err has a TransientError in its wrap chain
func HasTransientError(err error) bool {
	transientError := TransientError{}
	return errors.As(err, &transientError)
}
