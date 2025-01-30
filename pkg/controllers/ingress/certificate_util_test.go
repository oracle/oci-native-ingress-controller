/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2024 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package ingress

import (
	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestGetCertificate(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	certId := "id"
	certId2 := "id2"

	certificate, etag, err := GetCertificate(&certId, mockClient.GetCertClient())
	Expect(certificate != nil).Should(BeTrue())
	Expect(etag).Should(Equal("etag"))
	Expect(err).Should(BeNil())

	// cache fetch
	certificate, etag, err = GetCertificate(&certId, mockClient.GetCertClient())
	Expect(certificate != nil).Should(BeTrue())
	Expect(etag).Should(Equal("etag"))
	Expect(err).Should(BeNil())

	certificate, etag, err = GetCertificate(&certId2, mockClient.GetCertClient())
	Expect(certificate != nil).Should(BeTrue())
	Expect(etag).Should(Equal("etag"))
	Expect(err).Should(BeNil())
}

func TestVerifyOrGetCertificateIdByName(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	id := "id"
	name := "cert"
	compartmentId := "compartmentId"
	actualCertificateId, err := VerifyOrGetCertificateIdByName(id, name, compartmentId, mockClient.GetCertClient())
	Expect(err).Should(BeNil())
	Expect(actualCertificateId).Should(Equal(id))

	actualCertificateId, err = VerifyOrGetCertificateIdByName("", name, compartmentId, mockClient.GetCertClient())
	Expect(err).Should(BeNil())
	Expect(actualCertificateId).ShouldNot(Equal(""))
}

func TestFindCertificateWithName(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	name := "name"
	compartmentId := "compartmentId"
	certificateId, err := FindCertificateWithName(name, compartmentId, mockClient.GetCertClient())
	Expect(certificateId).ShouldNot(Equal(""))
	Expect(err).Should(BeNil())

	name = "nonexistent"
	certificateId, err = FindCertificateWithName(name, compartmentId, mockClient.GetCertClient())
	Expect(certificateId).Should(Equal(""))
	Expect(err).Should(BeNil())

	name = "error"
	certificateId, err = FindCertificateWithName(name, compartmentId, mockClient.GetCertClient())
	Expect(certificateId).Should(Equal(""))
	Expect(err).ShouldNot(BeNil())
}

func TestCreateImportedTypeCertificate(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	tlsSecretData := &TLSSecretData{
		ServerCertificate:  common.String("serverCert"),
		CaCertificateChain: common.String("certChain"),
		PrivateKey:         common.String("privateKey"),
	}
	certificateName := "name"
	compartmentId := "compartmentId"
	cert, err := CreateImportedTypeCertificate(tlsSecretData, certificateName, compartmentId, mockClient.GetCertClient())
	Expect(err).Should(BeNil())
	Expect(cert).ShouldNot(BeNil())

	certificateName = "error"
	cert, err = CreateImportedTypeCertificate(tlsSecretData, certificateName, compartmentId, mockClient.GetCertClient())
	Expect(err).ShouldNot(BeNil())
	Expect(cert).Should(BeNil())
}

func TestUpdateImportedTypeCertificate(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	tlsSecretData := &TLSSecretData{
		ServerCertificate:  common.String("serverCert"),
		CaCertificateChain: common.String("certChain"),
		PrivateKey:         common.String("privateKey"),
	}
	certificateId := common.String("id")
	cert, err := UpdateImportedTypeCertificate(certificateId, tlsSecretData, mockClient.GetCertClient())
	Expect(err).Should(BeNil())
	Expect(cert).ShouldNot(BeNil())

	*certificateId = "conflictError"
	cert, err = UpdateImportedTypeCertificate(certificateId, tlsSecretData, mockClient.GetCertClient())
	Expect(err).ShouldNot(BeNil())
	Expect(cert).Should(BeNil())

	*certificateId = "error"
	cert, err = UpdateImportedTypeCertificate(certificateId, tlsSecretData, mockClient.GetCertClient())
	Expect(err).ShouldNot(BeNil())
	Expect(cert).Should(BeNil())
}

func TestScheduleCertificateVersionDeletion(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	certificateId := "id"
	versionNumber := int64(2)
	err = ScheduleCertificateVersionDeletion(certificateId, versionNumber, mockClient.GetCertClient())
	Expect(err).Should(BeNil())

	certificateId = "error"
	err = ScheduleCertificateVersionDeletion(certificateId, versionNumber, mockClient.GetCertClient())
	Expect(err).ShouldNot(BeNil())
}

func TestPruneCertificateVersions(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	certificateId := "id"
	currentVersion := int64(8)
	err = PruneCertificateVersions(certificateId, currentVersion, 5, mockClient.GetCertClient())
	Expect(err).Should(BeNil())

	certificateId = "error"
	err = PruneCertificateVersions(certificateId, currentVersion, 5, mockClient.GetCertClient())
	Expect(err).ShouldNot(BeNil())
}

func TestIsCertificateVersionFailed(t *testing.T) {
	RegisterTestingT(t)

	certVersionStages := []certificatesmanagement.VersionStageEnum{
		certificatesmanagement.VersionStageLatest,
		certificatesmanagement.VersionStageCurrent,
	}
	Expect(isCertificateVersionFailed(certVersionStages)).Should(BeFalse())

	certVersionStages = []certificatesmanagement.VersionStageEnum{
		certificatesmanagement.VersionStageLatest,
		certificatesmanagement.VersionStageFailed,
	}
	Expect(isCertificateVersionFailed(certVersionStages)).Should(BeTrue())
}

func TestIsCertificateVersionCurrent(t *testing.T) {
	RegisterTestingT(t)

	certVersionStages := []certificatesmanagement.VersionStageEnum{
		certificatesmanagement.VersionStageLatest,
		certificatesmanagement.VersionStageCurrent,
	}
	Expect(isCertificateVersionCurrent(certVersionStages)).Should(BeTrue())

	certVersionStages = []certificatesmanagement.VersionStageEnum{
		certificatesmanagement.VersionStageLatest,
		certificatesmanagement.VersionStageFailed,
	}
	Expect(isCertificateVersionCurrent(certVersionStages)).Should(BeFalse())
}

func TestCertificateVersionStagesContainsStage(t *testing.T) {
	RegisterTestingT(t)

	stages := []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageLatest, certificatesmanagement.VersionStageCurrent}
	Expect(certificateVersionStagesContainsStage(stages, certificatesmanagement.VersionStageLatest)).Should(BeTrue())
	Expect(certificateVersionStagesContainsStage(stages, certificatesmanagement.VersionStageCurrent)).Should(BeTrue())
	Expect(certificateVersionStagesContainsStage(stages, certificatesmanagement.VersionStageFailed)).Should(BeFalse())
	Expect(certificateVersionStagesContainsStage(stages, certificatesmanagement.VersionStagePrevious)).Should(BeFalse())
}

