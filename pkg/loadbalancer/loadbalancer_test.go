package loadbalancer

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-native-ingress-controller/pkg/exception"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"

	"github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
)

type badRequestServiceError struct{}

func (e badRequestServiceError) Error() string {
	return "BadRequest"
}

func (e badRequestServiceError) GetHTTPStatusCode() int {
	return 400
}

func (e badRequestServiceError) GetMessage() string {
	return "BadRequest"
}

func (e badRequestServiceError) GetCode() string {
	return "InvalidParameter"
}

func (e badRequestServiceError) GetOpcRequestID() string {
	return "fake-opc-request-id"
}

type conflictServiceError struct{}

func (e conflictServiceError) Error() string {
	return "Conflict: already has listener 'route_8080'"
}

func (e conflictServiceError) GetHTTPStatusCode() int {
	return 409
}

func (e conflictServiceError) GetMessage() string {
	return "already has listener 'route_8080'"
}

func (e conflictServiceError) GetCode() string {
	return "Conflict"
}

func (e conflictServiceError) GetOpcRequestID() string {
	return "fake-opc-request-id"
}

func TestLoadBalancerClient_DeleteLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	err := loadBalancerClient.DeleteLoadBalancer(context.TODO(), "lbId")
	Expect(err).To(BeNil())
}

func TestLoadBalancerClient_CreateLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	id := "id"
	request := ociloadbalancer.CreateLoadBalancerRequest{
		OpcRequestId: &id,
	}
	_, err := loadBalancerClient.CreateLoadBalancer(context.TODO(), request)
	Expect(err).To(BeNil())
	id = "error"
	request = ociloadbalancer.CreateLoadBalancerRequest{
		OpcRequestId: &id,
	}
	_, err = loadBalancerClient.CreateLoadBalancer(context.TODO(), request)
	Expect(err).To(Not(BeNil()))
}

func TestLoadBalancerClient_GetBackendSetHealth(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	id := "id"
	_, err := loadBalancerClient.GetBackendSetHealth(context.TODO(), id, "k8s_adb5485972")
	Expect(err).To(BeNil())
}

func TestLoadBalancerClient_EnsureRoutingPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	var rules []ociloadbalancer.RoutingRule
	name := "route_80"
	condition := "cond"
	rule := ociloadbalancer.RoutingRule{
		Name:      &name,
		Condition: &condition,
		Actions:   nil,
	}
	rules = append(rules, rule)
	err := loadBalancerClient.EnsureRoutingPolicy(context.TODO(), "id", name, rules)
	Expect(err).To(BeNil())
	err = loadBalancerClient.EnsureRoutingPolicy(context.TODO(), "id", "listener", rules)
	Expect(err).To(Not(BeNil()))
	Expect(err.Error()).Should(Equal("listener listener not found"))
}

func TestLoadBalancerClient_CreateListener(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	id := "id"

	sslConfigDetail := getSslConfigurationDetails(id)

	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8080, util.ProtocolHTTP, util.DefaultBackendSetName, &sslConfigDetail)
	Expect(err).To(BeNil())
	err = loadBalancerClient.CreateListener(context.TODO(), "id", 8080, util.ProtocolHTTP2, util.DefaultBackendSetName, &sslConfigDetail)
	Expect(err).To(BeNil())

}

func TestLoadBalancerClient_CreateListener_PreservesMultiCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedCreateListenerRequest = nil

	sslConfigDetail := getSslConfigurationDetails("id")
	sslConfigDetail.CertificateIds = []string{"cert-1", "cert-2", "cert-3"}
	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8443, util.ProtocolHTTP, util.DefaultBackendSetName, &sslConfigDetail)
	Expect(err).To(BeNil())
	Expect(capturedCreateListenerRequest).ToNot(BeNil())
	Expect(capturedCreateListenerRequest.SslConfiguration.CertificateIds).To(Equal([]string{"cert-1", "cert-2", "cert-3"}))
}

func TestLoadBalancerClient_CreateListener_PassesDesiredTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedCreateListenerRequest = nil

	sslConfigDetail := getSslConfigurationDetails("id")
	sslConfigDetail.CipherSuiteName = common.String("desired-listener-cipher")
	sslConfigDetail.Protocols = []string{"TLSv1.2", "TLSv1.3"}
	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8443, util.ProtocolHTTP, util.DefaultBackendSetName, &sslConfigDetail)
	Expect(err).To(BeNil())
	Expect(capturedCreateListenerRequest).ToNot(BeNil())
	sslConfig := capturedCreateListenerRequest.SslConfiguration
	Expect(*sslConfig.CipherSuiteName).To(Equal("desired-listener-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestLoadBalancerClient_CreateGRPCListenerRequiresTLS(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedCreateListenerRequest = nil

	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8443, util.ProtocolGRPC, util.DefaultBackendSetName, nil)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("no TLS configuration provided for a GRPC listener"))
	Expect(capturedCreateListenerRequest).To(BeNil())
}

