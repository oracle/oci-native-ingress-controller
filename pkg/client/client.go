package client

import (
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/waf"
	"k8s.io/client-go/kubernetes"
)

type Client struct {
	kubernetesClient   kubernetes.Interface
	wafClient          *waf.Client
	lbClient           *loadbalancer.LoadBalancerClient
	certificatesClient *certificate.CertificatesClient
}

func NewWrapperClient(kubernetesClient kubernetes.Interface, wafClient *waf.Client, lbClient *loadbalancer.LoadBalancerClient, certificatesClient *certificate.CertificatesClient) *Client {
	return &Client{kubernetesClient: kubernetesClient, wafClient: wafClient, lbClient: lbClient, certificatesClient: certificatesClient}
}

func (c Client) GetK8Client() kubernetes.Interface {
	return c.kubernetesClient
}

func (c Client) GetWafClient() *waf.Client {
	return c.wafClient
}

func (c Client) GetLbClient() *loadbalancer.LoadBalancerClient {
	return c.lbClient
}

func (c Client) GetCertClient() *certificate.CertificatesClient {
	return c.certificatesClient
}
