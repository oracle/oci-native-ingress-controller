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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
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

func TestValidateMtlsConfig(t *testing.T) {
	RegisterTestingT(t)
	listenerTLSCandidates := map[int32][]listenerTLSCandidate{443: {{Config: TlsConfig{Type: ArtifactTypeCertificate, Artifact: "server-certificate"}}}}
	listenerMtlsConfigs := map[int32]MtlsConfig{}
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		util.IngressListenerMtlsConfigAnnotation: `{"trustedCertificateAuthorityIds":["client-ca-two","client-ca-one"]}`,
	}}}

	Expect(validateMtlsConfig(ingress, 443, listenerTLSCandidates, listenerMtlsConfigs)).To(Succeed())
	Expect(listenerMtlsConfigs[443]).To(Equal(MtlsConfig{
		TrustedCertificateAuthorityIds: []string{"client-ca-one", "client-ca-two"},
		VerifyDepth:                    util.DefaultListenerMtlsVerifyDepth,
	}))

	ingress.Annotations[util.IngressListenerMtlsConfigAnnotation] = `{"trustedCertificateAuthorityIds":["client-ca-one"],"verifyDepth":2}`
	Expect(validateMtlsConfig(ingress, 443, listenerTLSCandidates, listenerMtlsConfigs)).To(MatchError(fmt.Sprintf(MtlsConflictMessage, 443)))
	Expect(validateMtlsConfig(ingress, 8443, listenerTLSCandidates, map[int32]MtlsConfig{})).To(MatchError(fmt.Sprintf(MtlsRequiresTlsMessage, 8443)))
}

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

func setBackendTLSEnabled(ingress *networkingv1.Ingress, enabled bool) {
	if ingress.Annotations == nil {
		ingress.Annotations = map[string]string{}
	}
	ingress.Annotations[util.IngressBackendTlsEnabledAnnotation] = fmt.Sprintf("%t", enabled)
}

func TestListenerWithDifferentSecrets(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)
	setBackendTLSEnabled(&ingressList.Items[0], false)
	setBackendTLSEnabled(&ingressList.Items[1], false)
	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	listenerTlsConfigs := stateStore.GetTLSConfigForListener(943)
	Expect(listenerTlsConfigs).Should(ContainElement(TlsConfig{
		Type:      ArtifactTypeSecret,
		Artifact:  "secret_name_one",
		Namespace: "default",
	}))
	Expect(listenerTlsConfigs).Should(ContainElement(TlsConfig{
		Type:      ArtifactTypeSecret,
		Artifact:  "secret_name_two",
		Namespace: "default",
	}))
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
	backendTLSConfig := stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(backendTLSConfig.Artifact).Should(Equal(secretName))
	Expect(backendTLSConfig.Type).Should(Equal(ArtifactTypeSecret))
	Expect(backendTLSConfig.Namespace).Should(Equal("default"))

	listenerTlsConfigs := stateStore.GetTLSConfigForListener(943)
	Expect(listenerTlsConfigs).Should(Equal([]TlsConfig{{
		Type:      ArtifactTypeSecret,
		Artifact:  secretName,
		Namespace: "default",
	}}))

	allBs := stateStore.GetAllBackendSetForIngressClass()
	Expect(len(allBs)).Should(Equal(2))
}

func TestListenerWithSecretAndCertificate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)
	setBackendTLSEnabled(&ingressList.Items[0], false)
	setBackendTLSEnabled(&ingressList.Items[1], false)

	ingressList.Items[1].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[1].Annotations[util.IngressListenerTlsCertificateAnnotation] = "certificateId"

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	listenerTlsConfigs := stateStore.GetTLSConfigForListener(943)
	Expect(listenerTlsConfigs).Should(ContainElement(TlsConfig{
		Type:      ArtifactTypeSecret,
		Artifact:  "secret_name_one",
		Namespace: "default",
	}))
	Expect(listenerTlsConfigs).Should(ContainElement(TlsConfig{
		Type:      ArtifactTypeCertificate,
		Artifact:  "certificateId",
		Namespace: "default",
	}))
}

