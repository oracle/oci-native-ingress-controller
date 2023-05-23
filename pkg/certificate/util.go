/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package certificate

import (
	"context"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"k8s.io/klog/v2"

	"bitbucket.oci.oraclecorp.com/oke/oci-native-ingress-controller/pkg/util"
)

func CreateImportedTypeCertificate(caCertificatesChain *string, serverCertificate *string, privateKey *string, certificateName string, compartmentId string,
	certificatesClient *CertificatesClient) (*certificatesmanagement.Certificate, error) {
	configDetails := certificatesmanagement.CreateCertificateByImportingConfigDetails{
		CertChainPem:   caCertificatesChain,
		CertificatePem: serverCertificate,
		PrivateKeyPem:  privateKey,
	}

	certificateDetails := certificatesmanagement.CreateCertificateDetails{
		Name:              &certificateName,
		CertificateConfig: configDetails,
		CompartmentId:     &compartmentId,
	}
	createCertificateRequest := certificatesmanagement.CreateCertificateRequest{
		CreateCertificateDetails: certificateDetails,
		OpcRetryToken:            &certificateName,
	}

	createCertificate, err := certificatesClient.CreateCertificate(context.TODO(), createCertificateRequest)
	if err != nil {
		return nil, err
	}

	certificatesClient.setCertCache(createCertificate)
	klog.Infof("Created a certificate with ocid %s", *createCertificate.Id)
	return createCertificate, nil
}

func CreateInternalCaTypeCertificate(issuerCertificateAuthorityId *string, subject *string,
	subjectAlternativeNameMap map[string]string, certificateName string, compartmentId string,
	certificatesClient *CertificatesClient) (*certificatesmanagement.Certificate, error) {

	certificateSubject := &certificatesmanagement.CertificateSubject{
		CommonName: subject,
	}

	var subjectAlternativeNames []certificatesmanagement.CertificateSubjectAlternativeName
	for san := range subjectAlternativeNameMap {
		sanType := subjectAlternativeNameMap[san]
		sanValue := san
		sanInstance := certificatesmanagement.CertificateSubjectAlternativeName{
			Type:  certificatesmanagement.CertificateSubjectAlternativeNameTypeEnum(sanType),
			Value: &sanValue,
		}
		subjectAlternativeNames = append(subjectAlternativeNames, sanInstance)
	}

	configDetails := certificatesmanagement.CreateCertificateIssuedByInternalCaConfigDetails{
		IssuerCertificateAuthorityId: issuerCertificateAuthorityId,
		Subject:                      certificateSubject,
		SubjectAlternativeNames:      subjectAlternativeNames,
		CertificateProfileType:       certificatesmanagement.CertificateProfileTypeTlsServerOrClient,
		KeyAlgorithm:                 certificatesmanagement.KeyAlgorithmRsa2048,
		SignatureAlgorithm:           certificatesmanagement.SignatureAlgorithmSha256WithRsa,
	}

	certificateDetails := certificatesmanagement.CreateCertificateDetails{
		Name:              &certificateName,
		CertificateConfig: configDetails,
		CompartmentId:     &compartmentId,
	}
	createCertificateRequest := certificatesmanagement.CreateCertificateRequest{
		CreateCertificateDetails: certificateDetails,
		OpcRetryToken:            &certificateName,
	}

	createCertificate, err := certificatesClient.CreateCertificate(context.TODO(), createCertificateRequest)
	if err != nil {
		return nil, err
	}

	klog.Infof("Created a certificate with ocid %s", *createCertificate.Id)
	return createCertificate, nil
}

func GetCertificate(certificateId *string, certificatesClient *CertificatesClient) (*certificatesmanagement.Certificate, error) {
	certCacheObj := certificatesClient.getFromCertCache(*certificateId)
	if certCacheObj != nil {
		now := time.Now()
		if now.Sub(certCacheObj.age).Minutes() < util.CertificateCacheMaxAgeInMinutes {
			return certCacheObj.cert, nil
		}
		klog.Infof("Refreshing certificate %s", *certificateId)
	}
	getCertificateRequest := certificatesmanagement.GetCertificateRequest{
		CertificateId: certificateId,
	}

	cert, err := certificatesClient.GetCertificate(context.TODO(), getCertificateRequest)
	if err == nil {
		certificatesClient.setCertCache(cert)
	}
	return cert, err
}

