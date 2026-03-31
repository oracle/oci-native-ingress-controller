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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/oracle/oci-go-sdk/v65/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	v1 "k8s.io/client-go/informers/core/v1"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	events "k8s.io/client-go/tools/events"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	"github.com/oracle/oci-native-ingress-controller/pkg/server"
	"github.com/oracle/oci-native-ingress-controller/pkg/types"
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
	flag.StringVar(&opts.ClusterId, "cluster-id", "", "the oke cluster id")
	flag.StringVar(&opts.ControllerClass, "controller-class", "oci.oraclecloud.com/native-ingress-controller", "the controller class name")
	flag.StringVar(&opts.AuthType, "authType", "instance", "The auth type to be used - supported values : user, instance(Default).")
	flag.StringVar(&opts.AuthSecretName, "auth-secret-name", "", "Secret name used for auth, cannot be empty if authType is user principal")
	flag.StringVar(&opts.MetricsBackend, "metrics-backend", "prometheus", "Backend used for metrics")
	flag.IntVar(&opts.MetricsPort, "metrics-port", 2223, "Metrics port for metrics backend")
	flag.BoolVar(&opts.UseLbCompartmentForCertificates, "use-lb-compartment-for-certificates", false,
		"use the compartment supplied in IngressClassParam.spec.compartmentId for certificate management")
	flag.BoolVar(&opts.EmitEvents, "emit-events", false, "emit kubernetes events for Ingress/IngressClass errors observed during reconciliation")
	flag.Int64Var(&opts.CertDeletionGracePeriodInDays, "cert-deletion-grace-period-in-days", 0,
		"number of days before an unused oci certificate service resource is deleted, if non-positive this cleanup is disabled")

	var logFile string
	flag.StringVar(&logFile, "log-file", "", "absolute path to the file where application logs will be stored")

	flag.Parse()
	common.EnableInstanceMetadataServiceLookup()

	if logFile == "" {
		klog.Error("unable to get log file path (missing log-file flag).")
	} else {
		f, e := os.Create(logFile)
		if e != nil {
			klog.Fatal(e)
		}
		f.Close()
	}

	if logFile != "" {
		flag.Set("logtostderr", "false")
		flag.Set("alsologtostderr", "true")
		flag.Set("log_file", logFile)
	}

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

	if opts.ClusterId == "" {
		klog.Fatal("unable to get cluster id (missing cluster-id flag).")
	}

	if opts.AuthType == "user" {
		if opts.AuthSecretName == "" {
			klog.Fatal("unable to get secret name (missing secret-name flag) since authType is user.")
		}
	}

	if opts.UseLbCompartmentForCertificates {
		klog.Info("use-lb-compartment-for-certificates flag set to true, will use LB compartment for certificate management")
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
	// use a Go context, so we can tell the leaderelection code when we
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

	informerFactory := informers.NewSharedInformerFactoryWithOptions(client, 1*time.Minute,
		informers.WithCustomResyncConfig(
			map[metav1.Object]time.Duration{
				&corev1.Secret{}: 0,
			},
		))

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

	ingressClassInformer, ingressInformer, serviceInformer, secretInformer, endpointInformer, podInformer, nodeInformer, serviceAccountInformer := setupInformers(informerFactory, ctx, classParamInformer)

	var eventRecorder events.EventRecorder
	if opts.EmitEvents {
		eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
		eventBroadcaster.StartStructuredLogging(5)
		eventBroadcaster.StartRecordingToSink(ctx.Done())
		eventRecorder = eventBroadcaster.NewRecorder(scheme.Scheme, opts.ControllerClass)
	}

	server.SetupWebhookServer(ingressInformer, serviceInformer, client, ctx)
	mux := http.NewServeMux()
	reg, err := server.SetupMetricsServer(opts.MetricsBackend, opts.MetricsPort, mux, ctx)

	run := server.SetUpControllers(opts, ingressClassInformer, ingressInformer, client, serviceInformer, secretInformer, endpointInformer, podInformer, nodeInformer, serviceAccountInformer, c, reg, eventRecorder)

	metric.ServeMetrics(opts.MetricsPort, mux)
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

func setupInformers(informerFactory informers.SharedInformerFactory, ctx context.Context, classParamInformer ctrcache.Informer) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, v1.ServiceInformer, v1.SecretInformer, v1.EndpointsInformer, v1.PodInformer, v1.NodeInformer, v1.ServiceAccountInformer) {
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	go ingressClassInformer.Informer().Run(ctx.Done())

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	go ingressInformer.Informer().Run(ctx.Done())

	serviceInformer := informerFactory.Core().V1().Services()
	go serviceInformer.Informer().Run(ctx.Done())

	secretInformer := informerFactory.Core().V1().Secrets()
	go secretInformer.Informer().Run(ctx.Done())

	endpointInformer := informerFactory.Core().V1().Endpoints()
	go endpointInformer.Informer().Run(ctx.Done())

	podInformer := informerFactory.Core().V1().Pods()
	go podInformer.Informer().Run(ctx.Done())

	nodeInformer := informerFactory.Core().V1().Nodes()
	go nodeInformer.Informer().Run(ctx.Done())

	serviceAccountInformer := informerFactory.Core().V1().ServiceAccounts()
	go serviceAccountInformer.Informer().Run(ctx.Done())

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())
	klog.Info("waiting on caches to sync")

	// wait for the initial synchronization of the local cache.
	if !cache.WaitForCacheSync(ctx.Done(),
		ingressClassInformer.Informer().HasSynced,
		ingressInformer.Informer().HasSynced,
		serviceInformer.Informer().HasSynced,
		secretInformer.Informer().HasSynced,
		endpointInformer.Informer().HasSynced,
		podInformer.Informer().HasSynced,
		classParamInformer.HasSynced,
		serviceAccountInformer.Informer().HasSynced,
		nodeInformer.Informer().HasSynced) {

		klog.Fatal("failed to sync informers")
	}

	// Mark caches as synced for health checks
	healthChecker := server.GetHealthChecker()
	healthChecker.SetCachesSynced(true)

	return ingressClassInformer, ingressInformer, serviceInformer, secretInformer, endpointInformer, podInformer, nodeInformer, serviceAccountInformer
}