func TestListenerWithDifferentCertificates(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)
	setBackendTLSEnabled(&ingressList.Items[0], false)
	setBackendTLSEnabled(&ingressList.Items[1], false)

	ingressList.Items[0].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[0].Annotations[util.IngressListenerTlsCertificateAnnotation] = "certificateId"
	ingressList.Items[1].Spec.TLS = []networkingv1.IngressTLS{}
	ingressList.Items[1].Annotations[util.IngressListenerTlsCertificateAnnotation] = "differentCertificateId"

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	listenerTlsConfigs := stateStore.GetTLSConfigForListener(943)
	Expect(listenerTlsConfigs).Should(ContainElement(TlsConfig{
		Type:      ArtifactTypeCertificate,
		Artifact:  "certificateId",
		Namespace: "default",
	}))
	Expect(listenerTlsConfigs).Should(ContainElement(TlsConfig{
		Type:      ArtifactTypeCertificate,
		Artifact:  "differentCertificateId",
		Namespace: "default",
	}))
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
	Expect(lstTlsConfig.TlsConfigs).Should(Equal([]TlsConfig{{
		Type:      ArtifactTypeCertificate,
		Artifact:  certificateId,
		Namespace: "",
	}}))
}

func TestListenerWithSingleDirectCertificateConfiguresBackendTLS(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	certificateId := "certificateId"
	ingress := ingressList.Items[0]
	ingress.Spec.TLS = []networkingv1.IngressTLS{}
	ingress.Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: certificateId}
	ingressList = &networkingv1.IngressList{Items: []networkingv1.Ingress{ingress}}

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	backendTLSConfig := stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(backendTLSConfig.Artifact).Should(Equal(certificateId))
	Expect(backendTLSConfig.Type).Should(Equal(ArtifactTypeCertificate))
	Expect(backendTLSConfig.Namespace).Should(Equal("default"))

	Expect(stateStore.GetTLSConfigForListener(943)).Should(Equal([]TlsConfig{{
		Type:      ArtifactTypeCertificate,
		Artifact:  certificateId,
		Namespace: "default",
	}}))
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
	Expect(lstTlsConfig.TlsConfigs).Should(Equal([]TlsConfig{{
		Type:      ArtifactTypeCertificate,
		Artifact:  certificateId,
		Namespace: "default",
	}}))
}

func TestListenerWithMultipleDirectCertificatesConfiguresBackendTLSFromFirstCertificate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	ingress := ingressList.Items[0]
	ingress.Spec.TLS = []networkingv1.IngressTLS{}
	ingress.Annotations = map[string]string{util.IngressListenerTlsCertificateAnnotation: "certificateA, certificateB, certificateA"}
	ingressList = &networkingv1.IngressList{Items: []networkingv1.Ingress{ingress}}

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	backendTLSConfig := stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(backendTLSConfig.Artifact).Should(Equal("certificateA"))
	Expect(backendTLSConfig.Type).Should(Equal(ArtifactTypeCertificate))
	Expect(backendTLSConfig.Namespace).Should(Equal("default"))

	Expect(stateStore.GetTLSConfigForListener(943)).Should(Equal([]TlsConfig{
		{
			Type:      ArtifactTypeCertificate,
			Artifact:  "certificateA",
			Namespace: "default",
		},
		{
			Type:      ArtifactTypeCertificate,
			Artifact:  "certificateB",
			Namespace: "default",
		},
	}))
}

func TestListenerTLSAggregationDeterministicOrderingAndDedupe(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)
	ingressList.Items[0], ingressList.Items[1] = ingressList.Items[1], ingressList.Items[0]

	setBackendTLSEnabled(&ingressList.Items[0], false)
	setBackendTLSEnabled(&ingressList.Items[1], false)
	ingressList.Items[0].Annotations[util.IngressListenerTlsCertificateAnnotation] = "certificateA,certificateC"
	ingressList.Items[1].Annotations[util.IngressListenerTlsCertificateAnnotation] = "certificateB,certificateA,certificateB"

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	Expect(stateStore.GetTLSConfigForListener(943)).Should(Equal([]TlsConfig{
		{
			Type:      ArtifactTypeCertificate,
			Artifact:  "certificateB",
			Namespace: "default",
		},
		{
			Type:      ArtifactTypeCertificate,
			Artifact:  "certificateA",
			Namespace: "default",
		},
		{
			Type:      ArtifactTypeSecret,
			Artifact:  "secret_name_one",
			Namespace: "default",
		},
		{
			Type:      ArtifactTypeCertificate,
			Artifact:  "certificateC",
			Namespace: "default",
		},
		{
			Type:      ArtifactTypeSecret,
			Artifact:  "secret_name_two",
			Namespace: "default",
		},
	}))
}

