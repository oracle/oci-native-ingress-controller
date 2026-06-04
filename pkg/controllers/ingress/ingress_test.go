package ingress

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/exception"
	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/state"
	"github.com/oracle/oci-native-ingress-controller/pkg/tlspolicy"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/events"

	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	ingressPath              = "ingressPath.yaml"
	ingressPathWithFinalizer = "ingressPathWithFinalizer.yaml"
	ingressPathWithTlsSecret = "ingressPathWithTlsSecret.yaml"
	multiCertificateWarning  = "OCI Load Balancer multi-certificate listeners may not be supported in this tenancy or region; verify enablement or use one certificate"
)

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, coreinformers.ServiceAccountInformer, corelisters.ServiceLister, coreinformers.SecretInformer, *fakeclientset.Clientset) {
	return setUpWithSecrets(ctx, ingressClassList, ingressList, testService, &v1.SecretList{})
}

func setUpWithSecrets(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList, secretList *v1.SecretList) (networkinginformers.IngressClassInformer, networkinginformers.IngressInformer, coreinformers.ServiceAccountInformer, corelisters.ServiceLister, coreinformers.SecretInformer, *fakeclientset.Clientset) {
	fakeClient := fakeclientset.NewSimpleClientset()
	for i := range secretList.Items {
		secret := secretList.Items[i]
		_, _ = fakeClient.CoreV1().Secrets(secret.Namespace).Create(ctx, &secret, metav1.CreateOptions{})
	}
	action := "list"

	util.UpdateFakeClientCall(fakeClient, action, "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(fakeClient, action, "ingresses", ingressList)
	util.UpdateFakeClientCall(fakeClient, "get", "ingresses", &ingressList.Items[0])
	util.UpdateFakeClientCall(fakeClient, "update", "ingresses", &ingressList.Items[0])
	util.UpdateFakeClientCall(fakeClient, "patch", "ingresses", &ingressList.Items[0])
	util.UpdateFakeClientCall(fakeClient, action, "services", testService)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	secretInformer := informerFactory.Core().V1().Secrets()
	secretInformer.Lister()

	saInformer := informerFactory.Core().V1().ServiceAccounts()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), secretInformer.Informer().HasSynced)
	return ingressClassInformer, ingressInformer, saInformer, serviceLister, secretInformer, fakeClient
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList) *Controller {

	testService := util.GetServiceListResource(namespace, "testecho1", 80)
	lbClient := GetLoadBalancerClient()
	certClient := GetCertClient()
	certManageClient := GetCertManageClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	certificatesClient := &certificate.CertificatesClient{
		ManagementClient:   certManageClient,
		CertificatesClient: certClient,
		CertCache:          map[string]*ociclient.CertCacheObj{},
		CaBundleCache:      map[string]*ociclient.CaBundleCacheObj{},
	}

	ingressClassInformer, ingressInformer, saInformer, serviceLister, secretInformer, k8client := setUp(ctx, ingressClassList, ingressList, testService)
	wrapperClient := client.NewWrapperClient(k8client, nil, loadBalancerClient, nil, certificatesClient, nil)
	fakeClient := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	fakeRecorder := events.NewFakeRecorder(10)
	c := NewController("oci.oraclecloud.com/native-ingress-controller", "", ingressClassInformer,
		ingressInformer, saInformer, serviceLister, nil, nil, nil, secretInformer, fakeClient, nil, nil, false, fakeRecorder)
	return c
}

// Helper to build controller with a custom LB client to simulate edge cases
func initsWithCustomLB(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, lbIface ociclient.LoadBalancerInterface) *Controller {
	testService := util.GetServiceListResource(namespace, "testecho1", 80)
	return initsWithCustomLBAndServices(ctx, ingressClassList, ingressList, testService, lbIface)
}

func initsWithCustomLBAndServices(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList, lbIface ociclient.LoadBalancerInterface) *Controller {
	return initsWithCustomLBAndServicesAndSecrets(ctx, ingressClassList, ingressList, testService, &v1.SecretList{}, lbIface)
}

func initsWithCustomLBAndServicesAndSecrets(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList, secretList *v1.SecretList, lbIface ociclient.LoadBalancerInterface) *Controller {
	return initsWithCustomLBAndServicesSecretsAndCertManager(ctx, ingressClassList, ingressList, testService, secretList, lbIface, GetCertManageClient())
}

func initsWithCustomLBAndServicesSecretsAndCertManager(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList, secretList *v1.SecretList, lbIface ociclient.LoadBalancerInterface, certManageClient ociclient.CertificateManagementInterface) *Controller {
	certClient := GetCertClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbIface,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	certificatesClient := &certificate.CertificatesClient{
		ManagementClient:   certManageClient,
		CertificatesClient: certClient,
		CertCache:          map[string]*ociclient.CertCacheObj{},
		CaBundleCache:      map[string]*ociclient.CaBundleCacheObj{},
	}

	ingressClassInformer, ingressInformer, saInformer, serviceLister, secretInformer, k8client := setUpWithSecrets(ctx, ingressClassList, ingressList, testService, secretList)
	wrapperClient := client.NewWrapperClient(k8client, nil, loadBalancerClient, nil, certificatesClient, nil)
	fakeClient := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	fakeRecorder := events.NewFakeRecorder(10)
	return NewController("oci.oraclecloud.com/native-ingress-controller", "", ingressClassInformer,
		ingressInformer, saInformer, serviceLister, nil, nil, nil, secretInformer, fakeClient, nil, nil, false, fakeRecorder)
}

func TestGetInitialBackendsByBackendSetUsesEndpointBackends(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	c.serviceLister = getServiceLister(util.GetServiceListResource("default", "testecho1", 80))
	c.endpointLister = util.GetEndpointsListerResource(util.GetEndpointsResourceList("testecho1", "default", false))

	backendsByBackendSet, err := c.getInitialBackendsByBackendSet(&ingressList.Items[0])

	Expect(err).To(BeNil())
	backendSetName := util.GenerateBackendSetName("default", "testecho1", 80)
	Expect(backendsByBackendSet[backendSetName]).To(HaveLen(1))
	Expect(*backendsByBackendSet[backendSetName][0].IpAddress).To(Equal("6.7.8.9"))
}

func getServiceLister(services *v1.ServiceList) corelisters.ServiceLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for i := range services.Items {
		_ = indexer.Add(&services.Items[i])
	}
	return corelisters.NewServiceLister(indexer)
}

func drainControllerQueue(c *Controller, initialIngressCount int) {
	Eventually(func() int {
		return c.queue.Len()
	}).Should(Equal(initialIngressCount))
	for c.queue.Len() > 0 {
		item, shutdown := c.queue.Get()
		if shutdown {
			return
		}
		c.queue.Done(item)
	}
}

// Mock that returns a listener with nil Protocol
type MockLoadBalancerClientNilProtocol struct{ MockLoadBalancerClient }

func (m MockLoadBalancerClientNilProtocol) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	for k, l := range res.LoadBalancer.Listeners {
		lCopy := l
		lCopy.Protocol = nil
		res.LoadBalancer.Listeners[k] = lCopy
		break
	}
	return res, nil
}

// Mock that returns a listener with nil DefaultBackendSetName
type MockLoadBalancerClientNilDefaultBS struct{ MockLoadBalancerClient }

func (m MockLoadBalancerClientNilDefaultBS) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	for k, l := range res.LoadBalancer.Listeners {
		lCopy := l
		lCopy.DefaultBackendSetName = nil
		res.LoadBalancer.Listeners[k] = lCopy
		break
	}
	return res, nil
}

func TestEnsureIngress_NilListenerProtocol_DoesNotPanic(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)

	c := initsWithCustomLB(ctx, ingressClassList, ingressList, MockLoadBalancerClientNilProtocol{})
	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
}

func TestEnsureIngress_NilDefaultBackendSet_DoesNotPanic(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)

	c := initsWithCustomLB(ctx, ingressClassList, ingressList, MockLoadBalancerClientNilDefaultBS{})
	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
}

func TestSync(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.sync("default/ingress-readiness")

	Expect(err == nil).Should(Equal(false))
	Expect(err.Error()).Should(Equal("ingress class not ready"))
}

func TestEnsureIngressSuccess(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err == nil).Should(Equal(true))
}

func TestEnsureIngress_CreateListenerWithMultipleCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(1))
	Expect(capturingLB.CreateListenerRequests[0].CreateListenerDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
	Expect(*capturingLB.CreateListenerRequests[0].CreateListenerDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.Listener.CipherSuiteName))
	Expect(capturingLB.CreateListenerRequests[0].CreateListenerDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.Listener.Protocols))
	expectBackendSetRequestsWithoutSSLConfig(capturingLB)
}

func TestEnsureIngress_CreateListenerWithSingleCertificateSetsLockedDefaultPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(1))
	sslConfig := capturingLB.CreateListenerRequests[0].CreateListenerDetails.SslConfiguration
	Expect(sslConfig.CertificateIds).Should(Equal([]string{"certA"}))
	Expect(*sslConfig.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.Listener.CipherSuiteName))
	Expect(sslConfig.Protocols).Should(Equal(LockedDefaultTLSPolicy.Listener.Protocols))
}

