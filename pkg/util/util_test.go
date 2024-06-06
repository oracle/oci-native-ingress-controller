/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */
package util

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/util/sets"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/informers"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	fake2 "k8s.io/client-go/kubernetes/typed/networking/v1/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func TestGenerateBackendSetName(t *testing.T) {
	RegisterTestingT(t)
	bsName := GenerateBackendSetName("default", "serviceA", 80)
	Expect(bsName).Should(Equal("bs_cfb8fa8a831f46d"))

	// Make sure it is consistent
	bsNameNew := GenerateBackendSetName("default", "serviceA", 80)
	Expect(bsNameNew).Should(Equal(bsName))

	bsNameTest := GenerateBackendSetName("test", "serviceA", 80)
	Expect(bsNameNew).Should(Not(Equal(bsNameTest)))

	bsNameLong := GenerateBackendSetName("LoooooongNameSpace", "SomeLoooooooooooooooongServiceName", 80)
	Expect(len(bsNameLong) < 32).Should(Equal(true))
}

func TestGetIngressClassCompartmentId(t *testing.T) {
	RegisterTestingT(t)

	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			CompartmentId: "  ",
		},
	}

	defaultCompartment := "defaultCompartment"
	result := GetIngressClassCompartmentId(&icp, defaultCompartment)
	Expect(result).Should(Equal(defaultCompartment))

	someCompartment := "someCompartment"
	icp.Spec.CompartmentId = someCompartment

	result = GetIngressClassCompartmentId(&icp, defaultCompartment)
	Expect(result).Should(Equal(someCompartment))
}

func TestGetIngressClassLoadBalancerName(t *testing.T) {
	RegisterTestingT(t)

	ingressName := "someIngress"
	ic := networkingv1.IngressClass{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: ingressName,
		},
	}
	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			LoadBalancerName: "  ",
		},
	}

	result := GetIngressClassLoadBalancerName(&ic, &icp)
	Expect(result).Should(Equal(fmt.Sprintf("k8s-%s", ic.Name)))

	someLoadBalancer := "someLoadBalancer"
	icp.Spec.LoadBalancerName = someLoadBalancer

	result = GetIngressClassLoadBalancerName(&ic, &icp)
	Expect(result).Should(Equal(someLoadBalancer))
}

func TestGetIngressClassSubnetId(t *testing.T) {
	RegisterTestingT(t)

	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			SubnetId: "  ",
		},
	}

	defaultSubnet := "defaultSubnet"
	result := GetIngressClassSubnetId(&icp, defaultSubnet)
	Expect(result).Should(Equal(defaultSubnet))

	someSubnet := "someSubnet"
	icp.Spec.SubnetId = someSubnet

	result = GetIngressClassSubnetId(&icp, defaultSubnet)
	Expect(result).Should(Equal(someSubnet))
}

func TestGetIngressPolicy(t *testing.T) {
	RegisterTestingT(t)
	policy := "IP_HASH"
	i := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressPolicyAnnotation: policy},
		},
	}

	result := GetIngressPolicy(&i)
	Expect(result).Should(Equal(policy))

	i.Annotations = nil

	result = GetIngressPolicy(&i)
	Expect(result).Should(Equal(DefaultBackendSetRoutingPolicy))
}

func TestGetIngressProtocol(t *testing.T) {
	RegisterTestingT(t)
	protocol := "http2"
	i := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressProtocolAnnotation: protocol},
		},
	}

	result := GetIngressProtocol(&i)
	Expect(result).Should(Equal(strings.ToUpper(protocol)))

	i.Annotations = nil
	result = GetIngressProtocol(&i)
	Expect(result).Should(Equal(ProtocolHTTP))
}

func TestGetIngressClassLoadBalancerId(t *testing.T) {
	RegisterTestingT(t)
	lbId := "lbId"
	ic := networkingv1.IngressClass{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressClassLoadBalancerIdAnnotation: lbId},
		},
	}

	result := GetIngressClassLoadBalancerId(&ic)
	Expect(result).Should(Equal(lbId))

	ic.Annotations = nil
	result = GetIngressClassLoadBalancerId(&ic)
	Expect(result).Should(Equal(""))
}

func TestGetListenerTlsCertificateOcid(t *testing.T) {
	RegisterTestingT(t)
	certOcid := "certOcid"
	i := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressListenerTlsCertificateAnnotation: certOcid},
		},
	}

	result := GetListenerTlsCertificateOcid(&i)
	Expect(*result).Should(Equal(certOcid))

	i.Annotations = nil

	result = GetListenerTlsCertificateOcid(&i)
	Expect(result).To(BeNil())
}

