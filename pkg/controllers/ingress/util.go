/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package ingress

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

func compareHealthCheckers(healthCheckerDetails *ociloadbalancer.HealthCheckerDetails, healthChecker *ociloadbalancer.HealthChecker) bool {
	if reflect.DeepEqual(healthCheckerDetails.Protocol, healthChecker.Protocol) {
		if *healthChecker.Protocol == util.ProtocolTCP {
			return compareTcpHealthCheckerAttributes(healthCheckerDetails, healthChecker)
		} else if *healthChecker.Protocol == util.ProtocolHTTP {
			return compareHttpHealthCheckerAttributes(healthCheckerDetails, healthChecker)
		}
	}
	return false
}

func compareTcpHealthCheckerAttributes(healthCheckerDetails *ociloadbalancer.HealthCheckerDetails, healthChecker *ociloadbalancer.HealthChecker) bool {
	return reflect.DeepEqual(healthCheckerDetails.Port, healthChecker.Port) &&
		reflect.DeepEqual(healthCheckerDetails.IntervalInMillis, healthChecker.IntervalInMillis) &&
		reflect.DeepEqual(healthCheckerDetails.TimeoutInMillis, healthChecker.TimeoutInMillis) &&
		reflect.DeepEqual(healthCheckerDetails.Retries, healthChecker.Retries)
}

func compareHttpHealthCheckerAttributes(healthCheckerDetails *ociloadbalancer.HealthCheckerDetails, healthChecker *ociloadbalancer.HealthChecker) bool {
	return compareTcpHealthCheckerAttributes(healthCheckerDetails, healthChecker) &&
		reflect.DeepEqual(healthCheckerDetails.UrlPath, healthChecker.UrlPath) &&
		reflect.DeepEqual(healthCheckerDetails.ReturnCode, healthChecker.ReturnCode) &&
		reflect.DeepEqual(healthCheckerDetails.ResponseBodyRegex, healthChecker.ResponseBodyRegex) &&
		reflect.DeepEqual(healthCheckerDetails.IsForcePlainText, healthChecker.IsForcePlainText)
}

// SSL UTILS

func GetCertificateHash(certificate *certificatesmanagement.CertificateSummary) *string {
	hashPtr, ok := certificate.FreeformTags["oci-native-ingress-controller-certificate-hash"]
	if ok {
		return &hashPtr
	}
	return nil
}

func CreateImportedTypeCertificate(caCertificatesChain *string, serverCertificate *string, privateKey *string, certificateName string, certificateHash *string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, error) {
	configDetails := certificatesmanagement.CreateCertificateByImportingConfigDetails{
		CertChainPem:   caCertificatesChain,
		CertificatePem: serverCertificate,
		PrivateKeyPem:  privateKey,
	}

	certificateDetails := certificatesmanagement.CreateCertificateDetails{
		Name:              &certificateName,
		CertificateConfig: configDetails,
		CompartmentId:     &compartmentId,
		FreeformTags: map[string]string{
			"oci-native-ingress-controller-certificate-hash": *certificateHash,
		},
	}
	createCertificateRequest := certificatesmanagement.CreateCertificateRequest{
		CreateCertificateDetails: certificateDetails,
		OpcRetryToken:            &certificateName,
	}

	createCertificate, err := certificatesClient.CreateCertificate(context.TODO(), createCertificateRequest)
	if err != nil {
		return nil, err
	}

	certificatesClient.SetCertCache(createCertificate)
	klog.Infof("Created a certificate with ocid %s", *createCertificate.Id)
	return createCertificate, nil
}

func UpdateImportedTypeCertificate(caCertificatesChain *string, serverCertificate *string, privateKey *string, certificateName string, certificateHash *string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, error) {
	configDetails := certificatesmanagement.UpdateCertificateByImportingConfigDetails{
		CertChainPem:   caCertificatesChain,
		CertificatePem: serverCertificate,
		PrivateKeyPem:  privateKey,
	}

	certificateDetails := certificatesmanagement.UpdateCertificateDetails{
		CertificateConfig: configDetails,
		FreeformTags: map[string]string{
			"oci-native-ingress-controller-certificate-hash": *certificateHash,
		},
	}
	updateCertificateRequest := certificatesmanagement.UpdateCertificateRequest{
		UpdateCertificateDetails: certificateDetails,
		IfMatch:                  &certificateName,
	}

	updateCertificate, err := certificatesClient.UpdateCertificate(context.TODO(), updateCertificateRequest)
	if err != nil {
		return nil, err
	}

	certificatesClient.SetCertCache(updateCertificate)
	klog.Infof("Update a certificate with ocid %s", *updateCertificate.Id)
	return updateCertificate, nil
}

