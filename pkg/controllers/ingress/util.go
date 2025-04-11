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
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"reflect"
	"strings"
)

const (
	certificateVersionsToPreserveCount = 5
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

// SSL UTIL

type TLSSecretData struct {
	// This would hold server certificate and any chain of trust.
	CaCertificateChain *string
	ServerCertificate  *string
	PrivateKey         *string
}

func hashPublicTlsData(data *TLSSecretData) string {
	concatString := ""
	if data != nil && data.CaCertificateChain != nil {
		concatString = concatString + *data.CaCertificateChain
	}
	if data != nil && data.ServerCertificate != nil {
		concatString = concatString + *data.ServerCertificate
	}
	return hashString(&concatString)
}

func hashString(data *string) string {
	h := sha256.New()
	if data != nil {
		h.Write([]byte(*data))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashStringShort(data *string) string {
	hash := hashString(data)
	if len(hash) > 32 {
		hash = hash[:32]
	}
	return hash
}

func getTlsSecretContent(namespace string, secretName string, secretLister v1.SecretLister) (*TLSSecretData, error) {
	secret, err := secretLister.Secrets(namespace).Get(secretName)
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

func getCertificateNameFromSecret(namespace string, secretName string, secretLister v1.SecretLister) (string, error) {
	if secretName == "" {
		return "", nil
	}

	secret, err := secretLister.Secrets(namespace).Get(secretName)
	if err != nil {
		return "", fmt.Errorf("unable to GET secret %s: %w", klog.KRef(namespace, secretName), err)
	}

	return fmt.Sprintf("%s-%s", util.CertificateResourcePrefix, secret.UID), nil
}

func GetSSLConfigForBackendSet(namespace string, artifactType string, artifact string, lb *ociloadbalancer.LoadBalancer, bsName string,
	compartmentId string, secretLister v1.SecretLister, client *client.WrapperClient) (*ociloadbalancer.SslConfigurationDetails, error) {
	var backendSetSslConfig *ociloadbalancer.SslConfigurationDetails
	var caBundleId *string

	bs, ok := lb.BackendSets[bsName]

	if artifactType == state.ArtifactTypeSecret && artifact != "" {
		klog.Infof("Secret name for backend set %s is %s", bsName, artifact)

		currentCaBundleId := ""
		if ok && bs.SslConfiguration != nil && len(bs.SslConfiguration.TrustedCertificateAuthorityIds) > 0 &&
			isTrustAuthorityCaBundle(bs.SslConfiguration.TrustedCertificateAuthorityIds[0]) {
			currentCaBundleId = bs.SslConfiguration.TrustedCertificateAuthorityIds[0]
		}

		newCaBundleId, err := ensureCaBundleForBackendSet(currentCaBundleId, namespace, artifact, compartmentId, secretLister, client)
		if err != nil {
			return nil, err
		}
		caBundleId = newCaBundleId

		if caBundleId != nil {
			caBundleIds := []string{*caBundleId}
			backendSetSslConfig = &ociloadbalancer.SslConfigurationDetails{TrustedCertificateAuthorityIds: caBundleIds}
		}
	}

	if artifactType == state.ArtifactTypeCertificate && artifact != "" {
		cert, _, err := GetCertificate(&artifact, client.GetCertClient())
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

func GetSSLConfigForListener(namespace string, listener *ociloadbalancer.Listener, artifactType string, artifact string,
	compartmentId string, secretLister v1.SecretLister, client *client.WrapperClient) (*ociloadbalancer.SslConfigurationDetails, error) {
	var currentCertificateId string
	var newCertificateId string

	var listenerSslConfig *ociloadbalancer.SslConfigurationDetails

	if listener != nil && listener.SslConfiguration != nil && len(listener.SslConfiguration.CertificateIds) > 0 {
		currentCertificateId = listener.SslConfiguration.CertificateIds[0]
	}

	if state.ArtifactTypeCertificate == artifactType {
		newCertificateId = artifact
	}

	if state.ArtifactTypeSecret == artifactType && artifact != "" {
		cId, err := ensureCertificateForListener(currentCertificateId, namespace, artifact, compartmentId, secretLister, client)
		if err != nil {
			return nil, err
		}
		newCertificateId = cId
	}

	if newCertificateId != "" {
		certificateIds := []string{newCertificateId}
		listenerSslConfig = &ociloadbalancer.SslConfigurationDetails{CertificateIds: certificateIds}
	}
	return listenerSslConfig, nil
}

// ensureCertificateForListener creates/updates a certificate for Listeners, when the artifact is of type secret
// inputCertificateId is optional, pass if trying to update a certificate, name of certificate will still be checked
func ensureCertificateForListener(inputCertificateId string, namespace string, secretName string, compartmentId string, secretLister v1.SecretLister, client *client.WrapperClient) (string, error) {
	certificateName, err := getCertificateNameFromSecret(namespace, secretName, secretLister)
	if err != nil {
		return "", err
	}

	tlsSecretData, err := getTlsSecretContent(namespace, secretName, secretLister)
	if err != nil {
		return "", err
	}

	certificateId, err := VerifyOrGetCertificateIdByName(inputCertificateId, certificateName, compartmentId, client.GetCertClient())
	if err != nil {
		return "", nil
	}

	if certificateId == "" {
		klog.Infof("Need to create certificate for secret %s", klog.KRef(namespace, secretName))
		createCertificate, err := CreateImportedTypeCertificate(tlsSecretData, certificateName, compartmentId, client.GetCertClient())
		if err != nil {
			return "", err
		}

		certificateId = *createCertificate.Id
	} else {
		cert, _, err := GetCertificate(&certificateId, client.GetCertClient())
		if err != nil {
			return "", err
		}

		if cert.FreeformTags == nil || hashPublicTlsData(tlsSecretData) != cert.FreeformTags[util.CertificateHashTagKey] {
			klog.Infof("Need to update certificate %s for secret %s", certificateId, klog.KRef(namespace, secretName))
			cert, err = UpdateImportedTypeCertificate(&certificateId, tlsSecretData, client.GetCertClient())
			if err != nil {
				return "", err
			}

			klog.Infof("Pruning Certificate %s for stale versions", certificateId)
			err = PruneCertificateVersions(certificateId, *cert.CurrentVersion.VersionNumber, certificateVersionsToPreserveCount, client.GetCertClient())
			if err != nil {
				klog.Errorf("Unable to prune certificate %s for stale versions: %s", certificateId, err.Error())
			}
		}

		if !isCertificateCurrentVersionLatest(cert) {
			klog.Warningf("For certificate %s, current version detected is not the latest one. "+
				"Please update secret %s to have the latest desired details.", certificateId, klog.KRef(namespace, secretName))
			if cert.LifecycleDetails != nil {
				klog.Warningf("Lifecycle details for certificate %s: %s", certificateId, *cert.LifecycleDetails)
			}
		}
	}

	return certificateId, nil
}

func ensureCaBundleForBackendSet(inputCaBundleId string, namespace string, secretName string, compartmentId string, secretLister v1.SecretLister, client *client.WrapperClient) (*string, error) {
	caBundleName, err := getCertificateNameFromSecret(namespace, secretName, secretLister)
	if err != nil {
		return nil, err
	}

	tlsSecretData, err := getTlsSecretContent(namespace, secretName, secretLister)
	if err != nil {
		return nil, err
	}

	caBundleId, err := VerifyOrGetCaBundleIdByName(inputCaBundleId, caBundleName, compartmentId, client.GetCertClient())
	if err != nil {
		return nil, err
	}

	if caBundleId == nil {
		klog.Infof("Need to create ca bundle for secret %s", klog.KRef(namespace, secretName))
		createCaBundle, err := CreateCaBundle(caBundleName, compartmentId, client.GetCertClient(), tlsSecretData.CaCertificateChain)
		if err != nil {
			return nil, err
		}

		caBundleId = createCaBundle.Id
	} else {
		caBundle, _, err := GetCaBundle(*caBundleId, client.GetCertClient())
		if err != nil {
			return nil, err
		}

		if caBundle.FreeformTags == nil || hashString(tlsSecretData.CaCertificateChain) != caBundle.FreeformTags[util.CaBundleHashTagKey] {
			klog.Infof("Detected hash mismatch for ca bundle related to secret %s, will update ca bundle %s",
				klog.KRef(namespace, secretName), *caBundle.Id)
			_, err = UpdateCaBundle(*caBundleId, client.GetCertClient(), tlsSecretData.CaCertificateChain)
			if err != nil {
				return nil, err
			}
		}
	}

	return caBundleId, nil
}

func isTrustAuthorityCaBundle(id string) bool {
	return strings.Contains(id, "cabundle")
}

func backendSetSslConfigNeedsUpdate(calculatedConfig *ociloadbalancer.SslConfigurationDetails,
	currentBackendSet *ociloadbalancer.BackendSet) bool {
	if calculatedConfig == nil && currentBackendSet.SslConfiguration != nil {
		return true
	}

	if calculatedConfig != nil && (currentBackendSet.SslConfiguration == nil ||
		!reflect.DeepEqual(currentBackendSet.SslConfiguration.TrustedCertificateAuthorityIds, calculatedConfig.TrustedCertificateAuthorityIds)) {
		return true
	}

	return false
}

func isCertificateCurrentVersionLatest(cert *certificatesmanagement.Certificate) bool {
	for _, stage := range cert.CurrentVersion.Stages {
		if stage == certificatesmanagement.VersionStageLatest {
			return true
		}
	}
	return false
}
