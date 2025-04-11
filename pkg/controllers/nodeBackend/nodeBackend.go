package nodeBackend

import (
	"context"
	"fmt"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/events"
	"time"

	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"k8s.io/klog/v2"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

const (
	// ToBeDeletedTaint is a taint used by the CLuster Autoscaler before marking a node for deletion. Defined in
	// https://github.com/kubernetes/autoscaler/blob/e80ab518340f88f364fe3ef063f8303755125971/cluster-autoscaler/utils/deletetaint/delete.go#L36
	ToBeDeletedTaint = "ToBeDeletedByClusterAutoscaler"
)

type Controller struct {
	controllerClass string

	ingressClassLister networkinglisters.IngressClassLister
	ingressLister      networkinglisters.IngressLister
	serviceLister      corelisters.ServiceLister
	podLister          corelisters.PodLister
	nodeLister         corelisters.NodeLister
	endpointLister     corelisters.EndpointsLister
	saLister           corelisters.ServiceAccountLister
	eventRecorder      events.EventRecorder

	queue workqueue.RateLimitingInterface

	client *client.ClientProvider
}

func NewController(controllerClass string, ingressClassInformer networkinginformers.IngressClassInformer, ingressInformer networkinginformers.IngressInformer, saInformer coreinformers.ServiceAccountInformer, serviceLister corelisters.ServiceLister, endpointLister corelisters.EndpointsLister, podLister corelisters.PodLister, nodeLister corelisters.NodeLister,
	client *client.ClientProvider, eventRecorder events.EventRecorder) *Controller {

	c := &Controller{
		controllerClass:    controllerClass,
		ingressClassLister: ingressClassInformer.Lister(),
		ingressLister:      ingressInformer.Lister(),
		serviceLister:      serviceLister,
		endpointLister:     endpointLister,
		podLister:          podLister,
		nodeLister:         nodeLister,
		saLister:           saInformer.Lister(),
		client:             client,
		eventRecorder:      eventRecorder,
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
	util.HandleErrForBackendController(c.eventRecorder, c.ingressClassLister, c.queue, err, "Error syncing backends for ingress class", key)
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
	klog.V(4).InfoS("Started syncing backends for ingress class", "ingressClass", klog.KRef(namespace, name), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing backends for ingress class", "ingressClass", klog.KRef(namespace, name), "duration", time.Since(startTime))
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
		return util.ErrIngressClassNotReady
	}

	ctx, err := client.GetClientContext(ingressClass, c.saLister, c.client, namespace, name)
	if err != nil {
		return err
	}

	err = c.ensureBackends(ctx, ingressClass, lbID)
	if err != nil {
		return err
	}

	return nil
}

func getNodeConditionPredicate() NodeConditionPredicate {
	return func(node *corev1.Node) bool {

		// Remove nodes that are about to be deleted by the cluster autoscaler.
		for _, taint := range node.Spec.Taints {
			if taint.Key == ToBeDeletedTaint {
				return false
			}
		}

		// If we have no info, don't accept
		if len(node.Status.Conditions) == 0 {
			return false
		}
		for _, cond := range node.Status.Conditions {
			// We consider the node for load balancing only when its NodeReady condition status
			// is ConditionTrue
			if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
				return false
			}
		}
		return true
	}
}

func (c *Controller) ensureBackends(ctx context.Context, ingressClass *networkingv1.IngressClass, lbID string) error {

	ingresses, err := util.GetIngressesForClass(c.ingressLister, ingressClass)
	if err != nil {
		return err
	}
	nodes, err := filterNodes(c.nodeLister)
	if err != nil {
		return err
	}
	// If there are no available nodes.
	if len(nodes) == 0 {
		return fmt.Errorf("error: %s due to : %s", "UnAvailableNodes", "There are no available provisioned nodes")
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	for _, ingress := range ingresses {
		err := c.ensureBackendsForIngress(ingress, ingressClass, nodes, lbID, wrapperClient)
		if err != nil {
			return fmt.Errorf("for ingress %s, encountered error: %w", klog.KObj(ingress), err)
		}
	}
	// Sync default backends
	c.syncDefaultBackend(ctx, lbID, ingresses)
	return nil
}

func (c *Controller) ensureBackendsForIngress(ingress *networkingv1.Ingress, ingressClass *networkingv1.IngressClass,
	nodes []*corev1.Node, lbID string, wrapperClient *client.WrapperClient) error {
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {

			pSvc, svc, err := util.ExtractServices(path, c.serviceLister, ingress)
			if err != nil {
				return err
			}

			svcName, svcPort, nodePort, err := util.PathToServiceAndTargetPort(c.endpointLister, svc, pSvc, ingress.Namespace, true)
			if err != nil {
				return err
			}
			if svc == nil || svc.Spec.Ports == nil || nodePort == 0 {
				continue
			}

			var backends []ociloadbalancer.BackendDetails
			trafficPolicy := svc.Spec.ExternalTrafficPolicy
			if trafficPolicy == corev1.ServiceExternalTrafficPolicyTypeCluster {
				for _, node := range nodes {
					backends = append(backends, util.NewBackend(NodeInternalIP(node), nodePort))
				}
			} else {
				backends, err = getBackendsFromPods(c.endpointLister, c.podLister, c.nodeLister, ingress.Namespace, svcName, backends, nodePort)
				if err != nil {
					return err
				}
			}
			backendSetName := util.GenerateBackendSetName(ingress.Namespace, svcName, svcPort)
			err = wrapperClient.GetLbClient().UpdateBackends(context.TODO(), lbID, backendSetName, backends)
			if err != nil {
				return fmt.Errorf("unable to update backends for %s/%s: %w", ingressClass.Name, backendSetName, err)
			}
		}
	}

	return nil
}

func getBackendsFromPods(endpointLister corelisters.EndpointsLister, podLister corelisters.PodLister, nodeLister corelisters.NodeLister, namespace string, svcName string, backends []ociloadbalancer.BackendDetails, nodePort int32) ([]ociloadbalancer.BackendDetails, error) {
	pods, err := util.RetrievePods(endpointLister, podLister, namespace, svcName)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		node, err := nodeLister.Get(pod.Spec.NodeName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				klog.Infof("node %s has been deleted, skipping pod", pod.Spec.NodeName)
				continue
			}
			return nil, err
		}
		backends = append(backends, util.NewBackend(NodeInternalIP(node), nodePort))
	}
	return backends, nil
}

func NodeInternalIP(node *corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}

// NodeConditionPredicate is a function that indicates whether the given node's conditions meet
// some set of criteria defined by the function.
type NodeConditionPredicate func(node *corev1.Node) bool

// filterNodes gets nodes that matches predicate function.
func filterNodes(nodeLister corelisters.NodeLister) ([]*corev1.Node, error) {
	nodes, err := nodeLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var filtered []*corev1.Node
	predicate := getNodeConditionPredicate()
	for i := range nodes {
		if predicate(nodes[i]) {
			filtered = append(filtered, nodes[i])
		}
	}
	return filtered, nil
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

	svcName := backend.Service.Name
	svc, err := c.serviceLister.Services(namespace).Get(svcName)
	if err != nil {
		klog.Errorf("Default backend service not found for service : %s", svcName)
		return nil, err
	}

	if svc.Spec.Ports[0].NodePort == 0 {
		return nil, fmt.Errorf("Node port not found for service : %s", svcName)
	}

	nodePort := svc.Spec.Ports[0].NodePort

	backends, err = getBackendsFromPods(c.endpointLister, c.podLister, c.nodeLister, namespace, svcName, backends, nodePort)
	if err != nil {
		return nil, err
	}
	return backends, err
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