func TestValidateBackendTlsConflictWithMixedEnabledValues(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	sharedSecretName := "shared_secret_name"
	ingressList.Items[0].Spec.TLS[0].SecretName = sharedSecretName
	ingressList.Items[1].Spec.TLS[0].SecretName = sharedSecretName
	setBackendTLSEnabled(&ingressList.Items[0], true)
	setBackendTLSEnabled(&ingressList.Items[1], false)

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(BackendTlsEnabledConflictMessage, bsName)))
}

func TestValidateBackendTlsConflictWithMixedEnabledValuesAndNoBackendTLSInput(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	ingressList.Items[0].Spec.TLS = nil
	setBackendTLSEnabled(&ingressList.Items[0], true)
	setBackendTLSEnabled(&ingressList.Items[1], false)

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(BackendTlsEnabledConflictMessage, bsName)))
}

func TestValidateBackendTlsConflictWithDifferentArtifacts(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)
	setBackendTLSEnabled(&ingressList.Items[0], true)
	setBackendTLSEnabled(&ingressList.Items[1], true)

	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).Should(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(BackendTlsArtifactConflictMessage, bsName)))
}

func TestValidateBackendTlsNoConflictForMixedTLSAndNonTLSHostsSharedBackendSet(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	pathType := networkingv1.PathTypePrefix
	sharedSecretName := "shared_secret_name"
	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mixed-host-shared-backend-set",
			Namespace: "default",
			Annotations: map[string]string{
				util.IngressBackendTlsEnabledAnnotation: "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{"secure.foo.bar.com"},
					SecretName: sharedSecretName,
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: "plain.foo.bar.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: &pathType,
									Path:     "/plain",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "tls-test",
											Port: networkingv1.ServiceBackendPort{Number: 943},
										},
									},
								},
							},
						},
					},
				},
				{
					Host: "secure.foo.bar.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: &pathType,
									Path:     "/secure",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "tls-test",
											Port: networkingv1.ServiceBackendPort{Number: 943},
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

	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ingress}}
	testService := util.GetServiceListResource("default", "tls-test", 943)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "tls-test", 943)
	backendTLSConfig := stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(backendTLSConfig.Artifact).Should(Equal(sharedSecretName))
	Expect(backendTLSConfig.Type).Should(Equal(ArtifactTypeSecret))
	Expect(backendTLSConfig.Namespace).Should(Equal("default"))
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

	ingressBs := stateStore.GetIngressBackendSets("default", ingressName)
	Expect(len(ingressBs)).Should(Equal(3))

	ingressListeners := stateStore.GetIngressPorts("default", ingressName)
	Expect(len(ingressListeners)).Should(Equal(2))

	Expect(len(stateStore.IngressGroupState.BackendSetTLSConfigMap)).Should(Equal(3))
	Expect(len(stateStore.IngressGroupState.ListenerTLSConfigMap)).Should(Equal(3))

	listenerTlsConfigs := stateStore.GetTLSConfigForListener(80)
	Expect(listenerTlsConfigs).Should(Equal([]TlsConfig{{
		Type:      ArtifactTypeSecret,
		Artifact:  "secret_name",
		Namespace: "default",
	}}))

	listenerTlsConfigs = stateStore.GetTLSConfigForListener(90)
	Expect(listenerTlsConfigs).Should(Equal([]TlsConfig{{
		Type:      ArtifactTypeSecret,
		Artifact:  "secret_name",
		Namespace: "default",
	}}))

	listenerTlsConfigs = stateStore.GetTLSConfigForListener(100)
	Expect(listenerTlsConfigs).Should(BeNil())

	bsName := util.GenerateBackendSetName("default", "tls-test", 100)
	backendTLSConfig := stateStore.GetTLSConfigForBackendSet(bsName)
	Expect(backendTLSConfig.Artifact).Should(Equal(""))
	Expect(backendTLSConfig.Type).Should(Equal(""))
	Expect(backendTLSConfig.Namespace).Should(Equal(""))
}

