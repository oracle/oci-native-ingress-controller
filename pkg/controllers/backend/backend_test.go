package backend

import (
	"bytes"
	"context"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/typed/core/v1/fake"
	fake2 "k8s.io/client-go/kubernetes/typed/networking/v1/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	k8stesting "k8s.io/client-go/testing"

	"k8s.io/client-go/tools/cache"
)

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *corev1.Service,
	endpoints *corev1.Endpoints, pod *corev1.Pod) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer,
	corelisters.ServiceLister, corelisters.EndpointsLister, corelisters.PodLister, *fakeclientset.Clientset) {
	client := fakeclientset.NewSimpleClientset()
	client.NetworkingV1().(*fake2.FakeNetworkingV1).
		PrependReactor("list", "ingressclasses", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, ingressClassList, nil
		})

	client.NetworkingV1().(*fake2.FakeNetworkingV1).
		PrependReactor("list", "ingresses", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, ingressList, nil
		})

	client.CoreV1().Services("").(*fake.FakeServices).Fake.
		PrependReactor("get", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, testService, nil
		})

	client.CoreV1().(*fake.FakeCoreV1).
		PrependReactor("get", "endpoints", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, endpoints, nil
		})

	client.CoreV1().(*fake.FakeCoreV1).
		PrependReactor("get", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, pod, nil
		})

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

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), endpointInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), podInformer.Informer().HasSynced)
	return ingressClassInformer, ingressInformer, serviceLister, endpointLister, podLister, client
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
