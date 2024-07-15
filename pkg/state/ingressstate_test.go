/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */
package state

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/informers"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	TlsConfigValidationsFilePath              = "validate-tls-config.yaml"
	HealthCheckerConfigValidationsFilePath    = "validate-hc-config.yaml"
	BackendSetPolicyConfigValidationsFilePath = "validate-bs-policy-config.yaml"
	ListenerProtocolConfigValidationsFilePath = "validate-listener-protocol-config.yaml"
	TestIngressStateFilePath                  = "test-ingress-state.yaml"
	TestIngressStateWithPortNameFilePath      = "test-ingress-state_withportname.yaml"
	TestIngressStateWithNamedClassesFilePath  = "test-ingress-state_withnamedclasses.yaml"
	TestSslTerminationAtLb                    = "test-ssl-termination-lb.yaml"
	DefaultBackendSetValidationsFilePath      = "validate-default-backend-set.yaml"
)

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList, ingressList *networkingv1.IngressList, testService *v1.ServiceList) (networkinglisters.IngressClassLister, networkinglisters.IngressLister, corelisters.ServiceLister) {
	client := fakeclientset.NewSimpleClientset()

	action := "list"
	util.UpdateFakeClientCall(client, action, "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(client, action, "ingresses", ingressList)
	util.UpdateFakeClientCall(client, action, "services", testService)

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassLister := ingressClassInformer.Lister()

	ingressInformer := informerFactory.Networking().V1().Ingresses()
	ingressLister := ingressInformer.Lister()

	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), ingressInformer.Informer().HasSynced)
	cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)
	return ingressClassLister, ingressLister, serviceLister
}

func TestListenerWithDifferentSecrets(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)
	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(BeNil())
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(PortConflictMessage, 943)))
}

func TestListenerWithSameSecrets(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	secretName := "same_secret_name"
	ingressList.Items[0].Spec.TLS[0].SecretName = secretName
	ingressList.Items[1].Spec.TLS[0].SecretName = secretName

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	artifact, artifactType := stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(artifact).Should(Equal(secretName))
	Expect(artifactType).Should(Equal(ArtifactTypeSecret))

	artifact, artifactType = stateStore.GetTLSConfigForListener(943)
	Expect(artifact).Should(Equal(secretName))
	Expect(artifactType).Should(Equal(ArtifactTypeSecret))

	allBs := stateStore.GetAllBackendSetForIngressClass()
	Expect(len(allBs)).Should(Equal(2))
}

func TestListenerWithSecretAndCertificate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	ingressList.Items[1].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[1].Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: "certificateId"}

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	fmt.Printf("FATAL: %+v\n", err)
	Expect(err).NotTo(BeNil())
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(PortConflictMessage, 943)))
}

func TestListenerWithDifferentCertificates(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	ingressList.Items[0].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[0].Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: "certificateId"}
	ingressList.Items[1].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[1].Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: "differentCertificateId"}

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(BeNil())
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(PortConflictMessage, 943)))
}

func TestListenerWithSameCertificate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	certificateId := "certificateId"
	ingressList.Items[0].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[0].Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: certificateId}

	ingressList.Items[1].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[1].Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: certificateId}

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	bsTlsConfig := stateStore.IngressGroupState.BackendSetTLSConfigMap[bsName]
	Expect(bsTlsConfig.Artifact).Should(Equal(certificateId))
	Expect(bsTlsConfig.Type).Should(Equal(ArtifactTypeCertificate))

	lstTlsConfig := stateStore.IngressGroupState.ListenerTLSConfigMap[943]
	Expect(lstTlsConfig.Artifact).Should(Equal(certificateId))
	Expect(lstTlsConfig.Type).Should(Equal(ArtifactTypeCertificate))
}

func TestIngressState(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TestIngressStateFilePath)

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	assertCases(stateStore)
}

func TestIngressStateWithPortName(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TestIngressStateWithPortNameFilePath)

	testService := util.GetServiceListResourceWithPortName("default", "tls-test", 80, "tls-port")
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	assertCases(stateStore)
}

func TestIngressStateWithNamedClasses(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TestIngressStateWithNamedClassesFilePath)

	testService := util.GetServiceListResourceWithPortName("default", "tls-test", 80, "tls-port")
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	assertCases(stateStore)
}

func assertCases(stateStore *StateStore) {
	ingressName := "ingress-state"
	allBs := stateStore.GetAllBackendSetForIngressClass()
	// 4 including default_ingress
	Expect(len(allBs)).Should(Equal(5))

	ingressBs := stateStore.GetIngressBackendSets(ingressName)
	Expect(len(ingressBs)).Should(Equal(3))

	ingressListeners := stateStore.GetIngressPorts(ingressName)
	Expect(len(ingressListeners)).Should(Equal(2))

	Expect(len(stateStore.IngressGroupState.BackendSetTLSConfigMap)).Should(Equal(3))
	Expect(len(stateStore.IngressGroupState.ListenerTLSConfigMap)).Should(Equal(3))

	artifact, artifactType := stateStore.GetTLSConfigForListener(80)
	Expect(artifact).Should(Equal("secret_name"))
	Expect(artifactType).Should(Equal(ArtifactTypeSecret))

	artifact, artifactType = stateStore.GetTLSConfigForListener(90)
	Expect(artifact).Should(Equal("secret_name"))
	Expect(artifactType).Should(Equal(ArtifactTypeSecret))

	artifact, artifactType = stateStore.GetTLSConfigForListener(100)
	Expect(artifact).Should(Equal(""))
	Expect(artifactType).Should(Equal(""))

	bsName := util.GenerateBackendSetName("default", "tls-test", 100)
	artifact, artifactType = stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(artifact).Should(Equal(""))
	Expect(artifactType).Should(Equal(""))
}