func TestEnsureIngress_CreateListenerWithProtocolsOnlyPolicyAnnotationUsesProtocolCompatibleDefault(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{"protocols":["TLSv1.3"]}`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(1))
	sslConfig := capturingLB.CreateListenerRequests[0].CreateListenerDetails.SslConfiguration
	Expect(sslConfig.CertificateIds).Should(Equal([]string{"certA"}))
	Expect(*sslConfig.CipherSuiteName).Should(Equal(listenerTLS13CipherSuite))
	Expect(sslConfig.Protocols).Should(Equal([]string{"TLSv1.3"}))
}

func TestEnsureIngress_InvalidActiveListenerPolicyFailsBeforeOCIMutationsAndPublishesWarning(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{"protocols":["TLSv1.1"]}`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 80,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyInvalidAnnotation"))
	Expect(err.Error()).To(ContainSubstring("listener 80"))
	Expect(err.Error()).To(ContainSubstring(util.IngressListenerSslConfigAnnotation))
	expectNoCapturedOCIMutations(capturingLB)

	fakeRecorder, ok := c.eventRecorder.(*events.FakeRecorder)
	Expect(ok).To(BeTrue())
	c.handleErr(err, fmt.Sprintf("%s/%s", namespace, ingressList.Items[0].Name))
	select {
	case event := <-fakeRecorder.Events:
		Expect(event).To(ContainSubstring(tlspolicy.InvalidAnnotationReason))
		Expect(event).To(ContainSubstring("listener 80"))
		Expect(event).To(ContainSubstring(util.IngressListenerSslConfigAnnotation))
	case <-time.After(2 * time.Second):
		t.Fatalf("expected warning event for invalid listener TLS policy annotation")
	}
}

func TestEnsureIngress_InvalidHTTP2ListenerPolicyFailsBeforeBackendSetMutation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolHTTP2
	ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{"protocols":["TLSv1.1"]}`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{certificateList},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring(tlspolicy.InvalidAnnotationReason))
	Expect(err.Error()).To(ContainSubstring("listener 80"))
	Expect(err.Error()).To(ContainSubstring(util.IngressListenerSslConfigAnnotation))
	expectNoCapturedOCIMutations(capturingLB)
}

func TestEnsureIngress_InvalidSecretBackedHTTP2OrGRPCListenerPolicyFailsBeforeCertificateWrites(t *testing.T) {
	for _, protocol := range []string{util.ProtocolHTTP2, util.ProtocolGRPC} {
		t.Run(protocol, func(t *testing.T) {
			RegisterTestingT(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			secretName := "listener-secret"
			ingressClassList := util.GetIngressClassListWithLBSet("id")
			ingressList := &networkingv1.IngressList{
				Items: []networkingv1.Ingress{
					getIngressForServiceWithTLSSecret(namespace, "ingress-secret", "testecho1", secretName),
				},
			}
			ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = protocol
			ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{"protocols":["TLSv1.1"]}`
			testServices := util.GetServiceListResource(namespace, "testecho1", 80)
			secretList := &v1.SecretList{
				Items: []v1.Secret{
					*util.GetSampleCertSecret(namespace, secretName, "ca-chain", "server-cert", "private-key"),
				},
			}
			capturingLB := &CapturingLoadBalancerClient{
				ListenerPort:           80,
				ExistingCertificateIDs: []string{"id"},
			}
			capturingCertManager := &CapturingCertificateManagerClient{}
			c := initsWithCustomLBAndServicesSecretsAndCertManager(ctx, ingressClassList, ingressList, testServices, secretList, capturingLB, capturingCertManager)

			err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(tlspolicy.InvalidAnnotationReason))
			Expect(err.Error()).To(ContainSubstring("listener 80"))
			Expect(err.Error()).To(ContainSubstring(util.IngressListenerSslConfigAnnotation))
			Expect(capturingCertManager.CreateCertificateRequests).To(BeEmpty())
			Expect(capturingCertManager.UpdateCertificateRequests).To(BeEmpty())
			expectNoCapturedOCIMutations(capturingLB)
		})
	}
}

func TestEnsureIngress_InvalidActiveBackendSetPolicyFailsBeforeOCIMutations(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressBackendSetSslConfigAnnotation] = `{"protocols":["TLSv1.1"]}`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 80,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyInvalidAnnotation"))
	Expect(err.Error()).To(ContainSubstring("backend set " + util.GenerateBackendSetName(namespace, "testecho1", 80)))
	Expect(err.Error()).To(ContainSubstring(util.IngressBackendSetSslConfigAnnotation))
	expectNoCapturedOCIMutations(capturingLB)
}

func TestEnsureIngress_InvalidListenerPolicyAnnotationInertWithoutListenerTLSInputs(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("")
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{not-json`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 80,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(0))
	expectBackendSetRequestsWithoutSSLConfig(capturingLB)
}

func TestEnsureIngress_CreatePathSyncsSharedListenerFoundAfterRefresh(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressA := getIngressForService("ingress-a", "testecho1", "certA")
	ingressB := getIngressForService("ingress-b", "testecho2", "certB")
	ingressA.Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	ingressB.Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ingressA, ingressB}}
	testServices := util.GetServiceListResource(namespace, "testecho1", 80)
	testServices.Items = append(testServices.Items, util.GetServiceListResource(namespace, "testecho2", 80).Items...)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:             80,
		ListenerAppearsOnGetCall: 2,
		ExistingCertificateIDs:   []string{"certA"},
	}
	c := initsWithCustomLBAndServices(ctx, ingressClassList, ingressList, testServices, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[1], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CipherSuiteName).Should(BeNil())
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.Protocols).Should(BeEmpty())
}

func TestEnsureIngress_CreateListenerAlreadyExistsRetryThenUpdatesSharedListener(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:             80,
		ListenerAppearsOnGetCall: 100,
		ExistingCertificateIDs:   []string{"certA"},
		CreateListenerErr:        listenerAlreadyExistsServiceError{},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(exception.HasTransientError(err)).To(BeTrue())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))

	capturingLB.CreateListenerErr = nil
	capturingLB.ListenerAppearsOnGetCall = 0
	err = c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
}

func TestEnsureIngress_UpdateListenerSingleToMultiCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CipherSuiteName).Should(BeNil())
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.Protocols).Should(BeEmpty())
	expectBackendSetRequestsWithoutSSLConfig(capturingLB)
}

func TestEnsureIngress_UpdateListenerWithoutAnnotationPreservesExistingPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"old-cert"},
		ExistingCipherSuiteName: "existing-listener-cipher",
		ExistingTLSProtocols:    []string{"TLSv1.2"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	sslConfig := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration
	Expect(sslConfig.CertificateIds).Should(Equal([]string{"certA"}))
	Expect(*sslConfig.CipherSuiteName).Should(Equal("existing-listener-cipher"))
	Expect(sslConfig.Protocols).Should(Equal([]string{"TLSv1.2"}))
}

func TestEnsureIngress_UpdateListenerMultiToSingleCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA"}))
	Expect(*capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.Listener.CipherSuiteName))
	Expect(capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.Listener.Protocols))
}

func TestEnsureIngress_UpdateListenerNoopWhenCertificateIdsMatch(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
}

func TestEnsureIngress_UpdateListenerNoopWhenMultiCertProtocolsReordered(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    []string{"TLSv1.3", "TLSv1.2"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
}

func TestEnsureIngress_UpdateListenerPreservesMissingPolicyWhenCertificatesMatch(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA", "certB"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
}

func TestEnsureIngress_UpdateHTTP2ListenerWithMultipleCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolHTTP2
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	updateDetails := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails
	Expect(*updateDetails.Protocol).Should(Equal(util.ProtocolHTTP2))
	Expect(updateDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
	Expect(updateDetails.SslConfiguration.CipherSuiteName).Should(BeNil())
	Expect(updateDetails.SslConfiguration.Protocols).Should(BeEmpty())
}

func TestEnsureIngress_UpdateGRPCListener(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolGRPC
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingProtocol:       util.ProtocolHTTP,
		ExistingCertificateIDs: []string{"certA"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	updateDetails := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails
	Expect(*updateDetails.Protocol).Should(Equal(util.ProtocolGRPC))
	Expect(updateDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA"}))
	Expect(updateDetails.SslConfiguration.CipherSuiteName).Should(BeNil())
	Expect(updateDetails.SslConfiguration.Protocols).Should(BeEmpty())
}

func TestEnsureIngress_UpdateHTTP2OrGRPCListenerTransitionWithoutAnnotationDefaultsOnlyForManagedPolicy(t *testing.T) {
	tests := []struct {
		name             string
		targetProtocol   string
		currentCipher    string
		currentProtocols []string
		expectedCipher   string
		expectedProto    []string
	}{
		{
			name:             "HTTP2 managed default",
			targetProtocol:   util.ProtocolHTTP2,
			currentCipher:    LockedDefaultTLSPolicy.Listener.CipherSuiteName,
			currentProtocols: LockedDefaultTLSPolicy.Listener.Protocols,
			expectedCipher:   LockedDefaultTLSPolicy.HTTP2Listener.CipherSuiteName,
			expectedProto:    LockedDefaultTLSPolicy.HTTP2Listener.Protocols,
		},
		{
			name:             "HTTP2 custom policy preserved",
			targetProtocol:   util.ProtocolHTTP2,
			currentCipher:    "existing-custom-cipher",
			currentProtocols: []string{"TLSv1.2"},
			expectedCipher:   "existing-custom-cipher",
			expectedProto:    []string{"TLSv1.2"},
		},
		{
			name:             "GRPC custom policy preserved",
			targetProtocol:   util.ProtocolGRPC,
			currentCipher:    "existing-custom-cipher",
			currentProtocols: []string{"TLSv1.2"},
			expectedCipher:   "existing-custom-cipher",
			expectedProto:    []string{"TLSv1.2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterTestingT(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ingressClassList := util.GetIngressClassListWithLBSet("id")
			ingressList := getIngressListWithDirectCertificates("certA")
			ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = tt.targetProtocol
			capturingLB := &CapturingLoadBalancerClient{
				ListenerPort:            80,
				ExistingProtocol:        util.ProtocolHTTP,
				ExistingCertificateIDs:  []string{"certA"},
				ExistingCipherSuiteName: tt.currentCipher,
				ExistingTLSProtocols:    tt.currentProtocols,
			}
			c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

			err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
			updateDetails := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails
			Expect(*updateDetails.Protocol).Should(Equal(tt.targetProtocol))
			Expect(updateDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA"}))
			Expect(*updateDetails.SslConfiguration.CipherSuiteName).Should(Equal(tt.expectedCipher))
			Expect(updateDetails.SslConfiguration.Protocols).Should(Equal(tt.expectedProto))
		})
	}
}

func TestEnsureIngress_UpdateHTTP2OrGRPCListenerTransitionAppliesExplicitPolicyFields(t *testing.T) {
	tests := []struct {
		name           string
		targetProtocol string
		annotation     string
		expectedCipher string
		expectedProto  []string
	}{
		{
			name:           "cipher only",
			targetProtocol: util.ProtocolHTTP2,
			annotation:     `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3"}`,
			expectedCipher: "oci-tls-12-13-ssl-cipher-suite-v3",
			expectedProto:  LockedDefaultTLSPolicy.HTTP2Listener.Protocols,
		},
		{
			name:           "protocols only TLS 1.3",
			targetProtocol: util.ProtocolHTTP2,
			annotation:     `{"protocols":["TLSv1.3"]}`,
			expectedCipher: http2ListenerTLS13CipherSuite,
			expectedProto:  []string{"TLSv1.3"},
		},
		{
			name:           "protocols only TLS 1.2",
			targetProtocol: util.ProtocolHTTP2,
			annotation:     `{"protocols":["TLSv1.2"]}`,
			expectedCipher: http2ListenerTLS12CipherSuite,
			expectedProto:  []string{"TLSv1.2"},
		},
		{
			name:           "both fields",
			targetProtocol: util.ProtocolHTTP2,
			annotation:     `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3","protocols":["TLSv1.2"]}`,
			expectedCipher: "oci-tls-12-13-ssl-cipher-suite-v3",
			expectedProto:  []string{"TLSv1.2"},
		},
		{
			name:           "grpc cipher only",
			targetProtocol: util.ProtocolGRPC,
			annotation:     `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3"}`,
			expectedCipher: "oci-tls-12-13-ssl-cipher-suite-v3",
			expectedProto:  LockedDefaultTLSPolicy.HTTP2Listener.Protocols,
		},
		{
			name:           "grpc protocols only TLS 1.3",
			targetProtocol: util.ProtocolGRPC,
			annotation:     `{"protocols":["TLSv1.3"]}`,
			expectedCipher: http2ListenerTLS13CipherSuite,
			expectedProto:  []string{"TLSv1.3"},
		},
		{
			name:           "grpc protocols only TLS 1.2",
			targetProtocol: util.ProtocolGRPC,
			annotation:     `{"protocols":["TLSv1.2"]}`,
			expectedCipher: http2ListenerTLS12CipherSuite,
			expectedProto:  []string{"TLSv1.2"},
		},
		{
			name:           "grpc both fields",
			targetProtocol: util.ProtocolGRPC,
			annotation:     `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3","protocols":["TLSv1.2"]}`,
			expectedCipher: "oci-tls-12-13-ssl-cipher-suite-v3",
			expectedProto:  []string{"TLSv1.2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterTestingT(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ingressClassList := util.GetIngressClassListWithLBSet("id")
			ingressList := getIngressListWithDirectCertificates("certA")
			ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = tt.targetProtocol
			ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = tt.annotation
			capturingLB := &CapturingLoadBalancerClient{
				ListenerPort:            80,
				ExistingProtocol:        util.ProtocolHTTP,
				ExistingCertificateIDs:  []string{"certA"},
				ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
				ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
			}
			c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

			err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
			updateDetails := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails
			Expect(*updateDetails.Protocol).Should(Equal(tt.targetProtocol))
			Expect(updateDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA"}))
			Expect(*updateDetails.SslConfiguration.CipherSuiteName).Should(Equal(tt.expectedCipher))
			Expect(updateDetails.SslConfiguration.Protocols).Should(Equal(tt.expectedProto))
		})
	}
}

