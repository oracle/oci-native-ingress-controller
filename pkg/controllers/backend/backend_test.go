package backend

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/events"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"

	"k8s.io/client-go/tools/cache"
)

const (
	backendPath                                  = "backendPath.yaml"
	backendPathWithDefaultBackend                = "backendPathWithDefaultBackend.yaml"
	backendPathWithNamedTargetPortService        = "backendPathWithNamedTargetPortService.yaml"
	backendPathWithNamedTargetPortDefaultBackend = "backendPathWithNamedTargetPortDefaultBackend.yaml"
	namespace                                    = "default"
)

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *corev1.ServiceList, endpoints *corev1.EndpointsList, pod *corev1.PodList) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, coreinformers.ServiceAccountInformer, corelisters.ServiceLister, coreinformers.EndpointsInformer, coreinformers.PodInformer, *fakeclientset.Clientset) {
	fakeClient := fakeclientset.NewSimpleClientset()

	action := "list"
	util.UpdateFakeClientCall(fakeClient, action, "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(fakeClient, action, "ingresses", ingressList)
	util.UpdateFakeClientCall(fakeClient, action, "services", testService)
	util.UpdateFakeClientCall(fakeClient, action, "endpoints", endpoints)
	util.UpdateFakeClientCall(fakeClient, action, "pods", pod)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	endpointInformer := informerFactory.Core().V1().Endpoints()
	endpointInformer.Lister()

	podInformer := informerFactory.Core().V1().Pods()
	podInformer.Lister()

	saInformer := informerFactory.Core().V1().ServiceAccounts()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), endpointInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), podInformer.Informer().HasSynced)
	return ingressClassInformer, ingressInformer, saInformer, serviceLister, endpointInformer, podInformer, fakeClient
}

func TestEnsureBackend(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath)

	err := c.ensureBackends(getContextWithClient(c, ctx), &ingressClassList.Items[0], "id")
	Expect(err == nil).Should(Equal(true))
}

func TestEnsureBackendPublishesEventsForNonServiceBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath)
	fakeRecorder := c.eventRecorder.(*events.FakeRecorder)
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: namespace},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{
					Path:     "/resource",
					PathType: &pathType,
					Backend:  networkingv1.IngressBackend{Resource: &corev1.TypedLocalObjectReference{Name: "unsupported-resource"}},
				},
				{
					Path:     "/missing-service",
					PathType: &pathType,
				},
			}}},
		}}},
	}

	err := c.ensureBackendsForIngress(context.TODO(), ingress, &ingressClassList.Items[0], "id", nil)

	Expect(err).NotTo(HaveOccurred())
	Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(util.UnsupportedBackendReason)))
	Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(util.MissingServiceBackendReason)))
}

func TestRunPusher(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath)

	c.runPusher()
	Expect(c.queue.Len()).Should(Equal(1))
}

func TestProcessNextItem(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath)

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
	c := inits(ctx, ingressClassList, backendPath)

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
	c := inits(ctx, ingressClassList, backendPath)
	ingresses, _ := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	backends, err := c.getDefaultBackends(ingresses)
	Expect(err == nil).Should(Equal(true))
	Expect(len(backends)).Should(Equal(0))
}

func TestGetBackendDetailsRequiresServiceOptInForTerminatingPods(t *testing.T) {
	RegisterTestingT(t)

	deletionTimestamp := metav1.Now()
	podIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})
	Expect(podIndexer.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "ready-pod",
		Namespace: namespace,
		UID:       "ready-uid",
	}})).To(Succeed())
	Expect(podIndexer.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:              "terminating-pod",
		Namespace:         namespace,
		UID:               "terminating-uid",
		DeletionTimestamp: &deletionTimestamp,
	}})).To(Succeed())

	c := &Controller{podLister: corelisters.NewPodLister(podIndexer)}
	endpointAddresses := []corev1.EndpointAddress{
		{
			IP: "10.0.0.1",
			TargetRef: &corev1.ObjectReference{
				Kind: "Pod",
				Name: "ready-pod",
				UID:  "ready-uid",
			},
		},
		{
			IP: "10.0.0.2",
			TargetRef: &corev1.ObjectReference{
				Kind: "Pod",
				Name: "terminating-pod",
				UID:  "terminating-uid",
			},
		},
	}

	backends, err := c.getBackendDetails(namespace, endpointAddresses, 8080, true)

	Expect(err).NotTo(HaveOccurred())
	Expect(backends).To(HaveLen(2))
	Expect(*backends[0].Drain).To(BeFalse())
	Expect(*backends[1].Drain).To(BeTrue())

	// Disabled Services preserve the legacy backend shape without consulting Pods.
	backends, err = (&Controller{}).getBackendDetails(namespace, endpointAddresses, 8080, false)
	Expect(err).NotTo(HaveOccurred())
	Expect(backends).To(HaveLen(2))
	Expect(*backends[0].Drain).To(BeFalse())
	Expect(*backends[1].Drain).To(BeFalse())
}