func GetCertificate(certificateId *string, certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.Certificate, error) {
	certCacheObj := certificatesClient.GetFromCertCache(*certificateId)
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
		certificatesClient.SetCertCache(cert)
	}
	return cert, err
}

func FindCertificateWithName(certificateName string, compartmentId string,
	certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.CertificateSummary, error) {
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
			return &listCertificates.Items[0], nil
		}
	}
	klog.Infof("Found no certificates with name %s in compartment %s.", certificateName, compartmentId)
	return nil, nil
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

func GetCaBundle(caBundleId string, certificatesClient *certificate.CertificatesClient) (*certificatesmanagement.CaBundle, error) {
	caBundleCacheObj := certificatesClient.GetFromCaBundleCache(caBundleId)
	if caBundleCacheObj != nil {
		return caBundleCacheObj.CaBundle, nil
	}

	klog.Infof("Getting ca bundle for id %s.", caBundleId)
	getCaBundleRequest := certificatesmanagement.GetCaBundleRequest{
		CaBundleId: &caBundleId,
	}

	caBundle, err := certificatesClient.GetCaBundle(context.TODO(), getCaBundleRequest)

	if err == nil {
		certificatesClient.SetCaBundleCache(caBundle)
	}
	return caBundle, err
}

func CreateCaBundle(certificateName string, compartmentId string, certificatesClient *certificate.CertificatesClient,
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

	certificatesClient.SetCaBundleCache(createCaBundle)
	return createCaBundle, nil
}

type TLSSecretData struct {
	// This would hold server certificate and any chain of trust.
	CaCertificateChain *string
	ServerCertificate  *string
	PrivateKey         *string
}

func HashTLSSecretData(tlsData *TLSSecretData) (*string, error) {
	data, err := json.Marshal(*tlsData)
	if err != nil {
		return nil, err
	}

	hash := sha256.New()
	hash.Write(data)
	hashString := hex.EncodeToString(hash.Sum(nil))

	return &hashString, nil
}

func getTlsSecretContent(namespace string, secretName string, client kubernetes.Interface) (*TLSSecretData, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var serverCertificate string
	privateKey := string(secret.Data["tls.key"])
	caCertificateChain := string(secret.Data["ca.crt"])

	if caCertificateChain != "" {
		serverCertificate = string(secret.Data["tls.crt"])
	} else {
		// If ca.crt is not available, we will assume tls.crt has the entire chain, leaf first
		serverCertificate, caCertificateChain, err = splitLeafAndCaCertChain(secret.Data["tls.crt"], secret.Data["tls.key"])
		if err != nil {
			return nil, err
		}
	}

	return &TLSSecretData{CaCertificateChain: &caCertificateChain, ServerCertificate: &serverCertificate, PrivateKey: &privateKey}, nil
}

func splitLeafAndCaCertChain(certChainPEMBlock []byte, keyPEMBlock []byte) (string, string, error) {
	certs, err := tls.X509KeyPair(certChainPEMBlock, keyPEMBlock)
	if err != nil {
		return "", "", fmt.Errorf("unable to parse cert chain, %w", err)
	}

	if len(certs.Certificate) <= 1 {
		return "", "", errors.New("tls.crt chain has less than two certificates")
	}

	leafCertString := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certs.Certificate[0]}))

	caCertChainString := ""
	for _, cert := range certs.Certificate[1:] {
		caCertString := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))
		caCertChainString += caCertString
	}

	return leafCertString, caCertChainString, nil
}

func getCertificateNameFromSecret(secretName string) string {
	if secretName == "" {
		return ""
	}
	return fmt.Sprintf("ic-%s", secretName)
}