func TestGetBackendTlsEnabled(t *testing.T) {
	RegisterTestingT(t)
	i := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressBackendTlsEnabledAnnotation: "true"},
		},
	}
	result := GetBackendTlsEnabled(&i)
	Expect(result).Should(Equal(true))

	i = networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressBackendTlsEnabledAnnotation: "false"},
		},
	}
	result = GetBackendTlsEnabled(&i)
	Expect(result).Should(Equal(false))

	i = networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressBackendTlsEnabledAnnotation: "scam"},
		},
	}
	result = GetBackendTlsEnabled(&i)
	Expect(result).Should(Equal(true))

	i = networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressBackendTlsEnabledAnnotation: ""},
		},
	}
	result = GetBackendTlsEnabled(&i)
	Expect(result).Should(Equal(true))
}

func TestGetIngressHealthCheckProtocol(t *testing.T) {
	RegisterTestingT(t)
	protocol := "http"
	i := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckProtocolAnnotation: protocol},
		},
	}

	result := GetIngressHealthCheckProtocol(&i)
	Expect(result).Should(Equal(strings.ToUpper(protocol)))

	i.Annotations = nil
	result = GetIngressHealthCheckProtocol(&i)
	Expect(result).Should(Equal(ProtocolTCP))
}

func TestGetIngressHealthCheckPath(t *testing.T) {
	RegisterTestingT(t)
	path := "/health"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckPathAnnotation: path},
		},
	}

	result := GetIngressHealthCheckPath(&i)
	Expect(result).Should(Equal(path))

	i.Annotations = nil
	result = GetIngressHealthCheckPath(&i)
	Expect(result).Should(Equal(""))
}

func TestGetIngressHealthCheckPort(t *testing.T) {
	RegisterTestingT(t)
	port := "9090"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckPortAnnotation: port},
		},
	}

	result, err := GetIngressHealthCheckPort(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(9090))

	i.Annotations = map[string]string{IngressHealthCheckPortAnnotation: "InvalidPortValue"}
	_, err = GetIngressHealthCheckPort(&i)
	Expect(err).NotTo(BeNil())

	i.Annotations = nil
	result, err = GetIngressHealthCheckPort(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(DefaultHealthCheckPort))
}

func TestGetIngressHealthCheckIntervalMilliseconds(t *testing.T) {
	RegisterTestingT(t)
	interval := "1000"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckIntervalMillisecondsAnnotation: interval},
		},
	}

	result, err := GetIngressHealthCheckIntervalMilliseconds(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(1000))

	i.Annotations = map[string]string{IngressHealthCheckIntervalMillisecondsAnnotation: "InvalidIntervalValue"}
	_, err = GetIngressHealthCheckIntervalMilliseconds(&i)
	Expect(err).NotTo(BeNil())

	i.Annotations = nil
	result, err = GetIngressHealthCheckIntervalMilliseconds(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(DefaultHealthCheckIntervalMilliSeconds))
}

func TestGetIngressHealthCheckTimeoutMilliseconds(t *testing.T) {
	RegisterTestingT(t)
	timeout := "1000"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckTimeoutMillisecondsAnnotation: timeout},
		},
	}

	result, err := GetIngressHealthCheckTimeoutMilliseconds(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(1000))

	i.Annotations = map[string]string{IngressHealthCheckTimeoutMillisecondsAnnotation: "InvalidTimeoutValue"}
	_, err = GetIngressHealthCheckTimeoutMilliseconds(&i)
	Expect(err).NotTo(BeNil())

	i.Annotations = nil
	result, err = GetIngressHealthCheckTimeoutMilliseconds(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(DefaultHealthCheckTimeOutMilliSeconds))
}

func TestGetIngressHealthCheckRetries(t *testing.T) {
	RegisterTestingT(t)
	retries := "3"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckRetriesAnnotation: retries},
		},
	}

	result, err := GetIngressHealthCheckRetries(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(3))

	i.Annotations = map[string]string{IngressHealthCheckRetriesAnnotation: "InvalidRetryValue"}
	_, err = GetIngressHealthCheckRetries(&i)
	Expect(err).NotTo(BeNil())

	i.Annotations = nil
	result, err = GetIngressHealthCheckRetries(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(DefaultHealthCheckRetries))
}

