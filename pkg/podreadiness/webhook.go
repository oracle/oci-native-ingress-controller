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
	"net/http"

	"k8s.io/klog/v2"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"bitbucket.oci.oraclecorp.com/oke/oci-native-ingress-controller/pkg/util"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
)

// PodReadinessGate annotates Pods
type PodReadinessGate struct {
	ingressLister networkinglisters.IngressLister
	serviceLister corelisters.ServiceLister

	client  kubernetes.Interface
	decoder *admission.Decoder
}

func NewWebhook(
	ingressLister networkinglisters.IngressLister,
	serviceLister corelisters.ServiceLister,
	client kubernetes.Interface,
) *PodReadinessGate {
	return &PodReadinessGate{
		ingressLister: ingressLister,
		serviceLister: serviceLister,
		client:        client,
	}
}

func (prg *PodReadinessGate) handleCreate(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}

	err := prg.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	klog.V(2).InfoS("processing pod creation for pod readiness", "pod", klog.KObj(pod))

	ingresses, err := prg.ingressLister.Ingresses(pod.Namespace).List(labels.Everything())
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	var targetHealthCondTypes []corev1.PodConditionType
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				svc, err := prg.serviceLister.Services(pod.Namespace).Get(path.Backend.Service.Name)
				if err != nil {
					return admission.Errored(http.StatusInternalServerError, err)
				}

				var svcSelector labels.Selector
				if len(svc.Spec.Selector) == 0 {
					svcSelector = labels.Nothing()
				} else {
					svcSelector = labels.SelectorFromSet(svc.Spec.Selector)
				}
				if svcSelector.Matches(labels.Set(pod.Labels)) {
					targetHealthCondType := util.GetPodReadinessCondition(ingress.Name, rule.Host, path)
					targetHealthCondTypes = append(targetHealthCondTypes, targetHealthCondType)
				}
			}
		}
	}

	if len(targetHealthCondTypes) == 0 {
		// nothing to do since pod doesn't need readiness gates.
		return admission.Allowed("no pod readiness gates required")
	}

	for _, cond := range targetHealthCondTypes {
		found := false
		for _, c := range pod.Spec.ReadinessGates {
			if c.ConditionType == cond {
				found = true
				break
			}
		}

		if !found {
			pod.Spec.ReadinessGates = append(pod.Spec.ReadinessGates, corev1.PodReadinessGate{
				ConditionType: cond,
			})
		}
	}
	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// PodReadinessGate adds an annotation to every incoming pods.
func (prg *PodReadinessGate) Handle(ctx context.Context, req admission.Request) admission.Response {
	var resp admission.Response

	switch req.Operation {
	case admissionv1.Create:
		resp = prg.handleCreate(ctx, req)
	default:
		resp = admission.Allowed("")
	}

	return resp
}

// InjectDecoder injects the decoder.
func (prg *PodReadinessGate) InjectDecoder(d *admission.Decoder) error {
	prg.decoder = d
	return nil
}
