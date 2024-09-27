package ingressclass

import (
	"context"
	"fmt"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-go-sdk/v65/waf"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/exception"

	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"

	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	WAF "github.com/oracle/oci-native-ingress-controller/pkg/waf"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	"k8s.io/client-go/tools/cache"
)

func TestEnsureLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)

	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestEnsureLoadBalancerWithLbIdSet(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	c := inits(ctx, ingressClassList)

	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestEnsureLoadBalancerWithNotFound(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()

	ingressClassList := util.GetIngressClassListWithLBSet("notfound")
	c := inits(ctx, ingressClassList)

	ic := &ingressClassList.Items[0]
	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), ic)
	Expect(err).Should(BeNil())

}

func TestEnsureLoadBalancerWithNetworkError(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()

	ingressClassList := util.GetIngressClassListWithLBSet("networkerror")
	c := inits(ctx, ingressClassList)

	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal("Failure due to network error"))
}

func TestIngressClassAdd(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	queueSize := c.queue.Len()
	c.ingressClassAdd(&ingressClassList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}

func TestIngressUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	queueSize := c.queue.Len()
	c.ingressClassUpdate(&ingressClassList.Items[0], &ingressClassList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}
func TestIngressClassDelete(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	queueSize := c.queue.Len()
	c.ingressClassDelete(&ingressClassList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}

func TestDeleteIngressClass(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.deleteIngressClass(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestDeleteLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.deleteLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestEnsureFinalizer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.ensureFinalizer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestSetupWebApplicationFirewall_WithPolicySet(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := "id"
	compartmentId := "ocid1.compartment.oc1..aaaaaaaaxaq3szzikh7cb53arlkdgbi4wz4g73qpnuqhdhqckr2d5rvdffya"
	annotations := map[string]string{util.IngressClassIsDefault: fmt.Sprint(false), util.IngressClassWafPolicyAnnotation: "ocid1.webappfirewallpolicy.oc1.phx.amaaaaaah4gjgpya3siqywzdmre3mv4op3rzpo"}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-withPolicy", annotations, "oci.oraclecloud.com/native-ingress-controller")
	c := inits(ctx, ingressClassList)
	err := c.setupWebApplicationFirewall(getContextWithClient(c, ctx), &ingressClassList.Items[0], &compartmentId, &id)
	Expect(err).Should(BeNil())
}

func TestSetupWebApplicationFirewall_NoPolicySet(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := "id"
	compartmentId := "ocid1.compartment.oc1..aaaaaaaaxaq3szzikh7cb53arlkdgbi4wz4g73qpnuqhdhqckr2d5rvdffya"

	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.setupWebApplicationFirewall(getContextWithClient(c, ctx), &ingressClassList.Items[0], &compartmentId, &id)
	Expect(err).Should(BeNil())
}

func TestCheckForIngressClassParameterUpdates(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	mockClient, err := c.client.GetClient(&MockConfigGetter{})
	Expect(err).To(BeNil())
	loadBalancer, _, _ := mockClient.GetLbClient().GetLoadBalancer(context.TODO(), "id")
	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			CompartmentId:    "",
			SubnetId:         "",
			LoadBalancerName: "testecho1-998",
			IsPrivate:        false,
			MinBandwidthMbps: 200,
			MaxBandwidthMbps: 400,
		},
	}
	err = c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), loadBalancer, &ingressClassList.Items[0], &icp, "etag")
	Expect(err).Should(BeNil())
}

func TestCheckForNetworkSecurityGroupsUpdate(t *testing.T) {
	RegisterTestingT(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingress-class-with-nsg",
		map[string]string{
			util.IngressClassNetworkSecurityGroupIdsAnnotation: "id1,id2,  id3",
			util.IngressClassLoadBalancerIdAnnotation:          "id",
		}, "oci.oraclecloud.com/native-ingress-controller")
	c := inits(ctx, ingressClassList)

	err := c.checkForNetworkSecurityGroupsUpdate(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).To(BeNil())
}

