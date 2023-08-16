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
	"errors"
	"fmt"
	"time"

	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/ingressclass"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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

var errIngressClassNotReady = errors.New("ingress class not ready")

type Controller struct {
	controllerClass string

	ingressClassLister networkinglisters.IngressClassLister
	ingressLister      networkinglisters.IngressLister
	serviceLister      corelisters.ServiceLister
	podLister          corelisters.PodLister
	endpointLister     corelisters.EndpointsLister

	queue  workqueue.RateLimitingInterface
	client *client.Client
}

func NewController(
	controllerClass string,
	ingressClassInformer networkinginformers.IngressClassInformer,
	ingressInformer networkinginformers.IngressInformer,
	serviceLister corelisters.ServiceLister,
	endpointLister corelisters.EndpointsLister,
	podLister corelisters.PodLister,
	client *client.Client,
) *Controller {

	c := &Controller{
		controllerClass:    controllerClass,
		ingressClassLister: ingressClassInformer.Lister(),
		ingressLister:      ingressInformer.Lister(),
		serviceLister:      serviceLister,
		endpointLister:     endpointLister,
		podLister:          podLister,
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
	c.handleErr(err, key)
	return true
}

// sync is the business logic of the controller.
func (c *Controller) sync(key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
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

	lbID := util.GetIngressClassLoadBalancerId(ingressClass)
	if lbID == "" {
		// Still waiting on the ingress class controller to provision the load balancer.
		return errIngressClassNotReady
	}

	err = c.ensureBackends(ingressClass, lbID)
	if err != nil {
		return err
	}

	return nil
}

// filters list of all ingresses down to the subset that apply to the ingress class specified.
func (c *Controller) getIngressesForClass(ingressClass *networkingv1.IngressClass) ([]*networkingv1.Ingress, error) {

	ingresses, err := c.ingressLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// Filter ingresses down to this ingress class we want to sync.
	var result []*networkingv1.Ingress
	for _, ingress := range ingresses {
		if ingress.Spec.IngressClassName == nil && ingressClass.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
			// ingress has on class name defined and our ingress class is default
			result = append(result, ingress)
		}
		if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName == ingressClass.Name {
			result = append(result, ingress)
		}
	}
	return result, nil
}

func newBackend(ip string, port int32) ociloadbalancer.BackendDetails {
	return ociloadbalancer.BackendDetails{
		IpAddress: common.String(ip),
		Port:      common.Int(int(port)),
		Weight:    common.Int(1),
		Drain:     common.Bool(false),
		Backup:    common.Bool(false),
		Offline:   common.Bool(false),
	}
}

func (c *Controller) ensureBackends(ingressClass *networkingv1.IngressClass, lbID string) error {

	ingresses, err := c.getIngressesForClass(ingressClass)
	if err != nil {
		return err
	}

	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				svcName, svcPort, targetPort, err := c.pathToServiceAndTargetPort(ingress.Namespace, path)
				if err != nil {
					return err
				}

				epAddrs, err := c.getEndpoints(ingress.Namespace, svcName)
				if err != nil {
					return fmt.Errorf("unable to fetch endpoints for %s/%s/%d: %w", ingress.Namespace, svcName, targetPort, err)
				}

				backends := []ociloadbalancer.BackendDetails{}
				for _, epAddr := range epAddrs {
					backends = append(backends, newBackend(epAddr.IP, targetPort))
				}

				backendSetName := util.GenerateBackendSetName(ingress.Namespace, svcName, svcPort)
				err = c.client.GetLbClient().UpdateBackends(context.TODO(), lbID, backendSetName, backends)
				if err != nil {
					return fmt.Errorf("unable to update backends for %s/%s: %w", ingressClass.Name, backendSetName, err)
				}

				backendSetHealth, err := c.client.GetLbClient().GetBackendSetHealth(context.TODO(), lbID, backendSetName)
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

					err = c.ensurePodReadinessCondition(pod, readinessCondition, backendSetHealth, backendName)
					if err != nil {
						return fmt.Errorf("%w", err)
					}
				}
			}
		}
	}
	// Sync default backends
	c.syncDefaultBackend(lbID, ingresses)
	return nil
}