func TestLoadBalancerClient_CreateGRPCListenerDoesNotSynthesizeTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedCreateListenerRequest = nil

	sslConfigDetail := getSslConfigurationDetails("id")
	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8443, util.ProtocolGRPC, util.DefaultBackendSetName, &sslConfigDetail)
	Expect(err).To(BeNil())
	Expect(capturedCreateListenerRequest).ToNot(BeNil())
	Expect(*capturedCreateListenerRequest.Protocol).To(Equal(util.ProtocolGRPC))
	Expect(capturedCreateListenerRequest.SslConfiguration.CipherSuiteName).To(BeNil())
	Expect(capturedCreateListenerRequest.SslConfiguration.Protocols).To(BeNil())
}

func TestLoadBalancerClient_CreateListener_WrapsUnsupportedCapabilityErrorsForMultiCert(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	mockCreateListenerErr = badRequestServiceError{}
	defer func() {
		mockCreateListenerErr = nil
	}()

	sslConfigDetail := getSslConfigurationDetails("id")
	sslConfigDetail.CertificateIds = []string{"cert-1", "cert-2"}
	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8443, util.ProtocolHTTP, util.DefaultBackendSetName, &sslConfigDetail)
	Expect(err).ToNot(BeNil())
	Expect(err.Error()).To(Equal(multiCertificateCapabilityErrorMessage))
	var capabilityErr *multiCertificateCapabilityError
	Expect(errors.As(err, &capabilityErr)).To(BeTrue())
}

func TestLoadBalancerClient_CreateListenerAlreadyExistsIsTransient(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	mockCreateListenerErr = conflictServiceError{}
	defer func() {
		mockCreateListenerErr = nil
	}()

	sslConfigDetail := getSslConfigurationDetails("id")
	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8080, util.ProtocolHTTP, util.DefaultBackendSetName, &sslConfigDetail)

	Expect(err).To(HaveOccurred())
	Expect(exception.HasTransientError(err)).To(BeTrue())
	Expect(err.Error()).To(ContainSubstring("may already be present"))
}

func getSslConfigurationDetails(id string) ociloadbalancer.SslConfigurationDetails {
	var certIds []string
	certIds = append(certIds, "secret-cert", "cabundle")
	sslConfigDetail := ociloadbalancer.SslConfigurationDetails{
		VerifyDepth:                    nil,
		VerifyPeerCertificate:          nil,
		TrustedCertificateAuthorityIds: nil,
		CertificateIds:                 certIds,
		CertificateName:                &id,
		Protocols:                      nil,
		CipherSuiteName:                nil,
		ServerOrderPreference:          "",
	}
	return sslConfigDetail
}

func TestLoadBalancerClient_UpdateBackends(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	ip := "127.0.0.29"
	port := 8080
	var backendsets []ociloadbalancer.BackendDetails
	details := ociloadbalancer.BackendDetails{
		IpAddress: &ip,
		Port:      &port,
	}
	backendsets = append(backendsets, details)

	err := loadBalancerClient.UpdateBackends(context.TODO(), "id", "testecho1", backendsets)
	Expect(err).To(Not(BeNil()))
	Expect(err.Error()).Should(Equal("backendset testecho1 was not found"))
	err = loadBalancerClient.UpdateBackends(context.TODO(), "id", "bs_f151df96ee98ff0", backendsets)
	Expect(err).To(BeNil())

}

func TestLoadBalancerClient_UpdateBackends_PreservesBackendSetTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateBackendSetRequest = nil
	mockLoadBalancerResponseMutator = func(res *ociloadbalancer.GetLoadBalancerResponse) {
		backendSet := res.BackendSets["bs_f151df96ee98ff0"]
		backendSet.SslConfiguration = &ociloadbalancer.SslConfiguration{
			TrustedCertificateAuthorityIds: []string{"ca-existing"},
			CertificateIds:                 []string{"listener-cert"},
			CertificateName:                common.String("legacy-cert-name"),
			CipherSuiteName:                common.String("existing-backend-cipher"),
			Protocols:                      []string{"TLSv1.2"},
		}
		res.BackendSets["bs_f151df96ee98ff0"] = backendSet
	}
	defer func() {
		mockLoadBalancerResponseMutator = nil
	}()

	ip := "127.0.0.29"
	port := 8080
	backends := []ociloadbalancer.BackendDetails{{
		IpAddress: &ip,
		Port:      &port,
	}}

	err := loadBalancerClient.UpdateBackends(context.TODO(), "id", "bs_f151df96ee98ff0", backends)
	Expect(err).To(BeNil())
	Expect(capturedUpdateBackendSetRequest).ToNot(BeNil())
	sslConfig := capturedUpdateBackendSetRequest.SslConfiguration
	Expect(sslConfig.TrustedCertificateAuthorityIds).To(Equal([]string{"ca-existing"}))
	Expect(sslConfig.CertificateIds).To(BeNil())
	Expect(sslConfig.CertificateName).To(BeNil())
	Expect(*sslConfig.CipherSuiteName).To(Equal("existing-backend-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.2"}))
}