func TestIngressStateNamespaceSafeKeys(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := util.ReadResourceAsIngressList(TlsConfigValidationsFilePath)

	// Two ingresses with same name in different namespaces should not collide in desired state.
	ingressList.Items[1].Name = ingressList.Items[0].Name
	ingressList.Items[1].Namespace = "other"

	defaultServices := util.GetServiceListResource("default", "tls-test", 943)
	otherServices := util.GetServiceListResource("other", "tls-test", 943)
	testService := &v1.ServiceList{Items: append(defaultServices.Items, otherServices.Items...)}

	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())
	Expect(len(stateStore.IngressState)).Should(Equal(2))

	defaultIngressPorts := stateStore.GetIngressPorts("default", ingressList.Items[0].Name)
	Expect(defaultIngressPorts).ShouldNot(BeNil())

	otherIngressPorts := stateStore.GetIngressPorts("other", ingressList.Items[0].Name)
	Expect(otherIngressPorts).ShouldNot(BeNil())
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

func TestValidateGRPCProtocolConfig(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()

	ingressList := util.ReadResourceAsIngressList(ListenerProtocolConfigValidationsFilePath)
	ingressList.Items[0].Annotations[util.IngressProtocolAnnotation] = "grpc"
	ingressList.Items[1].Annotations[util.IngressProtocolAnnotation] = util.ProtocolGRPC

	testService := util.GetServiceListResource("default", "test-protocol-annotation", 900)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	actualProtocol := stateStore.GetListenerProtocol(900)
	Expect(actualProtocol).Should(Equal(util.ProtocolGRPC))
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

func TestListenerBackendSetParticipationIncludesDefaultAndPathBackendSets(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingress := makeIngressWithPaths("ing-two-services", map[string]string{
		util.IngressHttpListenerPortAnnotation: "80",
	},
		ingressPathSpec{Path: "/app", ServiceName: "svc-a", ServicePort: 8080},
		ingressPathSpec{Path: "/api", ServiceName: "svc-b", ServicePort: 8081},
	)
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ingress}}

	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, &v1.ServiceList{})
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	expectedBackendSets := sets.NewString(
		util.DefaultBackendSetName,
		util.GenerateBackendSetName("default", "svc-a", 8080),
		util.GenerateBackendSetName("default", "svc-b", 8081),
	)
	Expect(stateStore.GetListenerDefaultBackendSet(80)).To(Equal(util.DefaultBackendSetName))
	Expect(stateStore.GetBackendSetsForListener(80).List()).To(Equal(expectedBackendSets.List()))
}

func TestListenerBackendSetParticipationIncludesMultipleIngressesSharingListener(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	annotations := map[string]string{util.IngressHttpListenerPortAnnotation: "80"}
	ingressA := makeIngressWithPaths("ing-a", annotations, ingressPathSpec{Path: "/a", ServiceName: "svc-a", ServicePort: 8080})
	ingressB := makeIngressWithPaths("ing-b", annotations, ingressPathSpec{Path: "/b", ServiceName: "svc-b", ServicePort: 8081})
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ingressB, ingressA}}

	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, &v1.ServiceList{})
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	expectedBackendSets := sets.NewString(
		util.DefaultBackendSetName,
		util.GenerateBackendSetName("default", "svc-a", 8080),
		util.GenerateBackendSetName("default", "svc-b", 8081),
	)
	Expect(stateStore.GetBackendSetsForListener(80).List()).To(Equal(expectedBackendSets.List()))
}

func TestListenerBackendSetParticipationDedupesRoutingPolicyBackendSetReferences(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingress := makeIngressWithPaths("ing-routing-policy", map[string]string{
		util.IngressHttpListenerPortAnnotation: "80",
	},
		ingressPathSpec{Path: "/", ServiceName: "svc-a", ServicePort: 8080},
		ingressPathSpec{Path: "/api", ServiceName: "svc-a", ServicePort: 8080},
	)
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ingress}}

	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, &v1.ServiceList{})
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)

	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	// HTTP routing policies are derived from path rules; two route-policy actions
	// pointing at the same backend set should still produce one participant.
	expectedBackendSets := sets.NewString(
		util.DefaultBackendSetName,
		util.GenerateBackendSetName("default", "svc-a", 8080),
	)
	Expect(stateStore.GetBackendSetsForListener(80).List()).To(Equal(expectedBackendSets.List()))
}