func FindCertificateWithName(certificateName string, compartmentId string,
	certificatesClient *CertificatesClient) (*string, error) {
	listCertificatesRequest := certificatesmanagement.ListCertificatesRequest{
		Name:           &certificateName,
		CompartmentId:  &compartmentId,
		LifecycleState: certificatesmanagement.ListCertificatesLifecycleStateActive,
	}

	klog.Infof("Searching for certificates with name %s in compartment %s.", certificateName, compartmentId)
	listCertificates, _, err := certificatesClient.ListCertificates(context.TODO(), listCertificatesRequest)
	if err != nil {
		return nil, err
	}

	if listCertificates.Items != nil {
		numberOfCertificates := len(listCertificates.Items)
		klog.Infof("Found %d certificates with name %s in compartment %s.", numberOfCertificates, certificateName, compartmentId)
		if numberOfCertificates > 0 {
			return listCertificates.Items[0].Id, nil
		}
	}
	klog.Infof("Found no certificates with name %s in compartment %s.", certificateName, compartmentId)
	return nil, nil
}

func FindCaBundleWithName(certificateName string, compartmentId string,
	certificatesClient *CertificatesClient) (*string, error) {
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

func GetCaBundle(caBundleId string, certificatesClient *CertificatesClient) (*certificatesmanagement.CaBundle, error) {
	caBundleCacheObj := certificatesClient.getFromCaBundleCache(caBundleId)
	if caBundleCacheObj != nil {
		return caBundleCacheObj.caBundle, nil
	}

	klog.Infof("Getting ca bundle for id %s.", caBundleId)
	getCaBundleRequest := certificatesmanagement.GetCaBundleRequest{
		CaBundleId: &caBundleId,
	}

	caBundle, err := certificatesClient.GetCaBundle(context.TODO(), getCaBundleRequest)

	if err == nil {
		certificatesClient.setCaBundleCache(caBundle)
	}
	return caBundle, err
}

func CreateCaBundle(certificateName string, compartmentId string, certificatesClient *CertificatesClient,
	certificateContents *string) (*certificatesmanagement.CaBundle, error) {
	caBundleDetails := certificatesmanagement.CreateCaBundleDetails{
		Name:          &certificateName,
		CompartmentId: &compartmentId,
		CaBundlePem:   certificateContents,
	}
	createCaBundleRequest := certificatesmanagement.CreateCaBundleRequest{
		CreateCaBundleDetails: caBundleDetails,
		OpcRetryToken:         &certificateName,
	}
	createCaBundle, err := certificatesClient.CreateCaBundle(context.TODO(), createCaBundleRequest)
	if err != nil {
		return nil, err
	}

	certificatesClient.setCaBundleCache(createCaBundle)
	return createCaBundle, nil
}

func ScheduleCertificatesForDeletionWithNamePrefix(prefix string, compartmentId string, certificatesClient *CertificatesClient) []error {
	listRequest := certificatesmanagement.ListCertificatesRequest{
		CompartmentId:  &compartmentId,
		LifecycleState: certificatesmanagement.ListCertificatesLifecycleStateActive,
	}

	var errorList []error
	for {
		certificates, nextPage, err := certificatesClient.ListCertificates(context.TODO(), listRequest)
		if err != nil {
			return append(errorList, err)
		}
		for i := range certificates.Items {
			cert := certificates.Items[i]
			if strings.HasPrefix(*cert.Name, prefix) {
				klog.Infof("Found a certificate %s with prefix %s. This will be scheduled for deletion.", *cert.Name, prefix)
				deleteRequest := certificatesmanagement.ScheduleCertificateDeletionRequest{
					CertificateId: cert.Id,
				}
				klog.Infof("Delete certificate request %s", util.PrettyPrint(deleteRequest))
				err := certificatesClient.ScheduleCertificateDeletion(context.TODO(), deleteRequest)
				if err != nil {
					klog.Error("Error deleting certificate %s, %s", *cert.Name, err.Error())
					errorList = append(errorList, err)
				}
			}
		}

		if nextPage == nil {
			break
		}

		listRequest.Page = nextPage
	}
	return errorList
}

func DeleteCaBundlesWithNamePrefix(prefix string, compartmentId string, certificatesClient *CertificatesClient) error {
	listRequest := certificatesmanagement.ListCaBundlesRequest{
		CompartmentId:  &compartmentId,
		LifecycleState: certificatesmanagement.ListCaBundlesLifecycleStateActive,
	}

	caBundles, err := certificatesClient.ListCaBundles(context.TODO(), listRequest)
	if err != nil {
		return err
	}
	for i := range caBundles.Items {
		bundle := caBundles.Items[i]
		if strings.HasPrefix(*bundle.Name, prefix) {
			klog.Infof("Found a ca bundle %s with prefix %s. This will be deleted..", *bundle.Name, prefix)
			deleteRequest := certificatesmanagement.DeleteCaBundleRequest{
				CaBundleId: bundle.Id,
			}
			_, err := certificatesClient.DeleteCaBundle(context.TODO(), deleteRequest)
			if err != nil {
				klog.Error("Error deleting ca bundle %s, %s", *bundle.Name, err.Error())
				return err
			}
		}
	}
	return nil
}
