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
	"fmt"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/util"
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

func GetCertificate(certificateId *string, certificatesClient *CertificatesClient) (*certificatesmanagement.Certificate, error) {
	certCacheObj := certificatesClient.getFromCertCache(*certificateId)
	if certCacheObj != nil {
		now := time.Now()
		if now.Sub(certCacheObj.Age).Minutes() < util.CertificateCacheMaxAgeInMinutes {
			return certCacheObj.Cert, nil
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
		return caBundleCacheObj.CaBundle, nil
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

func CreateOrGetCertificateForListener(namespace string, secretName string, compartmentId string, certificatesClient *CertificatesClient, client kubernetes.Interface) (*string, error) {
	certificateName := getCertificateNameFromSecret(secretName)
	certificateId, err := FindCertificateWithName(certificateName, compartmentId, certificatesClient)
	if err != nil {
		return nil, err
	}

	if certificateId == nil {
		tlsSecretData, err := getTlsSecretContent(namespace, secretName, client)
		if err != nil {
			return nil, err
		}

		createCertificate, err := CreateImportedTypeCertificate(tlsSecretData.CaCertificateChain, tlsSecretData.ServerCertificate,
			tlsSecretData.PrivateKey, certificateName, compartmentId, certificatesClient)
		if err != nil {
			return nil, err
		}

		certificateId = createCertificate.Id
	}
	return certificateId, nil
}

func CreateOrGetCaBundleForBackendSet(namespace string, secretName string, compartmentId string, certificatesClient *CertificatesClient, client kubernetes.Interface) (*string, error) {
	certificateName := getCertificateNameFromSecret(secretName)
	caBundleId, err := FindCaBundleWithName(certificateName, compartmentId, certificatesClient)
	if err != nil {
		return nil, err
	}

	if caBundleId == nil {
		tlsSecretData, err := getTlsSecretContent(namespace, secretName, client)
		if err != nil {
			return nil, err
		}
		createCaBundle, err := CreateCaBundle(certificateName, compartmentId, certificatesClient, tlsSecretData.CaCertificateChain)
		if err != nil {
			return nil, err
		}
		caBundleId = createCaBundle.Id
	}
	return caBundleId, nil
}

type TLSSecretData struct {
	// This would hold server certificate and any chain of trust.
	CaCertificateChain *string
	ServerCertificate  *string
	PrivateKey         *string
}

func getTlsSecretContent(namespace string, secretName string, client kubernetes.Interface) (*TLSSecretData, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	caCertificateChain := string(secret.Data["ca.crt"])
	serverCertificate := string(secret.Data["tls.crt"])
	privateKey := string(secret.Data["tls.key"])
	return &TLSSecretData{CaCertificateChain: &caCertificateChain, ServerCertificate: &serverCertificate, PrivateKey: &privateKey}, nil
}

func getCertificateNameFromSecret(secretName string) string {
	if secretName == "" {
		return ""
	}
	return fmt.Sprintf("ic-%s", secretName)
}

func GetSSLConfigForBackendSet(namespace string, artifactType string, artifact string, lb *ociloadbalancer.LoadBalancer, bsName string, compartmentId string, certificatesClient *CertificatesClient, client kubernetes.Interface) (*ociloadbalancer.SslConfigurationDetails, error) {
	var backendSetSslConfig *ociloadbalancer.SslConfigurationDetails
	createCaBundle := false
	var caBundleId *string

	bs, ok := lb.BackendSets[bsName]

	if artifactType == state.ArtifactTypeSecret && artifact != "" {
		klog.Infof("Secret name for backend set %s is %s", bsName, artifact)
		if ok && bs.SslConfiguration != nil && isTrustAuthorityCaBundle(bs.SslConfiguration.TrustedCertificateAuthorityIds[0]) {
			newCertificateName := getCertificateNameFromSecret(artifact)
			caBundle, err := GetCaBundle(bs.SslConfiguration.TrustedCertificateAuthorityIds[0], certificatesClient)
			if err != nil {
				return nil, err
			}

			klog.Infof("Ca bundle name is %s, new certificate name is %s", *caBundle.Name, newCertificateName)
			if *caBundle.Name != newCertificateName {
				klog.Infof("Ca bundle for backend set %s needs update. Old name %s, New name %s", *bs.Name, *caBundle.Name, newCertificateName)
				createCaBundle = true
			}
		} else {
			createCaBundle = true
		}

		if createCaBundle {
			cId, err := CreateOrGetCaBundleForBackendSet(namespace, artifact, compartmentId, certificatesClient, client)
			if err != nil {
				return nil, err
			}
			caBundleId = cId
		}

		if caBundleId != nil {
			caBundleIds := []string{*caBundleId}
			backendSetSslConfig = &ociloadbalancer.SslConfigurationDetails{TrustedCertificateAuthorityIds: caBundleIds}
		}
	}

	if artifactType == state.ArtifactTypeCertificate && artifact != "" {
		cert, err := GetCertificate(&artifact, certificatesClient)
		if err != nil {
			return nil, err
		}

		klog.Infof("Found a certificate %s with type %s and id %s", *cert.Name, cert.ConfigType, *cert.Id)
		if cert.ConfigType == certificatesmanagement.CertificateConfigTypeIssuedByInternalCa ||
			cert.ConfigType == certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa {
			caAuthorityIds := []string{*cert.IssuerCertificateAuthorityId}
			backendSetSslConfig = &ociloadbalancer.SslConfigurationDetails{TrustedCertificateAuthorityIds: caAuthorityIds}
		}

		if cert.ConfigType == certificatesmanagement.CertificateConfigTypeImported {
			caBundleId, _ := FindCaBundleWithName(*cert.Name, compartmentId, certificatesClient)
			if caBundleId == nil {
				versionNumber := cert.CurrentVersion.VersionNumber
				getCertificateBundleRequest := certificates.GetCertificateBundleRequest{
					CertificateId: &artifact,
					VersionNumber: versionNumber,
				}

				certificateBundle, err := certificatesClient.GetCertificateBundle(context.TODO(), getCertificateBundleRequest)
				if err != nil {
					return nil, err
				}

				createCaBundle, err := CreateCaBundle(*cert.Name, compartmentId, certificatesClient, certificateBundle.GetCertChainPem())
				if err != nil {
					return nil, err
				}
				caBundleId = createCaBundle.Id
			}

			if caBundleId != nil {
				caBundleIds := []string{*caBundleId}
				backendSetSslConfig = &ociloadbalancer.SslConfigurationDetails{TrustedCertificateAuthorityIds: caBundleIds}
			}
		}
	}
	return backendSetSslConfig, nil
}

func GetSSLConfigForListener(namespace string, listener *ociloadbalancer.Listener, artifactType string, artifact string, compartmentId string, certificatesClient *CertificatesClient, client kubernetes.Interface) (*ociloadbalancer.SslConfigurationDetails, error) {
	var currentCertificateId string
	var newCertificateId string
	createCertificate := false

	var listenerSslConfig *ociloadbalancer.SslConfigurationDetails

	if listener != nil && listener.SslConfiguration != nil {
		currentCertificateId = listener.SslConfiguration.CertificateIds[0]
		if state.ArtifactTypeCertificate == artifactType && currentCertificateId != artifact {
			newCertificateId = artifact
		} else if state.ArtifactTypeSecret == artifactType {
			cert, err := GetCertificate(&currentCertificateId, certificatesClient)
			if err != nil {
				return nil, err
			}
			certificateName := getCertificateNameFromSecret(artifact)
			if certificateName != "" && *cert.Name != certificateName {
				createCertificate = true
			}
		}
	} else {
		if state.ArtifactTypeSecret == artifactType {
			createCertificate = true
		}
		if state.ArtifactTypeCertificate == artifactType {
			newCertificateId = artifact
		}
	}

	if createCertificate {
		cId, err := CreateOrGetCertificateForListener(namespace, artifact, compartmentId, certificatesClient, client)
		if err != nil {
			return nil, err
		}
		newCertificateId = *cId
	}

	if newCertificateId != "" {
		certificateIds := []string{newCertificateId}
		listenerSslConfig = &ociloadbalancer.SslConfigurationDetails{CertificateIds: certificateIds}
	}
	return listenerSslConfig, nil
}

func isTrustAuthorityCaBundle(id string) bool {
	return strings.Contains(id, "cabundle")
}
