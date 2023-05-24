/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oracle/oci-native-ingress-controller/pkg/server"
	"github.com/oracle/oci-native-ingress-controller/pkg/types"
	"k8s.io/klog/v2"

	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"
)

func main() {

	klog.InitFlags(nil)

	var opts types.IngressOpts

	flag.StringVar(&opts.KubeConfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&opts.LeaseID, "id", uuid.New().String(), "the holder identity name")
	flag.StringVar(&opts.LeaseLockName, "lease-lock-name", "", "the lease lock resource name")
	flag.StringVar(&opts.LeaseLockNamespace, "lease-lock-namespace", "", "the lease lock resource namespace")

	flag.StringVar(&opts.CompartmentId, "compartment-id", "", "the default compartment for oci resource creation")
	flag.StringVar(&opts.SubnetId, "subnet-id", "", "the default subnet for load balancers")
	flag.StringVar(&opts.ControllerClass, "controller-class", "oci.oraclecloud.com/native-ingress-controller", "the controller class name")
	flag.StringVar(&opts.AuthType, "authType", "instance", "The auth type to be used - supported values : user, instance(Default).")
	flag.StringVar(&opts.AuthSecretName, "auth-secret-name", "", "Secret name used for auth, cannot be empty if authType is user principal")
	flag.Parse()

	if opts.LeaseLockName == "" {
		klog.Fatal("unable to get lease lock resource name (missing lease-lock-name flag).")
	}
	if opts.LeaseLockNamespace == "" {
		klog.Fatal("unable to get lease lock resource namespace (missing lease-lock-namespace flag).")
	}

	if opts.CompartmentId == "" {
		klog.Fatal("unable to get compartment id (missing compartment-id flag).")
	}

	if opts.SubnetId == "" {
		klog.Fatal("unable to get subnet id (missing subnet-id flag).")
	}

	if opts.AuthType == "user" {
		if opts.AuthSecretName == "" {
			klog.Fatal("unable to get secret name (missing secret-name flag) since authType is user.")
		}
	}

	// leader election uses the Kubernetes API by writing to a
	// lock object, which can be a LeaseLock object (preferred),
	// a ConfigMap, or an Endpoints (deprecated) object.
	// Conflicting writes are detected and each client handles those actions
	// independently.
	config, err := server.BuildConfig(opts.KubeConfig)
	if err != nil {
		klog.Fatal(err)
	}
	client := clientset.NewForConfigOrDie(config)

	// Register our CRD with the default scheme to be understood by clients.
	v1beta1.AddToScheme(scheme.Scheme)

	c, err := ctrcache.New(config, ctrcache.Options{
		Scheme: scheme.Scheme,
	})
	if err != nil {
		klog.Fatalf("failed to construct cache for crds: %w", err)
	}
	// use a Go context so we can tell the leaderelection code when we
	// want to step down
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	classParamInformer, err := c.GetInformer(ctx, &v1beta1.IngressClassParameters{})
	if err != nil {
		klog.Fatalf("failed to construct informer for class params: %w", err)
	}

	go func() {
		klog.Info("starting controller runtime informers")
		err = c.Start(ctx)
		if err != nil {
			klog.Fatalf("failed to start informers for class params: %w", err)
		}
	}()

	informerFactory := informers.NewSharedInformerFactory(client, 1*time.Minute)

	// listen for interrupts or the Linux SIGTERM signal and cancel
	// our context, which the leader election code will observe and
	// step down
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		klog.Info("Received termination, signaling shutdown")
		cancel()
	}()

	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	go ingressClassInformer.Informer().Run(ctx.Done())

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	go ingressInformer.Informer().Run(ctx.Done())

	serviceInformer := informerFactory.Core().V1().Services()
	go serviceInformer.Informer().Run(ctx.Done())

	endpointInformer := informerFactory.Core().V1().Endpoints()
	go endpointInformer.Informer().Run(ctx.Done())

	podInformer := informerFactory.Core().V1().Pods()
	go podInformer.Informer().Run(ctx.Done())

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())
	klog.Info("waiting on caches to sync")

	// wait for the initial synchronization of the local cache.
	if !cache.WaitForCacheSync(ctx.Done(),
		ingressClassInformer.Informer().HasSynced,
		ingressInformer.Informer().HasSynced,
		serviceInformer.Informer().HasSynced,
		endpointInformer.Informer().HasSynced,
		podInformer.Informer().HasSynced,
		classParamInformer.HasSynced) {

		klog.Fatal("failed to sync informers")
	}

	server.SetupWebhookServer(ingressInformer, serviceInformer, client, ctx)

	run := server.SetUpControllers(opts, ingressClassInformer, ingressInformer, client, serviceInformer, endpointInformer, podInformer, c)

	// we use the Lease lock type since edits to Leases are less common
	// and fewer objects in the cluster watch "all Leases".
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      opts.LeaseLockName,
			Namespace: opts.LeaseLockNamespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: opts.LeaseID,
		},
	}

	// start the leader election code loop
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: lock,
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// we're notified when we start - this is where you would
				// usually put your code
				run(ctx)
			},
			OnStoppedLeading: func() {
				klog.Infof("leader lost: %s", opts.LeaseID)
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				// we're notified when new leader elected
				if identity == opts.LeaseID {
					// I just got the lock
					return
				}
				klog.Infof("new leader elected: %s", identity)
			},
		},
	})
}
