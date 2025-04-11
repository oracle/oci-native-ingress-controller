package exception

import (
	"errors"
	"fmt"
	. "github.com/onsi/gomega"
	"testing"
)

func TestHasTransientError(t *testing.T) {
	RegisterTestingT(t)

	normalErr := errors.New("test-error")
	transientErr := NewTransientError(normalErr)
	wrappedTransientError := fmt.Errorf("wrapped err: %w", transientErr)

	Expect(HasTransientError(normalErr)).To(BeFalse())
	Expect(HasTransientError(transientErr)).To(BeTrue())
	Expect(HasTransientError(wrappedTransientError)).To(BeTrue())
}
