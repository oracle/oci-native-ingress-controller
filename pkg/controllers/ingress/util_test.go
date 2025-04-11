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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/oracle/oci-native-ingress-controller/pkg/exception"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
)

func TestCompareSameTcpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := util.GetDefaultHeathChecker()
	healthChecker := &ociloadbalancer.HealthChecker{
		Protocol:         common.String(util.DefaultHealthCheckProtocol),
		Port:             common.Int(util.DefaultHealthCheckPort),
		TimeoutInMillis:  common.Int(util.DefaultHealthCheckTimeOutMilliSeconds),
		IntervalInMillis: common.Int(util.DefaultHealthCheckIntervalMilliSeconds),
		Retries:          common.Int(util.DefaultHealthCheckRetries),
	}
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(true))
}

func TestCompareDifferentTcpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := util.GetDefaultHeathChecker()
	details.Port = common.Int(7070)

	healthChecker := &ociloadbalancer.HealthChecker{
		Protocol:         common.String(util.DefaultHealthCheckProtocol),
		Port:             common.Int(util.DefaultHealthCheckPort),
		TimeoutInMillis:  common.Int(util.DefaultHealthCheckTimeOutMilliSeconds),
		IntervalInMillis: common.Int(util.DefaultHealthCheckIntervalMilliSeconds),
		Retries:          common.Int(util.DefaultHealthCheckRetries),
	}
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(false))
}

func TestCompareSameHttpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := getHeathCheckerDetails()
	healthChecker := getHeathChecker()
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(true))
}

func TestCompareDifferentHttpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)

	details := getHeathCheckerDetails()
	healthChecker := getHeathChecker()
	healthChecker.Port = common.Int(9090)
	healthChecker.ResponseBodyRegex = common.String("/")

	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(false))
}

func TestCompareTcpAndHttpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := util.GetDefaultHeathChecker()
	healthChecker := getHeathChecker()
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(false))
}

func getHeathCheckerDetails() *ociloadbalancer.HealthCheckerDetails {
	return &ociloadbalancer.HealthCheckerDetails{
		Protocol:          common.String(util.ProtocolHTTP),
		UrlPath:           common.String("/health"),
		Port:              common.Int(8080),
		ReturnCode:        common.Int(200),
		Retries:           common.Int(3),
		TimeoutInMillis:   common.Int(5000),
		IntervalInMillis:  common.Int(10000),
		ResponseBodyRegex: common.String("*"),
	}
}

func getHeathChecker() *ociloadbalancer.HealthChecker {
	return &ociloadbalancer.HealthChecker{
		Protocol:          common.String(util.ProtocolHTTP),
		UrlPath:           common.String("/health"),
		Port:              common.Int(8080),
		ReturnCode:        common.Int(200),
		Retries:           common.Int(3),
		TimeoutInMillis:   common.Int(5000),
		IntervalInMillis:  common.Int(10000),
		ResponseBodyRegex: common.String("*"),
	}
}

// SSL Tests

const (
	errorMsg        = "no cert found"
	namespace       = "test"
	errorImportCert = "errorImportCert"
)

func getSecretListerForSecretList(list *corev1.SecretList) corelisters.SecretLister {
	k8client := fakeclientset.NewSimpleClientset()
	util.FakeClientGetCall(k8client, "list", "secrets", list)
	informerFactory := informers.NewSharedInformerFactory(k8client, 0)
	secretInformer := informerFactory.Core().V1().Secrets()
	secretInformer.Lister()
	informerFactory.Start(context.Background().Done())
	cache.WaitForCacheSync(context.Background().Done(), secretInformer.Informer().HasSynced)
	return secretInformer.Lister()
}