func TestValidateHealthCheckerConfig(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(HealthCheckerConfigValidationsFilePath)

	testService := util.GetServiceListResource("default", "test-health-checker-annotation", 800)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	defaultIngressHC := stateStore.GetBackendSetHealthChecker(util.DefaultBackendSetName)
	Expect(defaultIngressHC).Should(Equal(util.GetDefaultHeathChecker()))

	bsName := util.GenerateBackendSetName("default", "test-health-checker-annotation", 800)
	actualHc := stateStore.GetBackendSetHealthChecker(bsName)

	expectedHc := &loadbalancer.HealthCheckerDetails{
		Protocol:          common.String(util.ProtocolHTTP),
		UrlPath:           common.String("/health"),
		Port:              common.Int(8080),
		ReturnCode:        common.Int(200),
		Retries:           common.Int(3),
		TimeoutInMillis:   common.Int(3000),
		IntervalInMillis:  common.Int(10000),
		ResponseBodyRegex: common.String("*"),
		IsForcePlainText:  common.Bool(true),
	}

	Expect(actualHc).Should(Equal(expectedHc))
}

func TestValidateHealthCheckerConfigWithConflict(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(HealthCheckerConfigValidationsFilePath)

	ingressList.Items[1].Annotations[util.IngressHealthCheckPortAnnotation] = "9090"

	testService := util.GetServiceListResource("default", "test-health-checker-annotation", 800)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "test-health-checker-annotation", 800)
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(HealthCheckerConflictMessage, bsName)))
}

func TestValidatePolicyConfig(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(BackendSetPolicyConfigValidationsFilePath)

	testService := util.GetServiceListResource("default", "test-policy-annotation", 900)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	defaultIngressPolicy := stateStore.GetBackendSetPolicy(util.DefaultBackendSetName)
	Expect(defaultIngressPolicy).Should(Equal(util.DefaultBackendSetRoutingPolicy))

	bsName := util.GenerateBackendSetName("default", "test-policy-annotation", 900)
	actualPolicy := stateStore.GetBackendSetPolicy(bsName)
	Expect(actualPolicy).Should(Equal("ROUND_ROBIN"))
}

func TestValidatePolicyConfigWithConflict(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(BackendSetPolicyConfigValidationsFilePath)

	ingressList.Items[1].Annotations[util.IngressPolicyAnnotation] = "LEAST_CONNECTIONS"

	testService := util.GetServiceListResource("default", "test-policy-annotation", 900)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "test-policy-annotation", 900)
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(PolicyConflictMessage, bsName)))
}

func TestValidateProtocolConfig(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(ListenerProtocolConfigValidationsFilePath)

	testService := util.GetServiceListResource("default", "test-protocol-annotation", 900)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	actualProtocol := stateStore.GetListenerProtocol(900)
	Expect(actualProtocol).Should(Equal("HTTP2"))
}

func TestValidateProtocolConfigWithConflict(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(ListenerProtocolConfigValidationsFilePath)

	ingressList.Items[1].Annotations[util.IngressProtocolAnnotation] = "HTTP"

	testService := util.GetServiceListResource("default", "test-protocol-annotation", 900)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(ProtocolConflictMessage, 900)))
}

func TestSslTerminationAtLB(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TestSslTerminationAtLb)

	certificateId := "certificateId"
	ingressList.Items[0].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[0].Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: certificateId}

	testService := util.GetServiceListResource("default", "tls-test", 443)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 443)
	bsTlsConfig := stateStore.IngressGroupState.BackendSetTLSConfigMap[bsName]
	Expect(bsTlsConfig.Artifact).Should(Equal(""))
	Expect(bsTlsConfig.Type).Should(Equal(""))

	lstTlsConfig := stateStore.IngressGroupState.ListenerTLSConfigMap[443]
	Expect(lstTlsConfig.Artifact).Should(Equal(certificateId))
	Expect(lstTlsConfig.Type).Should(Equal(ArtifactTypeCertificate))
}

func TestValidateListenerDefaultBackendSet(t *testing.T) {
	RegisterTestingT(t)
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(DefaultBackendSetValidationsFilePath)

	testService := util.GetServiceListResource("default", "tcp-test", 8080)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).ShouldNot(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "host-es", 8080)
	Expect(stateStore.GetListenerDefaultBackendSet(8080)).Should(Equal(bsName))
}

func TestValidateListenerDefaultBackendSetWithConflict(t *testing.T) {
	RegisterTestingT(t)
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(DefaultBackendSetValidationsFilePath)

	testService := util.GetServiceListResource("default", "tcp-test", 8080)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	ingressList.Items[1].Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "tcp-test"

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(DefaultBackendSetConflictMessage, 8080)))
}