func TestDeleteFinalizer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	var finalizers []string
	finalizer := "oci.oraclecloud.com/ingress-controller-protection"
	finalizers = append(finalizers, finalizer)
	ingressClass := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "name",
			Annotations: map[string]string{util.IngressClassIsDefault: fmt.Sprint("isDefault")},
			Finalizers:  finalizers,
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: "controller",
		},
	}
	err := c.deleteFinalizer(getContextWithClient(c, ctx), ingressClass) // with finalizer
	Expect(err).Should(BeNil())
	err = c.deleteFinalizer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func getContextWithClient(c *Controller, ctx context.Context) context.Context {
	wc, err := c.client.GetClient(&MockConfigGetter{})
	Expect(err).To(BeNil())
	ctx = context.WithValue(ctx, util.WrapperClient, wc)
	return ctx
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList) *Controller {

	lbClient := getLoadBalancerClient()
	wafClient := getWafClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	firewallClient := &WAF.Client{
		WafClient: wafClient,
		Mu:        sync.Mutex{},
		Cache:     map[string]*WAF.CacheObj{},
	}

	ingressClassInformer, saInformer, k8client := setUp(ctx, ingressClassList)
	wrapperClient := client.NewWrapperClient(k8client, firewallClient, loadBalancerClient, nil, nil)
	mockClient := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	c := NewController("", "", "oci.oraclecloud.com/native-ingress-controller", ingressClassInformer, saInformer, mockClient, nil)
	return c
}

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList) (networkinginformers.IngressClassInformer, coreinformers.ServiceAccountInformer, *fakeclientset.Clientset) {
	fakeClient := fakeclientset.NewSimpleClientset()

	util.UpdateFakeClientCall(fakeClient, "list", "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(fakeClient, "patch", "ingressclasses", &ingressClassList.Items[0])

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	saInformer := informerFactory.Core().V1().ServiceAccounts()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)

	return ingressClassInformer, saInformer, fakeClient
}

func getLoadBalancerClient() ociclient.LoadBalancerInterface {
	return &MockLoadBalancerClient{}
}

func getWafClient() ociclient.WafInterface {
	return &MockWafClient{}
}

type MockWafClient struct {
}

func (m MockWafClient) GetWebAppFirewall(ctx context.Context, request waf.GetWebAppFirewallRequest) (waf.GetWebAppFirewallResponse, error) {
	return waf.GetWebAppFirewallResponse{}, nil
}

func (m MockWafClient) CreateWebAppFirewall(ctx context.Context, request waf.CreateWebAppFirewallRequest) (waf.CreateWebAppFirewallResponse, error) {

	return waf.CreateWebAppFirewallResponse{
		RawResponse: nil,
		WebAppFirewall: waf.WebAppFirewallLoadBalancer{
			Id: common.String("fireWallId"),
		},
		OpcRequestId: common.String("id"),
	}, nil
}

func (m MockWafClient) DeleteWebAppFirewall(ctx context.Context, request waf.DeleteWebAppFirewallRequest) (waf.DeleteWebAppFirewallResponse, error) {
	return waf.DeleteWebAppFirewallResponse{}, nil
}

type MockLoadBalancerClient struct {
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	if *request.LoadBalancerId == "networkerror" {
		return ociloadbalancer.GetLoadBalancerResponse{}, NetworkError{}
	}
	if *request.LoadBalancerId == "notfound" {
		return ociloadbalancer.GetLoadBalancerResponse{}, &exception.NotFoundServiceError{}
	}

	res := util.SampleLoadBalancerResponse()
	return res, nil
}

type NetworkError struct {
}

func (n NetworkError) Error() string {
	return "Failure due to network error"
}

func (m MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m MockLoadBalancerClient) UpdateNetworkSecurityGroups(ctx context.Context,
	request ociloadbalancer.UpdateNetworkSecurityGroupsRequest) (ociloadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	return ociloadbalancer.UpdateNetworkSecurityGroupsResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m MockLoadBalancerClient) CreateLoadBalancer(ctx context.Context, request ociloadbalancer.CreateLoadBalancerRequest) (ociloadbalancer.CreateLoadBalancerResponse, error) {
	id := "id"
	return ociloadbalancer.CreateLoadBalancerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
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
	return ociloadbalancer.CreateBackendSetResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateBackendSet(ctx context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	reqId := "opcrequestid"
	res := ociloadbalancer.UpdateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}
	return res, nil
}

