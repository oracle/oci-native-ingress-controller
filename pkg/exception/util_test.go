package exception

import (
	"errors"
	"fmt"
	. "github.com/onsi/gomega"
	"testing"
)

func TestNotFoundServiceError(t *testing.T) {
	RegisterTestingT(t)

	notfoundServiceError := NotFoundServiceError{}
	Expect(notfoundServiceError.GetHTTPStatusCode()).To(Equal(404))
	Expect(notfoundServiceError.GetMessage()).To(Equal("NotFound"))
	Expect(notfoundServiceError.GetCode()).To(Equal("NotFound"))
	Expect(notfoundServiceError.GetOpcRequestID()).To(Equal("fakeopcrequestid"))
	Expect(notfoundServiceError.Error()).To(Equal("NotFound"))
}

func TestConflictServiceError(t *testing.T) {
	RegisterTestingT(t)

	conflictServiceError := ConflictServiceError{}
	Expect(conflictServiceError.GetHTTPStatusCode()).To(Equal(409))
	Expect(conflictServiceError.GetMessage()).To(Equal("Conflict"))
	Expect(conflictServiceError.GetCode()).To(Equal("Conflict"))
	Expect(conflictServiceError.GetOpcRequestID()).To(Equal("fakeopcrequestid"))
	Expect(conflictServiceError.Error()).To(Equal("Conflict"))
}

func TestTransientError(t *testing.T) {
	RegisterTestingT(t)

	err := errors.New("test-error")
	transientErr := NewTransientError(err)
	Expect(transientErr.GetWrappedError()).To(Equal(err))
	Expect(transientErr.Error()).To(Equal(err.Error()))
}

func TestHasTransientError(t *testing.T) {
	RegisterTestingT(t)

	normalErr := errors.New("test-error")
	transientErr := NewTransientError(normalErr)
	wrappedTransientError := fmt.Errorf("wrapped err: %w", transientErr)

	Expect(HasTransientError(normalErr)).To(BeFalse())
	Expect(HasTransientError(transientErr)).To(BeTrue())
	Expect(HasTransientError(wrappedTransientError)).To(BeTrue())
}