func TestIngressStateTLSPolicyPropagatesToListenerPortAndBackendSetName(t *testing.T) {
	RegisterTestingT(t)

	ingress := makeIngress("ing-tls-policy", map[string]string{
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressListenerSslConfigAnnotation:      `{"protocols":["TLSv1.3","TLSv1.2"]}`,
		util.IngressBackendSetSslConfigAnnotation:    `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3"}`,
	}, "svc-a", 9443)

	stateStore, err := buildStateForTLSPolicyTest(t, ingress)
	Expect(err).NotTo(HaveOccurred())

	listenerPolicy := stateStore.GetTLSPolicyForListener(9443)
	Expect(listenerPolicy).NotTo(BeNil())
	Expect(listenerPolicy.HasCipherSuiteName).To(BeFalse())
	Expect(listenerPolicy.HasProtocols).To(BeTrue())
	Expect(listenerPolicy.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))

	bsName := util.GenerateBackendSetName("default", "svc-a", 9443)
	backendSetPolicy := stateStore.GetTLSPolicyForBackendSet(bsName)
	Expect(backendSetPolicy).NotTo(BeNil())
	Expect(backendSetPolicy.HasCipherSuiteName).To(BeTrue())
	Expect(backendSetPolicy.CipherSuiteName).To(Equal("oci-tls-12-13-ssl-cipher-suite-v3"))
	Expect(backendSetPolicy.HasProtocols).To(BeFalse())
}

func TestIngressStateTLSPolicyMissingAnnotationMeansNoOpinion(t *testing.T) {
	RegisterTestingT(t)

	ingress := makeIngress("ing-no-policy", map[string]string{
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
	}, "svc-a", 9443)

	stateStore, err := buildStateForTLSPolicyTest(t, ingress)
	Expect(err).NotTo(HaveOccurred())

	Expect(stateStore.GetTLSPolicyForListener(9443)).To(BeNil())
	Expect(stateStore.GetTLSPolicyForBackendSet(util.GenerateBackendSetName("default", "svc-a", 9443))).To(BeNil())
}

