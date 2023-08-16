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
	"net/http"
	"os"

	ctrcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ociwaf "github.com/oracle/oci-go-sdk/v65/waf"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"k8s.io/client-go/kubernetes"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/oracle/oci-native-ingress-controller/pkg/auth"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/backend"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/ingress"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/ingressclass"
	"github.com/oracle/oci-native-ingress-controller/pkg/controllers/routingpolicy"
	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	. "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/types"
	w "github.com/oracle/oci-native-ingress-controller/pkg/waf"
	"github.com/prometheus/client_golang/prometheus"

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
	ingressInformer networkinginformers.IngressInformer, k8client kubernetes.Interface,
	serviceInformer v1.ServiceInformer, endpointInformer v1.EndpointsInformer, podInformer v1.PodInformer, c ctrcache.Cache,
	reg *prometheus.Registry) func(ctx context.Context) {
	return func(ctx context.Context) {
		klog.Info("Controller loop...")

		client := setupClient(ctx, opts, k8client)

		ingressController := ingress.NewController(
			opts.ControllerClass,
			opts.CompartmentId,
			ingressClassInformer,
			ingressInformer,
			serviceInformer.Lister(),
			client,
			reg,
		)

		routingPolicyController := routingpolicy.NewController(
			opts.ControllerClass,
			ingressClassInformer,
			ingressInformer,
			serviceInformer.Lister(),
			client,
		)

		backendController := backend.NewController(
			opts.ControllerClass,
			ingressClassInformer,
			ingressInformer,
			serviceInformer.Lister(),
			endpointInformer.Lister(),
			podInformer.Lister(),
			client,
		)

		ingressClassController := ingressclass.NewController(
			opts.CompartmentId,
			opts.SubnetId,
			opts.ControllerClass,
			ingressClassInformer,
			client,
			c,
		)

		go ingressClassController.Run(3, ctx.Done())
		go ingressController.Run(3, ctx.Done())
		go routingPolicyController.Run(3, ctx.Done())
		go backendController.Run(3, ctx.Done())
	}
}

func setupClient(ctx context.Context, opts types.IngressOpts, k8client clientset.Interface) *client.Client {
	configProvider, err := auth.GetConfigurationProvider(ctx, opts, k8client)
	if err != nil {
		klog.Fatalf("failed to load authentication configuration provider: %v", err)
	}

	ociLBClient, err := ociloadbalancer.NewLoadBalancerClientWithConfigurationProvider(configProvider)
	if err != nil {
		klog.Fatalf("unable to construct oci load balancer client: %v", err)
	}

	ociCertificatesClient, err := certificates.NewCertificatesClientWithConfigurationProvider(configProvider)
	if err != nil {
		klog.Fatalf("unable to construct oci certificate client: %v", err)
	}

	ociCertificatesMgmtClient, err := certificatesmanagement.NewCertificatesManagementClientWithConfigurationProvider(configProvider)
	if err != nil {
		klog.Fatalf("unable to construct oci certificate management client: %v", err)
	}

	ociWafClient, err := ociwaf.NewWafClientWithConfigurationProvider(configProvider)
	if err != nil {
		klog.Fatalf("unable to construct oci web application firewall client: %v", err)
	}

	lbClient := loadbalancer.New(&ociLBClient)

	certificatesClient := certificate.New(&ociCertificatesMgmtClient, NewCertificateClient(&ociCertificatesClient))

	wafClient := w.New(&ociWafClient)

	return client.NewWrapperClient(k8client, wafClient, lbClient, certificatesClient)
}

func SetupWebhookServer(ingressInformer networkinginformers.IngressInformer, serviceInformer v1.ServiceInformer, client *clientset.Clientset, ctx context.Context) {
	klog.Info("setting up webhook server")

	server := &webhook.Server{}
	server.Register("/mutate-v1-pod", &webhook.Admission{Handler: podreadiness.NewWebhook(ingressInformer.Lister(), serviceInformer.Lister(), client)})

	go func() {
		klog.Infof("starting webhook server...")
		err := server.StartStandalone(ctx, nil)
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

	return reg, nil
}