func TestGetBackendDetailsLeavesStaleAddressesUndrainedAndContinues(t *testing.T) {
	RegisterTestingT(t)
	podIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	Expect(podIndexer.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "ready-pod",
		Namespace: namespace,
		UID:       "ready-uid",
	}})).To(Succeed())
	c := &Controller{podLister: corelisters.NewPodLister(podIndexer)}
	endpointAddresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: namespace, Name: "ready-pod", UID: "ready-uid"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: namespace, Name: "missing-pod", UID: "missing-uid"}},
	}

	backends, err := c.getBackendDetails(namespace, endpointAddresses, 8080, true)

	Expect(err).NotTo(HaveOccurred())
	Expect(backends).To(HaveLen(2))
	Expect(*backends[0].Drain).To(BeFalse())
	Expect(*backends[1].Drain).To(BeFalse())
}

func TestGetBackendDetailsRequiresMatchingPodIdentity(t *testing.T) {
	RegisterTestingT(t)
	deletionTimestamp := metav1.Now()
	podIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	Expect(podIndexer.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:              "recreated-pod",
		Namespace:         "endpoint-namespace",
		UID:               "new-uid",
		DeletionTimestamp: &deletionTimestamp,
	}})).To(Succeed())
	c := &Controller{podLister: corelisters.NewPodLister(podIndexer)}
	endpointAddresses := []corev1.EndpointAddress{
		{
			IP: "10.0.0.3",
			TargetRef: &corev1.ObjectReference{
				Kind:      "Pod",
				Namespace: "endpoint-namespace",
				Name:      "recreated-pod",
				UID:       "old-uid",
			},
		},
		{
			IP: "10.0.0.4",
			TargetRef: &corev1.ObjectReference{
				Kind:      "Pod",
				Namespace: "endpoint-namespace",
				Name:      "recreated-pod",
			},
		},
	}

	backends, err := c.getBackendDetails(namespace, endpointAddresses, 8080, true)

	Expect(err).NotTo(HaveOccurred())
	Expect(backends).To(HaveLen(2))
	Expect(*backends[0].Drain).To(BeFalse())
	Expect(*backends[1].Drain).To(BeFalse())
}

func TestPodTerminationEnqueuesAffectedIngressClass(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := inits(ctx, util.GetIngressClassList(), backendPath)
	setBackendDrainingAnnotation(c, "true", "testecho1")
	oldPod := util.GetPodResourceList("testpod", "echoserver").Items[0]
	newPod := oldPod.DeepCopy()
	deletionTimestamp := metav1.Now()
	newPod.DeletionTimestamp = &deletionTimestamp

	c.podUpdate(&oldPod, newPod)

	Expect(c.queue.Len()).To(Equal(1))
}

func TestPodTerminationDoesNotEnqueueServiceWithoutOptIn(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := inits(ctx, util.GetIngressClassList(), backendPath)
	oldPod := util.GetPodResourceList("testpod", "echoserver").Items[0]
	newPod := oldPod.DeepCopy()
	deletionTimestamp := metav1.Now()
	newPod.DeletionTimestamp = &deletionTimestamp

	c.podUpdate(&oldPod, newPod)

	Expect(c.queue.Len()).To(BeZero())
}

func TestEnsureBackendsPropagatesDefaultBackendErrors(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath)
	ingressClassName := ingressClassList.Items[0].Name
	ingressIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	Expect(ingressIndexer.Add(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "default-only", Namespace: namespace},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClassName,
			DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
				Name: "missing-service",
				Port: networkingv1.ServiceBackendPort{Number: 80},
			}},
		},
	})).To(Succeed())
	c.ingressLister = networkinglisters.NewIngressLister(ingressIndexer)

	err := c.ensureBackends(getContextWithClient(c, ctx), &ingressClassList.Items[0], "id")

	Expect(err).To(MatchError(ContainSubstring("missing-service")))
}