func initsUtil(secretList *corev1.SecretList) (*client.ClientProvider, ociloadbalancer.LoadBalancer, corelisters.SecretLister) {
	k8client := fakeclientset.NewSimpleClientset()
	secretLister := getSecretListerForSecretList(secretList)

	certClient := GetCertClient()
	certManageClient := GetCertManageClient()
	certificatesClient := &certificate.CertificatesClient{
		ManagementClient:   certManageClient,
		CertificatesClient: certClient,
		CertCache:          map[string]*ociclient.CertCacheObj{},
		CaBundleCache:      map[string]*ociclient.CaBundleCacheObj{},
	}
	var trustCa []string
	trustCa = append(trustCa, "cabundle")

	sslConfig := ociloadbalancer.SslConfiguration{
		VerifyDepth:                    nil,
		VerifyPeerCertificate:          nil,
		TrustedCertificateAuthorityIds: trustCa,
		CertificateIds:                 nil,
		CertificateName:                nil,
		ServerOrderPreference:          "",
		CipherSuiteName:                nil,
		Protocols:                      nil,
	}
	bsName := "testecho1"
	bs := ociloadbalancer.BackendSet{
		Name:                                    &bsName,
		Policy:                                  nil,
		Backends:                                nil,
		HealthChecker:                           nil,
		SslConfiguration:                        &sslConfig,
		SessionPersistenceConfiguration:         nil,
		LbCookieSessionPersistenceConfiguration: nil,
	}
	var backendsets = map[string]ociloadbalancer.BackendSet{
		bsName: bs,
	}

	lb := ociloadbalancer.LoadBalancer{
		Id:                      nil,
		CompartmentId:           nil,
		DisplayName:             nil,
		LifecycleState:          "",
		TimeCreated:             nil,
		ShapeName:               nil,
		IpAddresses:             nil,
		ShapeDetails:            nil,
		IsPrivate:               nil,
		SubnetIds:               nil,
		NetworkSecurityGroupIds: nil,
		Listeners:               nil,
		Hostnames:               nil,
		SslCipherSuites:         nil,
		Certificates:            nil,
		BackendSets:             backendsets,
		PathRouteSets:           nil,
		FreeformTags:            nil,
		DefinedTags:             nil,
		SystemTags:              nil,
		RuleSets:                nil,
		RoutingPolicies:         nil,
	}
	wrapperClient := client.NewWrapperClient(k8client, nil, nil, certificatesClient, nil)
	mockClient := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	return mockClient, lb, secretLister
}

func TestHashPublicTlsData(t *testing.T) {
	RegisterTestingT(t)

	tlsData := TLSSecretData{
		CaCertificateChain: common.String("ca-cert-chain"),
		ServerCertificate:  common.String("server-cert"),
		PrivateKey:         common.String("server-key"),
	}

	hashedString := hashPublicTlsData(&tlsData)
	Expect(hashedString).Should(Equal("e6920702515dc87338e84377a9d9fb985c2a7c7140c4518217831e9fbbd7cb43"))

	// Ensure it is consistent
	Expect(hashPublicTlsData(&tlsData)).Should(Equal(hashedString))
}

func TestHashString(t *testing.T) {
	RegisterTestingT(t)

	testString := "test-string"
	hashedString := hashString(&testString)
	Expect(hashedString).Should(Equal("ffe65f1d98fafedea3514adc956c8ada5980c6c5d2552fd61f48401aefd5c00e"))
}

func TestHashStringShort(t *testing.T) {
	RegisterTestingT(t)

	testString := "test-string"
	hashedString := hashStringShort(&testString)
	Expect(hashedString).Should(Equal("ffe65f1d98fafedea3514adc956c8ada"))
}

func TestGetSSLConfigForBackendSet(t *testing.T) {
	RegisterTestingT(t)
	c, lb, secretLister := initsUtil(&corev1.SecretList{
		Items: []corev1.Secret{
			*util.GetSampleCertSecret(namespace, "oci-config", "chain", "cert", "key"),
		},
	})
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	config, err := GetSSLConfigForBackendSet(namespace, state.ArtifactTypeSecret, "oci-config", &lb, "testecho1", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa), &lb, "testecho1", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa), &lb, "testecho1", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeImported), &lb, "testecho1", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	// No ca bundle scenario
	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, errorImportCert, &lb, "testecho1", "", secretLister, mockClient)
	Expect(err).Should(BeNil())

	_, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, "error", &lb, "testecho1", "", secretLister, mockClient)
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal(errorMsg))

}

