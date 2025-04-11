/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2025 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package certificatecleanup

import (
	"context"
	"fmt"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/klog/v2"
	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

const (
	cleanupTaskInterval = 24 * time.Hour
	deletionTimeTagKey  = "oci-native-ingress-controller-deletion-time"
)

type Task struct {
	controllerClass                 string
	gracePeriod                     time.Duration
	ingressClassLister              networkinglisters.IngressClassLister
	saLister                        corelisters.ServiceAccountLister
	useLbCompartmentForCertificates bool
	defaultCompartmentId            string

	client *client.ClientProvider
	cache  ctrcache.Cache
}

func NewTask(controllerClass string, ingressClassInformer networkinginformers.IngressClassInformer, saInformer coreinformers.ServiceAccountInformer,
	useLbCompartmentForCertificates bool, defaultCompartmentId string, gracePeriod time.Duration, client *client.ClientProvider, ctrcache ctrcache.Cache) *Task {
	return &Task{
		controllerClass:                 controllerClass,
		ingressClassLister:              ingressClassInformer.Lister(),
		saLister:                        saInformer.Lister(),
		useLbCompartmentForCertificates: useLbCompartmentForCertificates,
		defaultCompartmentId:            defaultCompartmentId,
		gracePeriod:                     gracePeriod,
		client:                          client,
		cache:                           ctrcache,
	}
}

func (t *Task) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	klog.Info("Starting Certificate cleanup task runner thread")
	time.Sleep(20 * time.Minute)
	go wait.Until(t.run, cleanupTaskInterval, stopCh)
	<-stopCh
	klog.Info("Stopping Certificate cleanup task runner thread")
}

func (t *Task) run() {
	klog.Infof("Running a Certificate cleanup task")
	err := t.cleanup()
	if err != nil {
		klog.Errorf("encountered error while trying to clean up certificate resources: %s", err.Error())
		utilruntime.HandleError(err)
	}
}

// cleanup is the top level method to clean all compartments
func (t *Task) cleanup() error {
	// To keep track of compartments that have been cleaned up
	cleanedUpCompartments := sets.New[string]()

	// Go through relevant IngressClasses, clean up LB compartment if we are managing certs in the LB compartment
	ingressClasses, err := t.ingressClassLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("unable to list ingress classes for cleaning certificates: %s", err.Error())
	} else {
		for _, ingressClass := range ingressClasses {
			if ingressClass.Spec.Controller == t.controllerClass {
				compartmentId := t.defaultCompartmentId

				if t.useLbCompartmentForCertificates {
					icp, err := util.GetIngressClassParameters(ingressClass, t.cache)
					if err != nil {
						klog.Errorf("unable to list ingress class parameters for %s for cleaning certificates: %s", klog.KObj(ingressClass), err.Error())
						continue
					}

					compartmentId = util.GetIngressClassCompartmentId(icp, t.defaultCompartmentId)
				}

				if cleanedUpCompartments.Has(compartmentId) {
					continue
				}

				err = t.cleanupCompartmentWithAppropriateContext(compartmentId, ingressClass, ingressClass.Namespace, ingressClass.Name)
				if err != nil {
					klog.Error(err)
					continue
				}

				cleanedUpCompartments.Insert(compartmentId)
			}
		}
	}

	// Finally, if default compartment is not cleaned up yet, use the set-up principal to clean it up
	if !cleanedUpCompartments.Has(t.defaultCompartmentId) {
		err = t.cleanupCompartmentWithAppropriateContext(t.defaultCompartmentId, nil, "", "")
		if err != nil {
			return fmt.Errorf("unable to clean certificate resources in default compartment %s: %w", t.defaultCompartmentId, err)
		}
	}

	return nil
}

// cleanupCompartmentWithAppropriateContext cleans up certificate resources in compartmentId with appropriate context provided by the ingressClass.
// If ingressClass is nil, we will end up using the principal used while setting up NIC
func (t *Task) cleanupCompartmentWithAppropriateContext(compartmentId string, ingressClass *networkingv1.IngressClass, namespace, name string) error {
	klog.Infof("cleaing up unused certificate resources in compartment %s", compartmentId)
	ctx, err := client.GetClientContext(ingressClass, t.saLister, t.client, namespace, name)
	if err != nil {
		return fmt.Errorf("unable to get client context for %s while cleaning up certificates for compartment %s: %w",
			klog.KObj(ingressClass), compartmentId, err)
	}

	err = cleanupCompartment(ctx, compartmentId, t.gracePeriod)
	if err != nil {
		return fmt.Errorf("failed to clean up certificate resources for compartment Id %s: %s", compartmentId, err)
	}

	return nil
}

// cleanupCompartment cleans up certificate resources in compartmentId by using the client supplied in ctx
//
// Cleaning up these resources is a two-step process
//  1. Mark a resource eligible for cleaning with a deletionTimestamp of gracePeriod ahead in the future, using a freeform tag
//  2. Subsequently, if we see a resource that has deletionTimestamp tag that is in the past, we delete it
//
// If in the interval of being marked and being deleted, the resource is used again by NIC, the deletionTimestamp tag will be removed
// NIC's update logic (which will detect that the resource-hash tag is absent and will try to restore it)
func cleanupCompartment(ctx context.Context, compartmentId string, gracePeriod time.Duration) error {
	wrapperClient, ok := ctx.Value(util.WrapperClient).(*client.WrapperClient)
	if !ok {
		return fmt.Errorf(util.OciClientNotFoundInContextError)
	}

	err := cleanupCaBundlesForCompartment(wrapperClient.GetCertClient(), compartmentId, gracePeriod)
	if err != nil {
		return err
	}

	err = cleanupCertificatesForCompartment(wrapperClient.GetCertClient(), compartmentId, gracePeriod)
	if err != nil {
		return err
	}

	return nil
}

func cleanupCaBundlesForCompartment(client *certificate.CertificatesClient, compartmentId string, gracePeriod time.Duration) error {
	caBundlesToBeMarked, caBundlesToBeDeleted, err := getCaBundlesToBeMarkedOrCleanedUp(client, compartmentId)
	if err != nil {
		return err
	}

	markCaBundlesWithDeletionTime(client, caBundlesToBeMarked, gracePeriod)
	deleteExpiredCaBundles(client, caBundlesToBeDeleted)

	return nil
}

func cleanupCertificatesForCompartment(client *certificate.CertificatesClient, compartmentId string, gracePeriod time.Duration) error {
	certificatesToBeMarked, certificatesToBeDeleted, err := getCertificatesToBeMarkedOrCleanedUp(client, compartmentId)
	if err != nil {
		return err
	}

	markCertificatesWithDeletionTime(client, certificatesToBeMarked, gracePeriod)
	deleteExpiredCertificates(client, certificatesToBeDeleted)

	return nil
}
