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
	coreinformers "k8s.io/client-go/informers/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"time"

	"github.com/oracle/oci-native-ingress-controller/pkg/client"
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
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	"github.com/pkg/errors"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

var errIngressClassNotReady = errors.New("ingress class not ready")

const (
	OnicResource = "oci-native-ingress-controller-resource"
)

// Controller demonstrates how to implement a controller with client-go.
type Controller struct {
	defaultCompartmentId string
	defaultSubnetId      string
	controllerClass      string

	lister   networkinglisters.IngressClassLister
	queue    workqueue.RateLimitingInterface
	informer networkinginformers.IngressClassInformer
	saLister corelisters.ServiceAccountLister
	client   *client.ClientProvider
	cache    ctrcache.Cache
}

// NewController creates a new Controller.
func NewController(
	defaultCompartmentId string,
	defaultSubnetId string,
	controllerClass string,
	informer networkinginformers.IngressClassInformer,
	saInformer coreinformers.ServiceAccountInformer,
	client *client.ClientProvider, ctrcache ctrcache.Cache) *Controller {

	c := &Controller{
		defaultCompartmentId: defaultCompartmentId,
		defaultSubnetId:      defaultSubnetId,
		controllerClass:      controllerClass,
		informer:             informer,
		lister:               informer.Lister(),
		saLister:             saInformer.Lister(),
		client:               client,
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

	ctx, err := client.GetClientContext(ingressClass, c.saLister, c.client, namespace, name)
	if err != nil {
		return err
	}

	if isIngressControllerDeleting(ingressClass) {
		return c.deleteIngressClass(ctx, ingressClass)
	}

	err = c.ensureFinalizer(ctx, ingressClass)
	if err != nil {
		return err
	}

	err = c.ensureLoadBalancer(ctx, ingressClass)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) getLoadBalancer(ctx context.Context, ic *networkingv1.IngressClass) (*ociloadbalancer.LoadBalancer, string, error) {
	lbID := util.GetIngressClassLoadBalancerId(ic)
	if lbID == "" {
		klog.Infof("LB id not set for ingressClass: %s", ic.Name)
		return nil, "", nil // LoadBalancer ID not set, Trigger new LB creation
	}
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return nil, "", fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	lb, etag, err := wrapperClient.GetLbClient().GetLoadBalancer(context.TODO(), lbID)
	if err != nil {
		klog.Errorf("Error while fetching LB %s for ingressClass: %s, err: %s", lbID, ic.Name, err.Error())

		// Check if Service error 404, then ignore it since LB is not found.
		svcErr, ok := common.IsServiceError(err)
		if ok && svcErr.GetHTTPStatusCode() == 404 {
			return nil, "", nil // Redirect new LB creation
		}
		return nil, "", err
	}

	return lb, etag, nil
}

func (c *Controller) ensureLoadBalancer(ctx context.Context, ic *networkingv1.IngressClass) error {

	lb, etag, err := c.getLoadBalancer(ctx, ic)
	if err != nil {
		return err
	}

	icp := &v1beta1.IngressClassParameters{}
	if ic.Spec.Parameters != nil {
		namespace := ""
		if ic.Spec.Parameters.Namespace != nil {
			namespace = *ic.Spec.Parameters.Namespace
		}
		err = c.cache.Get(context.TODO(), ctrclient.ObjectKey{
			Name:      ic.Spec.Parameters.Name,
			Namespace: namespace,
		}, icp)
		if err != nil {
			return fmt.Errorf("unable to fetch IngressClassParameters %s: %w", ic.Spec.Parameters.Name, err)
		}
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}
	compartmentId := common.String(util.GetIngressClassCompartmentId(icp, c.defaultCompartmentId))
	if lb == nil {
		klog.V(2).InfoS("Creating load balancer for ingress class", "ingressClass", ic.Name)

		createDetails := ociloadbalancer.CreateLoadBalancerDetails{
			CompartmentId:           compartmentId,
			DisplayName:             common.String(util.GetIngressClassLoadBalancerName(ic, icp)),
			ShapeName:               common.String("flexible"),
			SubnetIds:               []string{util.GetIngressClassSubnetId(icp, c.defaultSubnetId)},
			IsPrivate:               common.Bool(icp.Spec.IsPrivate),
			NetworkSecurityGroupIds: util.GetIngressClassNetworkSecurityGroupIds(ic),
			BackendSets: map[string]ociloadbalancer.BackendSetDetails{
				util.DefaultBackendSetName: {
					Policy: common.String("LEAST_CONNECTIONS"),
					HealthChecker: &ociloadbalancer.HealthCheckerDetails{
						Protocol: common.String("TCP"),
					},
				},
			},
			FreeformTags: map[string]string{OnicResource: "loadbalancer"},
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
		lb, err = wrapperClient.GetLbClient().CreateLoadBalancer(context.Background(), createLbRequest)
		if err != nil {
			return err
		}
	} else {
		err = c.checkForIngressClassParameterUpdates(ctx, lb, ic, icp, etag)
		if err != nil {
			return err
		}

		err = c.checkForNetworkSecurityGroupsUpdate(ctx, ic)
		if err != nil {
			return err
		}
	}

	if *lb.Id != util.GetIngressClassLoadBalancerId(ic) {
		klog.InfoS("Adding load balancer id to ingress class", "lbId", *lb.Id, "ingressClass", klog.KObj(ic))
		patchError, done := util.PatchIngressClassWithAnnotation(wrapperClient.GetK8Client(), ic, util.IngressClassLoadBalancerIdAnnotation, *lb.Id)
		if done {
			return patchError
		}
	}

	// Add Web Application Firewall to LB
	if wrapperClient.GetWafClient() != nil {
		err = c.setupWebApplicationFirewall(ctx, ic, compartmentId, lb.Id)
		if err != nil {
			return err
		}
	}

	klog.V(4).InfoS("checking if updates are required for load balancer", "ingressClass", klog.KObj(ic))
	return nil
}

func (c *Controller) setupWebApplicationFirewall(ctx context.Context, ic *networkingv1.IngressClass, compartmentId *string, lbId *string) error {
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}
	firewall, conflictError, throwableError, updateRequired := wrapperClient.GetWafClient().GetFireWallId(wrapperClient.GetK8Client(), ic, compartmentId, lbId)
	if !updateRequired {
		return throwableError
	}
	// update to ingressclass
	if conflictError == nil && firewall.GetId() != nil {
		patchError, done := util.PatchIngressClassWithAnnotation(wrapperClient.GetK8Client(), ic, util.IngressClassFireWallIdAnnotation, *firewall.GetId())
		if done {
			return patchError
		}
	}
	return nil
}

func (c *Controller) checkForIngressClassParameterUpdates(ctx context.Context, lb *ociloadbalancer.LoadBalancer,
	ic *networkingv1.IngressClass, icp *v1beta1.IngressClassParameters, etag string) error {
	// check LoadBalancerName AND  MinBandwidthMbps ,MaxBandwidthMbps
	displayName := util.GetIngressClassLoadBalancerName(ic, icp)
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}
	if *lb.DisplayName != displayName {

		detail := ociloadbalancer.UpdateLoadBalancerDetails{
			DisplayName: &displayName,
		}
		req := ociloadbalancer.UpdateLoadBalancerRequest{
			OpcRetryToken:             common.String(fmt.Sprintf("update-lb-detail-%s", ic.UID)),
			UpdateLoadBalancerDetails: detail,
			LoadBalancerId:            lb.Id,
		}

		klog.Infof("Update lb details request: %s", util.PrettyPrint(req))
		_, err := wrapperClient.GetLbClient().UpdateLoadBalancer(context.Background(), req)
		if err != nil {
			return err
		}

	}

	if *lb.ShapeDetails.MaximumBandwidthInMbps != icp.Spec.MaxBandwidthMbps ||
		*lb.ShapeDetails.MinimumBandwidthInMbps != icp.Spec.MinBandwidthMbps {
		shapeDetails := &ociloadbalancer.ShapeDetails{
			MinimumBandwidthInMbps: common.Int(icp.Spec.MinBandwidthMbps),
			MaximumBandwidthInMbps: common.Int(icp.Spec.MaxBandwidthMbps),
		}

		req := ociloadbalancer.UpdateLoadBalancerShapeRequest{
			LoadBalancerId: lb.Id,
			IfMatch:        common.String(etag),
			UpdateLoadBalancerShapeDetails: ociloadbalancer.UpdateLoadBalancerShapeDetails{
				ShapeName:    common.String("flexible"),
				ShapeDetails: shapeDetails,
			},
		}
		klog.Infof("Update lb shape request: %s", util.PrettyPrint(req))
		_, err := wrapperClient.GetLbClient().UpdateLoadBalancerShape(context.Background(), req)
		if err != nil {
			return err
		}

	}
	return nil
}

