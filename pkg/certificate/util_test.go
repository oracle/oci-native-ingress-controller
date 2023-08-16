package certificate

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	. "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
)

const (
	errorMsg        = "no cert found"
	namespace       = "test"
	errorImportCert = "errorImportCert"
)

func inits() (*CertificatesClient, *fakeclientset.Clientset, ociloadbalancer.LoadBalancer) {
	client := fakeclientset.NewSimpleClientset()
	secret := util.GetSampleCertSecret()
	action := "get"
	resource := "secrets"
	obj := secret
	util.FakeClientGetCall(client, action, resource, obj)

	certClient := GetCertClient()
	certManageClient := GetCertManageClient()
	certificatesClient := &CertificatesClient{
		ManagementClient:   certManageClient,
		CertificatesClient: certClient,
		CertCache:          map[string]*CertCacheObj{},
		CaBundleCache:      map[string]*CaBundleCacheObj{},
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

	return certificatesClient, client, lb
}

func TestGetSSLConfigForBackendSet(t *testing.T) {
	RegisterTestingT(t)
	certificatesClient, client, lb := inits()

	config, err := GetSSLConfigForBackendSet(namespace, state.ArtifactTypeSecret, "oci-config", &lb, "testecho1", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa), &lb, "testecho1", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa), &lb, "testecho1", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, string(certificatesmanagement.CertificateConfigTypeImported), &lb, "testecho1", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(config != nil).Should(BeTrue())

	// No ca bundle scenario
	config, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, errorImportCert, &lb, "testecho1", "", certificatesClient, client)
	Expect(err).Should(BeNil())

	_, err = GetSSLConfigForBackendSet(namespace, state.ArtifactTypeCertificate, "error", &lb, "testecho1", "", certificatesClient, client)
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal(errorMsg))

}

func TestGetSSLConfigForListener(t *testing.T) {
	RegisterTestingT(t)
	certificatesClient, client, _ := inits()

	//no listener for cert
	sslConfig, err := GetSSLConfigForListener(namespace, nil, state.ArtifactTypeCertificate, "certificate", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("certificate"))

	//no listener for secret
	sslConfig, err = GetSSLConfigForListener(namespace, nil, state.ArtifactTypeSecret, "secret", "", certificatesClient, client)
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
	sslConfig, err = GetSSLConfigForListener(namespace, &listener, state.ArtifactTypeCertificate, "certificate", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("certificate"))

	// Listener + secret
	sslConfig, err = GetSSLConfigForListener(namespace, &listener, state.ArtifactTypeSecret, "secret-cert", "", certificatesClient, client)
	Expect(err).Should(BeNil())
	Expect(sslConfig != nil).Should(BeTrue())
	Expect(len(sslConfig.CertificateIds)).Should(Equal(1))
	Expect(sslConfig.CertificateIds[0]).Should(Equal("id"))

}

func TestGetCertificate(t *testing.T) {
	RegisterTestingT(t)
	certificatesClient, _, _ := inits()

	certId := "id"
	certId2 := "id2"

	certificate, err := GetCertificate(&certId, certificatesClient)
	Expect(certificate != nil).Should(BeTrue())
	Expect(err).Should(BeNil())

	// cache fetch
	certificate, err = GetCertificate(&certId, certificatesClient)
	Expect(certificate != nil).Should(BeTrue())
	Expect(err).Should(BeNil())

	certificate, err = GetCertificate(&certId2, certificatesClient)
	Expect(certificate != nil).Should(BeTrue())
	Expect(err).Should(BeNil())
}

func GetCertManageClient() CertificateManagementInterface {
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

func GetCertClient() CertificateInterface {
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

func (m MockCertificateClient) GetFromCertCache(certId string) *CertCacheObj {
	cert := certificatesmanagement.Certificate{}
	var now time.Time
	if certId == "id" {
		now = time.Now()
	} else {
		now = time.Now()
		now.Add(time.Minute * 15)
	}
	return &CertCacheObj{
		Cert: &cert,
		Age:  now,
	}
}

func (m MockCertificateClient) SetCaBundleCache(caBundle *certificatesmanagement.CaBundle) {

}

func (m MockCertificateClient) GetFromCaBundleCache(id string) *CaBundleCacheObj {
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
