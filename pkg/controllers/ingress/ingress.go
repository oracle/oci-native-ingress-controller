/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/events"
	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/pkg/errors"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
)

var errIngressClassNotReady = errors.New("ingress class not ready")

const (
	// ToBeDeletedTaint is set by cluster-autoscaler before a node is removed.
	ToBeDeletedTaint = "ToBeDeletedByClusterAutoscaler"
)

// Controller demonstrates how to implement a controller with client-go.
type Controller struct {
	controllerClass      string
	defaultCompartmentId string

	ingressClassLister              networkinglisters.IngressClassLister
	ingressLister                   networkinglisters.IngressLister
	serviceLister                   corelisters.ServiceLister
	endpointLister                  corelisters.EndpointsLister
	podLister                       corelisters.PodLister
	nodeLister                      corelisters.NodeLister
	saLister                        corelisters.ServiceAccountLister
	secretLister                    corelisters.SecretLister
	queue                           workqueue.RateLimitingInterface
	informer                        networkinginformers.IngressInformer
	client                          *client.ClientProvider
	metricsCollector                *metric.IngressCollector
	ctrCache                        ctrcache.Cache
	useLbCompartmentForCertificates bool
	useNodeBackends                 bool
	eventRecorder                   events.EventRecorder
}

// NewController creates a new Controller.
func NewController(controllerClass string, defaultCompartmentId string,
	ingressClassInformer networkinginformers.IngressClassInformer, ingressInformer networkinginformers.IngressInformer,
	saInformer coreinformers.ServiceAccountInformer, serviceLister corelisters.ServiceLister, endpointLister corelisters.EndpointsLister,
	podLister corelisters.PodLister, nodeLister corelisters.NodeLister, secretInformer coreinformers.SecretInformer,
	client *client.ClientProvider, reg *prometheus.Registry, ctrCache ctrcache.Cache, useLbCompartmentForCertificates bool, eventRecorder events.EventRecorder) *Controller {

	c := &Controller{
		controllerClass:                 controllerClass,
		defaultCompartmentId:            defaultCompartmentId,
		ingressClassLister:              ingressClassInformer.Lister(),
		ingressLister:                   ingressInformer.Lister(),
		informer:                        ingressInformer,
		serviceLister:                   serviceLister,
		endpointLister:                  endpointLister,
		podLister:                       podLister,
		nodeLister:                      nodeLister,
		saLister:                        saInformer.Lister(),
		secretLister:                    secretInformer.Lister(),
		client:                          client,
		queue:                           workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(10*time.Second, 5*time.Minute)),
		metricsCollector:                metric.NewIngressCollector(controllerClass, reg),
		ctrCache:                        ctrCache,
		useLbCompartmentForCertificates: useLbCompartmentForCertificates,
		useNodeBackends:                 nodeLister != nil,
		eventRecorder:                   eventRecorder,
	}

	ingressInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.ingressAdd,
			UpdateFunc: c.ingressUpdate,
			DeleteFunc: c.ingressDelete,
		},
	)

	secretInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerDetailedFuncs{
			AddFunc:    c.secretAdd,
			UpdateFunc: c.secretUpdate,
		},
	)

	return c
}

func (c *Controller) enqueueIngress(ingress *networkingv1.Ingress) {
	key, err := cache.MetaNamespaceKeyFunc(ingress)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %#v: %v", ingress, err))
		return
	}
	c.queue.Add(key)
}

func (c *Controller) ingressAdd(obj interface{}) {
	if c.metricsCollector != nil {
		c.metricsCollector.IncrementIngressAddOperation()
	}
	ic := obj.(*networkingv1.Ingress)
	klog.V(4).InfoS("Adding ingress", "ingress", klog.KObj(ic))
	c.enqueueIngress(ic)
}