func TestGetSSLConfigForListener(t *testing.T) {
	RegisterTestingT(t)

	secretList := &corev1.SecretList{
		Items: []corev1.Secret{
			*util.GetSampleCertSecret(namespace, "secret", "chain", "cert", "key"),
			*util.GetSampleCertSecret(namespace, "secret-cert", "chain", "cert", "key"),
		},
	}

	c, _, secretLister := initsUtil(secretList)
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	//no listener for cert
	sslConfig, err := GetSSLConfigForListener(namespace, nil, state.ArtifactTypeCertificate, "certificate", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("certificate"))

	//no listener for secret
	sslConfig, err = GetSSLConfigForListener(namespace, nil, state.ArtifactTypeSecret, "secret", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("id"))

	// Listener + certificate
	var certIds []string
	certIds = append(certIds, "secret-cert", "cabundle")
	customSslConfig := ociloadbalancer.SslConfiguration{
		CertificateIds: certIds,
	}
	listener := ociloadbalancer.Listener{
		SslConfiguration: &customSslConfig,
	}
	sslConfig, err = GetSSLConfigForListener(namespace, &listener, state.ArtifactTypeCertificate, "certificate", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("certificate"))

	// Listener + secret
	sslConfig, err = GetSSLConfigForListener(namespace, &listener, state.ArtifactTypeSecret, "secret-cert", "", secretLister, mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("id"))

}

func TestGetTlsSecretContent(t *testing.T) {
	RegisterTestingT(t)

	testCaChain, testCert, testKey := generateTestCertsAndKey()

	secretWithCaCrt := util.GetSampleCertSecret("test", "secretWithCaCrt", testCaChain, testCert, testKey)
	secretWithCorrectChain := util.GetSampleCertSecret("test", "secretWithCorrectChain", "", testCert+testCaChain, testKey)
	secretWithWrongChain := util.GetSampleCertSecret("test", "secretWithWrongChain", "", testCaChain+testCert, testKey)
	secretWithoutCaCrt := util.GetSampleCertSecret("test", "secretWithoutCaCrt", "", testCert, testKey)

	secretList := &corev1.SecretList{
		Items: []corev1.Secret{
			*secretWithCaCrt,
			*secretWithCorrectChain,
			*secretWithWrongChain,
			*secretWithoutCaCrt,
		},
	}

	secretLister := getSecretListerForSecretList(secretList)

	secretData1, err := getTlsSecretContent("test", "secretWithCaCrt", secretLister)
	Expect(err).ToNot(HaveOccurred())
	Expect(*secretData1.CaCertificateChain).To(Equal(testCaChain))
	Expect(*secretData1.ServerCertificate).To(Equal(testCert))
	Expect(*secretData1.PrivateKey).To(Equal(testKey))

	secretData2, err := getTlsSecretContent("test", "secretWithCorrectChain", secretLister)
	Expect(err).ToNot(HaveOccurred())
	Expect(*secretData2.CaCertificateChain).To(Equal(testCaChain))
	Expect(*secretData2.ServerCertificate).To(Equal(testCert))
	Expect(*secretData2.PrivateKey).To(Equal(testKey))

	_, err = getTlsSecretContent("test", "secretWithWrongChain", secretLister)
	Expect(err).To(HaveOccurred())

	_, err = getTlsSecretContent("test", "secretWithoutCaCrt", secretLister)
	Expect(err).To(HaveOccurred())

	_, err = getTlsSecretContent("test", "nonexistent", secretLister)
	Expect(err).To(HaveOccurred())
}

func TestSplitLeafAndCaCertChain(t *testing.T) {
	RegisterTestingT(t)

	// tls.X509KeyPair validates key against leaf cert, so need to create an actual pair
	// Will create a CA and a test cert
	testCaChain, testCert, testKey := generateTestCertsAndKey()

	// Adding dummy intermediate certs to chain
	testCaChain = `-----BEGIN CERTIFICATE-----
intermediatecert
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
intermediatecert
-----END CERTIFICATE-----
` + testCaChain

	serverCert, caCertChain, err := splitLeafAndCaCertChain([]byte(testCert+testCaChain), []byte(testKey))
	Expect(err).ToNot(HaveOccurred())
	Expect(serverCert).To(Equal(testCert))
	Expect(caCertChain).To(Equal(testCaChain))

	serverCert, caCertChain, err = splitLeafAndCaCertChain([]byte(testCert), []byte(testKey))
	Expect(err).To(HaveOccurred())

	noCertString := "Has no certificates"
	serverCert, caCertChain, err = splitLeafAndCaCertChain([]byte(noCertString), []byte(testKey))
	Expect(err).To(HaveOccurred())
}