func TestLoadBalancerClient_UpdateBackendSetDetails(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	lbId := "id"
	etag := "etag"
	policy := "policy"
	bsName := util.GenerateBackendSetName("default", "testecho1", 80)

	lb := util.SampleLoadBalancerResponse()
	sslConfigDetails := ociloadbalancer.SslConfigurationDetails{TrustedCertificateAuthorityIds: []string{"trusted-cert"}}
	healthCheckerDetails := ociloadbalancer.HealthCheckerDetails{}

	bs := lb.BackendSets[bsName]

	err := loadBalancerClient.UpdateBackendSetDetails(context.TODO(), lbId, etag, &bs, &sslConfigDetails,
		&healthCheckerDetails, policy, nil, nil)
	Expect(err).To(BeNil())
}

func TestLoadBalancerClient_UpdateBackendSetDetailsRejectsCustomizedCipherSuitePreserve(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateBackendSetRequest = nil

	lbId := "id"
	etag := "etag"
	policy := "policy"
	bsName := util.GenerateBackendSetName("default", "testecho1", 80)

	lb := util.SampleLoadBalancerResponse()
	bs := lb.BackendSets[bsName]
	bs.SslConfiguration = &ociloadbalancer.SslConfiguration{
		TrustedCertificateAuthorityIds: []string{"ca-current"},
		CipherSuiteName:                common.String("oci-customized-ssl-cipher-suite"),
		Protocols:                      []string{"TLSv1.1"},
	}
	sslConfigDetails := ociloadbalancer.SslConfigurationDetails{TrustedCertificateAuthorityIds: []string{"ca-desired"}}
	healthCheckerDetails := ociloadbalancer.HealthCheckerDetails{}

	err := loadBalancerClient.UpdateBackendSetDetails(context.TODO(), lbId, etag, &bs, &sslConfigDetails,
		&healthCheckerDetails, policy, nil, nil)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyPreserveFailed"))
	Expect(capturedUpdateBackendSetRequest).To(BeNil())
}

func TestLoadBalancerClient_UpdateBackendSetDetails_AllowsIntentionalBackendTLSDisable(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateBackendSetRequest = nil

	lbId := "id"
	etag := "etag"
	policy := "policy"
	bsName := util.GenerateBackendSetName("default", "testecho1", 80)

	lb := util.SampleLoadBalancerResponse()
	bs := lb.BackendSets[bsName]
	bs.SslConfiguration = &ociloadbalancer.SslConfiguration{
		TrustedCertificateAuthorityIds: []string{"ca-current"},
		CipherSuiteName:                common.String("existing-backend-cipher"),
		Protocols:                      []string{"TLSv1.2"},
	}
	healthCheckerDetails := ociloadbalancer.HealthCheckerDetails{}

	err := loadBalancerClient.UpdateBackendSetDetails(context.TODO(), lbId, etag, &bs, nil,
		&healthCheckerDetails, policy, nil, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateBackendSetRequest).ToNot(BeNil())
	Expect(capturedUpdateBackendSetRequest.SslConfiguration).To(BeNil())
}

func TestBackendSetSslConfigurationDetailsFromCurrentPreservesRequestableExistingPolicy(t *testing.T) {
	RegisterTestingT(t)

	current := &ociloadbalancer.SslConfiguration{
		TrustedCertificateAuthorityIds: []string{"ca-a"},
		CertificateIds:                 []string{"listener-cert"},
		CertificateName:                common.String("legacy-cert-name"),
		CipherSuiteName:                common.String("existing-backend-cipher"),
		Protocols:                      []string{"TLSv1.1"},
	}

	sslConfig, err := backendSetSslConfigurationDetailsFromCurrent(current)

	Expect(err).NotTo(HaveOccurred())
	Expect(sslConfig.TrustedCertificateAuthorityIds).To(Equal([]string{"ca-a"}))
	Expect(sslConfig.CertificateIds).To(BeNil())
	Expect(sslConfig.CertificateName).To(BeNil())
	Expect(*sslConfig.CipherSuiteName).To(Equal("existing-backend-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.1"}))
}

func TestBackendSetSslConfigurationDetailsFromCurrentRejectsCustomizedCipherSuitePreserve(t *testing.T) {
	RegisterTestingT(t)

	current := &ociloadbalancer.SslConfiguration{
		TrustedCertificateAuthorityIds: []string{"ca-a"},
		CipherSuiteName:                common.String("oci-customized-ssl-cipher-suite"),
		Protocols:                      []string{"TLSv1.1"},
	}

	sslConfig, err := backendSetSslConfigurationDetailsFromCurrent(current)

	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyPreserveFailed"))
	Expect(sslConfig).To(BeNil())
}

func TestLoadBalancerClient_DeleteBackendSet(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	err := loadBalancerClient.DeleteBackendSet(context.TODO(), "id", "bs_f151df96ee98ff0")
	Expect(err).To(BeNil())

	err = loadBalancerClient.DeleteBackendSet(context.TODO(), "id", "random")
	Expect(err).To(BeNil())

}

func TestLoadBalancerClient_DeleteRoutingPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	err := loadBalancerClient.DeleteRoutingPolicy(context.TODO(), "id", "route_80")
	Expect(err).To(BeNil())
	err = loadBalancerClient.DeleteRoutingPolicy(context.TODO(), "id", "random")
	Expect(err).To(Not(BeNil()))

}