func TestIngressStateTLSPolicyMergesComplementaryListenerAnnotations(t *testing.T) {
	RegisterTestingT(t)

	ingressA := makeIngress("ing-a", map[string]string{
		util.IngressHttpsListenerPortAnnotation:      "443",
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressListenerSslConfigAnnotation:      `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3"}`,
	}, "svc-a", 8080)
	ingressB := makeIngress("ing-b", map[string]string{
		util.IngressHttpsListenerPortAnnotation:      "443",
		util.IngressListenerTlsCertificateAnnotation: "certificateB",
		util.IngressListenerSslConfigAnnotation:      `{"protocols":["TLSv1.3","TLSv1.2"]}`,
	}, "svc-b", 8081)

	stateStore, err := buildStateForTLSPolicyTest(t, ingressB, ingressA)
	Expect(err).NotTo(HaveOccurred())

	listenerPolicy := stateStore.GetTLSPolicyForListener(443)
	Expect(listenerPolicy).NotTo(BeNil())
	Expect(listenerPolicy.HasCipherSuiteName).To(BeTrue())
	Expect(listenerPolicy.CipherSuiteName).To(Equal("oci-tls-12-13-ssl-cipher-suite-v3"))
	Expect(listenerPolicy.HasProtocols).To(BeTrue())
	Expect(listenerPolicy.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestIngressStateTLSPolicyMergesComplementaryBackendSetAnnotations(t *testing.T) {
	RegisterTestingT(t)

	ingressA := makeIngress("ing-a", map[string]string{
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressBackendSetSslConfigAnnotation:    `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3"}`,
	}, "svc-a", 9443)
	ingressB := makeIngress("ing-b", map[string]string{
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressBackendSetSslConfigAnnotation:    `{"protocols":["TLSv1.3","TLSv1.2"]}`,
	}, "svc-a", 9443)

	stateStore, err := buildStateForTLSPolicyTest(t, ingressB, ingressA)
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "svc-a", 9443)
	backendSetPolicy := stateStore.GetTLSPolicyForBackendSet(bsName)
	Expect(backendSetPolicy).NotTo(BeNil())
	Expect(backendSetPolicy.HasCipherSuiteName).To(BeTrue())
	Expect(backendSetPolicy.CipherSuiteName).To(Equal("oci-tls-12-13-ssl-cipher-suite-v3"))
	Expect(backendSetPolicy.HasProtocols).To(BeTrue())
	Expect(backendSetPolicy.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestIngressStateTLSPolicyEquivalentProtocolSetsDoNotConflict(t *testing.T) {
	RegisterTestingT(t)

	ingressA := makeIngress("ing-a", map[string]string{
		util.IngressHttpsListenerPortAnnotation:      "443",
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressListenerSslConfigAnnotation:      `{"protocols":["TLSv1.3","TLSv1.2"]}`,
	}, "svc-a", 8080)
	ingressB := makeIngress("ing-b", map[string]string{
		util.IngressHttpsListenerPortAnnotation:      "443",
		util.IngressListenerTlsCertificateAnnotation: "certificateB",
		util.IngressListenerSslConfigAnnotation:      `{"protocols":["TLSv1.2","TLSv1.3","TLSv1.2"]}`,
	}, "svc-b", 8081)

	stateStore, err := buildStateForTLSPolicyTest(t, ingressB, ingressA)
	Expect(err).NotTo(HaveOccurred())

	listenerPolicy := stateStore.GetTLSPolicyForListener(443)
	Expect(listenerPolicy).NotTo(BeNil())
	Expect(listenerPolicy.HasProtocols).To(BeTrue())
	Expect(listenerPolicy.Protocols).To(Equal([]string{"TLSv1.2", "TLSv1.3"}))
}

func TestIngressStateTLSPolicyConflictingListenerAnnotationsFailDeterministically(t *testing.T) {
	RegisterTestingT(t)

	ingressA := makeIngress("ing-a", map[string]string{
		util.IngressHttpsListenerPortAnnotation:      "443",
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressListenerSslConfigAnnotation:      `{"cipherSuiteName":"oci-tls-12-13-ssl-cipher-suite-v3"}`,
	}, "svc-a", 8080)
	ingressB := makeIngress("ing-b", map[string]string{
		util.IngressHttpsListenerPortAnnotation:      "443",
		util.IngressListenerTlsCertificateAnnotation: "certificateB",
		util.IngressListenerSslConfigAnnotation:      `{"cipherSuiteName":"oci-default-http2-tls-12-13-ssl-cipher-suite-v1"}`,
	}, "svc-b", 8081)

	_, errAB := buildStateForTLSPolicyTest(t, ingressA, ingressB)
	_, errBA := buildStateForTLSPolicyTest(t, ingressB, ingressA)
	Expect(errAB).To(MatchError(errBA.Error()))
	Expect(errAB.Error()).To(ContainSubstring("TLSPolicyConflict: listener 443"))
	Expect(errAB.Error()).To(ContainSubstring("field cipherSuiteName"))
	Expect(errAB.Error()).To(ContainSubstring("default/ing-a"))
	Expect(errAB.Error()).To(ContainSubstring("default/ing-b"))
}

func TestIngressStateTLSPolicyConflictingBackendSetAnnotationsFailDeterministically(t *testing.T) {
	RegisterTestingT(t)

	ingressA := makeIngress("ing-a", map[string]string{
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressBackendSetSslConfigAnnotation:    `{"protocols":["TLSv1.2"]}`,
	}, "svc-a", 9443)
	ingressB := makeIngress("ing-b", map[string]string{
		util.IngressListenerTlsCertificateAnnotation: "certificateA",
		util.IngressBackendSetSslConfigAnnotation:    `{"protocols":["TLSv1.3"]}`,
	}, "svc-a", 9443)

	_, errAB := buildStateForTLSPolicyTest(t, ingressA, ingressB)
	_, errBA := buildStateForTLSPolicyTest(t, ingressB, ingressA)
	bsName := util.GenerateBackendSetName("default", "svc-a", 9443)
	Expect(errAB).To(MatchError(errBA.Error()))
	Expect(errAB.Error()).To(ContainSubstring("TLSPolicyConflict: backend set " + bsName))
	Expect(errAB.Error()).To(ContainSubstring("field protocols"))
	Expect(errAB.Error()).To(ContainSubstring("default/ing-a"))
	Expect(errAB.Error()).To(ContainSubstring("default/ing-b"))
}

func TestIngressStateTLSPolicyAnnotationsAreInertWithoutTLSInputs(t *testing.T) {
	RegisterTestingT(t)

	ingress := makeIngress("ing-no-tls", map[string]string{
		util.IngressListenerSslConfigAnnotation:   `{not-json`,
		util.IngressBackendSetSslConfigAnnotation: `{not-json`,
	}, "svc-a", 8080)

	stateStore, err := buildStateForTLSPolicyTest(t, ingress)
	Expect(err).NotTo(HaveOccurred())
	Expect(stateStore.GetTLSPolicyForListener(8080)).To(BeNil())
	Expect(stateStore.GetTLSPolicyForBackendSet(util.GenerateBackendSetName("default", "svc-a", 8080))).To(BeNil())
}

func buildStateForTLSPolicyTest(t *testing.T, ingresses ...networkingv1.Ingress) (*StateStore, error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()
	ingressList := &networkingv1.IngressList{Items: ingresses}
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, &v1.ServiceList{})
	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	return stateStore, stateStore.BuildState(&ingressClassList.Items[0])
}

type ingressPathSpec struct {
	Path        string
	ServiceName string
	ServicePort int32
}

func makeIngressWithPaths(name string, annotations map[string]string, paths ...ingressPathSpec) networkingv1.Ingress {
	pathType := networkingv1.PathType("Prefix")
	ingressAnnotations := map[string]string{}
	for key, value := range annotations {
		ingressAnnotations[key] = value
	}

	httpPaths := make([]networkingv1.HTTPIngressPath, 0, len(paths))
	for _, path := range paths {
		httpPaths = append(httpPaths, networkingv1.HTTPIngressPath{
			Path:     path.Path,
			PathType: &pathType,
			Backend: networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: path.ServiceName,
					Port: networkingv1.ServiceBackendPort{Number: path.ServicePort},
				},
			},
		})
	}

	return networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Annotations: ingressAnnotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: httpPaths,
						},
					},
				},
			},
		},
	}
}

