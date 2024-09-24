package ingress

import (
	"context"
	"sync"
	"testing"

	coreinformers "k8s.io/client-go/informers/core/v1"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"

	"k8s.io/client-go/tools/cache"
)

const (
	ingressPath              = "ingressPath.yaml"
	ingressPathWithFinalizer = "ingressPathWithFinalizer.yaml"
)

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, coreinformers.ServiceAccountInformer, coreinformers.SecretInformer, corelisters.ServiceLister, *fakeclientset.Clientset) {
	fakeClient := fakeclientset.NewSimpleClientset()
	action := "list"

	util.UpdateFakeClientCall(fakeClient, action, "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(fakeClient, action, "ingresses", ingressList)
	util.UpdateFakeClientCall(fakeClient, "get", "ingresses", &ingressList.Items[0])
	util.UpdateFakeClientCall(fakeClient, "update", "ingresses", &ingressList.Items[0])
	util.UpdateFakeClientCall(fakeClient, "patch", "ingresses", &ingressList.Items[0])
	util.UpdateFakeClientCall(fakeClient, action, "services", testService)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	saInformer := informerFactory.Core().V1().ServiceAccounts()

	secretInformer := informerFactory.Core().V1().Secrets()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	return ingressClassInformer, ingressInformer, saInformer, secretInformer, serviceLister, fakeClient
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList) *Controller {

	testService := util.GetServiceListResource(namespace, "testecho1", 80)
	lbClient := GetLoadBalancerClient()
	certClient := GetCertClient()
	certManageClient := GetCertManageClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	certificatesClient := &certificate.CertificatesClient{
		ManagementClient:   certManageClient,
		CertificatesClient: certClient,
		CertCache:          map[string]*ociclient.CertCacheObj{},
		CaBundleCache:      map[string]*ociclient.CaBundleCacheObj{},
	}

	ingressClassInformer, ingressInformer, saInformer, secretInformer, serviceLister, k8client := setUp(ctx, ingressClassList, ingressList, testService)
	wrapperClient := client.NewWrapperClient(k8client, nil, loadBalancerClient, certificatesClient, nil)
	fakeClient := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	c := NewController("oci.oraclecloud.com/native-ingress-controller", "", ingressClassInformer,
		ingressInformer, saInformer, secretInformer, serviceLister, fakeClient, nil)
	return c
}

func TestSync(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.sync("default/ingress-readiness")

	Expect(err == nil).Should(Equal(false))
	Expect(err.Error()).Should(Equal("ingress class not ready"))
}

func TestEnsureIngressSuccess(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err == nil).Should(Equal(true))
}

func getContextWithClient(c *Controller, ctx context.Context) context.Context {
	wc, err := c.client.GetClient(&MockConfigGetter{})
	Expect(err).To(BeNil())
	ctx = context.WithValue(ctx, util.WrapperClient, wc)
	return ctx
}
func TestEnsureLoadBalancerIP(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.ensureLoadBalancerIP(getContextWithClient(c, ctx), "ip", &ingressList.Items[0])
	Expect(err == nil).Should(Equal(true))
}

func TestEnsureFinalizer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.ensureFinalizer(getContextWithClient(c, ctx), &ingressList.Items[0])
	Expect(err == nil).Should(Equal(true))
	err = c.ensureFinalizer(getContextWithClient(c, ctx), &ingressList.Items[1])
	Expect(err == nil).Should(Equal(true))
}

func TestDeleteIngress(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.deleteIngress(&ingressList.Items[0])
	Expect(err == nil).Should(Equal(true))
	err = c.deleteIngress(&ingressList.Items[1])
	Expect(err == nil).Should(Equal(true))
}

func TestIngressAdd(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	queueSize := c.queue.Len()
	c.ingressAdd(&ingressList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}

func TestIngressUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	queueSize := c.queue.Len()
	c.ingressUpdate(&ingressList.Items[0], &ingressList.Items[1])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}
func TestIngressDelete(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	queueSize := c.queue.Len()
	c.ingressDelete(&ingressList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}

func TestProcessNextItem(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)

	c.queue.Add("default-ingress-class")
	res := c.processNextItem()
	Expect(res).Should(BeTrue())
}

func GetLoadBalancerClient() ociclient.LoadBalancerInterface {
	return &MockLoadBalancerClient{}
}

type MockLoadBalancerClient struct {
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	return res, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{}, nil
}

func (m MockLoadBalancerClient) CreateLoadBalancer(ctx context.Context, request ociloadbalancer.CreateLoadBalancerRequest) (ociloadbalancer.CreateLoadBalancerResponse, error) {
	return ociloadbalancer.CreateLoadBalancerResponse{}, nil
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
	reqId := "opcrequestid"
	return ociloadbalancer.CreateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}, nil
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
	return ociloadbalancer.DeleteBackendSetResponse{
		OpcRequestId:     common.String("OpcRequestId"),
		OpcWorkRequestId: common.String("OpcWorkRequestId"),
	}, nil
}

func (m MockLoadBalancerClient) GetBackendSetHealth(ctx context.Context, request ociloadbalancer.GetBackendSetHealthRequest) (ociloadbalancer.GetBackendSetHealthResponse, error) {
	return ociloadbalancer.GetBackendSetHealthResponse{}, nil
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
	reqId := "opcrequestid"
	res := ociloadbalancer.CreateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}
	return res, nil
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