func TestBackendSetSslConfigNeedsUpdate(t *testing.T) {
	RegisterTestingT(t)

	caBundleId1 := []string{"caCert1"}
	caBundleId2 := []string{"caCert2"}

	backendSetWithNilSslConfig := &ociloadbalancer.BackendSet{}
	presentBackendSet1 := &ociloadbalancer.BackendSet{
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			TrustedCertificateAuthorityIds: caBundleId1,
		},
	}
	presentBackendSet2 := &ociloadbalancer.BackendSet{
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			TrustedCertificateAuthorityIds: caBundleId2,
		},
	}
	calculatedConfig1 := &ociloadbalancer.SslConfigurationDetails{
		TrustedCertificateAuthorityIds: caBundleId1,
	}

	Expect(backendSetSslConfigNeedsUpdate(nil, backendSetWithNilSslConfig)).To(BeFalse())
	Expect(backendSetSslConfigNeedsUpdate(nil, presentBackendSet1)).To(BeTrue())
	Expect(backendSetSslConfigNeedsUpdate(calculatedConfig1, presentBackendSet1)).To(BeFalse())
	Expect(backendSetSslConfigNeedsUpdate(calculatedConfig1, presentBackendSet2)).To(BeTrue())
	Expect(backendSetSslConfigNeedsUpdate(calculatedConfig1, backendSetWithNilSslConfig)).To(BeTrue())
}

func TestGetCertificateNameFromSecret(t *testing.T) {
	RegisterTestingT(t)

	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "secret",
			Namespace: "namespace",
			UID:       "uid",
		},
	}

	secretList := &corev1.SecretList{
		Items: []corev1.Secret{
			*secret,
		},
	}

	secretLister := getSecretListerForSecretList(secretList)

	secretName, err := getCertificateNameFromSecret("namespace", "", secretLister)
	Expect(err).To(BeNil())
	Expect(secretName).To(Equal(""))

	secretName, err = getCertificateNameFromSecret("namespace", "nonexistent", secretLister)
	Expect(err).ToNot(BeNil())
	Expect(secretName).To(Equal(""))

	secretName, err = getCertificateNameFromSecret("namespace", "secret", secretLister)
	Expect(err).To(BeNil())
	Expect(secretName).To(Equal("oci-nic-uid"))
}

func TestIsCertificateCurrentVersionLatest(t *testing.T) {
	RegisterTestingT(t)

	cert := &certificatesmanagement.Certificate{
		CurrentVersion: &certificatesmanagement.CertificateVersionSummary{
			Stages: []certificatesmanagement.VersionStageEnum{
				certificatesmanagement.VersionStageCurrent,
				certificatesmanagement.VersionStageFailed,
			},
		},
	}
	Expect(isCertificateCurrentVersionLatest(cert)).To(BeFalse())

	cert.CurrentVersion.Stages = []certificatesmanagement.VersionStageEnum{
		certificatesmanagement.VersionStageCurrent,
		certificatesmanagement.VersionStageLatest,
	}
	Expect(isCertificateCurrentVersionLatest(cert)).To(BeTrue())
}

func generateTestCertsAndKey() (string, string, string) {
	caCert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"MyOrg, INC."},
			Country:      []string{"US"},
			Province:     []string{"CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caPrivKey, _ := rsa.GenerateKey(rand.Reader, 4096)
	caBytes, _ := x509.CreateCertificate(rand.Reader, caCert, caCert, &caPrivKey.PublicKey, caPrivKey)
	testCaChain := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes}))

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"MyOrg, INC."},
			Country:      []string{"US"},
			Province:     []string{"CA"},
		},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(10, 0, 0),
	}
	certPrivKey, _ := rsa.GenerateKey(rand.Reader, 4096)
	certBytes, _ := x509.CreateCertificate(rand.Reader, cert, caCert, &certPrivKey.PublicKey, caPrivKey)
	testCert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes}))
	testKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey)}))

	return testCaChain, testCert, testKey
}

func GetCertManageClient() ociclient.CertificateManagementInterface {
	return &MockCertificateManagerClient{}
}

type MockCertificateManagerClient struct {
}