func GetSSLConfigForBackendSet(namespace string, artifactType string, artifact string, lb *ociloadbalancer.LoadBalancer, bsName string, compartmentId string, client *client.WrapperClient) (*ociloadbalancer.SslConfigurationDetails, error) {
	var backendSetSslConfig *ociloadbalancer.SslConfigurationDetails
	createCaBundle := false
	var caBundleId *string

	bs, ok := lb.BackendSets[bsName]

	if artifactType == state.ArtifactTypeSecret && artifact != "" {
		klog.Infof("Secret name for backend set %s is %s", bsName, artifact)
		if ok && bs.SslConfiguration != nil && isTrustAuthorityCaBundle(bs.SslConfiguration.TrustedCertificateAuthorityIds[0]) {
			newCertificateName := getCertificateNameFromSecret(artifact)
			caBundle, err := GetCaBundle(bs.SslConfiguration.TrustedCertificateAuthorityIds[0], client.GetCertClient())
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
			cId, err := CreateOrGetCaBundleForBackendSet(namespace, artifact, compartmentId, client)
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
		cert, err := GetCertificate(&artifact, client.GetCertClient())
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
			caBundleId, _ := FindCaBundleWithName(*cert.Name, compartmentId, client.GetCertClient())
			if caBundleId == nil {
				versionNumber := cert.CurrentVersion.VersionNumber
				getCertificateBundleRequest := certificates.GetCertificateBundleRequest{
					CertificateId: &artifact,
					VersionNumber: versionNumber,
				}

				certificateBundle, err := client.GetCertClient().GetCertificateBundle(context.TODO(), getCertificateBundleRequest)
				if err != nil {
					return nil, err
				}

				createCaBundle, err := CreateCaBundle(*cert.Name, compartmentId, client.GetCertClient(), certificateBundle.GetCertChainPem())
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

func GetSSLConfigForListener(namespace string, listener *ociloadbalancer.Listener, artifactType string, artifact string, compartmentId string, client *client.WrapperClient) (*ociloadbalancer.SslConfigurationDetails, error) {
	var currentCertificateId string
	var newCertificateId string
	createCertificate := false

	var listenerSslConfig *ociloadbalancer.SslConfigurationDetails

	if listener != nil && listener.SslConfiguration != nil {
		currentCertificateId = listener.SslConfiguration.CertificateIds[0]
		if state.ArtifactTypeCertificate == artifactType && currentCertificateId != artifact {
			newCertificateId = artifact
		} else if state.ArtifactTypeSecret == artifactType {
			cert, err := GetCertificate(&currentCertificateId, client.GetCertClient())
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
		cId, err := CreateOrGetCertificateForListener(namespace, artifact, compartmentId, client)
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

func CreateOrGetCertificateForListener(namespace string, secretName string, compartmentId string, client *client.WrapperClient) (*string, error) {
	certificateName := getCertificateNameFromSecret(secretName)
	certificate, err := FindCertificateWithName(certificateName, compartmentId, client.GetCertClient())
	if err != nil {
		return nil, err
	}

	tlsSecretData, err := getTlsSecretContent(namespace, secretName, client.GetK8Client())
	if err != nil {
		return nil, err
	}
	tlsHash, err := HashTLSSecretData(tlsSecretData)
	if err != nil {
		return nil, err
	}

	certificateId := certificate.Id
	if certificateId == nil {
		createCertificate, err := CreateImportedTypeCertificate(tlsSecretData.CaCertificateChain, tlsSecretData.ServerCertificate,
			tlsSecretData.PrivateKey, certificateName, tlsHash, compartmentId, client.GetCertClient())
		if err != nil {
			return nil, err
		}
		certificateId = createCertificate.Id
	} else {
		certificateHash := GetCertificateHash(certificate)
		if certificateHash == nil || tlsHash != certificateHash {
			updateCertificate, err := UpdateImportedTypeCertificate(tlsSecretData.CaCertificateChain, tlsSecretData.ServerCertificate,
				tlsSecretData.PrivateKey, certificateName, tlsHash, compartmentId, client.GetCertClient())
			if err != nil {
				return nil, err
			}
			certificateId = updateCertificate.Id
		}
	}
	return certificateId, nil
}

func CreateOrGetCaBundleForBackendSet(namespace string, secretName string, compartmentId string, client *client.WrapperClient) (*string, error) {
	certificateName := getCertificateNameFromSecret(secretName)
	caBundleId, err := FindCaBundleWithName(certificateName, compartmentId, client.GetCertClient())
	if err != nil {
		return nil, err
	}

	if caBundleId == nil {
		tlsSecretData, err := getTlsSecretContent(namespace, secretName, client.GetK8Client())
		if err != nil {
			return nil, err
		}
		createCaBundle, err := CreateCaBundle(certificateName, compartmentId, client.GetCertClient(), tlsSecretData.CaCertificateChain)
		if err != nil {
			return nil, err
		}
		caBundleId = createCaBundle.Id
	}
	return caBundleId, nil
}

func isTrustAuthorityCaBundle(id string) bool {
	return strings.Contains(id, "cabundle")
}
