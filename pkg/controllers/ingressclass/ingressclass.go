/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package ingressclass

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/klog/v2"

	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrclient "sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	"github.com/pkg/errors"

	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"
	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
)

var errIngressClassNotReady = errors.New("ingress class not ready")

const DefaultIngress = "default_ingress"

// Controller demonstrates how to implement a controller with client-go.
type Controller struct {
	defaultCompartmentId string
	defaultSubnetId      string
	controllerClass      string

	lister   networkinglisters.IngressClassLister
	queue    workqueue.RateLimitingInterface
	informer networkinginformers.IngressClassInformer
	client   kubernetes.Interface
	cache    ctrcache.Cache

	lbClient *loadbalancer.LoadBalancerClient
}

// NewController creates a new Controller.
func NewController(
	defaultCompartmentId string,
	defaultSubnetId string,
	controllerClass string,
	informer networkinginformers.IngressClassInformer,
	client kubernetes.Interface,
	lbClient *loadbalancer.LoadBalancerClient,
	ctrcache ctrcache.Cache,

) *Controller {

	c := &Controller{
		defaultCompartmentId: defaultCompartmentId,
		defaultSubnetId:      defaultSubnetId,
		controllerClass:      controllerClass,
		informer:             informer,
		lister:               informer.Lister(),
		client:               client,
		lbClient:             lbClient,
		cache:                ctrcache,
		queue:                workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(10*time.Second, 5*time.Minute)),
	}

	informer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.ingressClassAdd,
			UpdateFunc: c.ingressClassUpdate,
			DeleteFunc: c.ingressClassDelete,
		},
	)

	return c
}

func (c *Controller) enqueueIngressClass(ingressClass *networkingv1.IngressClass) {
	key, err := cache.MetaNamespaceKeyFunc(ingressClass)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %#v: %v", ingressClass, err))
		return
	}

	c.queue.Add(key)
}

func (c *Controller) ingressClassAdd(obj interface{}) {
	ic := obj.(*networkingv1.IngressClass)
	klog.V(4).InfoS("Adding ingress class", "ingressClass", klog.KObj(ic))
	c.enqueueIngressClass(ic)
}

func (c *Controller) ingressClassUpdate(old, new interface{}) {
	oldIngressClass := old.(*networkingv1.IngressClass)
	newIngressClass := new.(*networkingv1.IngressClass)

	klog.V(4).InfoS("Updating ingress class", "ingressClass", klog.KObj(oldIngressClass))
	c.enqueueIngressClass(newIngressClass)
}

func (c *Controller) ingressClassDelete(obj interface{}) {
	ic, ok := obj.(*networkingv1.IngressClass)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}
		ic, ok = tombstone.Obj.(*networkingv1.IngressClass)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Ingress Class %#v", obj))
			return
		}
	}

	klog.V(4).InfoS("Deleting ingress Class", "ingressClass", klog.KObj(ic))
	c.enqueueIngressClass(ic)
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
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.ErrorS(err, "Failed to split meta namespace cache key", "cacheKey", key)
		return err
	}

	startTime := time.Now()
	klog.V(4).InfoS("Started syncing ingress class", "ingressClass", klog.KRef(namespace, name), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing ingress class", "ingressClass", klog.KRef(namespace, name), "duration", time.Since(startTime))
	}()

	ingressClass, err := c.lister.Get(name)
	if apierrors.IsNotFound(err) {
		klog.V(2).InfoS("ingress class has been deleted", "ingressClass", klog.KRef(namespace, name))
		return nil
	}
	if err != nil {
		return err
	}

	if ingressClass.Spec.Controller != c.controllerClass {
		klog.V(2).Info("skipping ingress class not for this controller")
		// skipping since ingress class is not applicable to this controller
		return nil
	}

	if isIngressControllerDeleting(ingressClass) {
		return c.deleteIngressClass(ingressClass)
	}

	err = c.ensureFinalizer(ingressClass)
	if err != nil {
		return err
	}

	err = c.ensureLoadBalancer(ingressClass)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) getLoadBalancer(ic *networkingv1.IngressClass) (*ociloadbalancer.LoadBalancer, error) {
	lbID := util.GetIngressClassLoadBalancerId(ic)
	if lbID == "" {
		return nil, &notFoundServiceError{}
	}

	lb, _, err := c.lbClient.GetLoadBalancer(context.TODO(), lbID)
	if err != nil {
		return nil, err
	}

	return lb, nil
}