func (m MockCertificateManagerClient) CreateCertificate(ctx context.Context, request certificatesmanagement.CreateCertificateRequest) (certificatesmanagement.CreateCertificateResponse, error) {
	if *request.Name == "error" {
		return certificatesmanagement.CreateCertificateResponse{}, errors.New("cert create error")
	}

	id := "id"
	etag := "etag"
	return certificatesmanagement.CreateCertificateResponse{
		RawResponse: nil,
		Certificate: certificatesmanagement.Certificate{
			Id: &id,
		},
		Etag:         &etag,
		OpcRequestId: &id,
	}, nil
}

func (m MockCertificateManagerClient) GetCertificate(ctx context.Context, request certificatesmanagement.GetCertificateRequest) (certificatesmanagement.GetCertificateResponse, error) {

	if *request.CertificateId == "error" {
		return certificatesmanagement.GetCertificateResponse{}, errors.New(errorMsg)
	}
	id := "id"
	name := "cert"
	authorityId := "authId"
	etag := "etag"
	var confType certificatesmanagement.CertificateConfigTypeEnum
	if *request.CertificateId == errorImportCert {
		name = "errorImportCert"
		confType = certificatesmanagement.CertificateConfigTypeImported
	} else {
		confType, _ = certificatesmanagement.GetMappingCertificateConfigTypeEnum(*request.CertificateId)
	}
	var number int64
	number = 234
	certVersionSummary := certificatesmanagement.CertificateVersionSummary{
		VersionNumber: &number,
	}
	return certificatesmanagement.GetCertificateResponse{
		RawResponse: nil,
		Certificate: certificatesmanagement.Certificate{
			Id:                           &id,
			Name:                         &name,
			ConfigType:                   confType,
			IssuerCertificateAuthorityId: &authorityId,
			CurrentVersion:               &certVersionSummary,
			LifecycleState:               certificatesmanagement.CertificateLifecycleStateActive,
		},
		Etag:         &etag,
		OpcRequestId: nil,
	}, nil
}

func (m MockCertificateManagerClient) ListCertificates(ctx context.Context, request certificatesmanagement.ListCertificatesRequest) (certificatesmanagement.ListCertificatesResponse, error) {
	if *request.Name == "error" {
		return certificatesmanagement.ListCertificatesResponse{}, errors.New("cert list error")
	}

	if *request.Name == "nonexistent" {
		return certificatesmanagement.ListCertificatesResponse{}, nil
	}

	id := "id"
	return certificatesmanagement.ListCertificatesResponse{
		RawResponse: nil,
		CertificateCollection: certificatesmanagement.CertificateCollection{
			Items: []certificatesmanagement.CertificateSummary{
				{
					Id: common.String(id),
				},
			},
		},
		OpcRequestId: &id,
		OpcNextPage:  &id,
	}, nil
}

func (m MockCertificateManagerClient) UpdateCertificate(ctx context.Context, request certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error) {
	if *request.CertificateId == "error" {
		return certificatesmanagement.UpdateCertificateResponse{}, errors.New("cert update error")
	}

	if *request.CertificateId == "conflictError" {
		return certificatesmanagement.UpdateCertificateResponse{}, &exception.ConflictServiceError{}
	}

	return certificatesmanagement.UpdateCertificateResponse{}, nil
}

func (m MockCertificateManagerClient) ScheduleCertificateDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateDeletionRequest) (certificatesmanagement.ScheduleCertificateDeletionResponse, error) {
	var err error
	if *request.CertificateId == "error" {
		err = errors.New("cert error deletion")
	}
	return certificatesmanagement.ScheduleCertificateDeletionResponse{}, err
}

func (m MockCertificateManagerClient) ListCertificateVersions(ctx context.Context, request certificatesmanagement.ListCertificateVersionsRequest) (certificatesmanagement.ListCertificateVersionsResponse, error) {
	if *request.CertificateId == "error" {
		return certificatesmanagement.ListCertificateVersionsResponse{}, errors.New("list cert versions error")
	}

	createCertificateVersionSummary := func(versionNumber int64, stages []certificatesmanagement.VersionStageEnum) certificatesmanagement.CertificateVersionSummary {
		return certificatesmanagement.CertificateVersionSummary{
			VersionNumber: common.Int64(versionNumber),
			Stages:        stages,
		}
	}

	return certificatesmanagement.ListCertificateVersionsResponse{
		CertificateVersionCollection: certificatesmanagement.CertificateVersionCollection{
			Items: []certificatesmanagement.CertificateVersionSummary{
				createCertificateVersionSummary(int64(9), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageFailed, certificatesmanagement.VersionStageLatest}),
				createCertificateVersionSummary(int64(8), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageCurrent}),
				createCertificateVersionSummary(int64(7), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageFailed}),
				createCertificateVersionSummary(int64(6), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStagePrevious}),
				createCertificateVersionSummary(int64(5), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageDeprecated}),
				createCertificateVersionSummary(int64(4), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageDeprecated}),
				createCertificateVersionSummary(int64(3), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageDeprecated}),
				createCertificateVersionSummary(int64(2), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageFailed}),
				createCertificateVersionSummary(int64(1), []certificatesmanagement.VersionStageEnum{certificatesmanagement.VersionStageDeprecated}),
			},
		},
	}, nil
}

