package exception

import (
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
