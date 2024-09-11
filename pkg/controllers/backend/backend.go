/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package backend

import (
	"context"
	"encoding/json"
	"fmt"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"time"

	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"k8s.io/klog/v2"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

type Controller struct {
	controllerClass string

	ingressClassLister networkinglisters.IngressClassLister
	ingressLister      networkinglisters.IngressLister
	serviceLister      corelisters.ServiceLister
	podLister          corelisters.PodLister
	endpointLister     corelisters.EndpointsLister
	saLister           corelisters.ServiceAccountLister

	queue  workqueue.RateLimitingInterface
	client *client.ClientProvider
}

func NewController(
	controllerClass string,
	ingressClassInformer networkinginformers.IngressClassInformer,
	ingressInformer networkinginformers.IngressInformer,
	saInformer coreinformers.ServiceAccountInformer,
	serviceLister corelisters.ServiceLister,
	endpointLister corelisters.EndpointsLister,
	podLister corelisters.PodLister,
	client *client.ClientProvider,
) *Controller {

	c := &Controller{
		controllerClass:    controllerClass,
		ingressClassLister: ingressClassInformer.Lister(),
		ingressLister:      ingressInformer.Lister(),
		serviceLister:      serviceLister,
		endpointLister:     endpointLister,
		podLister:          podLister,
		saLister:           saInformer.Lister(),
		client:             client,
		queue:              workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(10*time.Second, 5*time.Minute)),
	}

	return c
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.sync(key.(string))

	// Handle the error if something went wrong during the execution of the business logic
	util.HandleErr(c.queue, err, "Error syncing backends for ingress class", key)
	return true
}

// sync is the business logic of the controller.
func (c *Controller) sync(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.ErrorS(err, "Failed to split meta namespace cache key", "cacheKey", key)
		return err
	}

	startTime := time.Now()
	klog.V(4).InfoS("Started syncing backends for ingress class", "ingressClass", klog.KRef("", name), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing backends for ingress class", "ingressClass", klog.KRef("", name), "duration", time.Since(startTime))
	}()

	ingressClass, err := c.ingressClassLister.Get(name)
	if apierrors.IsNotFound(err) {
		klog.V(2).InfoS("ingress class is not found or has been deleted", "ingressClass", klog.KRef("", name))
		return nil
	}
	if err != nil {
		return err
	}

	if ingressClass.Spec.Controller != c.controllerClass {
		klog.V(4).InfoS("skipping ingress class, not for this controller", "ingressClass", klog.KObj(ingressClass))
		return nil
	}

	ctx, err := client.GetClientContext(ingressClass, c.saLister, c.client, namespace, name)
	if err != nil {
		return err
	}

	lbID := util.GetIngressClassLoadBalancerId(ingressClass)
	if lbID == "" {
		// Still waiting on the ingress class controller to provision the load balancer.
		return util.ErrIngressClassNotReady
	}

	err = c.ensureBackends(ctx, ingressClass, lbID)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) ensureBackends(ctx context.Context, ingressClass *networkingv1.IngressClass, lbID string) error {

	ingresses, err := util.GetIngressesForClass(c.ingressLister, ingressClass)
	if err != nil {
		return err
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				pSvc, svc, err := util.ExtractServices(path, c.serviceLister, ingress)
				if err != nil {
					return err
				}
				svcName, svcPort, targetPort, err := util.PathToServiceAndTargetPort(c.endpointLister, svc, pSvc, ingress.Namespace, false)
				if err != nil {
					return err
				}
				epAddrs, err := util.GetEndpoints(c.endpointLister, ingress.Namespace, svcName)
				if err != nil {
					return fmt.Errorf("unable to fetch endpoints for %s/%s/%d: %w", ingress.Namespace, svcName, targetPort, err)
				}
				backends := []ociloadbalancer.BackendDetails{}
				for _, epAddr := range epAddrs {
					backends = append(backends, util.NewBackend(epAddr.IP, targetPort))
				}
				backendSetName := util.GenerateBackendSetName(ingress.Namespace, svcName, svcPort)
				err = wrapperClient.GetLbClient().UpdateBackends(context.TODO(), lbID, backendSetName, backends)
				if err != nil {
					return fmt.Errorf("unable to update backends for %s/%s: %w", ingressClass.Name, backendSetName, err)
				}
				backendSetHealth, err := wrapperClient.GetLbClient().GetBackendSetHealth(context.TODO(), lbID, backendSetName)
				if err != nil {
					return fmt.Errorf("unable to fetch backendset health: %w", err)
				}
				for _, epAddr := range epAddrs {
					pod, err := c.podLister.Pods(ingress.Namespace).Get(epAddr.TargetRef.Name)
					if err != nil {
						return fmt.Errorf("failed to fetch pod %s/%s: %w", ingress.Namespace, epAddr.TargetRef.Name, err)
					}
					backendName := fmt.Sprintf("%s:%d", epAddr.IP, targetPort)
					readinessCondition := util.GetPodReadinessCondition(ingress.Name, rule.Host, path)
					err = c.ensurePodReadinessCondition(ctx, pod, readinessCondition, backendSetHealth, backendName)
					if err != nil {
						return fmt.Errorf("%w", err)
					}
				}
			}
		}
	}
	// Sync default backends
	c.syncDefaultBackend(ctx, lbID, ingresses)
	return nil
}