func (m MockCertificateManagerClient) ScheduleCertificateVersionDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateVersionDeletionRequest) (certificatesmanagement.ScheduleCertificateVersionDeletionResponse, error) {
	if *request.CertificateId == "error" {
		return certificatesmanagement.ScheduleCertificateVersionDeletionResponse{}, errors.New("cert version delete error")
	}

	return certificatesmanagement.ScheduleCertificateVersionDeletionResponse{}, nil
}

func (m MockCertificateManagerClient) CreateCaBundle(ctx context.Context, request certificatesmanagement.CreateCaBundleRequest) (certificatesmanagement.CreateCaBundleResponse, error) {
	if *request.Name == "error" {
		return certificatesmanagement.CreateCaBundleResponse{}, errors.New("caBundle create error")
	}

	id := "id"
	etag := "etag"
	return certificatesmanagement.CreateCaBundleResponse{
		RawResponse: nil,
		CaBundle: certificatesmanagement.CaBundle{
			Id: &id,
		},
		Etag:         &etag,
		OpcRequestId: nil,
	}, nil
}

func (m MockCertificateManagerClient) GetCaBundle(ctx context.Context, request certificatesmanagement.GetCaBundleRequest) (certificatesmanagement.GetCaBundleResponse, error) {
	if *request.CaBundleId == "error" {
		return certificatesmanagement.GetCaBundleResponse{}, errors.New("no ca bundle found")
	}

	id := "id"
	name := "cabundle"
	etag := "etag"
	return certificatesmanagement.GetCaBundleResponse{
		RawResponse: nil,
		CaBundle: certificatesmanagement.CaBundle{
			Id:             &id,
			Name:           &name,
			LifecycleState: certificatesmanagement.CaBundleLifecycleStateActive,
		},
		OpcRequestId: &id,
		Etag:         &etag,
	}, nil
}

func (m MockCertificateManagerClient) ListCaBundles(ctx context.Context, request certificatesmanagement.ListCaBundlesRequest) (certificatesmanagement.ListCaBundlesResponse, error) {
	if *request.Name == "error" {
		return certificatesmanagement.ListCaBundlesResponse{}, errors.New("caBundle list error")
	}

	if *request.Name == "nonexistent" {
		return certificatesmanagement.ListCaBundlesResponse{}, nil
	}

	var items []certificatesmanagement.CaBundleSummary
	name := "ic-oci-config"
	id := "id"
	item := certificatesmanagement.CaBundleSummary{
		Id:   &id,
		Name: &name,
	}
	items = append(items, item)

	return certificatesmanagement.ListCaBundlesResponse{
		RawResponse: nil,
		CaBundleCollection: certificatesmanagement.CaBundleCollection{
			Items: items,
		},
		OpcRequestId: nil,
		OpcNextPage:  nil,
	}, nil
}

func (m MockCertificateManagerClient) UpdateCaBundle(ctx context.Context, request certificatesmanagement.UpdateCaBundleRequest) (certificatesmanagement.UpdateCaBundleResponse, error) {
	if *request.CaBundleId == "error" {
		return certificatesmanagement.UpdateCaBundleResponse{}, errors.New("caBundle update error")
	}

	if *request.CaBundleId == "conflictError" {
		return certificatesmanagement.UpdateCaBundleResponse{}, &exception.ConflictServiceError{}
	}

	return certificatesmanagement.UpdateCaBundleResponse{}, nil
}