func (c *Controller) syncDefaultBackend(lbID string, ingresses []*networkingv1.Ingress) error {
	klog.V(4).InfoS("Syncing default backend")

	// if no backend then update
	backends, err := c.getDefaultBackends(ingresses)
	if err != nil {
		klog.ErrorS(err, "Error processing default backend sync")
		return nil
	}

	err = c.client.GetLbClient().UpdateBackends(context.TODO(), lbID, ingressclass.DefaultIngress, backends)
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
			if backend != nil && backend != ingress.Spec.DefaultBackend {
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

	svcName := backend.Service.Name
	targetPort := backend.Service.Port.Number
	epAdrress, err := c.getEndpoints(namespace, svcName)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch endpoints for %s/%s/%d: %w", namespace, svcName, targetPort, err)
	}

	for _, epAddr := range epAdrress {
		backends = append(backends, newBackend(epAddr.IP, targetPort))
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

func (c *Controller) ensurePodReadinessCondition(pod *corev1.Pod, readinessGate corev1.PodConditionType, backendSetHealth *ociloadbalancer.BackendSetHealth, backendName string) error {
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

	_, err = c.client.GetK8Client().CoreV1().Pods(pod.Namespace).Patch(context.TODO(), pod.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "status")
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

func (c *Controller) getEndpoints(namespace string, service string) ([]corev1.EndpointAddress, error) {
	endpoints, err := c.endpointLister.Endpoints(namespace).Get(service)
	if err != nil {
		return nil, err
	}

	var addresses []corev1.EndpointAddress
	for _, endpoint := range endpoints.Subsets {
		for _, address := range endpoint.Addresses {
			if address.TargetRef == nil || address.TargetRef.Kind != "Pod" {
				continue
			}

			addresses = append(addresses, address)
		}
		for _, address := range endpoint.NotReadyAddresses {
			if address.TargetRef == nil || address.TargetRef.Kind != "Pod" {
				continue
			}

			addresses = append(addresses, address)
		}
	}

	return addresses, nil
}

func (c *Controller) getTargetPortForService(namespace string, name string, port int32, portName string) (int32, int32, error) {
	svc, err := c.serviceLister.Services(namespace).Get(name)
	if err != nil {
		return 0, 0, err
	}

	for _, p := range svc.Spec.Ports {
		if (p.Port != 0 && p.Port == port) || p.Name == portName {
			if p.TargetPort.Type != intstr.Int {
				return 0, 0, fmt.Errorf("service %s/%s has non-integer ports: %s", namespace, name, p.Name)
			}
			return p.Port, int32(p.TargetPort.IntVal), nil
		}
	}

	return 0, 0, fmt.Errorf("service %s/%s does not have port: %s (%d)", namespace, name, portName, port)
}

func (c *Controller) pathToServiceAndTargetPort(ingressNamespace string, path networkingv1.HTTPIngressPath) (string, int32, int32, error) {
	if path.Backend.Service == nil {
		return "", 0, 0, fmt.Errorf("backend service is not defined for ingress")
	}

	pSvc := *path.Backend.Service

	svcPort, targetPort, err := c.getTargetPortForService(ingressNamespace, pSvc.Name, pSvc.Port.Number, pSvc.Port.Name)
	if err != nil {
		return "", 0, 0, err
	}

	return pSvc.Name, svcPort, targetPort, nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	if errors.Is(err, errIngressClassNotReady) {
		c.queue.AddAfter(key, 10*time.Second)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing backends for ingress class %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	klog.Infof("Dropping backends for ingress class %q out of the queue: %v", key, err)
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
