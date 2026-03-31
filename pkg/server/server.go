/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package server

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"time"

	"github.com/oracle/oci-native-ingress-controller/pkg/task/certificatecleanup"

	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/containerengine"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/nodeBackend"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"

	"github.com/oracle/oci-native-ingress-controller/pkg/auth"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/backend"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/ingress"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/ingressclass"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/routingpolicy"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	"github.com/oracle/oci-native-ingress-controller/pkg/types"
	v1 "k8s.io/client-go/informers/core/v1"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/podreadiness"
)

func BuildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func SetUpControllers(opts types.IngressOpts, ingressClassInformer networkinginformers.IngressClassInformer,
	ingressInformer networkinginformers.IngressInformer, k8client kubernetes.Interface, serviceInformer v1.ServiceInformer, secretInformer v1.SecretInformer,
	endpointInformer v1.EndpointsInformer, podInformer v1.PodInformer, nodeInformer v1.NodeInformer, serviceAccountInformer v1.ServiceAccountInformer,
	c ctrcache.Cache, reg *prometheus.Registry, eventRecorder events.EventRecorder) func(ctx context.Context) {
	return func(ctx context.Context) {
		klog.Info("Controller loop...")

		client := setupClient(ctx, opts, k8client)

		wc, err := client.GetClient(client.DefaultConfigGetter)
		if err != nil {
			klog.Fatalf("failed to create client, %v", err)
		}

		cni, err := fetchCniType(opts.ClusterId, wc.GetContainerEngineClient())
		if err != nil {
			klog.Fatalf("failed to get cluster details: %v", err)
		}
		ingressController := ingress.NewController(
			opts.ControllerClass,
			opts.CompartmentId,
			ingressClassInformer,
			ingressInformer,
			serviceAccountInformer,
			serviceInformer.Lister(),
			secretInformer,
			client,
			reg,
			c,
			opts.UseLbCompartmentForCertificates,
			eventRecorder,
		)

		routingPolicyController := routingpolicy.NewController(
			opts.ControllerClass,
			ingressClassInformer,
			ingressInformer,
			serviceAccountInformer,
			serviceInformer.Lister(),
			client,
			eventRecorder,
		)

		ingressClassController := ingressclass.NewController(
			opts.CompartmentId,
			opts.SubnetId,
			opts.ControllerClass,
			ingressClassInformer,
			serviceAccountInformer,
			client,
			c,
			eventRecorder,
		)

		go ingressClassController.Run(3, ctx.Done())
		go ingressController.Run(3, ctx.Done())
		go routingPolicyController.Run(3, ctx.Done())

		klog.Infof("CNI Type of given cluster : %s", cni)
		if cni == string(containerengine.ClusterPodNetworkOptionDetailsCniTypeFlannelOverlay) {

			backendController := nodeBackend.NewController(
				opts.ControllerClass,
				ingressClassInformer,
				ingressInformer,
				serviceAccountInformer,
				serviceInformer.Lister(),
				endpointInformer.Lister(),
				podInformer.Lister(),
				nodeInformer.Lister(),
				client,
				eventRecorder,
			)
			go backendController.Run(3, ctx.Done())
		} else {
			backendController := backend.NewController(
				opts.ControllerClass,
				ingressClassInformer,
				ingressInformer,
				serviceAccountInformer,
				serviceInformer.Lister(),
				endpointInformer.Lister(),
				podInformer.Lister(),
				client,
				eventRecorder,
			)
			go backendController.Run(3, ctx.Done())
		}

		if opts.CertDeletionGracePeriodInDays > 0 {
			certificateCleanUpTask := certificatecleanup.NewTask(
				opts.ControllerClass,
				ingressClassInformer,
				serviceAccountInformer,
				opts.UseLbCompartmentForCertificates,
				opts.CompartmentId,
				time.Duration(opts.CertDeletionGracePeriodInDays)*(24*time.Hour),
				client,
				c,
			)
			go certificateCleanUpTask.Run(ctx.Done())
		}

		// Mark controllers as ready for health checks
		go func() {
			time.Sleep(2 * time.Second) // Give controllers a moment to start
			GetHealthChecker().SetControllersReady(true)
			klog.Info("Controllers marked as ready for health checks")
		}()
	}
}

func fetchCniType(id string, client *containerengine.ContainerEngineClient) (string, error) {
	resp := getCluster(id, client)
	return GetCniFromCluster(resp)
}

func GetCniFromCluster(resp containerengine.Cluster) (string, error) {
	cni := resp.ClusterPodNetworkOptions
	for _, n := range cni {
		switch n.(type) {
		case containerengine.OciVcnIpNativeClusterPodNetworkOptionDetails:
			return string(containerengine.ClusterPodNetworkOptionDetailsCniTypeOciVcnIpNative), nil
		default:
			return string(containerengine.ClusterPodNetworkOptionDetailsCniTypeFlannelOverlay), nil
		}
	}
	return string(containerengine.ClusterPodNetworkOptionDetailsCniTypeFlannelOverlay), nil
}

func getCluster(id string, client *containerengine.ContainerEngineClient) containerengine.Cluster {
	// Create a request and dependent object(s).
	req := containerengine.GetClusterRequest{ClusterId: common.String(id)}

	// Send the request using the service client
	resp, err := client.GetCluster(context.Background(), req)
	if err != nil {
		klog.Fatalf("failed to get cluster details: %v", err)
	}
	return resp.Cluster
}

func setupClient(ctx context.Context, opts types.IngressOpts, k8client clientset.Interface) *client.ClientProvider {
	configGetter := auth.DefaultOCIConfigGetter(ctx, opts, k8client)
	return client.New(k8client, configGetter)
}

func SetupWebhookServer(ingressInformer networkinginformers.IngressInformer, serviceInformer v1.ServiceInformer, client *clientset.Clientset, ctx context.Context) {
	klog.Info("setting up webhook server")

	server := &webhook.DefaultServer{
		Options: webhook.Options{
			TLSOpts: []func(*tls.Config){
				func(config *tls.Config) {
					config.MinVersion = tls.VersionTLS12
				},
			},
		},
	}
	server.Register("/mutate-v1-pod", &webhook.Admission{Handler: podreadiness.NewWebhook(ingressInformer.Lister(), serviceInformer.Lister(), client)})

	go func() {
		klog.Infof("starting webhook server...")
		err := server.Start(ctx)
		if err != nil {
			klog.Errorf("failed to run webhook server: %v", err)
			os.Exit(1)
		}
	}()
}

func SetupMetricsServer(metricsBackend string, metricsPort int, mux *http.ServeMux, ctx context.Context) (*prometheus.Registry, error) {
	// initialize metrics exporter before creating measurements
	reg, err := metric.InitMetricsExporter(metricsBackend)
	if err != nil {
		klog.Error("failed to initialize metrics exporter: %s", err.Error())
		return nil, err
	}
	metric.RegisterMetrics(reg, mux)

	// Register health check endpoints
	hc := GetHealthChecker()
	mux.HandleFunc("/healthz/ready", hc.HandleReadiness)
	mux.HandleFunc("/healthz/live", hc.HandleLiveness)

	return reg, nil
}