func TestLoadBalancerClient_DeleteListener(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	err := loadBalancerClient.DeleteListener(context.TODO(), "id", "route_80")
	Expect(err).To(BeNil())
	err = loadBalancerClient.DeleteListener(context.TODO(), "id", "random")
	Expect(err).To(BeNil())
}

func TestLoadBalancerClient_CreateBackendSet(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	sslConfig := getSslConfigurationDetails("id")
	err := loadBalancerClient.CreateBackendSet(context.TODO(), "id", "bs_f151df96ee98ff0", "", nil, &sslConfig, nil, nil)
	Expect(err).To(BeNil())
	err = loadBalancerClient.CreateBackendSet(context.TODO(), "id", "bs_f151df96ee98778", "", nil, &sslConfig, nil, nil)
	Expect(err).To(BeNil())
	err = loadBalancerClient.CreateBackendSet(context.TODO(), "id", "error", "", nil, &sslConfig, nil, nil)
	Expect(err).To(Not(BeNil()))
}

func TestLoadBalancerClient_CreateBackendSet_PassesDesiredTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedCreateBackendSetRequest = nil

	sslConfig := ociloadbalancer.SslConfigurationDetails{
		TrustedCertificateAuthorityIds: []string{"ca-desired"},
		CipherSuiteName:                common.String("desired-backend-cipher"),
		Protocols:                      []string{"TLSv1.2"},
	}
	err := loadBalancerClient.CreateBackendSet(context.TODO(), "id", "new-backend-set", "policy", nil, &sslConfig, nil, nil)
	Expect(err).To(BeNil())
	Expect(capturedCreateBackendSetRequest).ToNot(BeNil())
	capturedSslConfig := capturedCreateBackendSetRequest.SslConfiguration
	Expect(capturedSslConfig.TrustedCertificateAuthorityIds).To(Equal([]string{"ca-desired"}))
	Expect(*capturedSslConfig.CipherSuiteName).To(Equal("desired-backend-cipher"))
	Expect(capturedSslConfig.Protocols).To(Equal([]string{"TLSv1.2"}))
}

func TestLoadBalancerClient_UpdateBackendSet_PassesDesiredTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateBackendSetRequest = nil

	sslConfig := ociloadbalancer.SslConfigurationDetails{
		TrustedCertificateAuthorityIds: []string{"ca-desired"},
		CipherSuiteName:                common.String("desired-backend-cipher"),
		Protocols:                      []string{"TLSv1.2"},
	}
	err := loadBalancerClient.UpdateBackendSet(context.TODO(), "id", "etag", "backend-set", "policy", nil, &sslConfig, nil, nil, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateBackendSetRequest).ToNot(BeNil())
	capturedSslConfig := capturedUpdateBackendSetRequest.SslConfiguration
	Expect(capturedSslConfig.TrustedCertificateAuthorityIds).To(Equal([]string{"ca-desired"}))
	Expect(*capturedSslConfig.CipherSuiteName).To(Equal("desired-backend-cipher"))
	Expect(capturedSslConfig.Protocols).To(Equal([]string{"TLSv1.2"}))
}

func TestLoadBalancerClient_CreateBackendSetIncludesInitialBackends(t *testing.T) {
	RegisterTestingT(t)
	mockClient := &captureLoadBalancerClient{}
	loadBalancerClient := &LoadBalancerClient{
		LbClient: mockClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*LbCacheObj{},
	}

	backends := []ociloadbalancer.BackendDetails{util.NewBackend("10.0.0.1", 8080)}
	err := loadBalancerClient.CreateBackendSet(context.TODO(), "id", "new_backend_set", "LEAST_CONNECTIONS",
		util.GetDefaultHeathChecker(), nil, nil, nil, backends)

	Expect(err).To(BeNil())
	Expect(mockClient.createBackendSetRequest).ToNot(BeNil())
	Expect(mockClient.createBackendSetRequest.CreateBackendSetDetails.Backends).To(HaveLen(1))
	Expect(*mockClient.createBackendSetRequest.CreateBackendSetDetails.Backends[0].IpAddress).To(Equal("10.0.0.1"))
	Expect(*mockClient.createBackendSetRequest.CreateBackendSetDetails.Backends[0].Port).To(Equal(8080))
}

func TestLoadBalancerClient_UpdateListener(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	proto2 := util.ProtocolHTTP2
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:              &pname,
		Port:              &port,
		Protocol:          &proto,
		RoutingPolicyName: &pname,
	}
	ssConfig := getSslConfigurationDetails(id)
	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &ssConfig, &proto, nil)
	Expect(err).To(BeNil())
	err = loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &ssConfig, &proto2, nil)
	Expect(err).To(BeNil())
}

