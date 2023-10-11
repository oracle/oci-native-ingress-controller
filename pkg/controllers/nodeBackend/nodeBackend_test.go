package nodeBackend

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"

	"k8s.io/client-go/tools/cache"
)

const (
	backendPath                   = "ingressPath.yaml"
	backendPathWithDefaultBackend = "ingressWithDefault.yaml"
	namespace                     = "default"
)

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *corev1.ServiceList, endpoints *corev1.EndpointsList, pod *corev1.PodList, nodes *corev1.NodeList) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, corelisters.ServiceLister, corelisters.EndpointsLister, corelisters.PodLister, corelisters.NodeLister, *fakeclientset.Clientset) {
	client := fakeclientset.NewSimpleClientset()

	action := "list"
	util.UpdateFakeClientCall(client, action, "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(client, action, "ingresses", ingressList)
	util.UpdateFakeClientCall(client, action, "services", testService)
	util.UpdateFakeClientCall(client, action, "endpoints", endpoints)
	util.UpdateFakeClientCall(client, "get", "endpoints", endpoints)
	util.UpdateFakeClientCall(client, action, "pods", pod)
	util.UpdateFakeClientCall(client, action, "nodes", nodes)
	util.UpdateFakeClientCall(client, "get", "nodes", &nodes.Items[0])

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	endpointInformer := informerFactory.Core().V1().Endpoints()
	endpointLister := endpointInformer.Lister()

	podInformer := informerFactory.Core().V1().Pods()
	podLister := podInformer.Lister()

	nodeInformer := informerFactory.Core().V1().Nodes()
	nodeLister := nodeInformer.Lister()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), endpointInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), podInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), nodeInformer.Informer().HasSynced)
	return ingressClassInformer, ingressInformer, serviceLister, endpointLister, podLister, nodeLister, client
}

func TestEnsureBackend(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, false)

	err := c.ensureBackends(&ingressClassList.Items[0], "id")
	Expect(err == nil).Should(Equal(true))
}

func TestRunPusher(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, false)

	c.runPusher()
	Expect(c.queue.Len()).Should(Equal(1))
}

func TestProcessNextItem(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, false)

	c.queue.Add("default-ingress-class")
	res := c.processNextItem()
	Expect(res).Should(BeTrue())
	time.Sleep(11 * time.Second) // since we get "ingress class not ready" error, and re-enqueue.
	Expect(c.queue.Len()).Should(Equal(1))
}

func TestProcessNextItemWithNginx(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithNginx()
	c := inits(ctx, ingressClassList, backendPath, false)

	c.queue.Add("nginx-ingress-class")
	res := c.processNextItem()
	Expect(res).Should(BeTrue())
	Expect(c.queue.Len()).Should(Equal(0))
}

func TestNoDefaultBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, false)
	ingresses, _ := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	backends, err := c.getDefaultBackends(ingresses)
	Expect(err == nil).Should(Equal(true))
	Expect(len(backends)).Should(Equal(0))
}
func TestDefaultBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPathWithDefaultBackend, false)
	ingresses, _ := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	backends, err := c.getDefaultBackends(ingresses)
	Expect(err == nil).Should(Equal(true))
	Expect(len(backends)).Should(Equal(1))
}

func TestGetEndpoints(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, true)

	endpoints, err := util.GetEndpoints(c.endpointLister, "test", "testecho1")
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal("endpoints \"testecho1\" not found"))

	endpoints, err = util.GetEndpoints(c.endpointLister, "default", "testecho1")
	Expect(err).Should(BeNil())
	Expect(endpoints).Should(Not(BeNil()))
	Expect(len(endpoints)).Should(Equal(2))
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList, yamlPath string, allCase bool) *Controller {

	ingressList := util.ReadResourceAsIngressList(yamlPath)
	testService := util.GetServiceListResource(namespace, "testecho1", 80)
	endpoints := util.GetEndpointsResourceList("testecho1", namespace, allCase)
	pod := util.GetPodResourceList("testpod", "echoserver")
	nodes := util.GetNodesList()
	lbClient := getLoadBalancerClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	ingressClassInformer, ingressInformer, serviceLister, endpointLister, podLister, nodeLister, k8client := setUp(ctx, ingressClassList, ingressList, testService, endpoints, pod, nodes)
	client := client.NewWrapperClient(k8client, nil, loadBalancerClient, nil)
	c := NewController("oci.oraclecloud.com/native-ingress-controller", ingressClassInformer, ingressInformer, serviceLister, endpointLister, podLister, nodeLister, client)
	return c
}

func TestListWithPredicate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, false)

	nodes, err := filterNodes(c.nodeLister)
	Expect(err).Should(BeNil())
	Expect(nodes).Should(Not(BeNil()))
	Expect(len(nodes)).Should(Equal(1))

}

func TestGetIngressesForClass(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath, false)
	ic, err := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	Expect(err == nil).Should(Equal(true))
	Expect(len(ic)).Should(Equal(1))
	count := 0
	for _, ingress := range ic {
		for _, rule := range ingress.Spec.Rules {
			for range rule.HTTP.Paths {
				count++
			}
		}
	}
	Expect(count).Should(Equal(1))

}

func getLoadBalancerClient() ociclient.LoadBalancerInterface {
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
	res := util.SampleLoadBalancerResponse()
	return res, nil
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