func (c *Controller) checkForNetworkSecurityGroupsUpdate(ctx context.Context, ic *networkingv1.IngressClass) error {
	lb, _, err := c.getLoadBalancer(ctx, ic)
	if err != nil {
		return err
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	nsgIdsFromSpec := util.GetIngressClassNetworkSecurityGroupIds(ic)

	/*
		Only check if desired and actual slices have the same elements, ignoring order and duplicates
		We don't check if lb.NetworkSecurityGroupIds is nil since util.StringSlicesHaveSameElements returns true if
		one argument is nil and the other is empty.
	*/
	if util.StringSlicesHaveSameElements(nsgIdsFromSpec, lb.NetworkSecurityGroupIds) {
		return nil
	}

	_, err = wrapperClient.GetLbClient().UpdateNetworkSecurityGroups(context.Background(), *lb.Id, nsgIdsFromSpec)
	return err
}

func (c *Controller) deleteIngressClass(ctx context.Context, ic *networkingv1.IngressClass) error {
	if util.GetIngressClassDeleteProtectionEnabled(ic) {
		err := c.clearLoadBalancer(ctx, ic)
		if err != nil {
			return err
		}
	} else {
		err := c.deleteLoadBalancer(ctx, ic)
		if err != nil {
			return err
		}
	}

	err := c.deleteFinalizer(ctx, ic)
	if err != nil {
		return err
	}

	return nil
}

// clearLoadBalancer clears the default_ingress backend, NSG attachment, and WAF firewall from the LB
func (c *Controller) clearLoadBalancer(ctx context.Context, ic *networkingv1.IngressClass) error {
	lb, _, err := c.getLoadBalancer(ctx, ic)
	if err != nil {
		return err
	}

	if lb == nil {
		klog.Infof("Tried to clear LB for ic %s/%s, but it is deleted", ic.Namespace, ic.Name)
		return nil
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	fireWallId := util.GetIngressClassFireWallId(ic)
	if fireWallId != "" {
		wrapperClient.GetWafClient().DeleteWebAppFirewallWithId(fireWallId)
	}

	nsgIds := util.GetIngressClassNetworkSecurityGroupIds(ic)
	if len(nsgIds) > 0 {
		_, err = wrapperClient.GetLbClient().UpdateNetworkSecurityGroups(context.Background(), *lb.Id, make([]string, 0))
		if err != nil {
			klog.Errorf("While clearing LB %s, cannot clear NSG IDs due to %s, will proceed with IngressClass deletion for %s/%s",
				*lb.Id, err.Error(), ic.Namespace, ic.Name)
		}
	}

	err = wrapperClient.GetLbClient().DeleteBackendSet(context.Background(), *lb.Id, util.DefaultBackendSetName)
	if err != nil {
		klog.Errorf("While clearing LB %s, cannot clear BackendSet %s due to %s, will proceed with IngressClass deletion for %s/%s",
			*lb.Id, util.DefaultBackendSetName, err.Error(), ic.Namespace, ic.Name)
	}

	return nil
}

func (c *Controller) deleteLoadBalancer(ctx context.Context, ic *networkingv1.IngressClass) error {
	lbID := util.GetIngressClassLoadBalancerId(ic)
	if lbID == "" {
		return nil
	}

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}
	return wrapperClient.GetLbClient().DeleteLoadBalancer(context.Background(), lbID)
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

func (c *Controller) ensureFinalizer(ctx context.Context, ic *networkingv1.IngressClass) error {
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

		wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
		if !ok {
			return fmt.Errorf(util.OciClientNotFoundInContextError)
		}
		_, err = wrapperClient.GetK8Client().NetworkingV1().IngressClasses().Patch(ctx, ic.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) limit was reached while attempting to remove finalizer", retry.DefaultBackoff.Steps)
	}

	return err
}

func (c *Controller) deleteFinalizer(ctx context.Context, ic *networkingv1.IngressClass) error {
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

		wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
		if !ok {
			return fmt.Errorf(util.OciClientNotFoundInContextError)
		}
		_, err = wrapperClient.GetK8Client().NetworkingV1().IngressClasses().Patch(context.TODO(), ic.Name, types.MergePatchType, patch, metav1.PatchOptions{})
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