func TestGetIngressHealthCheckReturnCode(t *testing.T) {
	RegisterTestingT(t)
	returnCode := "200"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckReturnCodeAnnotation: returnCode},
		},
	}

	result, err := GetIngressHealthCheckReturnCode(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(200))

	i.Annotations = map[string]string{IngressHealthCheckReturnCodeAnnotation: "InvalidReturnCodeValue"}
	_, err = GetIngressHealthCheckReturnCode(&i)
	Expect(err).NotTo(BeNil())

	i.Annotations = nil
	result, err = GetIngressHealthCheckReturnCode(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(result).Should(Equal(DefaultHealthCheckReturnCode))
}

func TestGetIngressHealthCheckResponseBodyRegex(t *testing.T) {
	RegisterTestingT(t)
	regex := "/"
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckResponseBodyRegexAnnotation: regex},
		},
	}

	result := GetIngressHealthCheckResponseBodyRegex(&i)
	Expect(result).Should(Equal(regex))

	i.Annotations = nil
	result = GetIngressHealthCheckResponseBodyRegex(&i)
	Expect(result).Should(Equal(""))
}

func TestGetIngressHealthCheckForcePlainText(t *testing.T) {
	RegisterTestingT(t)
	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{IngressHealthCheckForcePlainTextAnnotation: "true"},
		},
	}

	result := GetIngressHealthCheckForcePlainText(&i)
	Expect(result).Should(Equal(true))

	i.Annotations = map[string]string{IngressHealthCheckForcePlainTextAnnotation: "InvalidFlagValue"}
	result = GetIngressHealthCheckForcePlainText(&i)
	Expect(result).Should(Equal(false))

	i.Annotations = nil
	result = GetIngressHealthCheckForcePlainText(&i)
	Expect(result).Should(Equal(false))
}

func TestPathToRoutePolicyName(t *testing.T) {
	RegisterTestingT(t)

	pathType := networkingv1.PathType("Exact")
	path := networkingv1.HTTPIngressPath{
		Path:     "/test",
		PathType: &pathType,
		Backend: networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: "TestService",
				Port: networkingv1.ServiceBackendPort{},
			},
		},
	}

	result := PathToRoutePolicyName("ingressName", "foo.bar.com", path)
	Expect(result).Should(Equal("k8s_925195924b"))

	result = PathToRoutePolicyName("newIngressName", "anything.bar.com", path)
	Expect(result).Should(Equal("k8s_1ef318b767"))
}

func TestGenerateListenerName(t *testing.T) {
	RegisterTestingT(t)

	result := GenerateListenerName(8080)
	Expect(result).Should(Equal("route_8080"))
}

func TestGetPodReadinessCondition(t *testing.T) {
	RegisterTestingT(t)

	pathType := networkingv1.PathType("Exact")
	path := networkingv1.HTTPIngressPath{
		Path:     "/test",
		PathType: &pathType,
		Backend: networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: "TestService",
				Port: networkingv1.ServiceBackendPort{},
			},
		},
	}

	result := GetPodReadinessCondition("ingressName", "foo.bar.com", path)
	Expect(result).Should(Equal(v1.PodConditionType("podreadiness.ingress.oraclecloud.com/k8s_925195924b")))
}

func TestGetIngressClass(t *testing.T) {
	RegisterTestingT(t)
	client := fakeclientset.NewSimpleClientset()

	ingressClassList := getIngressClassList()

	client.NetworkingV1().(*fake2.FakeNetworkingV1).
		PrependReactor("list", "ingressclasses", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, ingressClassList, nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassLister := ingressClassInformer.Lister()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)

	ingressClassName := "ingress-class"

	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClassName,
		},
	}

	ic, err := GetIngressClass(&i, ingressClassLister)
	Expect(err).NotTo(HaveOccurred())
	Expect(ic.Name).Should(Equal(ingressClassName))

	i.Spec.IngressClassName = nil
	ic, err = GetIngressClass(&i, ingressClassLister)
	Expect(err).NotTo(HaveOccurred())
	Expect(ic.Name).Should(Equal("default-ingress-class"))

	i.Spec.IngressClassName = common.String("unknownIngress")
	ic, err = GetIngressClass(&i, ingressClassLister)
	Expect(err).To(HaveOccurred())
}