func (m MockCertificateManagerClient) DeleteCaBundle(ctx context.Context, request certificatesmanagement.DeleteCaBundleRequest) (certificatesmanagement.DeleteCaBundleResponse, error) {
	res := http.Response{
		Status: "200",
	}
	var err error
	if *request.CaBundleId == "error" {
		err = errors.New("error deleting cabundle")
	}
	return certificatesmanagement.DeleteCaBundleResponse{
		RawResponse:  &res,
		OpcRequestId: nil,
	}, err
}

func GetCertClient() ociclient.CertificateInterface {
	return &MockCertificateClient{}
}

type MockCertificateClient struct {
}

func (m MockCertificateClient) GetCertificateBundle(ctx context.Context, request certificates.GetCertificateBundleRequest) (certificates.GetCertificateBundleResponse, error) {

	var bundle certificates.CertificateBundle
	bundle = getMockBundle()

	return certificates.GetCertificateBundleResponse{
		RawResponse:       nil,
		CertificateBundle: bundle,
		Etag:              nil,
		OpcRequestId:      nil,
	}, nil
}

func getMockBundle() certificates.CertificateBundle {
	return &MockCertificateBundle{}
}

type MockCertificateBundle struct {
}

func (m MockCertificateBundle) GetCertificateId() *string {
	return nil
}

func (m MockCertificateBundle) GetCertificateName() *string {
	return nil
}

func (m MockCertificateBundle) GetVersionNumber() *int64 {
	return nil
}

func (m MockCertificateBundle) GetSerialNumber() *string {
	return nil
}

func (m MockCertificateBundle) GetTimeCreated() *common.SDKTime {
	return nil
}

func (m MockCertificateBundle) GetValidity() *certificates.Validity {
	return nil
}

func (m MockCertificateBundle) GetStages() []certificates.VersionStageEnum {
	return nil
}

func (m MockCertificateBundle) GetCertificatePem() *string {
	return nil
}

func (m MockCertificateBundle) GetCertChainPem() *string {
	data := "chain"
	return &data
}

func (m MockCertificateBundle) GetVersionName() *string {
	return nil
}

func (m MockCertificateBundle) GetRevocationStatus() *certificates.RevocationStatus {
	return nil
}

func (m MockCertificateClient) SetCertCache(cert *certificatesmanagement.Certificate) {

}

func (m MockCertificateClient) GetFromCertCache(certId string) *ociclient.CertCacheObj {
	cert := certificatesmanagement.Certificate{}
	var now time.Time
	if certId == "id" {
		now = time.Now()
	} else {
		now = time.Now()
		now.Add(time.Minute * 15)
	}
	return &ociclient.CertCacheObj{
		Cert: &cert,
		Age:  now,
	}
}

func (m MockCertificateClient) SetCaBundleCache(caBundle *certificatesmanagement.CaBundle) {

}

func (m MockCertificateClient) GetFromCaBundleCache(id string) *ociclient.CaBundleCacheObj {
	return nil
}

func (m MockCertificateClient) CreateCertificate(ctx context.Context, req certificatesmanagement.CreateCertificateRequest) (*certificatesmanagement.Certificate, error) {
	return nil, nil
}

func (m MockCertificateClient) CreateCaBundle(ctx context.Context, req certificatesmanagement.CreateCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	return nil, nil
}

func (m MockCertificateClient) GetCertificate(ctx context.Context, req certificatesmanagement.GetCertificateRequest) (*certificatesmanagement.Certificate, error) {
	id := "id"
	return &certificatesmanagement.Certificate{
		Id: &id,
	}, nil
}

func (m MockCertificateClient) ListCertificates(ctx context.Context, req certificatesmanagement.ListCertificatesRequest) (*certificatesmanagement.CertificateCollection, *string, error) {
	return nil, nil, nil
}

func (m MockCertificateClient) ScheduleCertificateDeletion(ctx context.Context, req certificatesmanagement.ScheduleCertificateDeletionRequest) error {
	return nil
}

func (m MockCertificateClient) GetCaBundle(ctx context.Context, req certificatesmanagement.GetCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	return nil, nil
}

func (m MockCertificateClient) ListCaBundles(ctx context.Context, req certificatesmanagement.ListCaBundlesRequest) (*certificatesmanagement.CaBundleCollection, error) {
	return nil, nil
}

func (m MockCertificateClient) DeleteCaBundle(ctx context.Context, req certificatesmanagement.DeleteCaBundleRequest) (*http.Response, error) {
	return nil, nil
}