func TestEnsureBackendsPropagatesDefaultBackendUpdateErrors(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c, _, _ := initsWithLoadBalancerClient(ctx, ingressClassList, backendPathWithDefaultBackend, &failingDefaultLoadBalancerClient{})

	err := c.ensureBackends(getContextWithClient(c, ctx), &ingressClassList.Items[0], "id")

	Expect(err).To(MatchError(ContainSubstring("default backend update failed")))
}

func TestTerminationLifecycleReconcilesPathAndDefaultBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	lifecycleLB := newLifecycleLoadBalancerClient()
	c, endpointInformer, podInformer := initsWithLoadBalancerClient(ctx, ingressClassList, backendPathWithDefaultBackend, lifecycleLB)
	setBackendDrainingAnnotation(c, "true", "testecho1", "host-es")
	ingressClass := &ingressClassList.Items[0]

	// Healthy Pod and unchanged Endpoint membership require no OCI mutation.
	Expect(c.ensureBackends(getContextWithClient(c, ctx), ingressClass, "id")).To(Succeed())
	Expect(lifecycleLB.updateBackendRequests).To(BeEmpty())
	Expect(lifecycleLB.updateBackendSetRequests).To(BeEmpty())

	oldPod, err := podInformer.Lister().Pods(namespace).Get("testpod")
	Expect(err).NotTo(HaveOccurred())
	terminatingPod := oldPod.DeepCopy()
	deletionTimestamp := metav1.Now()
	terminatingPod.DeletionTimestamp = &deletionTimestamp
	Expect(podInformer.Informer().GetIndexer().Update(terminatingPod)).To(Succeed())

	// The Pod update event enqueues reconciliation immediately. Both the path
	// and default backend keep the same IP/port and transition to drain=true.
	c.podUpdate(oldPod, terminatingPod)
	Expect(c.queue.Len()).To(Equal(1))
	Expect(c.processNextItem()).To(BeTrue())
	Expect(lifecycleLB.updateBackendRequests).To(HaveLen(2))
	for _, request := range lifecycleLB.updateBackendRequests {
		Expect(*request.Drain).To(BeTrue())
		Expect(*request.BackendName).To(Equal("6.7.8.9:0"))
	}

	oldPathEndpoints, err := endpointInformer.Lister().Endpoints(namespace).Get("testecho1")
	Expect(err).NotTo(HaveOccurred())
	oldDefaultEndpoints, err := endpointInformer.Lister().Endpoints(namespace).Get("host-es")
	Expect(err).NotTo(HaveOccurred())
	removedPathEndpoints := oldPathEndpoints.DeepCopy()
	removedPathEndpoints.Subsets = nil
	removedDefaultEndpoints := oldDefaultEndpoints.DeepCopy()
	removedDefaultEndpoints.Subsets = nil
	Expect(endpointInformer.Informer().GetIndexer().Update(removedPathEndpoints)).To(Succeed())
	Expect(endpointInformer.Informer().GetIndexer().Update(removedDefaultEndpoints)).To(Succeed())

	// Endpoint removal enqueues another reconciliation and removes both OCI
	// backends after their drain window.
	c.endpointUpdate(oldPathEndpoints, removedPathEndpoints)
	c.endpointUpdate(oldDefaultEndpoints, removedDefaultEndpoints)
	Expect(c.queue.Len()).To(Equal(1))
	Expect(c.processNextItem()).To(BeTrue())
	Expect(lifecycleLB.updateBackendSetRequests).To(HaveLen(2))
	for _, request := range lifecycleLB.updateBackendSetRequests {
		Expect(request.Backends).To(BeEmpty())
	}
}

func TestTerminatingPodWithoutServiceOptInRemainsUndrained(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	lifecycleLB := newLifecycleLoadBalancerClient()
	c, _, podInformer := initsWithLoadBalancerClient(ctx, ingressClassList, backendPathWithDefaultBackend, lifecycleLB)

	pod, err := podInformer.Lister().Pods(namespace).Get("testpod")
	Expect(err).NotTo(HaveOccurred())
	terminatingPod := pod.DeepCopy()
	deletionTimestamp := metav1.Now()
	terminatingPod.DeletionTimestamp = &deletionTimestamp
	Expect(podInformer.Informer().GetIndexer().Update(terminatingPod)).To(Succeed())

	Expect(c.ensureBackends(getContextWithClient(c, ctx), &ingressClassList.Items[0], "id")).To(Succeed())
	Expect(lifecycleLB.updateBackendRequests).To(BeEmpty())
	Expect(lifecycleLB.updateBackendSetRequests).To(BeEmpty())
}