func (c *Controller) ingressUpdate(old, new interface{}) {
	oldIngress := old.(*networkingv1.Ingress)
	newIngress := new.(*networkingv1.Ingress)

	// Ignore regular resyncs that didn't come from an actual update.
	if oldIngress.ResourceVersion == newIngress.ResourceVersion {
		return
	}
	if c.metricsCollector != nil {
		c.metricsCollector.IncrementIngressUpdateOperation()
	}

	klog.V(4).InfoS("Updating ingress", "ingress", klog.KObj(oldIngress))
	c.enqueueIngress(newIngress)
}

func (c *Controller) ingressDelete(obj interface{}) {
	if c.metricsCollector != nil {
		c.metricsCollector.IncrementIngressDeleteOperation()
	}
	ic, ok := obj.(*networkingv1.Ingress)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}
		ic, ok = tombstone.Obj.(*networkingv1.Ingress)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Ingress %#v", obj))
			return
		}
	}

	klog.V(4).InfoS("Deleting ingress", "ingress", klog.KObj(ic))
	c.enqueueIngress(ic)
}

func (c *Controller) secretAdd(obj interface{}, isInInitialList bool) {
	if isInInitialList {
		return
	}
	c.secretAddOrUpdate(obj)
}

func (c *Controller) secretUpdate(old, new interface{}) {
	c.secretAddOrUpdate(new)
}