func (c *Controller) ensureLoadBalancer(ic *networkingv1.IngressClass) error {

	lb, err := c.getLoadBalancer(ic)
	svcErr, ok := common.IsServiceError(err)
	if err != nil && (ok && svcErr.GetHTTPStatusCode() != 404) {
		return err
	}

	icp := &v1beta1.IngressClassParameters{}
	if ic.Spec.Parameters != nil {
		err = c.cache.Get(context.TODO(), ctrclient.ObjectKey{
			Name:      ic.Spec.Parameters.Name,
			Namespace: *ic.Spec.Parameters.Namespace,
		}, icp)
		if err != nil {
			return fmt.Errorf("unable to fetch IngressClassParameters %s: %w", ic.Spec.Parameters.Name, err)
		}
	}

	if lb == nil {
		klog.V(2).InfoS("Creating load balancer for ingress class", "ingressClass", ic.Name)

		createDetails := ociloadbalancer.CreateLoadBalancerDetails{
			CompartmentId: common.String(util.GetIngressClassCompartmentId(icp, c.defaultCompartmentId)),
			DisplayName:   common.String(util.GetIngressClassLoadBalancerName(ic, icp)),
			ShapeName:     common.String("flexible"),
			SubnetIds:     []string{util.GetIngressClassSubnetId(icp, c.defaultSubnetId)},
			IsPrivate:     common.Bool(icp.Spec.IsPrivate),
			BackendSets: map[string]ociloadbalancer.BackendSetDetails{
				DefaultIngress: {
					Policy: common.String("LEAST_CONNECTIONS"),
					HealthChecker: &ociloadbalancer.HealthCheckerDetails{
						Protocol: common.String("TCP"),
					},
				},
			},
		}

		if icp.Spec.ReservedPublicAddressId != "" {
			createDetails.ReservedIps = []ociloadbalancer.ReservedIp{{Id: common.String(icp.Spec.ReservedPublicAddressId)}}
		}

		createDetails.ShapeDetails = &ociloadbalancer.ShapeDetails{
			MinimumBandwidthInMbps: common.Int(icp.Spec.MinBandwidthMbps),
			MaximumBandwidthInMbps: common.Int(icp.Spec.MaxBandwidthMbps),
		}

		createLbRequest := ociloadbalancer.CreateLoadBalancerRequest{
			// Use UID as retry token so multiple requests in 24 hours won't recreate the same LoadBalancer,
			// but recreate of the IngressClass will trigger an LB within 24 hours.
			// If you used ingress class name it would disallow creation of more LB's even in different clusters potentially.
			OpcRetryToken:             common.String(fmt.Sprintf("create-lb-%s", ic.UID)),
			CreateLoadBalancerDetails: createDetails,
		}
		klog.Infof("Create lb request: %s", util.PrettyPrint(createLbRequest))
		lb, err = c.lbClient.CreateLoadBalancer(context.Background(), createLbRequest)
		if err != nil {
			return err
		}
	}

	if *lb.Id != util.GetIngressClassLoadBalancerId(ic) {
		klog.InfoS("Adding load balancer id to ingress class", "lbId", *lb.Id, "ingressClass", klog.KObj(ic))

		patchBytes := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, util.IngressClassLoadBalancerIdAnnotation, *lb.Id))

		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			_, err := c.client.NetworkingV1().IngressClasses().Patch(context.TODO(), ic.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
			return err
		})

		if apierrors.IsConflict(err) {
			return errors.Wrapf(err, "updateMaxRetries(%d) limit was reached while attempting to add load balancer id annotation", retry.DefaultBackoff.Steps)
		}

		if err != nil {
			return err
		}
	}

	klog.V(4).InfoS("checking if updates are required for load balancer", "ingressClass", klog.KObj(ic))

	return nil
}