func TestGetHealthChecker(t *testing.T) {
	RegisterTestingT(t)

	annotations := map[string]string{
		IngressHealthCheckProtocolAnnotation:             ProtocolTCP,
		IngressHealthCheckPortAnnotation:                 "9090",
		IngressHealthCheckRetriesAnnotation:              "3",
		IngressHealthCheckTimeoutMillisecondsAnnotation:  "3000",
		IngressHealthCheckIntervalMillisecondsAnnotation: "10000",
	}

	i := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: annotations,
		},
	}

	hcDetails, err := GetHealthChecker(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(*hcDetails.Protocol).Should(Equal(ProtocolTCP))
	Expect(*hcDetails.Port).Should(Equal(9090))
	Expect(*hcDetails.Retries).Should(Equal(3))
	Expect(*hcDetails.TimeoutInMillis).Should(Equal(3000))
	Expect(*hcDetails.IntervalInMillis).Should(Equal(10000))

	annotations[IngressHealthCheckProtocolAnnotation] = ProtocolHTTP
	annotations[IngressHealthCheckReturnCodeAnnotation] = "200"
	annotations[IngressHealthCheckResponseBodyRegexAnnotation] = "/"
	annotations[IngressHealthCheckPathAnnotation] = "/path"
	annotations[IngressHealthCheckForcePlainTextAnnotation] = "true"

	hcDetails, err = GetHealthChecker(&i)
	Expect(err).NotTo(HaveOccurred())
	Expect(*hcDetails.Protocol).Should(Equal(ProtocolHTTP))
	Expect(*hcDetails.Port).Should(Equal(9090))
	Expect(*hcDetails.Retries).Should(Equal(3))
	Expect(*hcDetails.TimeoutInMillis).Should(Equal(3000))
	Expect(*hcDetails.IntervalInMillis).Should(Equal(10000))
	Expect(*hcDetails.ReturnCode).Should(Equal(200))
	Expect(*hcDetails.ResponseBodyRegex).Should(Equal("/"))
	Expect(*hcDetails.UrlPath).Should(Equal("/path"))
	Expect(*hcDetails.IsForcePlainText).Should(Equal(true))
}

func TestIsIngressDeleting(t *testing.T) {
	RegisterTestingT(t)

	now := metav1.Now()
	i := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &now,
		},
	}
	Expect(IsIngressDeleting(&i)).Should(Equal(true))

	i.DeletionTimestamp = nil
	Expect(IsIngressDeleting(&i)).Should(Equal(false))
}

func TestPathToServiceAndTargetPort(t *testing.T) {
	RegisterTestingT(t)
	namespace := "test"
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "TestService",
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": "multi-test"},
			Ports: []v1.ServicePort{
				{
					Name:     "abcd",
					Protocol: v1.ProtocolTCP,
					Port:     443,
					TargetPort: intstr.IntOrString{
						IntVal: 8080,
					},
					NodePort: 30224,
				},
				{
					Name:     "efgh",
					Protocol: v1.ProtocolTCP,
					Port:     444,
					TargetPort: intstr.IntOrString{
						IntVal: 8080,
					},
					NodePort: 30225,
				},
				{
					Name:     "ijkl",
					Protocol: v1.ProtocolTCP,
					Port:     445,
					TargetPort: intstr.IntOrString{
						IntVal: 8080,
					},
					NodePort: 30226,
				},
			},
			ExternalTrafficPolicy: v1.ServiceExternalTrafficPolicyTypeLocal,
		},
	}
	ingBackend1 := networkingv1.IngressServiceBackend{
		Name: "TestService1",
		Port: networkingv1.ServiceBackendPort{
			Number: 443,
			Name:   "abcd",
		},
	}
	ingBackend2 := networkingv1.IngressServiceBackend{
		Name: "TestService2",
		Port: networkingv1.ServiceBackendPort{
			Number: 444,
			Name:   "efgh",
		},
	}
	ingBackend3 := networkingv1.IngressServiceBackend{
		Name: "TestService3",
		Port: networkingv1.ServiceBackendPort{
			Number: 445,
		},
	}

	runTests(svc, ingBackend1, namespace, "TestService1", "443", "30224")
	runTests(svc, ingBackend2, namespace, "TestService2", "444", "30225")
	runTests(svc, ingBackend3, namespace, "TestService3", "445", "30226")
}

func runTests(svc *v1.Service, ingBackend networkingv1.IngressServiceBackend, namespace string, expectedSvcName string, expectedPort string, np string) {
	svcName, svcPort, nodePort, err := PathToServiceAndTargetPort(svc, ingBackend, namespace, true)
	Expect(err == nil).Should(Equal(true))
	Expect(svcName).Should(Equal(expectedSvcName))
	Expect(strconv.Itoa(int(svcPort))).Should(Equal(expectedPort))
	Expect(strconv.Itoa(int(nodePort))).Should(Equal(np))
}

