/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */
package podreadiness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	corev1 "k8s.io/api/core/v1"

	cl "sigs.k8s.io/controller-runtime/pkg/client"

	admissionv1 "k8s.io/api/admission/v1"

	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/typed/core/v1/fake"
	fake2 "k8s.io/client-go/kubernetes/typed/networking/v1/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

const (
	podReadinessPath = "podreadinessingres.yaml"
)

func gvk(group, version, kind string) metav1.GroupVersionKind {
	return metav1.GroupVersionKind{Group: group, Version: version, Kind: kind}
}

func setUp(ctx context.Context, ingressList *networkingv1.IngressList, testService *v1.ServiceList) (networkinglisters.IngressLister, corelisters.ServiceLister, *fakeclientset.Clientset) {

	client := fakeclientset.NewSimpleClientset()
	client.NetworkingV1().(*fake2.FakeNetworkingV1).
		PrependReactor("list", "ingresses", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, ingressList, nil
		})
	client.CoreV1().(*fake.FakeCoreV1).
		PrependReactor("list", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, testService, nil
		})

	informerFactory := informers.NewSharedInformerFactory(client, 0)

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressLister := ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)
	return ingressLister, serviceLister, client
}

func TestHandle(t *testing.T) {

	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressList := util.ReadResourceAsIngressList(podReadinessPath)
	testService := util.GetServiceListResource("default", "testecho1", 80)
	ingressLister, serviceLister, client := setUp(ctx, ingressList, testService)
	wb := NewWebhook(ingressLister, serviceLister, client)

	scheme := kscheme.Scheme
	decoder, _ := admission.NewDecoder(scheme)
	wb.InjectDecoder(decoder)
	tests := []struct {
		name          string
		kind          metav1.GroupVersionKind
		obj           cl.Object
		op            admissionv1.Operation
		expectAllowed bool
	}{
		{
			name:          "Test Other operation",
			kind:          gvk("random", "v1", "Pod"),
			obj:           &unstructured.Unstructured{},
			op:            admissionv1.Update,
			expectAllowed: true,
		},
		{
			name:          "Test Create fail decode",
			kind:          gvk("", "v1", "Pod"),
			obj:           nil,
			op:            admissionv1.Create,
			expectAllowed: false,
		},
		{
			name: "Test simple pass with no pod readiness gates required",
			kind: gvk("", "v1", "Pod"),
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "echoserver",
						},
					},
				},
			},
			op:            admissionv1.Create,
			expectAllowed: true,
		},
		{
			name: "Test pass with pod readiness gates",
			kind: gvk("", "v1", "Pod"),
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Labels:    map[string]string{"app": "testecho1"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "echoserver",
						},
					},
				},
			},
			op:            admissionv1.Create,
			expectAllowed: true,
		},
		{
			name: "Test pass with existing pod readiness gates",
			kind: gvk("", "v1", "Pod"),
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Labels:    map[string]string{"app": "testecho1"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "echoserver",
						},
					},
					ReadinessGates: util.GetPodReadinessGates("ingress-readiness", "foo.bar.com"),
				},
			},
			op:            admissionv1.Create,
			expectAllowed: true,
		},
	}
	for _, tt := range tests {
		fmt.Print(tt.name)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bytes, err := json.Marshal(tt.obj)
			if err != nil {
				t.Fatal(err)
			}
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Kind:      tt.kind,
					Object:    getRawBytes(bytes),
					Operation: tt.op,
				},
			}
			resp := wb.Handle(context.Background(), req)
			if resp.Allowed != tt.expectAllowed {
				t.Errorf("resp.Allowed = %v, expected %v. Reason: %s", resp.Allowed, tt.expectAllowed, resp.Result.Reason)
			}
		})
	}
}

func getRawBytes(bytes []byte) runtime.RawExtension {
	if len(bytes) == 0 {
		return runtime.RawExtension{}
	}
	return runtime.RawExtension{Raw: bytes}
}