func (c *Controller) secretAddOrUpdate(obj interface{}) {
	secret := obj.(*corev1.Secret)

	ingresses, err := c.ingressLister.Ingresses(secret.Namespace).List(labels.Everything())
	if err != nil {
		klog.Errorf("error listing Ingresses for update of secret %s/%s: %s", secret.Namespace, secret.Name, err)
		return
	}

	for _, ingress := range ingresses {
		for _, tls := range ingress.Spec.TLS {
			if tls.SecretName == secret.Name {
				klog.V(4).Infof("updating ingress %s because of secret %s",
					klog.KObj(ingress), klog.KObj(secret))
				c.enqueueIngress(ingress)
			}
		}
	}
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

	// ingress_sync_time
	startBuildTime := util.GetCurrentTimeInUnixMillis()

	// Invoke the method containing the business logic
	err := c.sync(key.(string))

	endBuildTime := util.GetCurrentTimeInUnixMillis()
	if c.metricsCollector != nil {
		c.metricsCollector.AddIngressSyncTime(util.GetTimeDifferenceInSeconds(startBuildTime, endBuildTime))
	}

	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// sync is the business logic of the controller.
func (c *Controller) sync(key string) error {
	if c.metricsCollector != nil {
		c.metricsCollector.IncrementSyncCount()
	}
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.ErrorS(err, "Failed to split meta namespace cache key", "cacheKey", key)
		return err
	}

	startTime := time.Now()
	klog.V(4).InfoS("Started syncing ingress", "ingress", klog.KRef(namespace, name), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing ingress", "ingress", klog.KRef(namespace, name), "duration", time.Since(startTime))
	}()

	ingress, err := c.ingressLister.Ingresses(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		klog.V(2).InfoS("ingress has been deleted", "ingress", klog.KRef(namespace, name))
		return nil
	}
	if err != nil {
		return err
	}

	ingressClass, err := util.GetIngressClass(ingress, c.ingressClassLister)
	if err != nil {
		return err
	}

	if ingressClass == nil || ingressClass.Spec.Controller != c.controllerClass {
		klog.V(4).InfoS("skipping ingress class, not for this controller", "ingress", klog.KRef(namespace, name))
		// skipping since ingress class is not applicable to this controller, we remove our finalizer if it exists
		deleteFinalizerErr := c.deleteFinalizer(ingress)
		if deleteFinalizerErr != nil {
			klog.V(4).Infof("Found Ingress %s/%s with finalizer %s, but not managed by this controller. Unable to delete"+
				" finalizer due to error: %s", ingress.Namespace, ingress.Name, util.IngressControllerFinalizer, deleteFinalizerErr.Error())
		}

		return nil
	}

	ctx, err := client.GetClientContext(ingressClass, c.saLister, c.client, namespace, name)
	if err != nil {
		return err
	}

	lbID := util.GetIngressClassLoadBalancerId(ingressClass)
	if lbID == "" {
		return errIngressClassNotReady
	}

	if util.IsIngressDeleting(ingress) {
		klog.Infof("Found ingress %s in deleting state", ingress.Name)
		err = handleIngressDelete(ctx, c, ingressClass)
		if err != nil {
			return err
		}

		return c.deleteIngress(ingress)
	}

	klog.V(4).InfoS("ensuring ingress", "ingress", klog.KRef(namespace, name))

	err = c.ensureFinalizer(ctx, ingress)
	if err != nil {
		return err
	}

	err = c.ensureLoadBalancerIP(ctx, lbID, ingress)
	if err != nil {
		return err
	}

	err = c.ensureIngress(ctx, ingress, ingressClass)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) ensureLoadBalancerIP(ctx context.Context, lbID string, ingress *networkingv1.Ingress) error {
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	lb, _, err := wrapperClient.GetLbClient().GetLoadBalancer(context.TODO(), lbID)
	if err != nil {
		return errors.Wrapf(err, "unable to fetch ip from load balancer: %s", err.Error())
	}

	if len(lb.IpAddresses) < 1 {
		// Pending IP address assignment most likely since LB is creating
		return nil
	}

	ipAddress := *lb.IpAddresses[0].IpAddress

	found := false
	for _, i := range ingress.Status.LoadBalancer.Ingress {
		if i.IP == ipAddress {
			found = true
			break
		}
	}

	if found {
		klog.V(4).InfoS("ip address already set on ingress", "ingress", klog.KObj(ingress), "ipAddress", ipAddress)
		return nil
	}

	klog.V(2).InfoS("adding ip address to ingress", "ingress", klog.KObj(ingress), "ipAddress", ipAddress)

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		latest, err := wrapperClient.GetK8Client().NetworkingV1().Ingresses(ingress.Namespace).Get(context.TODO(), ingress.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		latest.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{
			{IP: ipAddress},
		}

		_, err = wrapperClient.GetK8Client().NetworkingV1().Ingresses(ingress.Namespace).UpdateStatus(context.TODO(), latest, metav1.UpdateOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) limit was reached while attempting to remove finalizer", retry.DefaultBackoff.Steps)
	}

	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) ensureIngress(ctx context.Context, ingress *networkingv1.Ingress, ingressClass *networkingv1.IngressClass) error {

	klog.Infof("Processing ingress %s/%s", ingressClass.Name, ingress.Name)

	certificateCompartmentId := c.defaultCompartmentId
	if c.useLbCompartmentForCertificates {
		ingressClassParameters, err := util.GetIngressClassParameters(ingressClass, c.ctrCache)
		if err != nil {
			return err
		}
		certificateCompartmentId = util.GetIngressClassCompartmentId(ingressClassParameters, c.defaultCompartmentId)
	}

	stateStore := state.NewStateStore(c.ingressClassLister, c.ingressLister, c.serviceLister, c.metricsCollector)
	ingressConfigError := stateStore.BuildState(ingressClass)

	if ingressConfigError != nil {
		return ingressConfigError
	}

	desiredPorts := stateStore.GetIngressPorts(ingress.Name)

	desiredBackendSets := stateStore.GetIngressBackendSets(ingress.Name)

	lbId := util.GetIngressClassLoadBalancerId(ingressClass)

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	lb, _, err := wrapperClient.GetLbClient().GetLoadBalancer(context.TODO(), lbId)
	if err != nil {
		return err
	}

	actualBackendSets := sets.NewString()
	for bsName := range lb.BackendSets {
		actualBackendSets.Insert(bsName)

		if desiredBackendSets.Has(bsName) {
			err = syncBackendSet(ctx, ingress, lbId, bsName, stateStore, certificateCompartmentId, c)
			if err != nil {
				return err
			}
		}
	}

	backendSetsToCreate := desiredBackendSets.Difference(actualBackendSets)
	initialBackendsByBackendSet, err := c.getInitialBackendsByBackendSet(ingress)
	if err != nil {
		return err
	}

	for _, bsName := range backendSetsToCreate.List() {
		startBuildTime := util.GetCurrentTimeInUnixMillis()
		klog.V(2).InfoS("creating backend set for ingress", "ingress", klog.KObj(ingress), "backendSetName", bsName)
		artifact, artifactType := stateStore.GetTLSConfigForBackendSet(bsName)
		backendSetSslConfig, err := GetSSLConfigForBackendSet(ingress.Namespace, artifactType, artifact, lb, bsName, certificateCompartmentId, c.secretLister, wrapperClient)
		if err != nil {
			return err
		}

		healthChecker := stateStore.GetBackendSetHealthChecker(bsName)
		policy := stateStore.GetBackendSetPolicy(bsName)
		appCookie, lbCookie := stateStore.GetBackendSetSessionPersistence(bsName)
		err = wrapperClient.GetLbClient().CreateBackendSet(context.TODO(), lbId, bsName, policy, healthChecker, backendSetSslConfig, appCookie, lbCookie, initialBackendsByBackendSet[bsName])
		if err != nil {
			return err
		}
		endBuildTime := util.GetCurrentTimeInUnixMillis()
		if c.metricsCollector != nil {
			c.metricsCollector.AddBackendCreationTime(util.GetTimeDifferenceInSeconds(startBuildTime, endBuildTime))
		}
	}

	if backendSetsToCreate.Len() > 0 {
		lb, _, err = wrapperClient.GetLbClient().RefreshLoadBalancer(context.TODO(), lbId)
		if err != nil {
			return err
		}
	}

	// Determine listeners... This is based off path ports.
	actualListenerPorts := sets.NewInt32()
	for _, listener := range lb.Listeners {
		listenerPort := int32(*listener.Port)
		actualListenerPorts.Insert(listenerPort)

		if desiredPorts.Has(listenerPort) {
			err := syncListener(ctx, ingress.Namespace, stateStore, &lbId, *listener.Name, certificateCompartmentId, c)
			if err != nil {
				return err
			}
		}
	}

	toCreate := desiredPorts.Difference(actualListenerPorts)

	for _, port := range toCreate.List() {
		klog.V(2).InfoS("adding listener for ingress", "ingress", klog.KObj(ingress), "port", port)

		var listenerSslConfig *ociloadbalancer.SslConfigurationDetails
		artifact, artifactType := stateStore.GetTLSConfigForListener(port)
		listenerSslConfig, err := GetSSLConfigForListener(ingress.Namespace, nil, artifactType, artifact, certificateCompartmentId, c.secretLister, wrapperClient)
		if err != nil {
			return err
		}

		protocol := stateStore.GetListenerProtocol(port)
		defaultBackendSet := stateStore.GetListenerDefaultBackendSet(port)
		err = wrapperClient.GetLbClient().CreateListener(context.TODO(), lbId, int(port), protocol, defaultBackendSet, listenerSslConfig)
		if err != nil {
			return err
		}
	}

	desiredListenerPorts := stateStore.GetAllListenersForIngressClass()
	if err != nil {
		return err
	}

	err = deleteListeners(actualListenerPorts, desiredListenerPorts, wrapperClient.GetLbClient(), lbId)
	if err != nil {
		return err
	}

	desiredBackendSets = stateStore.GetAllBackendSetForIngressClass()
	if err != nil {
		return err
	}

	return deleteBackendSets(actualBackendSets, desiredBackendSets, wrapperClient.GetLbClient(), lbId)
}

