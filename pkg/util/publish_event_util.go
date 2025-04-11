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
	"errors"
	"fmt"

	"github.com/oracle/oci-go-sdk/v65/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/exception"
)

func PublishWarningEventForIngress(eventRecorder events.EventRecorder, ingressLister networkinglisters.IngressLister,
	ingressKey interface{}, inputErr error, reason string, action string) {
	ingress, err := GetIngressFromMetaNamespaceKey(ingressKey, ingressLister)
	if err != nil {
		klog.Errorf("can't publish event for ingress key %v: %s", ingressKey, err.Error())
		return
	}
	PublishWarningEvent(eventRecorder, ingress, inputErr, reason, action)
}

func PublishWarningEventForIngressClass(eventRecorder events.EventRecorder, ingressClassLister networkinglisters.IngressClassLister,
	ingressKey interface{}, inputErr error, reason string, action string) {
	ingressClass, err := GetIngressClassFromMetaNamespaceKey(ingressKey, ingressClassLister)
	if err != nil {
		klog.Errorf("can't publish event for ingressClass key %v: %s", ingressKey, err.Error())
		return
	}
	PublishWarningEvent(eventRecorder, ingressClass, inputErr, reason, action)
}

func PublishWarningEvent(eventRecorder events.EventRecorder, regarding runtime.Object, inputErr error, reason string, action string) {
	if eventRecorder == nil || inputErr == nil {
		return
	}

	// Don't publish an event if the error is transient
	if exception.HasTransientError(inputErr) {
		return
	}

	message := inputErr.Error()
	var serviceErr common.ServiceErrorRichInfo
	ok := errors.As(inputErr, &serviceErr)
	if ok {
		message = fmt.Sprintf(`Error encountered performing %s operation for %s Service. HttpStatusCode: %d. ErrorCode: %s. Opc request id: %s. Message: %s
Request Endpoint: %s`, serviceErr.GetOperationName(), serviceErr.GetTargetService(), serviceErr.GetHTTPStatusCode(), serviceErr.GetCode(), serviceErr.GetOpcRequestID(),
			serviceErr.GetMessage(), serviceErr.GetRequestTarget())
	}

	eventRecorder.Eventf(regarding, nil, corev1.EventTypeWarning, reason, action, message)
}
