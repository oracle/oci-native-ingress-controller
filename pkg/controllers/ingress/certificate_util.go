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
	"context"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"k8s.io/klog/v2"
	"time"
)

// Interacting with OCI Cert service, opinionated for Ingress Controller

func GetCertificate(certificateId *string, certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, string, error) {
	certCacheObj := certificatesClient.GetFromCertCache(*certificateId)
	if certCacheObj != nil {
		now := time.Now()
		if now.Sub(certCacheObj.Age).Minutes() < util.CertificateCacheMaxAgeInMinutes {
			return certCacheObj.Cert, certCacheObj.Etag, nil
		}
		klog.Infof("Refreshing certificate %s", *certificateId)
	}
	return getCertificateBustCache(*certificateId, certificatesClient)
}

func getCertificateBustCache(certificateId string, certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, string, error) {
	getCertificateRequest := certificatesmanagement.GetCertificateRequest{
		CertificateId: &certificateId,
	}

	cert, etag, err := certificatesClient.GetCertificate(context.TODO(), getCertificateRequest)
	if err == nil {
		certificatesClient.SetCertCache(cert, etag)
	}
	return cert, etag, err
}

// VerifyOrGetCertificateIdByName verifies that cert with certificateId has name certificateName
// If not, try to find certificate with matching name
func VerifyOrGetCertificateIdByName(certificateId string, certificateName string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (string, error) {
	if certificateId != "" {
		cert, _, err := GetCertificate(&certificateId, certificatesClient)
		if err != nil {
			return "", err
		}

		if *cert.Name == certificateName {
			return certificateId, nil
		}
	}

	return FindCertificateWithName(certificateName, compartmentId, certificatesClient)
}

func FindCertificateWithName(certificateName string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (string, error) {
	listCertificatesRequest := certificatesmanagement.ListCertificatesRequest{
		Name:           &certificateName,
		CompartmentId:  &compartmentId,
		LifecycleState: certificatesmanagement.ListCertificatesLifecycleStateActive,
	}

	klog.Infof("Searching for certificates with name %s in compartment %s.", certificateName, compartmentId)
	listCertificates, _, err := certificatesClient.ListCertificates(context.TODO(), listCertificatesRequest)
	if err != nil {
		return "", err
	}

	if listCertificates.Items != nil {
		numberOfCertificates := len(listCertificates.Items)
		klog.Infof("Found %d certificates with name %s in compartment %s.", numberOfCertificates, certificateName, compartmentId)
		if numberOfCertificates > 0 {
			return *listCertificates.Items[0].Id, nil
		}
	}
	klog.Infof("Found no certificates with name %s in compartment %s.", certificateName, compartmentId)
	return "", nil
}

func CreateImportedTypeCertificate(tlsSecretData *TLSSecretData, certificateName string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, error) {
	configDetails := certificatesmanagement.CreateCertificateByImportingConfigDetails{
		CertChainPem:   tlsSecretData.CaCertificateChain,
		CertificatePem: tlsSecretData.ServerCertificate,
		PrivateKeyPem:  tlsSecretData.PrivateKey,
	}

	certificateDetails := certificatesmanagement.CreateCertificateDetails{
		Name:              &certificateName,
		CertificateConfig: configDetails,
		CompartmentId:     &compartmentId,
		FreeformTags: map[string]string{
			certificateHashTagKey: hashPublicTlsData(tlsSecretData),
		},
	}
	createCertificateRequest := certificatesmanagement.CreateCertificateRequest{
		CreateCertificateDetails: certificateDetails,
		OpcRetryToken:            common.String(hashStringShort(common.String(certificateName + compartmentId))),
	}

	createCertificate, etag, err := certificatesClient.CreateCertificate(context.TODO(), createCertificateRequest)
	if err != nil {
		return nil, err
	}

	certificatesClient.SetCertCache(createCertificate, etag)
	klog.Infof("Created a certificate with ocid %s", *createCertificate.Id)
	return createCertificate, nil
}

func UpdateImportedTypeCertificate(certificateId *string, tlsSecretData *TLSSecretData,
	certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, error) {
	_, etag, err := GetCertificate(certificateId, certificatesClient)
	if err != nil {
		return nil, err
	}

	configDetails := certificatesmanagement.UpdateCertificateByImportingConfigDetails{
		CertChainPem:   tlsSecretData.CaCertificateChain,
		CertificatePem: tlsSecretData.ServerCertificate,
		PrivateKeyPem:  tlsSecretData.PrivateKey,
	}

	updateCertificateDetails := certificatesmanagement.UpdateCertificateDetails{
		CertificateConfig: configDetails,
		FreeformTags: map[string]string{
			certificateHashTagKey: hashPublicTlsData(tlsSecretData),
		},
	}

	updateCertificateRequest := certificatesmanagement.UpdateCertificateRequest{
		CertificateId:            certificateId,
		IfMatch:                  common.String(etag),
		UpdateCertificateDetails: updateCertificateDetails,
	}

	updateCertificate, etag, err := certificatesClient.UpdateCertificate(context.TODO(), updateCertificateRequest)

	// This can happen by create/update listener calls that cause the certificate etag to change because of a new association
	if util.IsServiceError(err, 409) {
		klog.Infof("Update certificate returned code %d for certificate %s. Refreshing cache.", 409, *certificateId)
		getCertificateBustCache(*certificateId, certificatesClient)
		return nil, fmt.Errorf("unable to update certificate %s due to conflict, controller will retry later", *certificateId)
	}

	if err != nil {
		return nil, err
	}

	certificatesClient.SetCertCache(updateCertificate, etag)
	return updateCertificate, nil
}

func ScheduleCertificateVersionDeletion(certificateId string, versionNumber int64, certificatesClient *certificate.CertificatesClient) error {
	request := certificatesmanagement.ScheduleCertificateVersionDeletionRequest{
		ScheduleCertificateVersionDeletionDetails: certificatesmanagement.ScheduleCertificateVersionDeletionDetails{
			TimeOfDeletion: &common.SDKTime{
				Time: time.Now().Add(time.Hour * 24 * 10),
			},
		},
		CertificateId:            &certificateId,
		CertificateVersionNumber: &versionNumber,
	}

	updatedCert, etag, err := certificatesClient.ScheduleCertificateVersionDeletion(context.TODO(), request)
	if err != nil {
		return err
	}

	certificatesClient.SetCertCache(updatedCert, etag)
	return nil
}

func PruneCertificateVersions(certificateId string, currentVersion int64,
	versionsToPreserveCount int, certificatesClient *certificate.CertificatesClient) error {
	listCertificateVersionsRequest := certificatesmanagement.ListCertificateVersionsRequest{
		CertificateId: &certificateId,
		SortOrder:     certificatesmanagement.ListCertificateVersionsSortOrderDesc,
	}

	certificateVersionCollection, _, err := certificatesClient.ListCertificateVersions(context.TODO(), listCertificateVersionsRequest)
	if err != nil {
		return err
	}

	versionsToPreserveSeen := 0
	for _, certVersionSummary := range certificateVersionCollection.Items {
		if *certVersionSummary.VersionNumber > currentVersion {
			continue
		}

		if (isCertificateVersionFailed(certVersionSummary.Stages) || versionsToPreserveSeen >= versionsToPreserveCount) &&
			certVersionSummary.TimeOfDeletion == nil && !isCertificateVersionCurrent(certVersionSummary.Stages) {
			err = ScheduleCertificateVersionDeletion(certificateId, *certVersionSummary.VersionNumber, certificatesClient)
			if err != nil {
				klog.Errorf("unable to delete certificate version number %d for certificate %s, skipping for now: %s",
					*certVersionSummary.VersionNumber, certificateId, err.Error())
			}
			continue
		}
		versionsToPreserveSeen = versionsToPreserveSeen + 1
	}

	return nil
}

func isCertificateVersionFailed(certificateVersionStages []certificatesmanagement.VersionStageEnum) bool {
	return certificateVersionStagesContainsStage(certificateVersionStages, certificatesmanagement.VersionStageFailed)
}

func isCertificateVersionCurrent(certificateVersionStages []certificatesmanagement.VersionStageEnum) bool {
	return certificateVersionStagesContainsStage(certificateVersionStages, certificatesmanagement.VersionStageCurrent)
}

func certificateVersionStagesContainsStage(certificateVersionStages []certificatesmanagement.VersionStageEnum,
	stage certificatesmanagement.VersionStageEnum) bool {
	for _, value := range certificateVersionStages {
		if stage == value {
			return true
		}
	}
	return false
}

func GetCaBundle(caBundleId string, certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.CaBundle, string, error) {
	caBundleCacheObj := certificatesClient.GetFromCaBundleCache(caBundleId)
	if caBundleCacheObj != nil {
		now := time.Now()
		if now.Sub(caBundleCacheObj.Age).Minutes() < util.CertificateCacheMaxAgeInMinutes {
			return caBundleCacheObj.CaBundle, caBundleCacheObj.Etag, nil
		}
		klog.Infof("Refreshing ca bundle %s", caBundleId)
	}

	return getCaBundleBustCache(caBundleId, certificatesClient)
}

func getCaBundleBustCache(caBundleId string, certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.CaBundle, string, error) {
	klog.Infof("Getting ca bundle for id %s.", caBundleId)
	getCaBundleRequest := certificatesmanagement.GetCaBundleRequest{
		CaBundleId: &caBundleId,
	}

	caBundle, etag, err := certificatesClient.GetCaBundle(context.TODO(), getCaBundleRequest)

	if err == nil {
		certificatesClient.SetCaBundleCache(caBundle, etag)
	}
	return caBundle, etag, err
}

// VerifyOrGetCaBundleIdByName verifies that caBundle with caBundleId has name caBundleName
// If not, try to find ca bundle with matching name
func VerifyOrGetCaBundleIdByName(caBundleId string, caBundleName string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (*string, error) {
	if caBundleId != "" {
		caBundle, _, err := GetCaBundle(caBundleId, certificatesClient)
		if err != nil {
			return nil, err
		}

		if *caBundle.Name == caBundleName {
			return &caBundleId, nil
		}
	}

	return FindCaBundleWithName(caBundleName, compartmentId, certificatesClient)
}

func FindCaBundleWithName(certificateName string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (*string, error) {
	listCaBundlesRequest := certificatesmanagement.ListCaBundlesRequest{
		Name:           &certificateName,
		CompartmentId:  &compartmentId,
		LifecycleState: certificatesmanagement.ListCaBundlesLifecycleStateActive,
	}

	klog.Infof("Searching for ca bundles with name %s in compartment %s.", certificateName, compartmentId)
	listCaBundles, err := certificatesClient.ListCaBundles(context.TODO(), listCaBundlesRequest)
	if err != nil {
		return nil, err
	}

	if listCaBundles.Items != nil {
		numberOfCertificates := len(listCaBundles.Items)
		klog.Infof("Found %d bundles with name %s in compartment %s.", numberOfCertificates, certificateName, compartmentId)
		if numberOfCertificates > 0 {
			return listCaBundles.Items[0].Id, nil
		}
	}
	klog.Infof("Found no bundles with name %s in compartment %s.", certificateName, compartmentId)
	return nil, nil
}

func CreateCaBundle(certificateName string, compartmentId string, certificatesClient *certificate.CertificatesClient,
	certificateContents *string) (*certificatesmanagement.CaBundle, error) {
	caBundleDetails := certificatesmanagement.CreateCaBundleDetails{
		Name:          &certificateName,
		CompartmentId: &compartmentId,
		CaBundlePem:   certificateContents,
		FreeformTags: map[string]string{
			caBundleHashTagKey: hashString(certificateContents),
		},
	}
	createCaBundleRequest := certificatesmanagement.CreateCaBundleRequest{
		CreateCaBundleDetails: caBundleDetails,
		OpcRetryToken:         common.String(hashStringShort(common.String(certificateName + compartmentId))),
	}
	createCaBundle, etag, err := certificatesClient.CreateCaBundle(context.TODO(), createCaBundleRequest)
	if err != nil {
		return nil, err
	}

	certificatesClient.SetCaBundleCache(createCaBundle, etag)
	return createCaBundle, nil
}

func UpdateCaBundle(caBundleId string, certificatesClient *certificate.CertificatesClient,
	certificateContents *string) (*certificatesmanagement.CaBundle, error) {
	_, etag, err := GetCaBundle(caBundleId, certificatesClient)
	if err != nil {
		return nil, err
	}

	caBundleDetails := certificatesmanagement.UpdateCaBundleDetails{
		CaBundlePem: certificateContents,
		FreeformTags: map[string]string{
			caBundleHashTagKey: hashString(certificateContents),
		},
	}

	updateCaBundleRequest := certificatesmanagement.UpdateCaBundleRequest{
		CaBundleId:            &caBundleId,
		UpdateCaBundleDetails: caBundleDetails,
		IfMatch:               &etag,
	}

	updateCaBundle, etag, err := certificatesClient.UpdateCaBundle(context.TODO(), updateCaBundleRequest)

	// This can happen by create/update backend set calls that cause the certificate etag to change because of a new association
	if util.IsServiceError(err, 409) {
		klog.Infof("Update ca bundle returned code %d for ca bundle %s. Refreshing cache.", 409, caBundleId)
		getCaBundleBustCache(caBundleId, certificatesClient)
		return nil, fmt.Errorf("unable to update ca bundle %s due to conflict, controller will retry later", caBundleId)
	}

	if err != nil {
		return nil, err
	}

	certificatesClient.SetCaBundleCache(updateCaBundle, etag)
	return updateCaBundle, nil
}