func TestLoadBalancerClient_UpdateListenerPreservesExistingSSLWhenRequested(t *testing.T) {
	RegisterTestingT(t)
	mockClient := &captureLoadBalancerClient{}
	loadBalancerClient := &LoadBalancerClient{
		LbClient: mockClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*LbCacheObj{},
	}
	id := "id"
	name := "route_10901"
	proto := util.ProtocolTCP
	defaultBackendSet := util.DefaultBackendSetName
	port := 10901
	listener := ociloadbalancer.Listener{
		Name:                  &name,
		Port:                  &port,
		Protocol:              &proto,
		DefaultBackendSetName: &defaultBackendSet,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds: []string{"certificate-id"},
		},
	}

	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, nil, nil, &proto, &defaultBackendSet)
	Expect(err).To(BeNil())
	Expect(mockClient.updateListenerRequest).ToNot(BeNil())
	sslConfig := mockClient.updateListenerRequest.UpdateListenerDetails.SslConfiguration
	Expect(sslConfig).ToNot(BeNil())
	Expect(sslConfig.CertificateIds).To(Equal([]string{"certificate-id"}))
}

func TestLoadBalancerClient_UpdateListenerClearsSSLWhenNotPreserved(t *testing.T) {
	RegisterTestingT(t)
	mockClient := &captureLoadBalancerClient{}
	loadBalancerClient := &LoadBalancerClient{
		LbClient: mockClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*LbCacheObj{},
	}
	id := "id"
	name := "route_10901"
	proto := util.ProtocolTCP
	defaultBackendSet := util.DefaultBackendSetName
	port := 10901
	listener := ociloadbalancer.Listener{
		Name:                  &name,
		Port:                  &port,
		Protocol:              &proto,
		DefaultBackendSetName: &defaultBackendSet,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds: []string{"certificate-id"},
		},
	}

	err := loadBalancerClient.ClearListenerSSL(context.TODO(), &id, "", listener, nil, &proto, &defaultBackendSet)

	Expect(err).To(BeNil())
	Expect(mockClient.updateListenerRequest).ToNot(BeNil())
	Expect(mockClient.updateListenerRequest.UpdateListenerDetails.SslConfiguration).To(BeNil())
}

func TestLoadBalancerClient_UpdateListener_PreservesExistingMultiCertificateIdsWhenNilSslConfig(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds:  []string{"cert-a", "cert-b"},
			CertificateName: common.String("legacy-cert-name"),
			CipherSuiteName: common.String("existing-cipher"),
			Protocols:       []string{"TLSv1.3", "TLSv1.2"},
		},
	}
	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, nil, &proto, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	sslConfig := capturedUpdateListenerRequest.SslConfiguration
	Expect(sslConfig.CertificateIds).To(Equal([]string{"cert-a", "cert-b"}))
	Expect(sslConfig.CertificateName).To(BeNil())
	Expect(*sslConfig.CipherSuiteName).To(Equal("existing-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.3", "TLSv1.2"}))
}

func TestLoadBalancerClient_ClearListenerSSLDoesNotPreserveExistingSSL(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds:  []string{"cert-a", "cert-b"},
			CipherSuiteName: common.String("existing-cipher"),
			Protocols:       []string{"TLSv1.3", "TLSv1.2"},
		},
	}

	err := loadBalancerClient.ClearListenerSSL(context.TODO(), &id, "", listener, &pname, &proto, nil)

	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	Expect(capturedUpdateListenerRequest.SslConfiguration).To(BeNil())
}

func TestLoadBalancerClient_ClearListenerSSLRejectsHTTP2(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP2
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds: []string{"cert-a"},
		},
	}

	err := loadBalancerClient.ClearListenerSSL(context.TODO(), &id, "", listener, &pname, nil, nil)

	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyUnsupported"))
	Expect(capturedUpdateListenerRequest).To(BeNil())
}

func TestLoadBalancerClient_ClearListenerSSLRejectsGRPC(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolGRPC
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds: []string{"cert-a"},
		},
	}

	err := loadBalancerClient.ClearListenerSSL(context.TODO(), &id, "", listener, &pname, nil, nil)

	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyUnsupported"))
	Expect(capturedUpdateListenerRequest).To(BeNil())
}

func TestLoadBalancerClient_EnsureRoutingPolicy_PreservesListenerSSLPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil
	mockLoadBalancerResponseMutator = func(res *ociloadbalancer.GetLoadBalancerResponse) {
		listener := res.Listeners["route_80"]
		listener.RoutingPolicyName = nil
		listener.SslConfiguration = &ociloadbalancer.SslConfiguration{
			CertificateIds:  []string{"cert-a", "cert-b"},
			CertificateName: common.String("legacy-cert-name"),
			CipherSuiteName: common.String("existing-listener-cipher"),
			Protocols:       []string{"TLSv1.2", "TLSv1.3"},
		}
		res.Listeners["route_80"] = listener
	}
	defer func() {
		mockLoadBalancerResponseMutator = nil
	}()

	condition := "cond"
	rules := []ociloadbalancer.RoutingRule{{
		Name:      common.String("route_80"),
		Condition: &condition,
		Actions:   nil,
	}}

	err := loadBalancerClient.EnsureRoutingPolicy(context.TODO(), "id", "route_80", rules)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	Expect(*capturedUpdateListenerRequest.RoutingPolicyName).To(Equal("route_80"))
	sslConfig := capturedUpdateListenerRequest.SslConfiguration
	Expect(sslConfig.CertificateIds).To(Equal([]string{"cert-a", "cert-b"}))
	Expect(sslConfig.CertificateName).To(BeNil())
	Expect(*sslConfig.CipherSuiteName).To(Equal("existing-listener-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestLoadBalancerClient_UpdateListenerDetachRoutingPolicy_PreservesListenerSSLPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds:  []string{"cert-a", "cert-b"},
			CertificateName: common.String("legacy-cert-name"),
			CipherSuiteName: common.String("existing-listener-cipher"),
			Protocols:       []string{"TLSv1.2", "TLSv1.3"},
		},
	}

	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, nil, nil, &proto, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	Expect(capturedUpdateListenerRequest.RoutingPolicyName).To(BeNil())
	sslConfig := capturedUpdateListenerRequest.SslConfiguration
	Expect(sslConfig.CertificateIds).To(Equal([]string{"cert-a", "cert-b"}))
	Expect(sslConfig.CertificateName).To(BeNil())
	Expect(*sslConfig.CipherSuiteName).To(Equal("existing-listener-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestLoadBalancerClient_UpdateListenerRejectsCustomizedCipherSuitePreserve(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds:  []string{"cert-a"},
			CipherSuiteName: common.String("oci-customized-ssl-cipher-suite"),
			Protocols:       []string{"TLSv1.1"},
		},
	}

	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, nil, &proto, nil)

	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyPreserveFailed"))
	Expect(capturedUpdateListenerRequest).To(BeNil())
}