func (m MockLoadBalancerClient) DeleteBackendSet(ctx context.Context, request ociloadbalancer.DeleteBackendSetRequest) (ociloadbalancer.DeleteBackendSetResponse, error) {
	return ociloadbalancer.DeleteBackendSetResponse{}, nil
}

func (m MockLoadBalancerClient) GetBackendSetHealth(ctx context.Context, request ociloadbalancer.GetBackendSetHealthRequest) (ociloadbalancer.GetBackendSetHealthResponse, error) {
	backendCount := 1
	return ociloadbalancer.GetBackendSetHealthResponse{
		RawResponse: nil,
		BackendSetHealth: ociloadbalancer.BackendSetHealth{
			Status:                    ociloadbalancer.BackendSetHealthStatusOk,
			WarningStateBackendNames:  nil,
			CriticalStateBackendNames: nil,
			UnknownStateBackendNames:  nil,
			TotalBackendCount:         &backendCount,
		},
		OpcRequestId: nil,
		ETag:         nil,
	}, nil
}

func (m MockLoadBalancerClient) CreateRoutingPolicy(ctx context.Context, request ociloadbalancer.CreateRoutingPolicyRequest) (ociloadbalancer.CreateRoutingPolicyResponse, error) {
	return ociloadbalancer.CreateRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateRoutingPolicy(ctx context.Context, request ociloadbalancer.UpdateRoutingPolicyRequest) (ociloadbalancer.UpdateRoutingPolicyResponse, error) {
	return ociloadbalancer.UpdateRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteRoutingPolicy(ctx context.Context, request ociloadbalancer.DeleteRoutingPolicyRequest) (ociloadbalancer.DeleteRoutingPolicyResponse, error) {
	return ociloadbalancer.DeleteRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) CreateListener(ctx context.Context, request ociloadbalancer.CreateListenerRequest) (ociloadbalancer.CreateListenerResponse, error) {
	return ociloadbalancer.CreateListenerResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	return ociloadbalancer.UpdateListenerResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteListener(ctx context.Context, request ociloadbalancer.DeleteListenerRequest) (ociloadbalancer.DeleteListenerResponse, error) {
	return ociloadbalancer.DeleteListenerResponse{}, nil
}

// MockConfigGetter is a mock implementation of the ConfigGetter interface for testing purposes.
type MockConfigGetter struct {
	ConfigurationProvider common.ConfigurationProvider
	Key                   string
	Error                 error
}

// NewMockConfigGetter creates a new instance of MockConfigGetter.
func NewMockConfigGetter(configurationProvider common.ConfigurationProvider, key string, err error) *MockConfigGetter {
	return &MockConfigGetter{
		ConfigurationProvider: configurationProvider,
		Key:                   key,
		Error:                 err,
	}
}
func (m *MockConfigGetter) GetConfigurationProvider() (common.ConfigurationProvider, error) {
	return m.ConfigurationProvider, m.Error
}
func (m *MockConfigGetter) GetKey() string {
	return m.Key
}

type MockCacheStore struct {
	client *client.WrapperClient
}

func (m *MockCacheStore) Add(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) Update(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) Delete(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) List() []interface{} {
	return nil
}

func (m *MockCacheStore) ListKeys() []string {
	return nil
}

func (m *MockCacheStore) Get(obj interface{}) (item interface{}, exists bool, err error) {
	return nil, true, nil
}

func (m *MockCacheStore) Replace(i []interface{}, s string) error {
	return nil
}

func (m *MockCacheStore) Resync() error {
	return nil
}

func NewMockCacheStore(client *client.WrapperClient) *MockCacheStore {
	return &MockCacheStore{
		client: client,
	}
}

func (m *MockCacheStore) GetByKey(key string) (item interface{}, exists bool, err error) {
	return m.client, true, nil
}