func TestEnsureIngress_UpdateHTTP2OrGRPCListenerTransitionInvalidExplicitPolicyFailsBeforeOCIMutations(t *testing.T) {
	for _, protocol := range []string{util.ProtocolHTTP2, util.ProtocolGRPC} {
		t.Run(protocol, func(t *testing.T) {
			RegisterTestingT(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ingressClassList := util.GetIngressClassListWithLBSet("id")
			ingressList := getIngressListWithDirectCertificates("certA")
			ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = protocol
			ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{"protocols":["TLSv1.1"]}`
			capturingLB := &CapturingLoadBalancerClient{
				ListenerPort:           80,
				ExistingProtocol:       util.ProtocolHTTP,
				ExistingCertificateIDs: []string{"certA"},
			}
			c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

			err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("TLSPolicyInvalidAnnotation"))
			Expect(err.Error()).To(ContainSubstring("listener 80"))
			expectNoCapturedOCIMutations(capturingLB)
		})
	}
}

func TestEnsureIngress_UpdateHTTP2OrGRPCListenerBackToHTTPUsesNormalDefaultOnlyForManagedPolicy(t *testing.T) {
	tests := []struct {
		name             string
		currentProtocol  string
		currentCipher    string
		currentProtocols []string
		expectedCipher   string
		expectedProto    []string
	}{
		{
			name:             "HTTP2 managed default",
			currentProtocol:  util.ProtocolHTTP2,
			currentCipher:    LockedDefaultTLSPolicy.HTTP2Listener.CipherSuiteName,
			currentProtocols: LockedDefaultTLSPolicy.HTTP2Listener.Protocols,
			expectedCipher:   LockedDefaultTLSPolicy.Listener.CipherSuiteName,
			expectedProto:    LockedDefaultTLSPolicy.Listener.Protocols,
		},
		{
			name:             "GRPC managed default",
			currentProtocol:  util.ProtocolGRPC,
			currentCipher:    LockedDefaultTLSPolicy.HTTP2Listener.CipherSuiteName,
			currentProtocols: LockedDefaultTLSPolicy.HTTP2Listener.Protocols,
			expectedCipher:   LockedDefaultTLSPolicy.Listener.CipherSuiteName,
			expectedProto:    LockedDefaultTLSPolicy.Listener.Protocols,
		},
		{
			name:             "custom policy preserved",
			currentProtocol:  util.ProtocolHTTP2,
			currentCipher:    "existing-custom-cipher",
			currentProtocols: []string{"TLSv1.2"},
			expectedCipher:   "existing-custom-cipher",
			expectedProto:    []string{"TLSv1.2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterTestingT(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ingressClassList := util.GetIngressClassListWithLBSet("id")
			ingressList := getIngressListWithDirectCertificates("certA")
			capturingLB := &CapturingLoadBalancerClient{
				ListenerPort:            80,
				ExistingProtocol:        tt.currentProtocol,
				ExistingCertificateIDs:  []string{"certA"},
				ExistingCipherSuiteName: tt.currentCipher,
				ExistingTLSProtocols:    tt.currentProtocols,
			}
			c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

			err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
			updateDetails := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails
			Expect(*updateDetails.Protocol).Should(Equal(util.ProtocolHTTP))
			Expect(updateDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA"}))
			Expect(*updateDetails.SslConfiguration.CipherSuiteName).Should(Equal(tt.expectedCipher))
			Expect(updateDetails.SslConfiguration.Protocols).Should(Equal(tt.expectedProto))
		})
	}
}

func TestEnsureIngress_UnchangedHTTP2ListenerProtocolWithoutAnnotationPreservesExistingPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolHTTP2
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingProtocol:        util.ProtocolHTTP2,
		ExistingCertificateIDs:  []string{"certA"},
		ExistingCipherSuiteName: "existing-http2-cipher",
		ExistingTLSProtocols:    []string{"TLSv1.2"},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	updateDetails := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails
	Expect(*updateDetails.Protocol).Should(Equal(util.ProtocolHTTP2))
	Expect(updateDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
	Expect(*updateDetails.SslConfiguration.CipherSuiteName).Should(Equal("existing-http2-cipher"))
	Expect(updateDetails.SslConfiguration.Protocols).Should(Equal([]string{"TLSv1.2"}))
}

func TestEnsureIngress_HTTP2ListenerNoopWhenMultiCertPolicyMatches(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolHTTP2
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingProtocol:        util.ProtocolHTTP2,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(0))
}

func TestEnsureIngress_StagesManagedBackendSetsAcrossIngressClass(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	ingressList.Items = append(ingressList.Items, getIngressForService("ingress-other", "testecho2", certificateList))

	testServices := util.GetServiceListResource(namespace, "testecho1", 80)
	testServices.Items = append(testServices.Items, util.GetServiceListResource(namespace, "testecho2", 80).Items...)

	otherBackendSetName := util.GenerateBackendSetName(namespace, "testecho2", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:  []string{otherBackendSetName},
	}
	c := initsWithCustomLBAndServices(ctx, ingressClassList, ingressList, testServices, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	updatedBackendSets := sets.NewString()
	for _, request := range capturingLB.UpdateBackendSetRequests {
		updatedBackendSets.Insert(*request.BackendSetName)
	}
	Expect(updatedBackendSets.Has(otherBackendSetName)).To(BeTrue())
}

func TestEnsureIngress_StagesClassWideBackendSetsWithOwnerNamespaceTLSSecret(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ownerNamespace := "owner-ns"
	secretName := "shared-backend-secret"
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items = append(ingressList.Items,
		getIngressForServiceWithTLSSecret(ownerNamespace, "ingress-owner-update", "testecho2", secretName),
		getIngressForServiceWithTLSSecret(ownerNamespace, "ingress-owner-create", "testecho3", secretName),
	)

	testServices := util.GetServiceListResource(namespace, "testecho1", 80)
	testServices.Items = append(testServices.Items, util.GetServiceListResource(ownerNamespace, "testecho2", 80).Items...)
	testServices.Items = append(testServices.Items, util.GetServiceListResource(ownerNamespace, "testecho3", 80).Items...)
	secretList := &v1.SecretList{
		Items: []v1.Secret{
			*util.GetSampleCertSecret(ownerNamespace, secretName, "owner-ca-chain", "owner-cert", "owner-key"),
		},
	}

	updateBackendSetName := util.GenerateBackendSetName(ownerNamespace, "testecho2", 80)
	createBackendSetName := util.GenerateBackendSetName(ownerNamespace, "testecho3", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA"},
		AdditionalBackendSets:  []string{updateBackendSetName},
	}
	c := initsWithCustomLBAndServicesAndSecrets(ctx, ingressClassList, ingressList, testServices, secretList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	updatedBackendSets := sets.NewString()
	for _, request := range capturingLB.UpdateBackendSetRequests {
		updatedBackendSets.Insert(*request.BackendSetName)
		if *request.BackendSetName == updateBackendSetName {
			Expect(request.UpdateBackendSetDetails.SslConfiguration).NotTo(BeNil())
			Expect(*request.UpdateBackendSetDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.BackendSet.CipherSuiteName))
			Expect(request.UpdateBackendSetDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.BackendSet.Protocols))
		}
	}
	Expect(updatedBackendSets.Has(updateBackendSetName)).To(BeTrue())

	createdBackendSets := sets.NewString()
	for _, request := range capturingLB.CreateBackendSetRequests {
		createdBackendSets.Insert(*request.CreateBackendSetDetails.Name)
		if *request.CreateBackendSetDetails.Name == createBackendSetName {
			Expect(request.CreateBackendSetDetails.SslConfiguration).NotTo(BeNil())
			Expect(*request.CreateBackendSetDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.BackendSet.CipherSuiteName))
			Expect(request.CreateBackendSetDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.BackendSet.Protocols))
		}
	}
	Expect(createdBackendSets.Has(createBackendSetName)).To(BeTrue())
}

func TestEnsureIngress_UpdateBackendSetWithTLS13ProtocolsOnlyPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	ingressList.Items[0].Annotations[util.IngressBackendSetSslConfigAnnotation] = `{"protocols":["TLSv1.3"]}`
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{certificateList},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	updatedBackendSets := sets.NewString()
	for _, request := range capturingLB.UpdateBackendSetRequests {
		updatedBackendSets.Insert(*request.BackendSetName)
		if *request.BackendSetName == backendSetName {
			Expect(request.UpdateBackendSetDetails.SslConfiguration).NotTo(BeNil())
			Expect(*request.UpdateBackendSetDetails.SslConfiguration.CipherSuiteName).Should(Equal(http2ListenerTLS13CipherSuite))
			Expect(request.UpdateBackendSetDetails.SslConfiguration.Protocols).Should(Equal([]string{"TLSv1.3"}))
		}
	}
	Expect(updatedBackendSets.Has(backendSetName)).To(BeTrue())
}

func TestEnsureIngress_UpdateBackendSetWithTLS12ProtocolsOnlyPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	ingressList.Items[0].Annotations[util.IngressBackendSetSslConfigAnnotation] = `{"protocols":["TLSv1.2"]}`
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{certificateList},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	updatedBackendSets := sets.NewString()
	for _, request := range capturingLB.UpdateBackendSetRequests {
		updatedBackendSets.Insert(*request.BackendSetName)
		if *request.BackendSetName == backendSetName {
			Expect(request.UpdateBackendSetDetails.SslConfiguration).NotTo(BeNil())
			Expect(*request.UpdateBackendSetDetails.SslConfiguration.CipherSuiteName).Should(Equal(http2ListenerTLS12CipherSuite))
			Expect(request.UpdateBackendSetDetails.SslConfiguration.Protocols).Should(Equal([]string{"TLSv1.2"}))
		}
	}
	Expect(updatedBackendSets.Has(backendSetName)).To(BeTrue())
}

func TestEnsureIngress_StagesBackendSetPolicyWhenCurrentManagedListenerTransitionsToSingleCert(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)
	ingressList := &networkingv1.IngressList{
		Items: []networkingv1.Ingress{
			getIngressForService("ingress-single-cert", "testecho2", certificateList),
		},
	}
	testServices := util.GetServiceListResource(namespace, "testecho2", 80)
	backendSetName := util.GenerateBackendSetName(namespace, "testecho2", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
	}
	c := initsWithCustomLBAndServices(ctx, ingressClassList, ingressList, testServices, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	createdBackendSets := sets.NewString()
	for _, request := range capturingLB.CreateBackendSetRequests {
		createdBackendSets.Insert(*request.CreateBackendSetDetails.Name)
		if *request.CreateBackendSetDetails.Name == backendSetName {
			Expect(request.CreateBackendSetDetails.SslConfiguration).NotTo(BeNil())
			Expect(request.CreateBackendSetDetails.SslConfiguration.CipherSuiteName).NotTo(BeNil())
			Expect(*request.CreateBackendSetDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.BackendSet.CipherSuiteName))
			Expect(request.CreateBackendSetDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.BackendSet.Protocols))
		}
	}
	Expect(createdBackendSets.Has(backendSetName)).To(BeTrue())

	backendCreateIndex := operationIndex(capturingLB.OperationLog, "createBackendSet:"+backendSetName)
	listenerUpdateIndex := operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))
	Expect(backendCreateIndex).To(BeNumerically(">=", 0))
	Expect(listenerUpdateIndex).To(BeNumerically(">=", 0))
	Expect(backendCreateIndex).To(BeNumerically("<", listenerUpdateIndex))
}

func TestEnsureIngress_UpdatesBackendSetPolicyWhenCurrentManagedListenerTransitionsToSingleCert(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)
	ingressList := &networkingv1.IngressList{
		Items: []networkingv1.Ingress{
			getIngressForService("ingress-single-cert", "testecho2", certificateList),
		},
	}
	testServices := util.GetServiceListResource(namespace, "testecho2", 80)
	backendSetName := util.GenerateBackendSetName(namespace, "testecho2", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
		AdditionalBackendSets:   []string{backendSetName},
	}
	c := initsWithCustomLBAndServices(ctx, ingressClassList, ingressList, testServices, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	updatedBackendSets := sets.NewString()
	for _, request := range capturingLB.UpdateBackendSetRequests {
		updatedBackendSets.Insert(*request.BackendSetName)
		if *request.BackendSetName == backendSetName {
			Expect(request.UpdateBackendSetDetails.SslConfiguration).NotTo(BeNil())
			Expect(request.UpdateBackendSetDetails.SslConfiguration.CipherSuiteName).NotTo(BeNil())
			Expect(*request.UpdateBackendSetDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.BackendSet.CipherSuiteName))
			Expect(request.UpdateBackendSetDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.BackendSet.Protocols))
		}
	}
	Expect(updatedBackendSets.Has(backendSetName)).To(BeTrue())

	backendUpdateIndex := operationIndex(capturingLB.OperationLog, "updateBackendSet:"+backendSetName)
	listenerUpdateIndex := operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))
	Expect(backendUpdateIndex).To(BeNumerically(">=", 0))
	Expect(listenerUpdateIndex).To(BeNumerically(">=", 0))
	Expect(backendUpdateIndex).To(BeNumerically("<", listenerUpdateIndex))
}

func TestEnsureIngress_StagesBackendSetPolicyBeforeListenerMutation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:  []string{util.DefaultBackendSetName},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))

	backendUpdateIndex := operationIndex(capturingLB.OperationLog, "updateBackendSet:"+backendSetName)
	listenerUpdateIndex := operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))
	Expect(backendUpdateIndex).To(BeNumerically(">=", 0))
	Expect(listenerUpdateIndex).To(BeNumerically(">=", 0))
	Expect(backendUpdateIndex).To(BeNumerically("<", listenerUpdateIndex))

	refreshIndex := operationLastIndexBetween(capturingLB.OperationLog, "getLoadBalancer:", backendUpdateIndex, listenerUpdateIndex)
	Expect(refreshIndex).To(BeNumerically(">", backendUpdateIndex))
	Expect(refreshIndex).To(BeNumerically("<", listenerUpdateIndex))
	Expect(*capturingLB.UpdateListenerRequests[0].IfMatch).To(Equal(etagFromGetLoadBalancerOperation(capturingLB.OperationLog[refreshIndex])))
}

func TestEnsureIngress_ClearsStaleBackendSetSSLBeforeListenerMultiCertUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA"},
		BackendSetSSLConfigs: map[string]*ociloadbalancer.SslConfiguration{
			backendSetName: {
				TrustedCertificateAuthorityIds: []string{"old-ca"},
				CipherSuiteName:                common.String("oci-wider-compatible-ssl-cipher-suite-v1"),
				Protocols:                      []string{"TLSv1.2"},
			},
		},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(1))
	Expect(capturingLB.UpdateBackendSetRequests[0].UpdateBackendSetDetails.SslConfiguration).To(BeNil())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))

	backendUpdateIndex := operationIndex(capturingLB.OperationLog, "updateBackendSet:"+backendSetName)
	listenerUpdateIndex := operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))
	Expect(backendUpdateIndex).To(BeNumerically(">=", 0))
	Expect(listenerUpdateIndex).To(BeNumerically(">=", 0))
	Expect(backendUpdateIndex).To(BeNumerically("<", listenerUpdateIndex))

	refreshIndex := operationLastIndexBetween(capturingLB.OperationLog, "getLoadBalancer:", backendUpdateIndex, listenerUpdateIndex)
	Expect(refreshIndex).To(BeNumerically(">", backendUpdateIndex))
	Expect(refreshIndex).To(BeNumerically("<", listenerUpdateIndex))
}

func TestEnsureIngress_ClearsStaleDefaultBackendSetSSLBeforeListenerMultiCertUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA"},
		BackendSetSSLConfigs: map[string]*ociloadbalancer.SslConfiguration{
			util.DefaultBackendSetName: {
				TrustedCertificateAuthorityIds: []string{"old-ca"},
				CipherSuiteName:                common.String("oci-wider-compatible-ssl-cipher-suite-v1"),
				Protocols:                      []string{"TLSv1.2"},
			},
		},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	defaultBackendSetUpdateIndex := operationIndex(capturingLB.OperationLog, "updateBackendSet:"+util.DefaultBackendSetName)
	listenerUpdateIndex := operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))
	Expect(defaultBackendSetUpdateIndex).To(BeNumerically(">=", 0))
	Expect(listenerUpdateIndex).To(BeNumerically(">=", 0))
	Expect(defaultBackendSetUpdateIndex).To(BeNumerically("<", listenerUpdateIndex))
	for _, request := range capturingLB.UpdateBackendSetRequests {
		if *request.BackendSetName == util.DefaultBackendSetName {
			Expect(request.UpdateBackendSetDetails.SslConfiguration).To(BeNil())
			return
		}
	}
	t.Fatalf("expected stale default backend set SSL to be cleared")
}

func TestEnsureIngress_BackendSetStagingFailureStopsListenerMutation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:  []string{util.DefaultBackendSetName},
		UpdateBackendSetErr:    fmt.Errorf("backend stage failed"),
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyBackendStageFailed"))
	Expect(err.Error()).To(ContainSubstring(ingressClassList.Items[0].Name))
	Expect(err.Error()).To(ContainSubstring(backendSetName))
	Expect(err.Error()).To(ContainSubstring(LockedDefaultTLSPolicy.BackendSet.CipherSuiteName))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	Expect(operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))).To(Equal(-1))
}

func TestEnsureIngress_PostBackendSetStagingRefreshFailureStopsListenerMutation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	const explicitPostStageRefreshCall = 3
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:             80,
		ExistingCertificateIDs:   []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:    []string{util.DefaultBackendSetName},
		GetLoadBalancerErrOnCall: explicitPostStageRefreshCall,
		GetLoadBalancerErr:       fmt.Errorf("refresh failed"),
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyBackendStageFailed"))
	Expect(err.Error()).To(ContainSubstring("refresh load balancer after backend-set policy staging"))
	Expect(err.Error()).To(ContainSubstring(ingressClassList.Items[0].Name))
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))

	backendUpdateIndex := operationIndex(capturingLB.OperationLog, "updateBackendSet:"+backendSetName)
	refreshFailureIndex := operationIndex(capturingLB.OperationLog, "getLoadBalancerError:refresh failed")
	Expect(backendUpdateIndex).To(BeNumerically(">=", 0))
	Expect(refreshFailureIndex).To(BeNumerically(">", backendUpdateIndex))
	Expect(operationIndex(capturingLB.OperationLog, "updateListener:"+util.GenerateListenerName(80))).To(Equal(-1))
}

func TestWrapBackendSetTLSPolicyStageErrorClassifiesPolicySource(t *testing.T) {
	RegisterTestingT(t)

	policy := &TLSPolicy{
		CipherSuiteName: "oci-tls-12-13-ssl-cipher-suite-v3",
		Protocols:       []string{"TLSv1.2", "TLSv1.3"},
	}

	err := wrapBackendSetTLSPolicyStageError(fmt.Errorf("oci rejected policy"), "ingress-class", "backend-set", policy, false)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("TLSPolicyBackendStageFailed"))
	Expect(err.Error()).To(ContainSubstring("policy=backend-set-tls-policy"))
	Expect(err.Error()).NotTo(ContainSubstring("policy=managed-multi-cert-backend-set"))

	err = wrapBackendSetTLSPolicyStageError(fmt.Errorf("oci rejected managed stage"), "ingress-class", "backend-set", policy, true)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("policy=managed-multi-cert-backend-set"))

	err = wrapBackendSetTLSPolicyStageError(fmt.Errorf("oci rejected clear"), "ingress-class", "backend-set", nil, true)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("action=clear-incompatible-backend-set-ssl"))
}

func TestEnsureIngress_ListenerFailureDoesNotRollBackStagedBackendSetPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:  []string{util.DefaultBackendSetName},
		UpdateListenerErr:      fmt.Errorf("listener rejected"),
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	stagedSSLConfig := capturingLB.BackendSetSSLConfigs[backendSetName]
	Expect(stagedSSLConfig).NotTo(BeNil())
	Expect(*stagedSSLConfig.CipherSuiteName).To(Equal(LockedDefaultTLSPolicy.BackendSet.CipherSuiteName))
	Expect(stagedSSLConfig.Protocols).To(Equal(LockedDefaultTLSPolicy.BackendSet.Protocols))
}

func TestEnsureIngress_RetrySkipsAlreadyStagedBackendSetPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:  []string{util.DefaultBackendSetName},
		UpdateListenerErr:      fmt.Errorf("listener rejected"),
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(1))

	capturingLB.UpdateListenerErr = nil
	err = c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(2))
}

func TestEnsureIngress_BackendTLSDisabledMultiCertDoesNotCreateBackendSetSSLConfig(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	certificateList := fmt.Sprintf("%s,%s",
		certificatesmanagement.CertificateConfigTypeIssuedByInternalCa,
		certificatesmanagement.CertificateConfigTypeManagedExternallyIssuedByInternalCa)
	ingressList := getIngressListWithDirectCertificates(certificateList)
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{string(certificatesmanagement.CertificateConfigTypeIssuedByInternalCa)},
		AdditionalBackendSets:  []string{util.DefaultBackendSetName},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	Expect(capturingLB.UpdateListenerRequests[0].SslConfiguration.CipherSuiteName).To(BeNil())
	expectBackendSetRequestsWithoutSSLConfig(capturingLB)
}

func TestEnsureIngress_BackendSetPolicyAnnotationInertWhenBackendTLSDisabled(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	ingressList.Items[0].Annotations[util.IngressBackendSetSslConfigAnnotation] = `{not-json`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	expectBackendSetRequestsWithoutSSLConfig(capturingLB)
}

func TestEnsureIngress_BackendTLSRemovalIgnoresInvalidBackendSetPolicyAnnotation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressBackendTlsEnabledAnnotation] = "false"
	ingressList.Items[0].Annotations[util.IngressBackendSetSslConfigAnnotation] = `{not-json`
	backendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
		BackendSetSSLConfigs: map[string]*ociloadbalancer.SslConfiguration{
			backendSetName: {
				TrustedCertificateAuthorityIds: []string{"old-ca"},
				CipherSuiteName:                common.String("oci-wider-compatible-ssl-cipher-suite-v1"),
				Protocols:                      []string{"TLSv1.1"},
			},
		},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	foundBackendSetRemoval := false
	for _, request := range capturingLB.UpdateBackendSetRequests {
		if *request.BackendSetName == backendSetName {
			foundBackendSetRemoval = true
			Expect(request.UpdateBackendSetDetails.SslConfiguration).To(BeNil())
		}
	}
	Expect(foundBackendSetRemoval).To(BeTrue())
}

func TestEnsureIngress_SingleCertListenerNoopDoesNotStageBackendSetPolicy(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{"certA"},
		AdditionalBackendSets:  []string{util.DefaultBackendSetName},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(0))
	Expect(len(capturingLB.CreateBackendSetRequests)).Should(Equal(0))
}

func TestEnsureIngress_CreateHTTP2ListenerWithMultipleCertificateIds(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolHTTP2
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	createDetails := capturingLB.CreateListenerRequests[0].CreateListenerDetails
	Expect(*createDetails.Protocol).Should(Equal(util.ProtocolHTTP2))
	Expect(createDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA", "certB"}))
	Expect(*createDetails.SslConfiguration.CipherSuiteName).Should(Equal(LockedDefaultTLSPolicy.HTTP2Listener.CipherSuiteName))
	Expect(createDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.HTTP2Listener.Protocols))
	expectBackendSetRequestsWithoutSSLConfig(capturingLB)
}

func TestEnsureIngress_CreateGRPCListener(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA")
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = util.ProtocolGRPC
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort: 443,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(1))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	createDetails := capturingLB.CreateListenerRequests[0].CreateListenerDetails
	Expect(*createDetails.Protocol).Should(Equal(util.ProtocolGRPC))
	Expect(createDetails.SslConfiguration.CertificateIds).Should(Equal([]string{"certA"}))
	Expect(*createDetails.SslConfiguration.CipherSuiteName).Should(Equal(util.ProtocolHTTP2DefaultCipherSuite))
	Expect(createDetails.SslConfiguration.Protocols).Should(Equal(LockedDefaultTLSPolicy.HTTP2Listener.Protocols))
}

func TestEnsureIngress_UpdateListenerClearsSSLWhenDesiredSslConfigAbsentAndPolicyAnnotationInvalid(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("")
	ingressList.Items[0].Annotations[util.IngressListenerSslConfigAnnotation] = `{not-json`
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:            80,
		ExistingCertificateIDs:  []string{"certA", "certB"},
		ExistingCipherSuiteName: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
		ExistingTLSProtocols:    LockedDefaultTLSPolicy.Listener.Protocols,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(1))
	sslConfig := capturingLB.UpdateListenerRequests[0].UpdateListenerDetails.SslConfiguration
	Expect(sslConfig).To(BeNil())
}

func TestEnsureIngress_UpdateListenerNoopWhenExistingSslConfigAlreadyCleared(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:           80,
		ExistingCertificateIDs: []string{},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
}

func TestEnsureIngress_CreateListenerUnsupportedMultiCertErrorIsActionable(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:      443,
		CreateListenerErr: unsupportedCapabilityServiceError{},
		UpdateListenerErr: nil,
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(Equal(multiCertificateWarning))
}

func TestEnsureIngress_UpdateListenerUnsupportedMultiCertErrorPublishesActionableEvent(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := getIngressListWithDirectCertificates("certA,certB")
	capturingLB := &CapturingLoadBalancerClient{
		ListenerPort:      80,
		UpdateListenerErr: unsupportedCapabilityServiceError{},
	}
	c := initsWithCustomLB(ctx, ingressClassList, ingressList, capturingLB)

	err := c.ensureIngress(getContextWithClient(c, ctx), &ingressList.Items[0], &ingressClassList.Items[0])
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(Equal(multiCertificateWarning))

	fakeRecorder, ok := c.eventRecorder.(*events.FakeRecorder)
	Expect(ok).To(BeTrue())
	c.handleErr(err, fmt.Sprintf("%s/%s", namespace, ingressList.Items[0].Name))

	select {
	case event := <-fakeRecorder.Events:
		Expect(event).To(ContainSubstring("IngressReconcileFailed"))
		Expect(event).To(ContainSubstring(multiCertificateWarning))
	case <-time.After(2 * time.Second):
		t.Fatalf("expected warning event for unsupported multi-cert capability failure")
	}
}

func getContextWithClient(c *Controller, ctx context.Context) context.Context {
	wc, err := c.client.GetClient(&MockConfigGetter{})
	Expect(err).To(BeNil())
	ctx = context.WithValue(ctx, util.WrapperClient, wc)
	return ctx
}

func TestSyncListenerClearsStaleSSLConfigWhenTLSNotDesired(t *testing.T) {
	RegisterTestingT(t)
	port := 10901
	lbId := "id"
	listenerName := util.GenerateListenerName(int32(port))
	defaultBackendSet := util.GenerateBackendSetName(namespace, "testecho1", int32(port))
	mockClient := &staleSSLLoadBalancerClient{
		listenerName:      listenerName,
		port:              port,
		defaultBackendSet: defaultBackendSet,
		protocol:          util.ProtocolTCP,
		certificateIds:    []string{"certificate-id"},
	}
	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: mockClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}
	wrapperClient := client.NewWrapperClient(nil, nil, loadBalancerClient, nil, nil, nil)
	ctx := context.WithValue(context.Background(), util.WrapperClient, wrapperClient)
	stateStore := &state.StateStore{
		IngressGroupState: state.IngressClassState{
			ListenerProtocolMap: map[int32]string{
				int32(port): util.ProtocolTCP,
			},
			ListenerDefaultBsMap: map[int32]string{
				int32(port): defaultBackendSet,
			},
			ListenerTLSConfigMap: map[int32]state.ListenerTLSConfig{},
		},
	}

	err := syncListener(ctx, stateStore, &lbId, listenerName, "", &Controller{})

	Expect(err).To(BeNil())
	Expect(mockClient.updateListenerRequest).ToNot(BeNil())
	Expect(mockClient.updateListenerRequest.UpdateListenerDetails.SslConfiguration).To(BeNil())
}

func TestEnsureLoadBalancerIP(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.ensureLoadBalancerIP(getContextWithClient(c, ctx), "ip", &ingressList.Items[0])
	Expect(err == nil).Should(Equal(true))
}

func TestEnsureFinalizer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.ensureFinalizer(getContextWithClient(c, ctx), &ingressList.Items[0])
	Expect(err == nil).Should(Equal(true))
	err = c.ensureFinalizer(getContextWithClient(c, ctx), &ingressList.Items[1])
	Expect(err == nil).Should(Equal(true))
}

func TestDeleteIngress(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	err := c.deleteIngress(&ingressList.Items[0])
	Expect(err == nil).Should(Equal(true))
	err = c.deleteIngress(&ingressList.Items[1])
	Expect(err == nil).Should(Equal(true))
}

func drainIngressQueue(c *Controller) []interface{} {
	var items []interface{}
	for c.queue.Len() > 0 {
		item, shutdown := c.queue.Get()
		if shutdown {
			return items
		}
		items = append(items, item)
		c.queue.Forget(item)
		c.queue.Done(item)
	}
	return items
}

func queueItemsContain(items []interface{}, key string) bool {
	for _, item := range items {
		if item == key {
			return true
		}
	}
	return false
}

func TestIngressAdd(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPath)
	c := inits(ctx, ingressClassList, ingressList)
	drainControllerQueue(c, len(ingressList.Items))
	ingress := ingressList.Items[0].DeepCopy()
	ingress.Name = "ingress-add-unique"
	expectedKey, err := cache.MetaNamespaceKeyFunc(ingress)
	Expect(err).ShouldNot(HaveOccurred())
	c.ingressAdd(ingress)
	Expect(queueItemsContain(drainIngressQueue(c), expectedKey)).Should(BeTrue())
}

func TestIngressUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	drainControllerQueue(c, len(ingressList.Items))
	oldIngress := ingressList.Items[0].DeepCopy()
	newIngress := ingressList.Items[1].DeepCopy()
	oldIngress.Name = "ingress-update-unique"
	newIngress.Name = oldIngress.Name
	c.ingressUpdate(&ingressList.Items[0], &ingressList.Items[1])
	Expect(c.queue.Len()).Should(Equal(0))

	oldIngress.ResourceVersion = "1"
	newIngress.ResourceVersion = "2"

	expectedKey, err := cache.MetaNamespaceKeyFunc(newIngress)
	Expect(err).ShouldNot(HaveOccurred())
	c.ingressUpdate(oldIngress, newIngress)
	Expect(queueItemsContain(drainIngressQueue(c), expectedKey)).Should(BeTrue())
}

func TestIngressDelete(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)
	drainControllerQueue(c, len(ingressList.Items))
	ingress := ingressList.Items[0].DeepCopy()
	ingress.Name = "ingress-delete-unique"
	expectedKey, err := cache.MetaNamespaceKeyFunc(ingress)
	Expect(err).ShouldNot(HaveOccurred())
	c.ingressDelete(ingress)
	Expect(queueItemsContain(drainIngressQueue(c), expectedKey)).Should(BeTrue())
}

func TestSecretAdd(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithTlsSecret)
	c := inits(ctx, ingressClassList, ingressList)
	drainControllerQueue(c, len(ingressList.Items))
	expectedKey, err := cache.MetaNamespaceKeyFunc(&ingressList.Items[0])
	Expect(err).ShouldNot(HaveOccurred())
	c.secretAdd(&v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tls-secret", Namespace: "default"}}, false)
	Expect(queueItemsContain(drainIngressQueue(c), expectedKey)).Should(BeTrue())
}

func TestSecretAdd_IsInInitialList(t *testing.T) {
	RegisterTestingT(t)
	c := &Controller{
		queue: workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(time.Second, time.Second)),
	}
	defer c.queue.ShutDown()

	c.secretAdd(&v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tls-secret", Namespace: "default"}}, true)
	Expect(c.queue.Len()).Should(Equal(0))
}

func TestSecretUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(ingressPathWithTlsSecret)
	c := inits(ctx, ingressClassList, ingressList)
	drainControllerQueue(c, len(ingressList.Items))
	expectedKey, err := cache.MetaNamespaceKeyFunc(&ingressList.Items[0])
	Expect(err).ShouldNot(HaveOccurred())
	c.secretUpdate(nil, &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tls-secret", Namespace: "default"}})
	Expect(queueItemsContain(drainIngressQueue(c), expectedKey)).Should(BeTrue())
}

func TestProcessNextItem(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressList := util.ReadResourceAsIngressList(ingressPathWithFinalizer)
	c := inits(ctx, ingressClassList, ingressList)

	drainControllerQueue(c, len(ingressList.Items))
	c.queue.Add("default-ingress-class")
	res := c.processNextItem()
	Expect(res).Should(BeTrue())
}

func GetLoadBalancerClient() ociclient.LoadBalancerInterface {
	return &MockLoadBalancerClient{}
}

type MockLoadBalancerClient struct {
}

func (m MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	res := util.SampleLoadBalancerResponse()
	return res, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateNetworkSecurityGroups(ctx context.Context, request ociloadbalancer.UpdateNetworkSecurityGroupsRequest) (ociloadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	return ociloadbalancer.UpdateNetworkSecurityGroupsResponse{}, nil
}

func (m MockLoadBalancerClient) CreateLoadBalancer(ctx context.Context, request ociloadbalancer.CreateLoadBalancerRequest) (ociloadbalancer.CreateLoadBalancerResponse, error) {
	return ociloadbalancer.CreateLoadBalancerResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteLoadBalancer(ctx context.Context, request ociloadbalancer.DeleteLoadBalancerRequest) (ociloadbalancer.DeleteLoadBalancerResponse, error) {
	return ociloadbalancer.DeleteLoadBalancerResponse{
		OpcRequestId:     common.String("OpcRequestId"),
		OpcWorkRequestId: common.String("OpcWorkRequestId"),
	}, nil
}

func (m MockLoadBalancerClient) GetWorkRequest(ctx context.Context, request ociloadbalancer.GetWorkRequestRequest) (ociloadbalancer.GetWorkRequestResponse, error) {
	id := "id"
	requestId := "opcrequestid"
	return ociloadbalancer.GetWorkRequestResponse{
		RawResponse: nil,
		WorkRequest: ociloadbalancer.WorkRequest{
			Id:             &id,
			LoadBalancerId: &id,
			Type:           nil,
			LifecycleState: ociloadbalancer.WorkRequestLifecycleStateSucceeded,
		},
		OpcRequestId: &requestId,
	}, nil
}

func (m MockLoadBalancerClient) CreateBackendSet(ctx context.Context, request ociloadbalancer.CreateBackendSetRequest) (ociloadbalancer.CreateBackendSetResponse, error) {
	reqId := "opcrequestid"
	return ociloadbalancer.CreateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}, nil
}

func (m MockLoadBalancerClient) UpdateBackendSet(ctx context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	reqId := "opcrequestid"
	res := ociloadbalancer.UpdateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}
	return res, nil
}

func (m MockLoadBalancerClient) DeleteBackendSet(ctx context.Context, request ociloadbalancer.DeleteBackendSetRequest) (ociloadbalancer.DeleteBackendSetResponse, error) {
	return ociloadbalancer.DeleteBackendSetResponse{
		OpcRequestId:     common.String("OpcRequestId"),
		OpcWorkRequestId: common.String("OpcWorkRequestId"),
	}, nil
}

func (m MockLoadBalancerClient) GetBackendSetHealth(ctx context.Context, request ociloadbalancer.GetBackendSetHealthRequest) (ociloadbalancer.GetBackendSetHealthResponse, error) {
	return ociloadbalancer.GetBackendSetHealthResponse{}, nil
}

func (m MockLoadBalancerClient) CreateRoutingPolicy(ctx context.Context, request ociloadbalancer.CreateRoutingPolicyRequest) (ociloadbalancer.CreateRoutingPolicyResponse, error) {
	return ociloadbalancer.CreateRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) UpdateRoutingPolicy(ctx context.Context, request ociloadbalancer.UpdateRoutingPolicyRequest) (ociloadbalancer.UpdateRoutingPolicyResponse, error) {
	return ociloadbalancer.UpdateRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) DeleteRoutingPolicy(ctx context.Context, request ociloadbalancer.DeleteRoutingPolicyRequest) (ociloadbalancer.DeleteRoutingPolicyResponse, error) {
	return ociloadbalancer.DeleteRoutingPolicyResponse{}, nil
}

func (m MockLoadBalancerClient) CreateListener(ctx context.Context, request ociloadbalancer.CreateListenerRequest) (ociloadbalancer.CreateListenerResponse, error) {
	reqId := "opcrequestid"
	res := ociloadbalancer.CreateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}
	return res, nil
}

func (m MockLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	return ociloadbalancer.UpdateListenerResponse{}, nil
}

type staleSSLLoadBalancerClient struct {
	MockLoadBalancerClient
	listenerName          string
	port                  int
	defaultBackendSet     string
	protocol              string
	certificateIds        []string
	updateListenerRequest *ociloadbalancer.UpdateListenerRequest
}

func (m *staleSSLLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	lbId := "id"
	etag := "etag"
	listener := ociloadbalancer.Listener{
		Name:                  common.String(m.listenerName),
		Port:                  common.Int(m.port),
		Protocol:              common.String(m.protocol),
		DefaultBackendSetName: common.String(m.defaultBackendSet),
		SslConfiguration: &ociloadbalancer.SslConfiguration{
			CertificateIds: m.certificateIds,
		},
	}
	return ociloadbalancer.GetLoadBalancerResponse{
		LoadBalancer: ociloadbalancer.LoadBalancer{
			Id: common.String(lbId),
			Listeners: map[string]ociloadbalancer.Listener{
				m.listenerName: listener,
			},
		},
		ETag: common.String(etag),
	}, nil
}

func (m *staleSSLLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	m.updateListenerRequest = &request
	id := "id"
	return ociloadbalancer.UpdateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m MockLoadBalancerClient) DeleteListener(ctx context.Context, request ociloadbalancer.DeleteListenerRequest) (ociloadbalancer.DeleteListenerResponse, error) {
	return ociloadbalancer.DeleteListenerResponse{}, nil
}

type CapturingLoadBalancerClient struct {
	MockLoadBalancerClient
	ListenerPort             int
	ListenerAppearsOnGetCall int
	ExistingProtocol         string
	ExistingCertificateIDs   []string
	ExistingCipherSuiteName  string
	ExistingTLSProtocols     []string
	AdditionalBackendSets    []string
	CreatedBackendSets       []string
	BackendSetSSLConfigs     map[string]*ociloadbalancer.SslConfiguration
	OperationLog             []string
	CreateListenerErr        error
	UpdateListenerErr        error
	CreateBackendSetErr      error
	UpdateBackendSetErr      error
	GetLoadBalancerErrOnCall int
	GetLoadBalancerErr       error
	CreateListenerRequests   []ociloadbalancer.CreateListenerRequest
	UpdateListenerRequests   []ociloadbalancer.UpdateListenerRequest
	CreateBackendSetRequests []ociloadbalancer.CreateBackendSetRequest
	UpdateBackendSetRequests []ociloadbalancer.UpdateBackendSetRequest
	getLoadBalancerCalls     int
}

func (m *CapturingLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	m.getLoadBalancerCalls++
	if m.GetLoadBalancerErrOnCall > 0 && m.getLoadBalancerCalls == m.GetLoadBalancerErrOnCall {
		err := m.GetLoadBalancerErr
		if err == nil {
			err = fmt.Errorf("get load balancer failed")
		}
		m.recordOperation("getLoadBalancerError:" + err.Error())
		return ociloadbalancer.GetLoadBalancerResponse{}, err
	}
	etag := fmt.Sprintf("etag-%d", m.getLoadBalancerCalls)
	m.recordOperation("getLoadBalancer:" + etag)
	res := util.SampleLoadBalancerResponse()
	res.ETag = common.String(etag)
	primaryBackendSetName := util.GenerateBackendSetName(namespace, "testecho1", 80)
	res.LoadBalancer.BackendSets[primaryBackendSetName] = sampleBackendSet(primaryBackendSetName)
	if m.ListenerPort <= 0 {
		return res, nil
	}
	res.LoadBalancer.Listeners = map[string]ociloadbalancer.Listener{}
	if m.ListenerAppearsOnGetCall > 0 && m.getLoadBalancerCalls < m.ListenerAppearsOnGetCall {
		return res, nil
	}

	listenerName := util.GenerateListenerName(int32(m.ListenerPort))
	listenerPort := m.ListenerPort
	protocol := util.ProtocolHTTP
	if m.ExistingProtocol != "" {
		protocol = m.ExistingProtocol
	}
	defaultBackendSet := util.DefaultBackendSetName
	sslConfig := &ociloadbalancer.SslConfiguration{
		CertificateIds: append([]string(nil), m.ExistingCertificateIDs...),
		Protocols:      append([]string(nil), m.ExistingTLSProtocols...),
	}
	if m.ExistingCipherSuiteName != "" {
		sslConfig.CipherSuiteName = common.String(m.ExistingCipherSuiteName)
	}
	res.LoadBalancer.Listeners[listenerName] = ociloadbalancer.Listener{
		Name:                  common.String(listenerName),
		DefaultBackendSetName: common.String(defaultBackendSet),
		Port:                  common.Int(listenerPort),
		Protocol:              common.String(protocol),
		SslConfiguration:      sslConfig,
		RoutingPolicyName:     common.String(listenerName),
	}
	for _, backendSetName := range m.AdditionalBackendSets {
		res.LoadBalancer.BackendSets[backendSetName] = sampleBackendSet(backendSetName)
	}
	for _, backendSetName := range m.CreatedBackendSets {
		res.LoadBalancer.BackendSets[backendSetName] = sampleBackendSet(backendSetName)
	}
	for backendSetName, sslConfig := range m.BackendSetSSLConfigs {
		backendSet, ok := res.LoadBalancer.BackendSets[backendSetName]
		if !ok {
			backendSet = sampleBackendSet(backendSetName)
		}
		backendSet.SslConfiguration = copySSLConfiguration(sslConfig)
		res.LoadBalancer.BackendSets[backendSetName] = backendSet
	}
	return res, nil
}

func (m *CapturingLoadBalancerClient) CreateListener(ctx context.Context, request ociloadbalancer.CreateListenerRequest) (ociloadbalancer.CreateListenerResponse, error) {
	m.CreateListenerRequests = append(m.CreateListenerRequests, request)
	m.recordOperation("createListener:" + *request.Name)
	reqID := "opcrequestid"
	if m.CreateListenerErr != nil {
		return ociloadbalancer.CreateListenerResponse{
			RawResponse:      nil,
			OpcWorkRequestId: &reqID,
			OpcRequestId:     &reqID,
		}, m.CreateListenerErr
	}
	return ociloadbalancer.CreateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqID,
		OpcRequestId:     &reqID,
	}, nil
}

func (m *CapturingLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	m.UpdateListenerRequests = append(m.UpdateListenerRequests, request)
	m.recordOperation("updateListener:" + *request.ListenerName)
	reqID := "opcrequestid"
	if m.UpdateListenerErr != nil {
		return ociloadbalancer.UpdateListenerResponse{
			RawResponse:      nil,
			OpcWorkRequestId: &reqID,
			OpcRequestId:     &reqID,
		}, m.UpdateListenerErr
	}
	return ociloadbalancer.UpdateListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqID,
		OpcRequestId:     &reqID,
	}, nil
}

func (m *CapturingLoadBalancerClient) CreateBackendSet(ctx context.Context, request ociloadbalancer.CreateBackendSetRequest) (ociloadbalancer.CreateBackendSetResponse, error) {
	m.CreateBackendSetRequests = append(m.CreateBackendSetRequests, request)
	m.recordOperation("createBackendSet:" + *request.Name)
	reqID := "opcrequestid"
	if m.CreateBackendSetErr != nil {
		return ociloadbalancer.CreateBackendSetResponse{
			RawResponse:      nil,
			OpcWorkRequestId: &reqID,
			OpcRequestId:     &reqID,
		}, m.CreateBackendSetErr
	}
	m.CreatedBackendSets = appendStringIfMissing(m.CreatedBackendSets, *request.Name)
	m.setBackendSetSSLConfig(*request.Name, sslConfigurationDetailsToCurrent(request.SslConfiguration))
	return ociloadbalancer.CreateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqID,
		OpcRequestId:     &reqID,
	}, nil
}

func (m *CapturingLoadBalancerClient) UpdateBackendSet(ctx context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	m.UpdateBackendSetRequests = append(m.UpdateBackendSetRequests, request)
	m.recordOperation("updateBackendSet:" + *request.BackendSetName)
	reqID := "opcrequestid"
	if m.UpdateBackendSetErr != nil {
		return ociloadbalancer.UpdateBackendSetResponse{
			RawResponse:      nil,
			OpcWorkRequestId: &reqID,
			OpcRequestId:     &reqID,
		}, m.UpdateBackendSetErr
	}
	m.setBackendSetSSLConfig(*request.BackendSetName, sslConfigurationDetailsToCurrent(request.SslConfiguration))
	return ociloadbalancer.UpdateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqID,
		OpcRequestId:     &reqID,
	}, nil
}

func (m *CapturingLoadBalancerClient) GetWorkRequest(ctx context.Context, request ociloadbalancer.GetWorkRequestRequest) (ociloadbalancer.GetWorkRequestResponse, error) {
	m.recordOperation("getWorkRequest:" + *request.WorkRequestId)
	return m.MockLoadBalancerClient.GetWorkRequest(ctx, request)
}

func (m *CapturingLoadBalancerClient) recordOperation(operation string) {
	m.OperationLog = append(m.OperationLog, operation)
}

func (m *CapturingLoadBalancerClient) setBackendSetSSLConfig(backendSetName string, sslConfig *ociloadbalancer.SslConfiguration) {
	if m.BackendSetSSLConfigs == nil {
		m.BackendSetSSLConfigs = map[string]*ociloadbalancer.SslConfiguration{}
	}
	m.BackendSetSSLConfigs[backendSetName] = sslConfig
}

func (m *CapturingLoadBalancerClient) DeleteListener(ctx context.Context, request ociloadbalancer.DeleteListenerRequest) (ociloadbalancer.DeleteListenerResponse, error) {
	reqID := "opcrequestid"
	return ociloadbalancer.DeleteListenerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqID,
		OpcRequestId:     &reqID,
	}, nil
}

type CapturingCertificateManagerClient struct {
	MockCertificateManagerClient
	CreateCertificateRequests []certificatesmanagement.CreateCertificateRequest
	UpdateCertificateRequests []certificatesmanagement.UpdateCertificateRequest
}

func (m *CapturingCertificateManagerClient) CreateCertificate(ctx context.Context, request certificatesmanagement.CreateCertificateRequest) (certificatesmanagement.CreateCertificateResponse, error) {
	m.CreateCertificateRequests = append(m.CreateCertificateRequests, request)
	return m.MockCertificateManagerClient.CreateCertificate(ctx, request)
}

func (m *CapturingCertificateManagerClient) UpdateCertificate(ctx context.Context, request certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error) {
	m.UpdateCertificateRequests = append(m.UpdateCertificateRequests, request)
	return m.MockCertificateManagerClient.UpdateCertificate(ctx, request)
}

func getIngressListWithDirectCertificates(certificateList string) *networkingv1.IngressList {
	return &networkingv1.IngressList{
		Items: []networkingv1.Ingress{
			getIngressForService("ingress-multi-cert", "testecho1", certificateList),
		},
	}
}

func getIngressForService(name string, serviceName string, certificateList string) networkingv1.Ingress {
	pathType := networkingv1.PathTypeExact
	return networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				util.IngressListenerTlsCertificateAnnotation: certificateList,
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: fmt.Sprintf("%s.foo.bar.com", serviceName),
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: &pathType,
									Path:     "/" + serviceName,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func getIngressForServiceWithTLSSecret(ingressNamespace string, name string, serviceName string, secretName string) networkingv1.Ingress {
	ingress := getIngressForService(name, serviceName, "")
	ingress.Namespace = ingressNamespace
	delete(ingress.Annotations, util.IngressListenerTlsCertificateAnnotation)
	ingress.Spec.TLS = []networkingv1.IngressTLS{
		{
			Hosts:      []string{fmt.Sprintf("%s.foo.bar.com", serviceName)},
			SecretName: secretName,
		},
	}
	return ingress
}

func expectBackendSetRequestsWithoutSSLConfig(capturingLB *CapturingLoadBalancerClient) {
	for _, request := range capturingLB.UpdateBackendSetRequests {
		Expect(request.UpdateBackendSetDetails.SslConfiguration).To(BeNil())
	}
	for _, request := range capturingLB.CreateBackendSetRequests {
		Expect(request.CreateBackendSetDetails.SslConfiguration).To(BeNil())
	}
}

func expectNoCapturedOCIMutations(capturingLB *CapturingLoadBalancerClient) {
	Expect(len(capturingLB.CreateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.UpdateListenerRequests)).Should(Equal(0))
	Expect(len(capturingLB.CreateBackendSetRequests)).Should(Equal(0))
	Expect(len(capturingLB.UpdateBackendSetRequests)).Should(Equal(0))
}

func operationIndex(operations []string, prefix string) int {
	return operationIndexAfter(operations, prefix, -1)
}

func operationIndexAfter(operations []string, prefix string, after int) int {
	for i := after + 1; i < len(operations); i++ {
		if strings.HasPrefix(operations[i], prefix) {
			return i
		}
	}
	return -1
}

func operationLastIndexBetween(operations []string, prefix string, after int, before int) int {
	index := -1
	for i := after + 1; i < before; i++ {
		if strings.HasPrefix(operations[i], prefix) {
			index = i
		}
	}
	return index
}

func etagFromGetLoadBalancerOperation(operation string) string {
	return strings.TrimPrefix(operation, "getLoadBalancer:")
}

func appendStringIfMissing(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func sslConfigurationDetailsToCurrent(details *ociloadbalancer.SslConfigurationDetails) *ociloadbalancer.SslConfiguration {
	if details == nil {
		return nil
	}
	return &ociloadbalancer.SslConfiguration{
		TrustedCertificateAuthorityIds: append([]string(nil), details.TrustedCertificateAuthorityIds...),
		CertificateIds:                 append([]string(nil), details.CertificateIds...),
		CertificateName:                copyStringPtr(details.CertificateName),
		CipherSuiteName:                copyStringPtr(details.CipherSuiteName),
		Protocols:                      append([]string(nil), details.Protocols...),
	}
}

func copySSLConfiguration(current *ociloadbalancer.SslConfiguration) *ociloadbalancer.SslConfiguration {
	if current == nil {
		return nil
	}
	return &ociloadbalancer.SslConfiguration{
		TrustedCertificateAuthorityIds: append([]string(nil), current.TrustedCertificateAuthorityIds...),
		CertificateIds:                 append([]string(nil), current.CertificateIds...),
		CertificateName:                copyStringPtr(current.CertificateName),
		CipherSuiteName:                copyStringPtr(current.CipherSuiteName),
		Protocols:                      append([]string(nil), current.Protocols...),
	}
}

func copyStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	return common.String(*value)
}

func sampleBackendSet(name string) ociloadbalancer.BackendSet {
	policy := util.DefaultBackendSetRoutingPolicy
	healthChecker := util.GetDefaultHeathChecker()
	return ociloadbalancer.BackendSet{
		Name:   common.String(name),
		Policy: common.String(policy),
		HealthChecker: &ociloadbalancer.HealthChecker{
			Protocol:          healthChecker.Protocol,
			UrlPath:           healthChecker.UrlPath,
			Port:              healthChecker.Port,
			ReturnCode:        healthChecker.ReturnCode,
			Retries:           healthChecker.Retries,
			TimeoutInMillis:   healthChecker.TimeoutInMillis,
			IntervalInMillis:  healthChecker.IntervalInMillis,
			ResponseBodyRegex: healthChecker.ResponseBodyRegex,
			IsForcePlainText:  healthChecker.IsForcePlainText,
		},
	}
}

type unsupportedCapabilityServiceError struct{}

func (e unsupportedCapabilityServiceError) Error() string {
	return "BadRequest"
}

func (e unsupportedCapabilityServiceError) GetHTTPStatusCode() int {
	return 400
}

func (e unsupportedCapabilityServiceError) GetMessage() string {
	return "BadRequest"
}

func (e unsupportedCapabilityServiceError) GetCode() string {
	return "InvalidParameter"
}

func (e unsupportedCapabilityServiceError) GetOpcRequestID() string {
	return "fake-opc-request-id"
}

type listenerAlreadyExistsServiceError struct{}

func (e listenerAlreadyExistsServiceError) Error() string {
	return "Conflict: already has listener 'route_80'"
}

func (e listenerAlreadyExistsServiceError) GetHTTPStatusCode() int {
	return 409
}

func (e listenerAlreadyExistsServiceError) GetMessage() string {
	return "already has listener 'route_80'"
}

func (e listenerAlreadyExistsServiceError) GetCode() string {
	return "Conflict"
}

func (e listenerAlreadyExistsServiceError) GetOpcRequestID() string {
	return "fake-opc-request-id"
}

// MockConfigGetter is a mock implementation of the ConfigGetter interface for testing purposes.
type MockConfigGetter struct {
	ConfigurationProvider common.ConfigurationProvider
	Key                   string
	Error                 error
}

// NewMockConfigGetter creates a new instance of MockConfigGetter.
func NewMockConfigGetter(configurationProvider common.ConfigurationProvider, key string, err error) *MockConfigGetter {
	return &MockConfigGetter{
		ConfigurationProvider: configurationProvider,
		Key:                   key,
		Error:                 err,
	}
}
func (m *MockConfigGetter) GetConfigurationProvider() (common.ConfigurationProvider, error) {
	return m.ConfigurationProvider, m.Error
}
func (m *MockConfigGetter) GetKey() string {
	return m.Key
}

type MockCacheStore struct {
	client *client.WrapperClient
}

func (m *MockCacheStore) Add(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) Update(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) Delete(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) List() []interface{} {
	return nil
}

func (m *MockCacheStore) ListKeys() []string {
	return nil
}

func (m *MockCacheStore) Get(obj interface{}) (item interface{}, exists bool, err error) {
	return nil, true, nil
}

func (m *MockCacheStore) Replace(i []interface{}, s string) error {
	return nil
}

func (m *MockCacheStore) Resync() error {
	return nil
}

func NewMockCacheStore(client *client.WrapperClient) *MockCacheStore {
	return &MockCacheStore{
		client: client,
	}
}

func (m *MockCacheStore) GetByKey(key string) (item interface{}, exists bool, err error) {
	return m.client, true, nil
}