func TestLoadBalancerClient_UpdateListener_PreservesDesiredMultiCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
	}
	ssConfig := getSslConfigurationDetails(id)
	ssConfig.CertificateIds = []string{"cert-1", "cert-2", "cert-3"}
	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &ssConfig, &proto, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	Expect(capturedUpdateListenerRequest.SslConfiguration.CertificateIds).To(Equal([]string{"cert-1", "cert-2", "cert-3"}))
}

func TestLoadBalancerClient_UpdateListener_PassesDesiredTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
	}
	ssConfig := getSslConfigurationDetails(id)
	ssConfig.CipherSuiteName = common.String("desired-listener-cipher")
	ssConfig.Protocols = []string{"TLSv1.2", "TLSv1.3"}
	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &ssConfig, &proto, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	sslConfig := capturedUpdateListenerRequest.SslConfiguration
	Expect(*sslConfig.CipherSuiteName).To(Equal("desired-listener-cipher"))
	Expect(sslConfig.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestLoadBalancerClient_HTTP2DefaultDoesNotOverrideExplicitTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedCreateListenerRequest = nil
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP2
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
	}
	createSslConfig := getSslConfigurationDetails(id)
	createSslConfig.CertificateIds = []string{"cert-a", "cert-b"}
	createSslConfig.CipherSuiteName = common.String("oci-tls-12-13-ssl-cipher-suite-v3")
	createSslConfig.Protocols = []string{"TLSv1.2", "TLSv1.3"}
	err := loadBalancerClient.CreateListener(context.TODO(), "id", 8443, util.ProtocolHTTP2, util.DefaultBackendSetName, &createSslConfig)
	Expect(err).To(BeNil())
	Expect(capturedCreateListenerRequest).ToNot(BeNil())
	Expect(capturedCreateListenerRequest.SslConfiguration.CertificateIds).To(Equal([]string{"cert-a", "cert-b"}))
	Expect(*capturedCreateListenerRequest.SslConfiguration.CipherSuiteName).To(Equal("oci-tls-12-13-ssl-cipher-suite-v3"))
	Expect(capturedCreateListenerRequest.SslConfiguration.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))

	updateSslConfig := getSslConfigurationDetails(id)
	updateSslConfig.CertificateIds = []string{"cert-a", "cert-b"}
	updateSslConfig.CipherSuiteName = common.String("oci-tls-12-13-ssl-cipher-suite-v3")
	updateSslConfig.Protocols = []string{"TLSv1.2", "TLSv1.3"}
	err = loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &updateSslConfig, &proto, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	Expect(capturedUpdateListenerRequest.SslConfiguration.CertificateIds).To(Equal([]string{"cert-a", "cert-b"}))
	Expect(*capturedUpdateListenerRequest.SslConfiguration.CipherSuiteName).To(Equal("oci-tls-12-13-ssl-cipher-suite-v3"))
	Expect(capturedUpdateListenerRequest.SslConfiguration.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestLoadBalancerClient_UpdateGRPCListenerDoesNotSynthesizeTLSPolicy(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateListenerRequest = nil

	id := "id"
	pname := "route_80"
	proto := util.ProtocolGRPC
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
	}
	sslConfig := getSslConfigurationDetails(id)
	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &sslConfig, &proto, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateListenerRequest).ToNot(BeNil())
	Expect(*capturedUpdateListenerRequest.Protocol).To(Equal(util.ProtocolGRPC))
	Expect(capturedUpdateListenerRequest.SslConfiguration.CipherSuiteName).To(BeNil())
	Expect(capturedUpdateListenerRequest.SslConfiguration.Protocols).To(BeNil())
}

