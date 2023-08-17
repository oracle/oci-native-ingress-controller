package client

import (
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/waf"
	"k8s.io/client-go/kubernetes"
)

type ClientProvider struct {
	kubernetesClient   kubernetes.Interface
	wafClient          *waf.Client
	lbClient           *loadbalancer.LoadBalancerClient
	certificatesClient *certificate.CertificatesClient
}

func NewWrapperClient(kubernetesClient kubernetes.Interface, wafClient *waf.Client, lbClient *loadbalancer.LoadBalancerClient, certificatesClient *certificate.CertificatesClient) *ClientProvider {
	return &ClientProvider{kubernetesClient: kubernetesClient, wafClient: wafClient, lbClient: lbClient, certificatesClient: certificatesClient}
}

func (c ClientProvider) GetK8Client() kubernetes.Interface {
	return c.kubernetesClient
}

func (c ClientProvider) GetWafClient() *waf.Client {
	return c.wafClient
}

func (c ClientProvider) GetLbClient() *loadbalancer.LoadBalancerClient {
	return c.lbClient
}

func (c ClientProvider) GetCertClient() *certificate.CertificatesClient {
	return c.certificatesClient
}
