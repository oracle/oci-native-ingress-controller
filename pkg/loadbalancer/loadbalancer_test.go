package loadbalancer

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-native-ingress-controller/pkg/testutil"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"

	"github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
)

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
	err := loadBalancerClient.CreateBackendSet(context.TODO(), "id", "bs_f151df96ee98ff0", "", nil, &sslConfig)
	Expect(err).To(BeNil())
	err = loadBalancerClient.CreateBackendSet(context.TODO(), "id", "bs_f151df96ee98778", "", nil, &sslConfig)
	Expect(err).To(BeNil())
	err = loadBalancerClient.CreateBackendSet(context.TODO(), "id", "error", "", nil, &sslConfig)
	Expect(err).To(Not(BeNil()))
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

type MockLoadBalancerClient struct {
}

func (m MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{}, nil
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := testutil.SampleLoadBalancerResponse()
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
	id := "id"
	return ociloadbalancer.CreateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m MockLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	id := "id"
	var err error
	if *request.ListenerName == "error" {
		err = errors.New("listener error")
	}
	return ociloadbalancer.UpdateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, err
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
