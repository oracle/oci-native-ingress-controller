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

func initsUtil() (*client.ClientProvider, ociloadbalancer.LoadBalancer) {
	k8client := fakeclientset.NewSimpleClientset()
	secret := util.GetSampleCertSecret("test", "oci-cert", "chain", "cert", "key")
	action := "get"
	resource := "secrets"
	obj := secret
	util.FakeClientGetCall(k8client, action, resource, obj)

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
	return mockClient, lb
}

func TestGetSSLConfigForBackendSet(t *testing.T) {
	RegisterTestingT(t)
	c, lb := initsUtil()
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	config, err := GetSSLConfigForBackendSet(namespace, state.ArtifactTypeSecret, "oci-config", &lb, "testecho1", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa), &lb, "testecho1", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa), &lb, "testecho1", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeImported), &lb, "testecho1", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	// No ca bundle scenario
	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, errorImportCert, &lb, "testecho1", "", mockClient)
	Expect(err).Should(BeNil())

	_, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, "error", &lb, "testecho1", "", mockClient)
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal(errorMsg))

}

func TestGetSSLConfigForListener(t *testing.T) {
	RegisterTestingT(t)
	c, _ := initsUtil()
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	//no listener for cert
	sslConfig, err := GetSSLConfigForListener(namespace, nil, state.ArtifactTypeCertificate, "certificate", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("certificate"))

	//no listener for secret
	sslConfig, err = GetSSLConfigForListener(namespace, nil, state.ArtifactTypeSecret, "secret", "", mockClient)
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
	sslConfig, err = GetSSLConfigForListener(namespace, &listener, state.ArtifactTypeCertificate, "certificate", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("certificate"))

	// Listener + secret
	sslConfig, err = GetSSLConfigForListener(namespace, &listener, state.ArtifactTypeSecret, "secret-cert", "", mockClient)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("id"))

}

func TestGetCertificate(t *testing.T) {
	RegisterTestingT(t)
	c, _ := initsUtil()
	mockClient, err := c.GetClient(&MockConfigGetter{})
	Expect(err).Should(BeNil())

	certId := "id"
	certId2 := "id2"

	certificate, _, err := GetCertificate(&certId, mockClient.GetCertClient())
	Expect(certificate != nil).Should(BeTrue())
	Expect(err).Should(BeNil())

	// cache fetch
	certificate, _, err = GetCertificate(&certId, mockClient.GetCertClient())
	Expect(certificate != nil).Should(BeTrue())
	Expect(err).Should(BeNil())

	certificate, _, err = GetCertificate(&certId2, mockClient.GetCertClient())
	Expect(certificate != nil).Should(BeTrue())
	Expect(err).Should(BeNil())
}

func TestGetTlsSecretContent(t *testing.T) {
	RegisterTestingT(t)

	testCaChain, testCert, testKey := generateTestCertsAndKey()

	secretWithCaCrt := util.GetSampleCertSecret("test", "secretWithCaCrt", testCaChain, testCert, testKey)
	secretWithCorrectChain := util.GetSampleCertSecret("test", "secretWithCorrectChain", "", testCert+testCaChain, testKey)
	secretWithWrongChain := util.GetSampleCertSecret("test", "secretWithWrongChain", "", testCaChain+testCert, testKey)
	secretWithoutCaCrt := util.GetSampleCertSecret("test", "secretWithoutCaCrt", "", testCert, testKey)

	k8client := fakeclientset.NewSimpleClientset()
	util.FakeClientGetCall(k8client, "get", "secrets", secretWithCaCrt)

	secretData1, err := getTlsSecretContent("test", "secretWithCaCrt", k8client)
	Expect(err).ToNot(HaveOccurred())
	Expect(*secretData1.CaCertificateChain).To(Equal(testCaChain))
	Expect(*secretData1.ServerCertificate).To(Equal(testCert))
	Expect(*secretData1.PrivateKey).To(Equal(testKey))

	util.FakeClientGetCall(k8client, "get", "secrets", secretWithCorrectChain)
	secretData2, err := getTlsSecretContent("test", "secretWithCorrectChain", k8client)
	Expect(err).ToNot(HaveOccurred())
	Expect(*secretData2.CaCertificateChain).To(Equal(testCaChain))
	Expect(*secretData2.ServerCertificate).To(Equal(testCert))
	Expect(*secretData2.PrivateKey).To(Equal(testKey))

	util.FakeClientGetCall(k8client, "get", "secrets", secretWithWrongChain)
	_, err = getTlsSecretContent("test", "secretWithWrongChain", k8client)
	Expect(err).To(HaveOccurred())

	util.FakeClientGetCall(k8client, "get", "secrets", secretWithoutCaCrt)
	_, err = getTlsSecretContent("test", "secretWithoutCaCrt", k8client)
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
	id := "id"
	return certificatesmanagement.CreateCertificateResponse{
		RawResponse: nil,
		Certificate: certificatesmanagement.Certificate{
			Id: &id,
		},
		Etag:         nil,
		OpcRequestId: &id,
	}, nil
}

func (m MockCertificateManagerClient) UpdateCertificate(ctx context.Context, request certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error) {
	id := "id"
	return certificatesmanagement.UpdateCertificateResponse{
		RawResponse: nil,
		Certificate: certificatesmanagement.Certificate{
			Id: &id,
		},
		Etag:         nil,
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
	var confType certificatesmanagement.CertificateConfigTypeEnum
	if *request.CertificateId == errorImportCert {
		name = "error"
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
		},
		Etag:         nil,
		OpcRequestId: nil,
	}, nil
}

func (m MockCertificateManagerClient) ListCertificates(ctx context.Context, request certificatesmanagement.ListCertificatesRequest) (certificatesmanagement.ListCertificatesResponse, error) {
	id := "id"
	return certificatesmanagement.ListCertificatesResponse{
		RawResponse:           nil,
		CertificateCollection: certificatesmanagement.CertificateCollection{},
		OpcRequestId:          &id,
		OpcNextPage:           &id,
	}, nil
}

func (m MockCertificateManagerClient) ScheduleCertificateDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateDeletionRequest) (certificatesmanagement.ScheduleCertificateDeletionResponse, error) {
	var err error
	if *request.CertificateId == "error" {
		err = errors.New("cert error deletion")
	}
	return certificatesmanagement.ScheduleCertificateDeletionResponse{}, err
}

func (m MockCertificateManagerClient) CreateCaBundle(ctx context.Context, request certificatesmanagement.CreateCaBundleRequest) (certificatesmanagement.CreateCaBundleResponse, error) {
	id := "id"
	return certificatesmanagement.CreateCaBundleResponse{
		RawResponse: nil,
		CaBundle: certificatesmanagement.CaBundle{
			Id: &id,
		},
		Etag:         nil,
		OpcRequestId: nil,
	}, nil
}

func (m MockCertificateManagerClient) GetCaBundle(ctx context.Context, request certificatesmanagement.GetCaBundleRequest) (certificatesmanagement.GetCaBundleResponse, error) {
	id := "id"
	name := "cabundle"
	return certificatesmanagement.GetCaBundleResponse{
		RawResponse: nil,
		CaBundle: certificatesmanagement.CaBundle{
			Id:   &id,
			Name: &name,
		},
		OpcRequestId: &id,
	}, nil
}

func (m MockCertificateManagerClient) ListCaBundles(ctx context.Context, request certificatesmanagement.ListCaBundlesRequest) (certificatesmanagement.ListCaBundlesResponse, error) {
	if *request.Name == "error" {
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
