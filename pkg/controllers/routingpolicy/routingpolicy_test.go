package routingpolicy

import (
	"context"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"

	"k8s.io/client-go/tools/cache"
)

func TestEnsureRoutingRules(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, "routePath.yaml")

	err := c.ensureRoutingRules(&ingressClassList.Items[0])
	Expect(err == nil).Should(Equal(true))
}
func TestProcessRoutingPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, "routePath.yaml")

	listenerPaths := map[string][]*listenerPath{}
	desiredRoutingPolicies := sets.NewString()

	var ingresses []*networkingv1.Ingress

	var rules []networkingv1.IngressRule

	var httpIngressPath []networkingv1.HTTPIngressPath
	prefix := networkingv1.PathTypePrefix
	backend1 := networkingv1.IngressServiceBackend{
		Name: "nsacs-healthcheck-ui",
		Port: networkingv1.ServiceBackendPort{
			Number: 8000,
		},
	}
	backend2 := networkingv1.IngressServiceBackend{
		Name: "nsacs-auth-service",
		Port: networkingv1.ServiceBackendPort{
			Number: 3005,
		},
	}
	backend3 := networkingv1.IngressServiceBackend{
		Name: "nsacs-healthcheck-data",
		Port: networkingv1.ServiceBackendPort{
			Number: 3010,
		},
	}
	path1 := networkingv1.HTTPIngressPath{
		Path:     "/ui",
		PathType: &prefix,
		Backend: networkingv1.IngressBackend{
			Service:  &backend1,
			Resource: nil,
		},
	}
	path2 := networkingv1.HTTPIngressPath{
		Path:     "/auth",
		PathType: &prefix,
		Backend: networkingv1.IngressBackend{
			Service:  &backend2,
			Resource: nil,
		},
	}
	path3 := networkingv1.HTTPIngressPath{
		Path:     "/data",
		PathType: &prefix,
		Backend: networkingv1.IngressBackend{
			Service:  &backend3,
			Resource: nil,
		},
	}
	httpIngressPath = append(httpIngressPath, path1)
	httpIngressPath = append(httpIngressPath, path2)
	httpIngressPath = append(httpIngressPath, path3)
	rule := networkingv1.IngressRule{
		Host: "",
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: httpIngressPath,
			},
		},
	}
	rules = append(rules, rule)

	ingress := networkingv1.Ingress{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: networkingv1.IngressSpec{
			Rules: rules,
		},
		Status: networkingv1.IngressStatus{},
	}
	ingresses = append(ingresses, &ingress)

	err := processRoutingPolicy(ingresses, c.serviceLister, listenerPaths, desiredRoutingPolicies)
	Expect(err == nil).Should(Equal(true))
	Expect(len(listenerPaths)).Should(Equal(3))
	Expect(len(desiredRoutingPolicies)).Should(Equal(3))
}

func TestRunPusher(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, "routePath.yaml")

	c.runPusher()
	Expect(c.queue.Len()).Should(Equal(1))
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList, yamlPath string) *Controller {

	ingressList := util.ReadResourceAsIngressList(yamlPath)
	testService := util.GetServiceListResource("test", "testecho1", 80)
	lbClient := getLoadBalancerClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	ingressClassInformer, ingressInformer, serviceLister, client := setUp(ctx, ingressClassList, ingressList, testService)
	c := NewController("oci.oraclecloud.com/native-ingress-controller",
		ingressClassInformer, ingressInformer, serviceLister, client, loadBalancerClient)
	return c
}

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *corev1.ServiceList) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, corelisters.ServiceLister, *fakeclientset.Clientset) {
	client := fakeclientset.NewSimpleClientset()

	action := "list"
	util.UpdateFakeClientCall(client, action, "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(client, action, "ingresses", ingressList)
	util.UpdateFakeClientCall(client, action, "services", testService)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)
	return ingressClassInformer, ingressInformer, serviceLister, client
}

func getLoadBalancerClient() client.LoadBalancerInterface {
	return &MockLoadBalancerClient{}
}

type MockLoadBalancerClient struct {
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	return res, nil
}

func (m MockLoadBalancerClient) CreateLoadBalancer(ctx context.Context, request ociloadbalancer.CreateLoadBalancerRequest) (ociloadbalancer.CreateLoadBalancerResponse, error) {
	return ociloadbalancer.CreateLoadBalancerResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteLoadBalancer(ctx context.Context, request ociloadbalancer.DeleteLoadBalancerRequest) (ociloadbalancer.DeleteLoadBalancerResponse, error) {
	return ociloadbalancer.DeleteLoadBalancerResponse{}, nil
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
	return ociloadbalancer.UpdateBackendSetResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteBackendSet(ctx context.Context, request ociloadbalancer.DeleteBackendSetRequest) (ociloadbalancer.DeleteBackendSetResponse, error) {
	return ociloadbalancer.DeleteBackendSetResponse{}, nil
}

func (m MockLoadBalancerClient) GetBackendSetHealth(ctx context.Context, request ociloadbalancer.GetBackendSetHealthRequest) (ociloadbalancer.GetBackendSetHealthResponse, error) {
	return ociloadbalancer.GetBackendSetHealthResponse{}, nil
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
	id := "id"
	return ociloadbalancer.UpdateRoutingPolicyResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
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