func TestGetCaBundle(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	caBundleId := "id"
	caBundleErrorId := "error"

	caBundle, etag, err := GetCaBundle(caBundleId, mockClient.GetCertClient())
	Expect(caBundle != nil).Should(BeTrue())
	Expect(etag).Should(Equal("etag"))
	Expect(err).Should(BeNil())

	// cache fetch
	caBundle, etag, err = GetCaBundle(caBundleId, mockClient.GetCertClient())
	Expect(caBundle != nil).Should(BeTrue())
	Expect(etag).Should(Equal("etag"))
	Expect(err).Should(BeNil())

	caBundle, etag, err = GetCaBundle(caBundleErrorId, mockClient.GetCertClient())
	Expect(caBundle).Should(BeNil())
	Expect(etag).Should(Equal(""))
	Expect(err).ShouldNot(BeNil())
}

func TestVerifyOrGetCaBundleIdByName(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	id := "id"
	name := "caBundle"
	compartmentId := "compartmentId"
	actualCaBundleId, err := VerifyOrGetCaBundleIdByName(id, name, compartmentId, mockClient.GetCertClient())
	Expect(err).Should(BeNil())
	Expect(*actualCaBundleId).Should(Equal(id))

	actualCaBundleId, err = VerifyOrGetCaBundleIdByName("", name, compartmentId, mockClient.GetCertClient())
	Expect(err).Should(BeNil())
	Expect(*actualCaBundleId).ShouldNot(Equal(""))
}

func TestFindCaBundleWithName(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	name := "name"
	compartmentId := "compartmentId"
	caBundleId, err := FindCaBundleWithName(name, compartmentId, mockClient.GetCertClient())
	Expect(*caBundleId).ShouldNot(BeNil())
	Expect(err).Should(BeNil())

	name = "nonexistent"
	caBundleId, err = FindCaBundleWithName(name, compartmentId, mockClient.GetCertClient())
	Expect(caBundleId).Should(BeNil())
	Expect(err).Should(BeNil())

	name = "error"
	caBundleId, err = FindCaBundleWithName(name, compartmentId, mockClient.GetCertClient())
	Expect(caBundleId).Should(BeNil())
	Expect(err).ShouldNot(BeNil())
}

func TestCreateCaBundle(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	certificatesContent := common.String("certChain")
	caBundleName := "name"
	compartmentId := "compartmentId"
	cert, err := CreateCaBundle(caBundleName, compartmentId, mockClient.GetCertClient(), certificatesContent)
	Expect(err).Should(BeNil())
	Expect(cert).ShouldNot(BeNil())

	caBundleName = "error"
	cert, err = CreateCaBundle(caBundleName, compartmentId, mockClient.GetCertClient(), certificatesContent)
	Expect(err).ShouldNot(BeNil())
	Expect(cert).Should(BeNil())
}

func TestUpdateCaBundle(t *testing.T) {
	RegisterTestingT(t)
	c, _, _ := initsUtil(&corev1.SecretList{})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	certificatesContent := common.String("certChain")
	caBundleId := "id"
	cert, err := UpdateCaBundle(caBundleId, mockClient.GetCertClient(), certificatesContent)
	Expect(err).Should(BeNil())
	Expect(cert).ShouldNot(BeNil())

	caBundleId = "conflictError"
	cert, err = UpdateCaBundle(caBundleId, mockClient.GetCertClient(), certificatesContent)
	Expect(err).ShouldNot(BeNil())
	Expect(cert).Should(BeNil())

	caBundleId = "error"
	cert, err = UpdateCaBundle(caBundleId, mockClient.GetCertClient(), certificatesContent)
	Expect(err).ShouldNot(BeNil())
	Expect(cert).Should(BeNil())
}
