/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2025 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package certificatecleanup

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestGetCaBundlesToBeMarkedOrCleanedUp(t *testing.T) {
	RegisterTestingT(t)
	_, certClient := inits()

	caBundlesToBeMarked, caBundlesToBeDeleted, err := getCaBundlesToBeMarkedOrCleanedUp(certClient, "compartmentId")
	// These numbers are based on the examples defined in listCaBundle and listAssociation responses
	Expect(len(caBundlesToBeMarked)).To(Equal(2))
	Expect(len(caBundlesToBeDeleted)).To(Equal(1))
	Expect(err).To(BeNil())

	caBundlesToBeMarked, caBundlesToBeDeleted, err = getCaBundlesToBeMarkedOrCleanedUp(certClient, "error")
	Expect(len(caBundlesToBeMarked)).To(Equal(0))
	Expect(len(caBundlesToBeDeleted)).To(Equal(0))
	Expect(err).ToNot(BeNil())
}

func TestGetCertificatesToBeMarkedOrCleanedUp(t *testing.T) {
	RegisterTestingT(t)
	_, certClient := inits()

	certificatesToBeMarked, certificatesToBeDeleted, err := getCertificatesToBeMarkedOrCleanedUp(certClient, "compartmentId")
	// These numbers are based on the examples defined in listCertificate and listAssociation responses
	Expect(len(certificatesToBeMarked)).To(Equal(2))
	Expect(len(certificatesToBeDeleted)).To(Equal(1))
	Expect(err).To(BeNil())

	certificatesToBeMarked, certificatesToBeDeleted, err = getCertificatesToBeMarkedOrCleanedUp(certClient, "error")
	Expect(len(certificatesToBeMarked)).To(Equal(0))
	Expect(len(certificatesToBeDeleted)).To(Equal(0))
	Expect(err).ToNot(BeNil())
}

func TestHasDeletionTimePassed(t *testing.T) {
	RegisterTestingT(t)

	timeInPast := time.Now().Add(-time.Minute).Format(time.RFC3339)
	timeInFuture := time.Now().Add(time.Minute).Format(time.RFC3339)
	unparsableTime := "unparsable"

	hasPassed, err := hasDeletionTimePassed(timeInPast)
	Expect(hasPassed).To(BeTrue())
	Expect(err).To(BeNil())

	hasPassed, err = hasDeletionTimePassed(timeInFuture)
	Expect(hasPassed).To(BeFalse())
	Expect(err).To(BeNil())

	hasPassed, err = hasDeletionTimePassed(unparsableTime)
	Expect(hasPassed).To(BeFalse())
	Expect(err).ToNot(BeNil())
}
