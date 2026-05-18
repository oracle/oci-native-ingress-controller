/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2025 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package util

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"

	"github.com/oracle/oci-native-ingress-controller/pkg/exception"
	corev1 "k8s.io/api/core/v1"
)

func getListers(ingress *networkingv1.Ingress, ingressClass *networkingv1.IngressClass) (networkinglisters.IngressLister, networkinglisters.IngressClassLister) {
	fakeClient := fakeclientset.NewSimpleClientset()
	action := "list"

	UpdateFakeClientCall(fakeClient, action, "ingressclasses", &networkingv1.IngressClassList{Items: []networkingv1.IngressClass{*ingressClass}})
	UpdateFakeClientCall(fakeClient, action, "ingresses", &networkingv1.IngressList{Items: []networkingv1.Ingress{*ingress}})

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressInformer.Lister()

	informerFactory.Start(context.Background().Done())
	cache.WaitForCacheSync(context.Background().Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(context.Background().Done(), ingressInformer.Informer().HasSynced)

	return ingressInformer.Lister(), ingressClassInformer.Lister()
}

func TestPublishWarningEventForIngressAndIngressClass(t *testing.T) {
	RegisterTestingT(t)

	ingress := &networkingv1.Ingress{ObjectMeta: v1.ObjectMeta{Name: "name", Namespace: "namespace"}}
	ingressClass := &networkingv1.IngressClass{ObjectMeta: v1.ObjectMeta{Name: "name"}}
	ingressLister, ingressClassLister := getListers(ingress, ingressClass)

	fakeRecorder := events.NewFakeRecorder(10)
	err := errors.New("err")
	transientErr := exception.NewTransientError(err)
	reason := "Reason"
	action := "Action"
	ingressKey, _ := cache.MetaNamespaceKeyFunc(ingress)
	ingressClassKey, _ := cache.MetaNamespaceKeyFunc(ingressClass)

	_, keyErr := GetIngressClassFromMetaNamespaceKey("fake-key", ingressClassLister)
	Expect(keyErr).ShouldNot(BeNil())
	_, keyErr = GetIngressFromMetaNamespaceKey("fake-key", ingressLister)
	Expect(keyErr).ShouldNot(BeNil())

	PublishWarningEventForIngressClass(fakeRecorder, ingressClassLister, ingressClassKey, transientErr, reason, action)
	PublishWarningEventForIngress(fakeRecorder, ingressLister, ingressKey, transientErr, reason, action)
	Eventually(fakeRecorder.Events).ShouldNot(Receive())

	PublishWarningEventForIngressClass(fakeRecorder, ingressClassLister, ingressClassKey, err, reason, action)
	PublishWarningEventForIngress(fakeRecorder, ingressLister, ingressKey, err, reason, action)
	Eventually(fakeRecorder.Events).Should(Receive())
}

func TestPublishWarningEvent(t *testing.T) {
	RegisterTestingT(t)

	fakeRecorder := events.NewFakeRecorder(10)
	ingress := &networkingv1.Ingress{}
	err := errors.New("err")
	transientErr := exception.NewTransientError(err)
	reason := "Reason"
	action := "Action"

	PublishWarningEvent(nil, ingress, err, reason, action)
	PublishWarningEvent(nil, ingress, nil, reason, action)
	PublishWarningEvent(fakeRecorder, ingress, nil, reason, action)
	PublishWarningEvent(fakeRecorder, ingress, transientErr, reason, action)
	Eventually(fakeRecorder.Events).ShouldNot(Receive())

	PublishWarningEvent(fakeRecorder, ingress, err, reason, action)
	Eventually(fakeRecorder.Events).Should(Receive())
}

func TestPublishIngressBackendValidationEvents(t *testing.T) {
	RegisterTestingT(t)

	fakeRecorder := events.NewFakeRecorder(10)
	ingress := &networkingv1.Ingress{ObjectMeta: v1.ObjectMeta{Name: "name", Namespace: "namespace"}}
	path := networkingv1.HTTPIngressPath{
		Path: "/resource",
		Backend: networkingv1.IngressBackend{
			Resource: &corev1.TypedLocalObjectReference{Name: "unsupported-resource"},
		},
	}

	PublishUnsupportedBackendEvent(nil, ingress, "example.com", path)
	PublishUnsupportedBackendEvent(fakeRecorder, nil, "example.com", path)
	PublishMissingServiceBackendEvent(nil, ingress, "example.com", path)
	PublishMissingServiceBackendEvent(fakeRecorder, nil, "example.com", path)
	Eventually(fakeRecorder.Events).ShouldNot(Receive())

	PublishUnsupportedBackendEvent(fakeRecorder, ingress, "example.com", path)
	Eventually(fakeRecorder.Events).Should(Receive(And(
		ContainSubstring(UnsupportedBackendReason),
		ContainSubstring("resource backend is not supported"),
		ContainSubstring("/resource"),
		ContainSubstring("unsupported-resource"),
	)))

	missingServicePath := networkingv1.HTTPIngressPath{Path: "/missing-service"}
	PublishMissingServiceBackendEvent(fakeRecorder, ingress, "example.com", missingServicePath)
	Eventually(fakeRecorder.Events).Should(Receive(And(
		ContainSubstring(MissingServiceBackendReason),
		ContainSubstring("service backend is not configured"),
		ContainSubstring("/missing-service"),
	)))
}

func TestLogAndPublishIngressBackendValidationWarning(t *testing.T) {
	RegisterTestingT(t)

	fakeRecorder := events.NewFakeRecorder(10)
	ingress := &networkingv1.Ingress{ObjectMeta: v1.ObjectMeta{Name: "name", Namespace: "namespace"}}
	resourcePath := networkingv1.HTTPIngressPath{
		Path: "/resource",
		Backend: networkingv1.IngressBackend{
			Resource: &corev1.TypedLocalObjectReference{Name: "unsupported-resource"},
		},
	}

	LogAndPublishIngressBackendValidationWarning(fakeRecorder, nil, "example.com", resourcePath, "")
	Eventually(fakeRecorder.Events).ShouldNot(Receive())

	LogAndPublishIngressBackendValidationWarning(fakeRecorder, ingress, "example.com", resourcePath, " while testing")
	Eventually(fakeRecorder.Events).Should(Receive(And(
		ContainSubstring(UnsupportedBackendReason),
		ContainSubstring("unsupported-resource"),
	)))

	missingServicePath := networkingv1.HTTPIngressPath{Path: "/missing-service"}
	LogAndPublishIngressBackendValidationWarning(fakeRecorder, ingress, "example.com", missingServicePath, " while testing")
	Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(MissingServiceBackendReason)))
}