func TestDisablingServiceOptInUndrainsPathAndDefaultBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	lifecycleLB := newLifecycleLoadBalancerClient()
	for _, backendSetName := range []string{util.GenerateBackendSetName(namespace, "testecho1", 80), util.DefaultBackendSetName} {
		backendSet := lifecycleLB.response.BackendSets[backendSetName]
		backendSet.Backends[0].Drain = common.Bool(true)
		lifecycleLB.response.BackendSets[backendSetName] = backendSet
	}
	c, _, _ := initsWithLoadBalancerClient(ctx, ingressClassList, backendPathWithDefaultBackend, lifecycleLB)
	setBackendDrainingAnnotation(c, "false", "testecho1", "host-es")

	Expect(c.ensureBackends(getContextWithClient(c, ctx), &ingressClassList.Items[0], "id")).To(Succeed())
	Expect(lifecycleLB.updateBackendRequests).To(HaveLen(2))
	for _, request := range lifecycleLB.updateBackendRequests {
		Expect(*request.Drain).To(BeFalse())
	}
	Expect(lifecycleLB.updateBackendSetRequests).To(BeEmpty())
}

func TestStaleEndpointRemainsUndrainedThenIsRemovedAfterCacheCatchesUp(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	lifecycleLB := newLifecycleLoadBalancerClient()
	defaultBackendSet := lifecycleLB.response.BackendSets[util.DefaultBackendSetName]
	defaultBackendSet.Backends = nil
	lifecycleLB.response.BackendSets[util.DefaultBackendSetName] = defaultBackendSet
	c, endpointInformer, podInformer := initsWithLoadBalancerClient(ctx, ingressClassList, backendPath, lifecycleLB)
	setBackendDrainingAnnotation(c, "true", "testecho1")

	pod, err := podInformer.Lister().Pods(namespace).Get("testpod")
	Expect(err).NotTo(HaveOccurred())
	Expect(podInformer.Informer().GetIndexer().Delete(pod)).To(Succeed())

	// The periodic fallback sees the stale Endpoint after the Pod has already
	// disappeared. It preserves the undrained backend because the exact terminating
	// Pod cannot be verified, and continues instead of failing the set.
	c.runPusher()
	Expect(c.processNextItem()).To(BeTrue())
	Expect(lifecycleLB.updateBackendRequests).To(BeEmpty())

	oldEndpoints, err := endpointInformer.Lister().Endpoints(namespace).Get("testecho1")
	Expect(err).NotTo(HaveOccurred())
	removedEndpoints := oldEndpoints.DeepCopy()
	removedEndpoints.Subsets = nil
	Expect(endpointInformer.Informer().GetIndexer().Update(removedEndpoints)).To(Succeed())
	c.endpointUpdate(oldEndpoints, removedEndpoints)

	Expect(c.processNextItem()).To(BeTrue())
	Expect(lifecycleLB.updateBackendSetRequests).To(HaveLen(1))
	Expect(lifecycleLB.updateBackendSetRequests[0].Backends).To(BeEmpty())
}

func TestDefaultBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPathWithDefaultBackend)
	ingresses, _ := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	backends, err := c.getDefaultBackends(ingresses)
	Expect(err == nil).Should(Equal(true))
	Expect(len(backends)).Should(Equal(1))
}

func TestDefaultBackendsWithNamedTargetPort(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPathWithNamedTargetPortDefaultBackend)
	ingresses, _ := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	backends, err := c.getDefaultBackends(ingresses)
	Expect(err == nil).Should(Equal(true))
	Expect(len(backends)).Should(Equal(1))
}

func TestEnsureBackendWithNamedTargetPort(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPathWithNamedTargetPortService)
	err := c.ensureBackends(getContextWithClient(c, ctx), &ingressClassList.Items[0], "id")
	Expect(err == nil).Should(Equal(true))
}