// Helper to build a minimal Ingress targeting a service/port with optional annotations
func makeIngress(name string, annotations map[string]string, svcName string, port int32) networkingv1.Ingress {
	return makeIngressWithPaths(name, annotations, ingressPathSpec{Path: "/", ServiceName: svcName, ServicePort: port})
}

func TestValidateSessionPersistence_NoConflict_LbCookie(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	// OCI SDK envelope: LB cookie
	ann := map[string]string{
		util.IngressLbCookieJSONAnnotation: `{}`,
	}
	ing := makeIngress("ing-lb-cookie", ann, "test-persist", 8080)
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ing}}

	testService := util.GetServiceListResource("default", "test-persist", 8080)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "test-persist", 8080)
	app, lb := stateStore.GetBackendSetSessionPersistence(bsName)
	Expect(app).To(BeNil())
	Expect(lb).NotTo(BeNil())
}

func TestValidateSessionPersistence_NoConflict_AppCookie(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	// OCI SDK envelope: App cookie
	ann := map[string]string{
		util.IngressSessionPersistenceJSONAnnotation: `{"cookieName":"APPSESS"}`,
	}
	ing := makeIngress("ing-app-cookie", ann, "test-persist", 8081)
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ing}}

	testService := util.GetServiceListResource("default", "test-persist", 8081)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "test-persist", 8081)
	app, lb := stateStore.GetBackendSetSessionPersistence(bsName)
	Expect(lb).To(BeNil())
	Expect(app).NotTo(BeNil())
	Expect(*app.CookieName).To(Equal("APPSESS"))
}

func TestValidateSessionPersistence_ReconcileOnConflict(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassList()

	// Two ingresses pointing to the same backend set but different persistence modes -> reconcile (prefer lbCookie)
	annLb := map[string]string{
		util.IngressLbCookieJSONAnnotation: `{}`,
	}
	annApp := map[string]string{
		util.IngressSessionPersistenceJSONAnnotation: `{"cookieName":"APPSESS"}`,
	}
	ing1 := makeIngress("ing-lb", annLb, "test-persist", 8082)
	ing2 := makeIngress("ing-app", annApp, "test-persist", 8082)
	ingressList := &networkingv1.IngressList{Items: []networkingv1.Ingress{ing1, ing2}}

	testService := util.GetServiceListResource("default", "test-persist", 8082)
	ingressClassLister, ingressLister, serviceLister := setUp(ctx, ingressClassList, ingressList, testService)

	stateStore := NewStateStore(ingressClassLister, ingressLister, serviceLister, nil)
	err := stateStore.BuildState(&ingressClassList.Items[0])
	Expect(err).NotTo(HaveOccurred())

	bsName := util.GenerateBackendSetName("default", "test-persist", 8082)
	app, lb := stateStore.GetBackendSetSessionPersistence(bsName)
	Expect(app).To(BeNil())
	Expect(lb).NotTo(BeNil())
}
