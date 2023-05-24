/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package routingpolicy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

var errIngressClassNotReady = errors.New("ingress class not ready")

// Controller demonstrates how to implement a controller with client-go.
type Controller struct {
	controllerClass string

	ingressClassLister networkinglisters.IngressClassLister
	ingressLister      networkinglisters.IngressLister
	serviceLister      corelisters.ServiceLister
	queue              workqueue.RateLimitingInterface
	informer           networkinginformers.IngressInformer
	client             kubernetes.Interface

	lbClient *loadbalancer.LoadBalancerClient
}

// NewController creates a new Controller.
func NewController(
	controllerClass string,
	ingressClassInformer networkinginformers.IngressClassInformer,
	ingressInformer networkinginformers.IngressInformer,
	serviceLister corelisters.ServiceLister,
	client kubernetes.Interface,
	lbClient *loadbalancer.LoadBalancerClient,
) *Controller {

	c := &Controller{
		controllerClass:    controllerClass,
		ingressClassLister: ingressClassInformer.Lister(),
		ingressLister:      ingressInformer.Lister(),
		serviceLister:      serviceLister,
		informer:           ingressInformer,

		client:   client,
		lbClient: lbClient,
		queue:    workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(10*time.Second, 5*time.Minute)),
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
	klog.V(4).InfoS("Started syncing routing policies for ingress class", "ingressClass", klog.KRef("", name), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing routing policies for ingress class", "ingressClass", klog.KRef("", name), "duration", time.Since(startTime))
	}()

	ingressClass, err := c.ingressClassLister.Get(name)
	if apierrors.IsNotFound(err) {
		klog.V(2).InfoS("ingress class has been deleted", "ingressClass", klog.KRef("", name))
		return nil
	}
	if err != nil {
		return err
	}

	if ingressClass.Spec.Controller != c.controllerClass {
		klog.V(4).InfoS("skipping ingress class not for this controller", "ingressClass", klog.KObj(ingressClass))
		return nil
	}

	if util.GetIngressClassLoadBalancerId(ingressClass) == "" {
		return errIngressClassNotReady
	}

	err = c.ensureRoutingRules(ingressClass)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) ensureRoutingRules(ingressClass *networkingv1.IngressClass) error {
	allIngresses, err := c.ingressLister.List(labels.Everything())
	if err != nil {
		return err
	}

	// Filter ingresses down to this ingress class we want to sync.
	var ingresses []*networkingv1.Ingress
	for _, ingress := range allIngresses {
		// skip if the ingress is in deleting state
		if util.IsIngressDeleting(ingress) {
			continue
		}
		if ingress.Spec.IngressClassName == nil && ingressClass.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
			// ingress has on class name defined and our ingress class is default
			ingresses = append(ingresses, ingress)
		}
		if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName == ingressClass.Name {
			ingresses = append(ingresses, ingress)
		}
	}

	listenerPaths := map[string][]*listenerPath{}
	desiredRoutingPolicies := sets.NewString()

	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				serviceName, servicePort, err := util.PathToServiceAndPort(ingress.Namespace, path, c.serviceLister)
				if err != nil {
					return err
				}

				listenerName := util.GenerateListenerName(servicePort)
				listenerPaths[listenerName] = append(listenerPaths[listenerName], &listenerPath{
					IngressName:    ingress.Name,
					Host:           rule.Host,
					Path:           &path,
					BackendSetName: util.GenerateBackendSetName(ingress.Namespace, serviceName, servicePort),
				})
				desiredRoutingPolicies.Insert(listenerName)
			}
		}
	}

	lbID := util.GetIngressClassLoadBalancerId(ingressClass)

	errorSyncing := false
	for listenerName, paths := range listenerPaths {
		var rules []ociloadbalancer.RoutingRule
		// Sort the listener paths based on the actual path values so that the specific ones come first
		sort.Sort(ByPath(paths))
		for _, path := range paths {
			policyName := util.PathToRoutePolicyName(path.IngressName, path.Host, *path.Path)
			policyCondition := PathToRoutePolicyCondition(path.Host, *path.Path)
			rules = append(rules, ociloadbalancer.RoutingRule{
				Name:      common.String(policyName),
				Condition: common.String(policyCondition),
				Actions: []ociloadbalancer.Action{
					ociloadbalancer.ForwardToBackendSet{
						BackendSetName: common.String(path.BackendSetName),
					},
				},
			})
		}

		err = c.lbClient.EnsureRoutingPolicy(context.TODO(), lbID, listenerName, rules)
		if err != nil {
			// we purposefully only log here then return an error at the end, so we can attempt to sync all listeners.
			klog.ErrorS(err, "unable to ensure route policy", "ingressClass", klog.KObj(ingressClass), "listenerName", listenerName)
			errorSyncing = true
			continue
		}
	}

	lb, _, err := c.lbClient.GetLoadBalancer(context.TODO(), lbID)
	if err != nil {
		return err
	}

	actualRoutingPolicies := sets.NewString()
	for name := range lb.RoutingPolicies {
		actualRoutingPolicies.Insert(name)
	}

	// Clean up routing policies
	routingPoliciesToDelete := actualRoutingPolicies.Difference(desiredRoutingPolicies)
	if len(routingPoliciesToDelete) > 0 {
		klog.Infof("Following routing policies are eligible for deletion: %s", util.PrettyPrint(routingPoliciesToDelete))
		for routingPolicyToDelete := range routingPoliciesToDelete {
			lb, etag, err := c.lbClient.GetLoadBalancer(context.TODO(), lbID)
			if err != nil {
				return err
			}

			listener, listenerFound := lb.Listeners[routingPolicyToDelete]
			if listenerFound {
				klog.Infof("Detaching the routing policy %s from listener.", routingPolicyToDelete)
				err = c.lbClient.UpdateListener(context.TODO(), lb.Id, etag, listener, nil, nil, listener.Protocol)
				if err != nil {
					return err
				}
			}

			lb, etag, err = c.lbClient.GetLoadBalancer(context.TODO(), lbID)
			if err != nil {
				return err
			}

			_, routingPolicyFound := lb.RoutingPolicies[routingPolicyToDelete]
			if routingPolicyFound {
				err = c.lbClient.DeleteRoutingPolicy(context.TODO(), lbID, routingPolicyToDelete)
				if err != nil {
					return err
				}
			}
		}
	}

	if errorSyncing {
		return errors.New("error encountered syncing routing policies")
	}

	return nil
}

type listenerPath struct {
	IngressName    string
	Host           string
	BackendSetName string
	Path           *networkingv1.HTTPIngressPath
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
		klog.Infof("Error syncing routing policy %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	klog.Infof("Dropping routing policy %q out of the queue: %v", key, err)
}

// Run begins watching and syncing.
func (c *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("Starting Routing Policy Controller")

	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	go wait.Until(c.runPusher, 10*time.Second, stopCh)

	<-stopCh
	klog.Info("Stopping Routing Policy Controller")
}

func (c *Controller) runPusher() {
	ingressClasses, err := c.ingressClassLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("unable to list ingress classes for syncing routing policies: %v", err)
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
