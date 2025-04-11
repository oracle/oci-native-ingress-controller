package client

import (
	"context"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/containerengine"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	ociwaf "github.com/oracle/oci-go-sdk/v65/waf"
	"github.com/oracle/oci-native-ingress-controller/pkg/auth"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"github.com/oracle/oci-native-ingress-controller/pkg/waf"
	"k8s.io/api/networking/v1"
	v13 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	v12 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"os"
	"time"
)

const (
	clientCacheExpiryTime = time.Duration(6) * time.Hour
	okeHostOverrideEnvVar = "OKE_HOST_OVERRIDE"
)

type ClientProvider struct {
	K8sClient           kubernetes.Interface
	DefaultConfigGetter auth.ConfigGetter
	Cache               cache.Store
}

func New(k8sClient kubernetes.Interface, defaultConfigGetter auth.ConfigGetter) *ClientProvider {
	return &ClientProvider{
		k8sClient,
		defaultConfigGetter,
		cache.NewTTLStore(func(obj interface{}) (string, error) {
			client, ok := obj.(*WrapperClient)
			if !ok {
				return "", fmt.Errorf("unexpected object type: %T", obj)
			}
			return client.configGetter.GetKey(), nil
		}, clientCacheExpiryTime),
	}
}

func (client *ClientProvider) GetClient(getter auth.ConfigGetter) (*WrapperClient, error) {
	item, exists, err := client.Cache.GetByKey(getter.GetKey())
	if err != nil {
		return nil, err
	}
	if exists {
		return item.(*WrapperClient), nil
	}
	newWrapperClient, err := newWrapperClientFromConfig(getter, client.K8sClient)
	if err != nil {
		return nil, err
	}
	client.Cache.Add(newWrapperClient)
	return newWrapperClient, nil

}

type WrapperClient struct {
	configGetter          auth.ConfigGetter
	kubernetesClient      kubernetes.Interface
	wafClient             *waf.Client
	lbClient              *loadbalancer.LoadBalancerClient
	certificatesClient    *certificate.CertificatesClient
	containerEngineClient *containerengine.ContainerEngineClient
}

func newWrapperClientFromConfig(configGetter auth.ConfigGetter, k8sClient kubernetes.Interface) (*WrapperClient, error) {
	configProvider, err := configGetter.GetConfigurationProvider()
	if err != nil {
		return nil, err
	}
	ociLBClient, err := ociloadbalancer.NewLoadBalancerClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, err
	}

	ociCertificatesClient, err := certificates.NewCertificatesClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, err
	}

	ociCertificatesMgmtClient, err := certificatesmanagement.NewCertificatesManagementClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, err
	}

	ociWafClient, err := ociwaf.NewWafClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, err
	}

	containerEngineClient, err := containerengine.NewContainerEngineClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, err
	}

	return &WrapperClient{
		configGetter:          configGetter,
		kubernetesClient:      k8sClient,
		wafClient:             waf.New(&ociWafClient),
		lbClient:              loadbalancer.New(&ociLBClient),
		certificatesClient:    certificate.New(&ociCertificatesMgmtClient, ociclient.NewCertificateClient(&ociCertificatesClient)),
		containerEngineClient: &containerEngineClient,
	}, nil
}

func NewWrapperClient(kubernetesClient kubernetes.Interface, wafClient *waf.Client, lbClient *loadbalancer.LoadBalancerClient, certificatesClient *certificate.CertificatesClient, containerEngineClient *containerengine.ContainerEngineClient) *WrapperClient {
	return &WrapperClient{kubernetesClient: kubernetesClient, wafClient: wafClient, lbClient: lbClient, certificatesClient: certificatesClient, containerEngineClient: containerEngineClient}
}

func (c *WrapperClient) GetK8Client() kubernetes.Interface {
	return c.kubernetesClient
}

func (c *WrapperClient) GetWafClient() *waf.Client {
	return c.wafClient
}

func (c *WrapperClient) GetLbClient() *loadbalancer.LoadBalancerClient {
	return c.lbClient
}

func (c *WrapperClient) GetCertClient() *certificate.CertificatesClient {
	return c.certificatesClient
}

func (c *WrapperClient) GetContainerEngineClient() *containerengine.ContainerEngineClient {
	if os.Getenv(okeHostOverrideEnvVar) != "" {
		c.containerEngineClient.BaseClient.Host = os.Getenv(okeHostOverrideEnvVar)
	}
	return c.containerEngineClient
}

func GetClientContext(ingressClass *v1.IngressClass, saLister v12.ServiceAccountLister, provider *ClientProvider, namespace, name string) (context.Context, error) {
	saName, useWorkloadIdentity := "", false
	if ingressClass != nil {
		if annotations := ingressClass.Annotations; annotations != nil {
			saName, useWorkloadIdentity = annotations[util.IngressClassServiceAccountName]
		}
	}

	var ctx context.Context
	if useWorkloadIdentity {

		sa, err := provider.K8sClient.CoreV1().ServiceAccounts(*ingressClass.Spec.Parameters.Namespace).Get(context.TODO(), saName, v13.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to get service account", "serviceAccount", saName)
			return nil, err
		}

		parentRPTURL := ""
		if sa.Annotations != nil {
			parentRPTURL = sa.Annotations[util.ParentRPTURL]
		}

		klog.V(2).InfoS("Getting workloadIdentity configuration provider for a service account", "ingressClass", klog.KRef(namespace, name))
		wc, err := provider.GetClient(auth.NewWorkloadIdentityConfigGetter(auth.NewServiceAccountTokenProvider(
			saName, *ingressClass.Spec.Parameters.Namespace, saLister, provider.K8sClient), parentRPTURL))
		if err != nil {
			klog.ErrorS(err, "Failed to create client", "ingressClass", klog.KRef(namespace, name))
			return nil, err
		}
		ctx = context.WithValue(context.Background(), util.WrapperClient, wc)

	} else {
		wc, err := provider.GetClient(provider.DefaultConfigGetter)
		if err != nil {
			klog.ErrorS(err, "Failed to create client", "ingressClass", klog.KRef(namespace, name))
			return nil, err
		}
		ctx = context.WithValue(context.Background(), util.WrapperClient, wc)
	}
	return ctx, nil
}