func TestEnsurePodReadinessConditionWithExistingReadiness(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPathWithDefaultBackend)
	ingresses, _ := util.GetIngressesForClass(c.ingressLister, &ingressClassList.Items[0])
	ingress := ingresses[0]
	var readinessCondition corev1.PodConditionType
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			readinessCondition = util.GetPodReadinessCondition(ingress.Name, rule.Host, path)
			break
		}
		break
	}

	backendHealth := ociloadbalancer.BackendSetHealth{
		Status:                    ociloadbalancer.BackendSetHealthStatusOk,
		WarningStateBackendNames:  nil,
		CriticalStateBackendNames: nil,
		UnknownStateBackendNames:  nil,
		TotalBackendCount:         nil,
	}
	var condition []corev1.PodCondition
	condition = append(condition, corev1.PodCondition{
		Type:   readinessCondition,
		Status: corev1.ConditionTrue,
		Reason: "backend is healthy",
	})

	err := c.ensurePodReadinessCondition(context.TODO(), util.GetPodResourceWithReadiness("testecho1", "echoserver", "ingress-readiness", "foo.bar.com", condition), readinessCondition, &backendHealth, "testecho1")

	Expect(err == nil).Should(Equal(true))
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList, yamlPath string) *Controller {
	c, _, _ := initsWithLoadBalancerClient(ctx, ingressClassList, yamlPath, getLoadBalancerClient())
	return c
}

func initsWithLoadBalancerClient(ctx context.Context, ingressClassList *networkingv1.IngressClassList, yamlPath string, lbClient ociclient.LoadBalancerInterface) (*Controller, coreinformers.EndpointsInformer, coreinformers.PodInformer) {

	ingressList := util.ReadResourceAsIngressList(yamlPath)
	testService := util.GetServiceListResource(namespace, "testecho1", 80)
	endpoints := util.GetEndpointsResourceList("testecho1", namespace, false)
	pod := util.GetPodResourceList("testpod", "echoserver")

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	ingressClassInformer, ingressInformer, saInformer, serviceLister, endpointInformer, podInformer, k8client := setUp(ctx, ingressClassList, ingressList, testService, endpoints, pod)
	wrapperClient := client.NewWrapperClient(k8client, nil, loadBalancerClient, nil, nil, nil)
	client := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	fakeRecorder := events.NewFakeRecorder(10)
	metricsCollector := metric.NewIngressCollector("oci.oraclecloud.com/native-ingress-controller", prometheus.NewRegistry())
	c := NewController("oci.oraclecloud.com/native-ingress-controller", ingressClassInformer, ingressInformer, saInformer, serviceLister, endpointInformer, podInformer, client, metricsCollector, fakeRecorder)
	return c, endpointInformer, podInformer
}

func setBackendDrainingAnnotation(c *Controller, value string, serviceNames ...string) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, serviceName := range serviceNames {
		service, err := c.serviceLister.Services(namespace).Get(serviceName)
		Expect(err).NotTo(HaveOccurred())
		service = service.DeepCopy()
		if service.Annotations == nil {
			service.Annotations = map[string]string{}
		}
		service.Annotations[util.ServiceBackendDrainingAnnotation] = value
		Expect(indexer.Add(service)).To(Succeed())
	}
	c.serviceLister = corelisters.NewServiceLister(indexer)
}

func TestGetIngressesForClass(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList, backendPath)
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

func TestBuildPodConditionPatch(t *testing.T) {
	RegisterTestingT(t)
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test",
					Image: "echoserver",
				},
			},
		},
	}
	newCondition := corev1.PodCondition{
		Type:   corev1.ContainersReady,
		Status: corev1.ConditionTrue,
	}
	patch, err := BuildPodConditionPatch(pod, newCondition)
	Expect(err == nil).Should(Equal(true))
	Expect(bytes.Equal(patch, []byte("{\"status\":{\"conditions\":[{\"lastProbeTime\":null,\"lastTransitionTime\":null,\"status\":\"True\",\"type\":\"ContainersReady\"}]}}"))).Should(Equal(true))
}

func getContextWithClient(c *Controller, ctx context.Context) context.Context {
	wc, err := c.client.GetClient(&MockConfigGetter{})
	Expect(err).To(BeNil())
	ctx = context.WithValue(ctx, util.WrapperClient, wc)
	return ctx
}

func getLoadBalancerClient() ociclient.LoadBalancerInterface {
	return &MockLoadBalancerClient{}
}