func getIngressClassList() *networkingv1.IngressClassList {
	defaultIngressClass := getIngressClassResource("default-ingress-class", true, "oci.oraclecloud.com/native-ingress-controller")
	ingressClass := getIngressClassResource("ingress-class", false, "oci.oraclecloud.com/native-ingress-controller")
	ingressClassList := &networkingv1.IngressClassList{
		Items: []networkingv1.IngressClass{*defaultIngressClass, *ingressClass},
	}
	return ingressClassList
}

func getIngressClassResource(name string, isDefault bool, controller string) *networkingv1.IngressClass {
	return &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{IngressClassIsDefault: fmt.Sprint(isDefault)},
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controller,
		},
	}
}

func TestGetTimeDifferenceInSeconds(t *testing.T) {
	RegisterTestingT(t)
	Expect(GetTimeDifferenceInSeconds(1257894006000, 1257894009000)).Should(Equal(float64(3)))
	Expect(GetTimeDifferenceInSeconds(1257894006000, 1257894009600)).Should(Equal(3.6))
}

func TestDetermineListenerPort(t *testing.T) {
	RegisterTestingT(t)
	servicePort := int32(8080)
	httpPort := int32(80)
	httpsPort := int32(443)
	tlsConfiguredHosts := sets.NewString("tls-configured-1", "tls-configured-2")
	ingress := &networkingv1.Ingress{}
	annotations := map[string]string{}
	ingress.Annotations = annotations

	listenerPort, err := DetermineListenerPort(ingress, &tlsConfiguredHosts, "not-tls-configured", servicePort)
	Expect(err).NotTo(HaveOccurred())
	Expect(listenerPort).Should(Equal(servicePort))

	annotations[IngressHttpListenerPortAnnotation] = "80"
	listenerPort, err = DetermineListenerPort(ingress, &tlsConfiguredHosts, "not-tls-configured", servicePort)
	Expect(err).NotTo(HaveOccurred())
	Expect(listenerPort).Should(Equal(httpPort))

	listenerPort, err = DetermineListenerPort(ingress, &tlsConfiguredHosts, "tls-configured-1", servicePort)
	Expect(err).NotTo(HaveOccurred())
	Expect(listenerPort).Should(Equal(servicePort))

	annotations[IngressHttpsListenerPortAnnotation] = "443"
	listenerPort, err = DetermineListenerPort(ingress, &tlsConfiguredHosts, "tls-configured-1", servicePort)
	Expect(err).NotTo(HaveOccurred())
	Expect(listenerPort).Should(Equal(httpsPort))

	delete(annotations, IngressHttpsListenerPortAnnotation)
	annotations[IngressListenerTlsCertificateAnnotation] = "oci_cert"
	listenerPort, err = DetermineListenerPort(ingress, &tlsConfiguredHosts, "not-tls-configured", servicePort)
	Expect(err).NotTo(HaveOccurred())
	Expect(listenerPort).Should(Equal(servicePort))

	annotations[IngressHttpsListenerPortAnnotation] = "443"
	listenerPort, err = DetermineListenerPort(ingress, &tlsConfiguredHosts, "not-tls-configured", servicePort)
	Expect(err).NotTo(HaveOccurred())
	Expect(listenerPort).Should(Equal(httpsPort))
}

func TestIsBackendServiceEqual(t *testing.T) {
	RegisterTestingT(t)
	b1 := &networkingv1.IngressBackend{}
	b2 := &networkingv1.IngressBackend{
		Service: &networkingv1.IngressServiceBackend{
			Name: "default-backend-1",
			Port: networkingv1.ServiceBackendPort{
				Number: 80,
			},
		},
	}
	b3 := &networkingv1.IngressBackend{
		Service: &networkingv1.IngressServiceBackend{
			Name: "default-backend-1",
			Port: networkingv1.ServiceBackendPort{
				Number: 80,
			},
		},
	}
	b4 := &networkingv1.IngressBackend{
		Service: &networkingv1.IngressServiceBackend{
			Name: "default-backend-2",
			Port: networkingv1.ServiceBackendPort{
				Number: 80,
			},
		},
	}

	Expect(IsBackendServiceEqual(b2, b3)).To(BeTrue())

	Expect(IsBackendServiceEqual(b1, b2)).To(BeFalse())
	Expect(IsBackendServiceEqual(b1, b3)).To(BeFalse())
	Expect(IsBackendServiceEqual(b1, b4)).To(BeFalse())
	Expect(IsBackendServiceEqual(b2, b4)).To(BeFalse())
	Expect(IsBackendServiceEqual(b3, b4)).To(BeFalse())
}