func TestLoadBalancerClient_UpdateListener_WrapsUnsupportedCapabilityErrorsForMultiCert(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	mockUpdateListenerErr = badRequestServiceError{}
	defer func() {
		mockUpdateListenerErr = nil
	}()

	id := "id"
	pname := "route_80"
	proto := util.ProtocolHTTP
	port := 8080
	listener := ociloadbalancer.Listener{
		Name:     &pname,
		Port:     &port,
		Protocol: &proto,
	}
	ssConfig := getSslConfigurationDetails(id)
	ssConfig.CertificateIds = []string{"cert-1", "cert-2"}

	err := loadBalancerClient.UpdateListener(context.TODO(), &id, "", listener, &pname, &ssConfig, &proto, nil)
	Expect(err).ToNot(BeNil())
	Expect(err.Error()).To(Equal(multiCertificateCapabilityErrorMessage))
	var capabilityErr *multiCertificateCapabilityError
	Expect(errors.As(err, &capabilityErr)).To(BeTrue())
}

func TestLoadBalancerClient_UpdateNetworkSecurityGroups(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()

	_, err := loadBalancerClient.UpdateNetworkSecurityGroups(context.TODO(), "id", []string{"id1", "id2"})
	Expect(err).To(BeNil())
}

func TestLoadBalancerClient_UpdateLoadBalancerSecurityAttributes(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateLoadBalancerRequest = nil

	securityAttributes := map[string]map[string]interface{}{
		"Oracle-ZPR": {
			"MaxEgressCount": map[string]interface{}{
				"value": "42",
				"mode":  "enforce",
			},
		},
	}

	_, err := loadBalancerClient.UpdateLoadBalancer(context.TODO(), "id", "name", nil, nil, securityAttributes)
	Expect(err).To(BeNil())
	Expect(capturedUpdateLoadBalancerRequest).ToNot(BeNil())
	Expect(capturedUpdateLoadBalancerRequest.SecurityAttributes).To(Equal(securityAttributes))
}

func TestLoadBalancerClient_UpdateLoadBalancerClearsExistingSecurityAttributes(t *testing.T) {
	RegisterTestingT(t)
	loadBalancerClient := setupLBClient()
	capturedUpdateLoadBalancerRequest = nil
	mockLoadBalancerResponseMutator = func(response *ociloadbalancer.GetLoadBalancerResponse) {
		response.SecurityAttributes = map[string]map[string]interface{}{
			"Oracle-ZPR": {"MaxEgressCount": "42"},
		}
	}
	defer func() { mockLoadBalancerResponseMutator = nil }()

	_, err := loadBalancerClient.UpdateLoadBalancer(context.TODO(), "id", "name", nil, nil, nil)
	Expect(err).To(BeNil())
	Expect(capturedUpdateLoadBalancerRequest).ToNot(BeNil())
	Expect(capturedUpdateLoadBalancerRequest.SecurityAttributes).ToNot(BeNil())
	Expect(capturedUpdateLoadBalancerRequest.SecurityAttributes).To(BeEmpty())
}

func setupLBClient() *LoadBalancerClient {
	lbClient := GetLoadBalancerClient()

	loadBalancerClient := &LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*LbCacheObj{},
	}
	return loadBalancerClient
}

func GetLoadBalancerClient() client.LoadBalancerInterface {
	return &MockLoadBalancerClient{}
}

var capturedCreateBackendSetRequest *ociloadbalancer.CreateBackendSetRequest
var capturedCreateListenerRequest *ociloadbalancer.CreateListenerRequest
var capturedUpdateBackendSetRequest *ociloadbalancer.UpdateBackendSetRequest
var capturedUpdateListenerRequest *ociloadbalancer.UpdateListenerRequest
var capturedUpdateLoadBalancerRequest *ociloadbalancer.UpdateLoadBalancerDetails
var mockCreateListenerErr error
var mockLoadBalancerResponseMutator func(*ociloadbalancer.GetLoadBalancerResponse)
var mockUpdateListenerErr error

type MockLoadBalancerClient struct {
}