type MockLoadBalancerClient struct {
}

type lifecycleLoadBalancerClient struct {
	MockLoadBalancerClient
	mu                       sync.Mutex
	response                 ociloadbalancer.GetLoadBalancerResponse
	updateBackendRequests    []ociloadbalancer.UpdateBackendRequest
	updateBackendSetRequests []ociloadbalancer.UpdateBackendSetRequest
}

type failingDefaultLoadBalancerClient struct {
	MockLoadBalancerClient
}

func (m *failingDefaultLoadBalancerClient) UpdateBackendSet(ctx context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	if *request.BackendSetName == util.DefaultBackendSetName {
		return ociloadbalancer.UpdateBackendSetResponse{}, fmt.Errorf("default backend update failed")
	}
	return m.MockLoadBalancerClient.UpdateBackendSet(ctx, request)
}

func newLifecycleLoadBalancerClient() *lifecycleLoadBalancerClient {
	response := util.SampleLoadBalancerResponse()
	addDefaultBackendSet(&response)
	pathBackendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	backendNames := []string{pathBackendSetName, util.DefaultBackendSetName}
	for _, backendSetName := range backendNames {
		backendSet := response.BackendSets[backendSetName]
		backendSet.Backends = []ociloadbalancer.Backend{{
			Name:      common.String("6.7.8.9:0"),
			IpAddress: common.String("6.7.8.9"),
			Port:      common.Int(0),
			Weight:    common.Int(1),
			Drain:     common.Bool(false),
			Backup:    common.Bool(false),
			Offline:   common.Bool(false),
		}}
		response.BackendSets[backendSetName] = backendSet
	}
	return &lifecycleLoadBalancerClient{response: response}
}

func addDefaultBackendSet(response *ociloadbalancer.GetLoadBalancerResponse) {
	if _, exists := response.BackendSets[util.DefaultBackendSetName]; exists {
		return
	}
	pathBackendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	defaultBackendSet := response.BackendSets[pathBackendSetName]
	defaultBackendSet.Name = common.String(util.DefaultBackendSetName)
	defaultBackendSet.Backends = nil
	response.BackendSets[util.DefaultBackendSetName] = defaultBackendSet
}

func (m *lifecycleLoadBalancerClient) GetLoadBalancer(_ context.Context, _ ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.response, nil
}

func (m *lifecycleLoadBalancerClient) UpdateBackend(_ context.Context, request ociloadbalancer.UpdateBackendRequest) (ociloadbalancer.UpdateBackendResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBackendRequests = append(m.updateBackendRequests, request)
	backendSet := m.response.BackendSets[*request.BackendSetName]
	for i := range backendSet.Backends {
		if *backendSet.Backends[i].Name == *request.BackendName {
			backendSet.Backends[i].Drain = request.Drain
		}
	}
	m.response.BackendSets[*request.BackendSetName] = backendSet
	return ociloadbalancer.UpdateBackendResponse{
		OpcWorkRequestId: common.String("work-request"),
		OpcRequestId:     common.String("request"),
	}, nil
}

func (m *lifecycleLoadBalancerClient) UpdateBackendSet(_ context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBackendSetRequests = append(m.updateBackendSetRequests, request)
	backendSet := m.response.BackendSets[*request.BackendSetName]
	backendSet.Backends = make([]ociloadbalancer.Backend, 0, len(request.Backends))
	for _, details := range request.Backends {
		name := fmt.Sprintf("%s:%d", *details.IpAddress, *details.Port)
		backendSet.Backends = append(backendSet.Backends, ociloadbalancer.Backend{
			Name:           &name,
			IpAddress:      details.IpAddress,
			Port:           details.Port,
			Weight:         details.Weight,
			MaxConnections: details.MaxConnections,
			Drain:          details.Drain,
			Backup:         details.Backup,
			Offline:        details.Offline,
		})
	}
	m.response.BackendSets[*request.BackendSetName] = backendSet
	return ociloadbalancer.UpdateBackendSetResponse{
		OpcWorkRequestId: common.String("work-request"),
		OpcRequestId:     common.String("request"),
	}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateNetworkSecurityGroups(ctx context.Context, request ociloadbalancer.UpdateNetworkSecurityGroupsRequest) (ociloadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	return ociloadbalancer.UpdateNetworkSecurityGroupsResponse{}, nil
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	addDefaultBackendSet(&res)
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