func (c *Controller) syncDefaultBackend(ctx context.Context, lbID string, ingresses []*networkingv1.Ingress) error {
	klog.V(4).InfoS("Syncing default backend")

	// if no backend then update
	backends, err := c.getDefaultBackends(ingresses)
	if err != nil {
		klog.ErrorS(err, "Error processing default backend sync")
		return nil
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	err = wrapperClient.GetLbClient().UpdateBackends(context.TODO(), lbID, util.DefaultBackendSetName, backends)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) getDefaultBackends(ingresses []*networkingv1.Ingress) ([]ociloadbalancer.BackendDetails, error) {
	klog.V(4).InfoS("Retrieve default backends")

	var backend *networkingv1.IngressBackend
	var namespace = ""
	backends := []ociloadbalancer.BackendDetails{}

	for _, ingress := range ingresses {
		if ingress.Spec.DefaultBackend != nil {
			if backend != nil && !util.IsBackendServiceEqual(backend, ingress.Spec.DefaultBackend) {
				return nil, fmt.Errorf("conflict in default backend resource, only one is permitted")
			}
			backend = ingress.Spec.DefaultBackend
			namespace = ingress.Namespace
		}
	}

	if backend == nil {
		klog.V(4).InfoS("No default backend set for any ingress")
		return backends, nil
	}

	if backend.Service == nil {
		klog.V(4).InfoS("No service set for default backend on any ingress")
		return backends, nil

	}

	svc, err := c.serviceLister.Services(namespace).Get(backend.Service.Name)
	if err != nil {
		return nil, err
	}

	svcName, _, targetPort, err := util.PathToServiceAndTargetPort(c.endpointLister, svc, *backend.Service, namespace, false)
	if err != nil {
		return nil, err
	}

	epAdrress, err := util.GetEndpoints(c.endpointLister, namespace, svcName)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch endpoints for %s/%s/%d: %w", namespace, svcName, targetPort, err)
	}

	for _, epAddr := range epAdrress {
		backends = append(backends, util.NewBackend(epAddr.IP, targetPort))
	}
	return backends, err
}

func hasReadinessGate(pod *corev1.Pod, readinessGate corev1.PodConditionType) bool {
	for _, readiness := range pod.Spec.ReadinessGates {
		if readiness.ConditionType == readinessGate {
			return true
		}
	}
	return false
}

func getPodCondition(pod *corev1.Pod, conditionType corev1.PodConditionType) (corev1.PodCondition, bool) {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == conditionType {
			return cond, true
		}
	}

	return corev1.PodCondition{}, false
}

func (c *Controller) ensurePodReadinessCondition(ctx context.Context, pod *corev1.Pod, readinessGate corev1.PodConditionType, backendSetHealth *ociloadbalancer.BackendSetHealth, backendName string) error {
	klog.InfoS("validating pod readiness gate status", "pod", klog.KObj(pod), "gate", readinessGate)

	if !hasReadinessGate(pod, readinessGate) {
		// pod does not match this readiness gate so nothing to do.
		klog.V(4).InfoS("pod readiness gate status not set on pod", "pod", klog.KObj(pod), "gate", readinessGate)
		return nil
	}

	condStatus := corev1.ConditionTrue
	reason := "backend is healthy"
	message := ""

	for _, backend := range backendSetHealth.CriticalStateBackendNames {
		if backend == backendName {
			reason = "backend is CRITICAL"
			condStatus = corev1.ConditionFalse
			break
		}
	}
	for _, backend := range backendSetHealth.WarningStateBackendNames {
		if backend == backendName {
			reason = "backend is WARNING"
			condStatus = corev1.ConditionFalse
			break
		}
	}
	for _, backend := range backendSetHealth.UnknownStateBackendNames {
		if backend == backendName {
			reason = "backend is UNKNOWN"
			condStatus = corev1.ConditionFalse
			break
		}
	}

	existingCondition, exists := getPodCondition(pod, readinessGate)
	if exists &&
		existingCondition.Status == condStatus &&
		existingCondition.Reason == reason &&
		existingCondition.Message == message {
		// no change in condition so no work to do
		return nil
	}

	updatedCondition := corev1.PodCondition{
		Type:    readinessGate,
		Status:  condStatus,
		Reason:  reason,
		Message: message,
	}

	if !exists || updatedCondition.Status != condStatus {
		updatedCondition.LastTransitionTime = metav1.Now()
	}

	klog.InfoS("updating pod readiness gate from pod", "pod", klog.KObj(pod), "gate", readinessGate)

	patchBytes, err := BuildPodConditionPatch(pod, updatedCondition)
	if err != nil {
		return fmt.Errorf("unable to build pod condition for %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	_, err = wrapperClient.GetK8Client().CoreV1().Pods(pod.Namespace).Patch(context.TODO(), pod.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "status")
	if err != nil {
		return fmt.Errorf("unable to remove readiness gate %s from pod %s/%s: %w", readinessGate, pod.Namespace, pod.Name, err)
	}

	klog.InfoS("successfully updated pod readiness gate status", "pod", klog.KObj(pod), "gate", readinessGate)

	return nil
}

func BuildPodConditionPatch(pod *corev1.Pod, condition corev1.PodCondition) ([]byte, error) {
	oldData, err := json.Marshal(corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: nil,
		},
	})
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: pod.UID,
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{condition},
		},
	})
	if err != nil {
		return nil, err
	}

	return strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1.Pod{})
}

// Run begins watching and syncing.
func (c *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("Starting Backend controller")

	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	go wait.Until(c.runPusher, 10*time.Second, stopCh)

	<-stopCh
	klog.Info("Stopping Backend controller")
}

func (c *Controller) runPusher() {
	ingressClasses, err := c.ingressClassLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("unable to list ingress classes for syncing backends: %v", err)
		return
	}

	for _, ic := range ingressClasses {
		key, err := cache.MetaNamespaceKeyFunc(ic)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("couldn't get key for object %#v: %v", ic, err))
			return
		}
		c.queue.Add(key)
	}
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}