func (m MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	details := request.UpdateLoadBalancerDetails
	capturedUpdateLoadBalancerRequest = &details
	return ociloadbalancer.UpdateLoadBalancerResponse{OpcWorkRequestId: common.String("id")}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateNetworkSecurityGroups(ctx context.Context,
	request ociloadbalancer.UpdateNetworkSecurityGroupsRequest) (ociloadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	return ociloadbalancer.UpdateNetworkSecurityGroupsResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	if mockLoadBalancerResponseMutator != nil {
		mockLoadBalancerResponseMutator(&res)
	}
	return res, nil
}

func (m MockLoadBalancerClient) CreateLoadBalancer(ctx context.Context, request ociloadbalancer.CreateLoadBalancerRequest) (ociloadbalancer.CreateLoadBalancerResponse, error) {
	id := "id"
	var err error
	if *request.OpcRequestId == "error" {
		err = errors.New("error creating lb")
	}
	return ociloadbalancer.CreateLoadBalancerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
}

func (m MockLoadBalancerClient) DeleteLoadBalancer(ctx context.Context, request ociloadbalancer.DeleteLoadBalancerRequest) (ociloadbalancer.DeleteLoadBalancerResponse, error) {
	return ociloadbalancer.DeleteLoadBalancerResponse{
		OpcRequestId:     common.String("OpcRequestId"),
		OpcWorkRequestId: common.String("OpcWorkRequestId"),
	}, nil
}

func (m MockLoadBalancerClient) GetWorkRequest(ctx context.Context, request ociloadbalancer.GetWorkRequestRequest) (ociloadbalancer.GetWorkRequestResponse, error) {
	id := "id"
	requestId := "opcrequestid"
	return ociloadbalancer.GetWorkRequestResponse{
		RawResponse: nil,
		WorkRequest: ociloadbalancer.WorkRequest{
			Id:             &id,
			LoadBalancerId: &id,
			Type:           nil,
			LifecycleState: ociloadbalancer.WorkRequestLifecycleStateSucceeded,
		},
		OpcRequestId: &requestId,
	}, nil
}

func (m MockLoadBalancerClient) CreateBackendSet(ctx context.Context, request ociloadbalancer.CreateBackendSetRequest) (ociloadbalancer.CreateBackendSetResponse, error) {
	copied := request
	capturedCreateBackendSetRequest = &copied
	id := "id"
	var err error
	if *request.Name == "error" {
		err = errors.New("backend creation error")
	}
	return ociloadbalancer.CreateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
}

func (m MockLoadBalancerClient) UpdateBackendSet(ctx context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	copied := request
	capturedUpdateBackendSetRequest = &copied
	id := "id"
	return ociloadbalancer.UpdateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m MockLoadBalancerClient) DeleteBackendSet(ctx context.Context, request ociloadbalancer.DeleteBackendSetRequest) (ociloadbalancer.DeleteBackendSetResponse, error) {
	id := "id"
	var err error
	if *request.BackendSetName == "testecho1" {
		err = errors.New("Error backend")
	}

	return ociloadbalancer.DeleteBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
}

func (m MockLoadBalancerClient) GetBackendSetHealth(ctx context.Context, request ociloadbalancer.GetBackendSetHealthRequest) (ociloadbalancer.GetBackendSetHealthResponse, error) {
	return ociloadbalancer.GetBackendSetHealthResponse{
		RawResponse: nil,
		BackendSetHealth: ociloadbalancer.BackendSetHealth{
			Status:                    ociloadbalancer.BackendSetHealthStatusOk,
			WarningStateBackendNames:  nil,
			CriticalStateBackendNames: nil,
			UnknownStateBackendNames:  nil,
			TotalBackendCount:         nil,
		},
		OpcRequestId: nil,
		ETag:         nil,
	}, nil
}

func (m MockLoadBalancerClient) CreateRoutingPolicy(ctx context.Context, request ociloadbalancer.CreateRoutingPolicyRequest) (ociloadbalancer.CreateRoutingPolicyResponse, error) {
	id := "id"
	return ociloadbalancer.CreateRoutingPolicyResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m MockLoadBalancerClient) UpdateRoutingPolicy(ctx context.Context, request ociloadbalancer.UpdateRoutingPolicyRequest) (ociloadbalancer.UpdateRoutingPolicyResponse, error) {
	return ociloadbalancer.UpdateRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteRoutingPolicy(ctx context.Context, request ociloadbalancer.DeleteRoutingPolicyRequest) (ociloadbalancer.DeleteRoutingPolicyResponse, error) {
	id := "id"
	var err error
	if *request.RoutingPolicyName == "random" {
		err = errors.New("route not found")
	}
	return ociloadbalancer.DeleteRoutingPolicyResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
}

func (m MockLoadBalancerClient) CreateListener(ctx context.Context, request ociloadbalancer.CreateListenerRequest) (ociloadbalancer.CreateListenerResponse, error) {
	copied := request
	capturedCreateListenerRequest = &copied
	id := "id"
	if mockCreateListenerErr != nil {
		return ociloadbalancer.CreateListenerResponse{
			RawResponse:      nil,
			OpcWorkRequestId: &id,
			OpcRequestId:     &id,
		}, mockCreateListenerErr
	}
	return ociloadbalancer.CreateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m MockLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	copied := request
	capturedUpdateListenerRequest = &copied
	id := "id"
	var err error
	if mockUpdateListenerErr != nil {
		err = mockUpdateListenerErr
	} else if *request.ListenerName == "error" {
		err = errors.New("listener error")
	}
	return ociloadbalancer.UpdateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
}

type captureLoadBalancerClient struct {
	MockLoadBalancerClient
	createBackendSetRequest *ociloadbalancer.CreateBackendSetRequest
	updateListenerRequest   *ociloadbalancer.UpdateListenerRequest
}

func (m *captureLoadBalancerClient) CreateBackendSet(ctx context.Context, request ociloadbalancer.CreateBackendSetRequest) (ociloadbalancer.CreateBackendSetResponse, error) {
	m.createBackendSetRequest = &request
	id := "id"
	return ociloadbalancer.CreateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m *captureLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	m.updateListenerRequest = &request
	id := "id"
	return ociloadbalancer.UpdateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m MockLoadBalancerClient) DeleteListener(ctx context.Context, request ociloadbalancer.DeleteListenerRequest) (ociloadbalancer.DeleteListenerResponse, error) {
	id := "id"
	var err error
	if *request.ListenerName == "error" {
		err = errors.New("listener error")
	}
	return ociloadbalancer.DeleteListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
}