func (c *Controller) deleteIngressClass(ic *networkingv1.IngressClass) error {

	err := c.deleteLoadBalancer(ic)
	if err != nil {
		return err
	}

	err = c.deleteFinalizer(ic)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) deleteLoadBalancer(ic *networkingv1.IngressClass) error {
	lbID := util.GetIngressClassLoadBalancerId(ic)
	if lbID == "" {
		return nil
	}

	return c.lbClient.DeleteLoadBalancer(context.Background(), lbID)
}

func isIngressControllerDeleting(ic *networkingv1.IngressClass) bool {
	return ic.DeletionTimestamp != nil
}

func hasFinalizer(ic *networkingv1.IngressClass) bool {
	for _, f := range ic.Finalizers {
		if f == util.IngressControllerFinalizer {
			return true
		}
	}
	return false
}

func (c *Controller) ensureFinalizer(ic *networkingv1.IngressClass) error {
	if hasFinalizer(ic) {
		return nil
	}

	klog.Infof("adding finalizer to ingress class %v", ic.Name)

	var finalizers []string
	if ic.Finalizers != nil {
		finalizers = ic.Finalizers
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		patch, err := json.Marshal(&objectForFinalizersPatch{
			ObjectMetaForFinalizersPatch: ObjectMetaForFinalizersPatch{
				ResourceVersion: ic.GetResourceVersion(),
				Finalizers:      append(finalizers, util.IngressControllerFinalizer),
			},
		})
		if err != nil {
			return err
		}

		_, err = c.client.NetworkingV1().IngressClasses().Patch(context.TODO(), ic.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) limit was reached while attempting to remove finalizer", retry.DefaultBackoff.Steps)
	}

	return err
}

func (c *Controller) deleteFinalizer(ic *networkingv1.IngressClass) error {
	if !hasFinalizer(ic) {
		return nil
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		patch, err := json.Marshal(&objectForFinalizersPatch{
			ObjectMetaForFinalizersPatch: ObjectMetaForFinalizersPatch{
				ResourceVersion: ic.GetResourceVersion(),
				Finalizers:      removeStrings(ic.GetFinalizers(), []string{util.IngressControllerFinalizer}),
			},
		})
		if err != nil {
			return err
		}

		_, err = c.client.NetworkingV1().IngressClasses().Patch(context.TODO(), ic.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) limit was reached attempting to remove finalizer", retry.DefaultBackoff.Steps)
	}

	return err
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

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 || errors.Is(err, errIngressClassNotReady) {
		klog.Infof("Error syncing ingress class %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	klog.Infof("Dropping ingress class %q out of the queue: %v", key, err)
}

// Run begins watching and syncing.
func (c *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("Starting Ingress Class controller")

	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("Stopping ingress class controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

type objectForFinalizersPatch struct {
	ObjectMetaForFinalizersPatch `json:"metadata"`
}

// ObjectMetaForFinalizersPatch defines object meta struct for finalizers patch operation.
type ObjectMetaForFinalizersPatch struct {
	ResourceVersion string   `json:"resourceVersion"`
	Finalizers      []string `json:"finalizers"`
}

func removeStrings(slice []string, strings []string) (result []string) {
OUTER:
	for _, item := range slice {
		for _, s := range strings {

			if item == s {
				continue OUTER
			}
		}
		result = append(result, item)
	}
	return
}

// helper struct since the oci go sdk doesn't let you create errors directly
// since the struct is private...
type notFoundServiceError struct {
	common.ServiceError
}

func (e *notFoundServiceError) GetHTTPStatusCode() int {
	return 404
}

// The human-readable error string as sent by the service
func (e *notFoundServiceError) GetMessage() string {
	return "NotFound"
}

// A short error code that defines the error, meant for programmatic parsing.
// See https://docs.cloud.oracle.com/Content/API/References/apierrors.htm
func (e *notFoundServiceError) GetCode() string {
	return "NotFound"
}

// Unique Oracle-assigned identifier for the request.
// If you need to contact Oracle about a particular request, please provide the request ID.
func (e *notFoundServiceError) GetOpcRequestID() string {
	return "fakeopcrequestid"
}

func (e *notFoundServiceError) Error() string {
	return "NotFound"
}