func handleIngressDelete(ctx context.Context, c *Controller, ingressClass *networkingv1.IngressClass) error {
	stateStore := state.NewStateStore(c.ingressClassLister, c.ingressLister, c.serviceLister, c.metricsCollector)
	ingressConfigError := stateStore.BuildState(ingressClass)

	if ingressConfigError != nil {
		return ingressConfigError
	}

	lbId := util.GetIngressClassLoadBalancerId(ingressClass)

	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	lb, _, err := wrapperClient.GetLbClient().GetLoadBalancer(context.TODO(), lbId)
	if err != nil {
		return err
	}

	actualListeners := sets.NewInt32()
	for _, listener := range lb.Listeners {
		actualListeners.Insert(int32(*listener.Port))
	}

	err = deleteListeners(actualListeners, stateStore.GetAllListenersForIngressClass(), wrapperClient.GetLbClient(), lbId)
	if err != nil {
		return err
	}

	actualBackendSets := sets.NewString()
	for bsName := range lb.BackendSets {
		actualBackendSets.Insert(bsName)
	}

	err = deleteBackendSets(actualBackendSets, stateStore.GetAllBackendSetForIngressClass(), wrapperClient.GetLbClient(), lbId)
	if err != nil {
		return err
	}

	return nil
}

func deleteBackendSets(actualBackendSets sets.String, desiredBackendSets sets.String, lbClient *loadbalancer.LoadBalancerClient, lbId string) error {
	backendSetsToDelete := actualBackendSets.Difference(desiredBackendSets)
	if len(backendSetsToDelete) > 0 {
		klog.Infof("Backend sets to delete %s", util.PrettyPrint(backendSetsToDelete))
		for _, name := range backendSetsToDelete.List() {
			klog.Infof("Deleting backend set %s ", name)

			err := lbClient.DeleteBackendSet(context.TODO(), lbId, name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteListeners(actualListeners sets.Int32, desiredListeners sets.Int32, lbClient *loadbalancer.LoadBalancerClient, lbId string) error {
	listenersToDelete := actualListeners.Difference(desiredListeners)
	if len(listenersToDelete) > 0 {
		klog.Infof("Listeners to delete %s", util.PrettyPrint(listenersToDelete))
		for _, port := range listenersToDelete.List() {
			name := util.GenerateListenerName(port)
			klog.Infof("Deleting listener set %s ", name)

			err := lbClient.DeleteListener(context.TODO(), lbId, name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func syncListener(ctx context.Context, namespace string, stateStore *state.StateStore, lbId *string,
	listenerName string, certificateCompartmentId string, c *Controller) error {
	startTime := util.GetCurrentTimeInUnixMillis()
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}
	lb, etag, err := wrapperClient.GetLbClient().GetLoadBalancer(context.TODO(), *lbId)
	if err != nil {
		return err
	}

	listener, ok := lb.Listeners[listenerName]
	if !ok {
		return fmt.Errorf("during update, listener %s was not found", listenerName)
	}

	needsUpdate := false
	artifact, artifactType := stateStore.GetTLSConfigForListener(int32(*listener.Port))
	var sslConfig *ociloadbalancer.SslConfigurationDetails
	if artifact != "" {
		sslConfig, err = GetSSLConfigForListener(namespace, &listener, artifactType, artifact, certificateCompartmentId, c.secretLister, wrapperClient)
		if err != nil {
			return err
		}

		if sslConfig != nil {
			if listener.SslConfiguration == nil || !reflect.DeepEqual(listener.SslConfiguration.CertificateIds, sslConfig.CertificateIds) {
				klog.Infof("SSL config for listener update is %s", util.PrettyPrint(sslConfig))
				needsUpdate = true
			}
		}
	}

	protocol := stateStore.GetListenerProtocol(int32(*listener.Port))
	protocolExisting := listener.Protocol
	if protocol != "" && (protocolExisting == nil || protocol != *protocolExisting) {
		klog.Infof("Protocol for listener %d needs update, new protocol %s", *listener.Name, protocol)
		needsUpdate = true
	}

	defaultBackendSet := stateStore.GetListenerDefaultBackendSet(int32(*listener.Port))
	defaultBackendSetExisting := listener.DefaultBackendSetName
	if defaultBackendSetExisting == nil || defaultBackendSet != *defaultBackendSetExisting {
		klog.Infof("Default BackendSet for listener %d needs update, new Default Backend Set %s", *listener.Name, defaultBackendSet)
		needsUpdate = true
	}

	if needsUpdate {
		err := wrapperClient.GetLbClient().UpdateListener(context.TODO(), lbId, etag, listener, listener.RoutingPolicyName, sslConfig, &protocol, &defaultBackendSet)
		if err != nil {
			return err
		}
	}
	endTime := util.GetCurrentTimeInUnixMillis()
	if c.metricsCollector != nil {
		c.metricsCollector.AddIngressListenerSyncTime(util.GetTimeDifferenceInSeconds(startTime, endTime))
	}
	return nil
}

func syncBackendSet(ctx context.Context, ingress *networkingv1.Ingress, lbID string, backendSetName string,
	stateStore *state.StateStore, certificateCompartmentId string, c *Controller) error {

	startTime := util.GetCurrentTimeInUnixMillis()
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}
	lb, etag, err := wrapperClient.GetLbClient().GetLoadBalancer(context.TODO(), lbID)
	if err != nil {
		return err
	}

	bs, ok := lb.BackendSets[backendSetName]
	if !ok {
		return fmt.Errorf("during update, backendset %s was not found", backendSetName)
	}

	needsUpdate := false
	artifact, artifactType := stateStore.GetTLSConfigForBackendSet(*bs.Name)
	sslConfig, err := GetSSLConfigForBackendSet(ingress.Namespace, artifactType, artifact, lb, *bs.Name, certificateCompartmentId, c.secretLister, wrapperClient)
	if err != nil {
		return err
	}

	if backendSetSslConfigNeedsUpdate(sslConfig, &bs) {
		klog.Infof("SSL config for backend set %s update is %s", *bs.Name, util.PrettyPrint(sslConfig))
		needsUpdate = true
	}

	healthChecker := stateStore.GetBackendSetHealthChecker(*bs.Name)
	healthCheckerExisting := bs.HealthChecker
	if healthChecker != nil && !compareHealthCheckers(healthChecker, healthCheckerExisting) {
		klog.Infof("Health checker for backend set %s needs update, new health checker %s", *bs.Name, util.PrettyPrint(healthChecker))
		needsUpdate = true
	}

	policy := stateStore.GetBackendSetPolicy(*bs.Name)
	policyExisting := bs.Policy
	if policy != "" && policy != *policyExisting {
		klog.Infof("Policy for backend set %s needs update, new policy %s", *bs.Name, policy)
		needsUpdate = true
	}

	desiredAppCookie, desiredLbCookie := stateStore.GetBackendSetSessionPersistence(*bs.Name)
	if !reflect.DeepEqual(bs.SessionPersistenceConfiguration, desiredAppCookie) || !reflect.DeepEqual(bs.LbCookieSessionPersistenceConfiguration, desiredLbCookie) {
		var desiredConfig string
		var currentConfig string
		if desiredLbCookie != nil {
			desiredConfig = util.PrettyPrint(desiredLbCookie)
			currentConfig = util.PrettyPrint(bs.LbCookieSessionPersistenceConfiguration)
		} else {
			desiredConfig = util.PrettyPrint(desiredAppCookie)
			currentConfig = util.PrettyPrint(bs.SessionPersistenceConfiguration)
		}

		klog.Infof("Session persistence for backend set %s needs update. Current configurations : %s, Desired configurations : %s", *bs.Name, currentConfig, desiredConfig)
		needsUpdate = true
	}

	if needsUpdate {
		err = wrapperClient.GetLbClient().UpdateBackendSetDetails(context.TODO(), *lb.Id, etag, &bs, sslConfig, healthChecker, policy, desiredAppCookie, desiredLbCookie)
		if err != nil {
			return err
		}
	}

	endTime := util.GetCurrentTimeInUnixMillis()
	if c.metricsCollector != nil {
		c.metricsCollector.AddIngressBackendSyncTime(util.GetTimeDifferenceInSeconds(startTime, endTime))
	}
	return nil
}

func (c *Controller) getInitialBackendsByBackendSet(ingress *networkingv1.Ingress) (map[string][]ociloadbalancer.BackendDetails, error) {
	backendsByBackendSet := map[string][]ociloadbalancer.BackendDetails{}
	if c.endpointLister == nil {
		return backendsByBackendSet, nil
	}

	for _, rule := range ingress.Spec.Rules {
		if !util.HasHTTPPaths(rule) {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if !util.HasServiceBackend(path) {
				continue
			}

			pSvc, svc, err := util.ExtractServices(path, c.serviceLister, ingress)
			if err != nil {
				return nil, err
			}

			svcName, svcPort, targetPort, err := util.PathToServiceAndTargetPort(c.endpointLister, svc, pSvc, ingress.Namespace, c.useNodeBackends)
			if err != nil {
				klog.InfoS("unable to determine initial backend target port, creating backend set without initial backends",
					"ingress", klog.KObj(ingress), "service", pSvc.Name, "err", err)
				continue
			}

			backendSetName := util.GenerateBackendSetName(ingress.Namespace, svcName, svcPort)
			if _, ok := backendsByBackendSet[backendSetName]; ok {
				continue
			}

			backends, err := c.getInitialBackends(ingress.Namespace, svc, svcName, targetPort)
			if err != nil {
				klog.InfoS("unable to determine initial backends, creating backend set without initial backends",
					"ingress", klog.KObj(ingress), "service", svcName, "backendSet", backendSetName, "err", err)
				backends = nil
			}
			backendsByBackendSet[backendSetName] = backends
		}
	}

	return backendsByBackendSet, nil
}

func (c *Controller) getInitialBackends(namespace string, svc *corev1.Service, svcName string, targetPort int32) ([]ociloadbalancer.BackendDetails, error) {
	if c.useNodeBackends {
		return c.getInitialNodeBackends(namespace, svc, svcName, targetPort)
	}

	epAddrs, err := util.GetEndpoints(c.endpointLister, namespace, svcName)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch endpoints for %s/%s/%d: %w", namespace, svcName, targetPort, err)
	}

	backends := []ociloadbalancer.BackendDetails{}
	for _, epAddr := range epAddrs {
		backends = append(backends, util.NewBackend(epAddr.IP, targetPort))
	}
	return backends, nil
}

func (c *Controller) getInitialNodeBackends(namespace string, svc *corev1.Service, svcName string, nodePort int32) ([]ociloadbalancer.BackendDetails, error) {
	if c.nodeLister == nil || c.podLister == nil || svc == nil || svc.Spec.Ports == nil || nodePort == 0 {
		return nil, nil
	}

	backends := []ociloadbalancer.BackendDetails{}
	trafficPolicy := svc.Spec.ExternalTrafficPolicy
	if trafficPolicy == corev1.ServiceExternalTrafficPolicyTypeCluster {
		nodes, err := c.filterNodes()
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			backends = append(backends, util.NewBackend(nodeInternalIP(node), nodePort))
		}
		return backends, nil
	}

	pods, err := util.RetrievePods(c.endpointLister, c.podLister, namespace, svcName)
	if err != nil {
		return nil, err
	}
	seenBackends := map[string]struct{}{}
	for _, pod := range pods {
		node, err := c.nodeLister.Get(pod.Spec.NodeName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				klog.Infof("node %s has been deleted, skipping pod", pod.Spec.NodeName)
				continue
			}
			return nil, err
		}
		nodeIP := nodeInternalIP(node)
		backendKey := fmt.Sprintf("%s:%d", nodeIP, nodePort)
		if _, ok := seenBackends[backendKey]; ok {
			continue
		}
		seenBackends[backendKey] = struct{}{}
		backends = append(backends, util.NewBackend(nodeIP, nodePort))
	}
	return backends, nil
}

func (c *Controller) filterNodes() ([]*corev1.Node, error) {
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var filtered []*corev1.Node
	for i := range nodes {
		if isNodeReadyForBackends(nodes[i]) {
			filtered = append(filtered, nodes[i])
		}
	}
	return filtered, nil
}

func isNodeReadyForBackends(node *corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == ToBeDeletedTaint {
			return false
		}
	}

	if len(node.Status.Conditions) == 0 {
		return false
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
			return false
		}
	}
	return true
}

func nodeInternalIP(node *corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}

func (c *Controller) deleteIngress(i *networkingv1.Ingress) error {
	klog.V(4).InfoS("deleting ingress", "ingress", klog.KObj(i))

	err := c.deleteFinalizer(i)
	if err != nil {
		return err
	}

	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	} else if c.eventRecorder != nil {
		util.PublishWarningEventForIngress(c.eventRecorder, c.ingressLister, key, err, "IngressReconcileFailed", "IngressReconcile")
	}

	if errors.Is(err, errIngressClassNotReady) {
		c.queue.AddAfter(key, 10*time.Second)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing ingress %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	fmt.Printf("FATAL: %+v\n", err)
	utilruntime.HandleError(err)
	klog.Infof("Dropping ingress %q out of the queue: %v", key, err)
}

func (c *Controller) ensureFinalizer(ctx context.Context, ingress *networkingv1.Ingress) error {
	if hasFinalizer(ingress) {
		return nil
	}

	klog.V(2).InfoS("adding finalizer to ingress", "ingress", klog.KObj(ingress))

	var finalizers []string
	if ingress.Finalizers != nil {
		finalizers = ingress.Finalizers
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		patch, err := json.Marshal(&objectForFinalizersPatch{
			ObjectMetaForFinalizersPatch: ObjectMetaForFinalizersPatch{
				ResourceVersion: ingress.GetResourceVersion(),
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
		_, err = wrapperClient.GetK8Client().NetworkingV1().Ingresses(ingress.Namespace).Patch(context.TODO(), ingress.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) was reached attempting to remove finalizer", retry.DefaultBackoff.Steps)
	}

	return err
}

func (c *Controller) deleteFinalizer(ingress *networkingv1.Ingress) error {
	if !hasFinalizer(ingress) {
		return nil
	}

	klog.V(2).InfoS("removing finalizer from ingress", "ingress", klog.KObj(ingress))

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		patch, err := json.Marshal(&objectForFinalizersPatch{
			ObjectMetaForFinalizersPatch: ObjectMetaForFinalizersPatch{
				ResourceVersion: ingress.GetResourceVersion(),
				Finalizers:      removeStrings(ingress.GetFinalizers(), []string{util.IngressControllerFinalizer}),
			},
		})

		if err != nil {
			return err
		}

		_, err = c.client.K8sClient.NetworkingV1().Ingresses(ingress.Namespace).Patch(context.TODO(), ingress.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) was reached attempting to remove finalizer", retry.DefaultBackoff.Steps)
	}

	return err
}

// Run begins watching and syncing.
func (c *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("Starting Ingress controller")

	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("Stopping Ingress controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func hasFinalizer(i *networkingv1.Ingress) bool {
	for _, f := range i.Finalizers {
		if f == util.IngressControllerFinalizer {
			return true
		}
	}
	return false
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
