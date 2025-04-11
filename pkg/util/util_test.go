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
	corelisters "k8s.io/client-go/listers/core/v1"
	networkingListers "k8s.io/client-go/listers/networking/v1"
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

func TestGetIngressClassNetworkSecurityGroupIds(t *testing.T) {
	RegisterTestingT(t)

	getIngressClassWithNsgAnnotation := func(annotation string) *networkingv1.IngressClass {
		return &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{IngressClassNetworkSecurityGroupIdsAnnotation: annotation},
			},
		}
	}

	ingressClassWithNoAnnotation := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
	}
	ingressClassWithZeroNsg := getIngressClassWithNsgAnnotation("")
	ingressClassWithOneNsg := getIngressClassWithNsgAnnotation(" id")
	ingressClassWithMultipleNsg := getIngressClassWithNsgAnnotation("  id1,  id2,id3,id4  ")
	ingressClassWithRedundantCommas := getIngressClassWithNsgAnnotation("  id1, , id2,, id3,id4,, ")

	Expect(GetIngressClassNetworkSecurityGroupIds(ingressClassWithNoAnnotation)).
		Should(Equal([]string{}))
	Expect(GetIngressClassNetworkSecurityGroupIds(ingressClassWithZeroNsg)).
		Should(Equal([]string{}))
	Expect(GetIngressClassNetworkSecurityGroupIds(ingressClassWithOneNsg)).
		Should(Equal([]string{"id"}))
	Expect(GetIngressClassNetworkSecurityGroupIds(ingressClassWithMultipleNsg)).
		Should(Equal([]string{"id1", "id2", "id3", "id4"}))
	Expect(GetIngressClassNetworkSecurityGroupIds(ingressClassWithRedundantCommas)).
		Should(Equal([]string{"id1", "id2", "id3", "id4"}))
}

func TestGetIngressClassDeleteProtectionEnabled(t *testing.T) {
	RegisterTestingT(t)

	getIngressClassWithDeleteProtectionEnabledAnnotation := func(annotation string) *networkingv1.IngressClass {
		return &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{IngressClassDeleteProtectionEnabledAnnotation: annotation},
			},
		}
	}

	ingressClassWithNoAnnotation := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
	}
	ingressClassWithEmptyAnnotation := getIngressClassWithDeleteProtectionEnabledAnnotation("")
	ingressClassWithProtectionEnabled := getIngressClassWithDeleteProtectionEnabledAnnotation("true")
	ingressClassWithProtectionDisabled := getIngressClassWithDeleteProtectionEnabledAnnotation("false")
	ingressClassWithWrongAnnotation := getIngressClassWithDeleteProtectionEnabledAnnotation("n/a")

	Expect(GetIngressClassDeleteProtectionEnabled(ingressClassWithNoAnnotation)).Should(BeFalse())
	Expect(GetIngressClassDeleteProtectionEnabled(ingressClassWithEmptyAnnotation)).Should(BeFalse())
	Expect(GetIngressClassDeleteProtectionEnabled(ingressClassWithProtectionEnabled)).Should(BeTrue())
	Expect(GetIngressClassDeleteProtectionEnabled(ingressClassWithProtectionDisabled)).Should(BeFalse())
	Expect(GetIngressClassDeleteProtectionEnabled(ingressClassWithWrongAnnotation)).Should(BeFalse())
}

func TestGetIngressClassDefinedTags(t *testing.T) {
	RegisterTestingT(t)

	getIngressClassWithDefinedTagsAnnotation := func(annotation string) *networkingv1.IngressClass {
		return &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{IngressClassDefinedTagsAnnotation: annotation},
			},
		}
	}

	emptyTags := ""
	sampleTags := `{"n1": {"k1": "v1", "k2": {"ik1": "iv1"}},"n2": {"k3": "v3", "k4": ["a1", "a2"]}}`
	faultyTags := `{"n1": {"k1"}}`

	emptyTagsResult := map[string]map[string]interface{}{}
	sampleTagsResult := map[string]map[string]interface{}{
		"n1": {"k1": "v1", "k2": map[string]interface{}{"ik1": "iv1"}},
		"n2": {"k3": "v3", "k4": []interface{}{"a1", "a2"}},
	}

	ingressClassWithNoAnnotation := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
	ingressClassWithEmptyTags := getIngressClassWithDefinedTagsAnnotation(emptyTags)
	ingressClassWithSampleTags := getIngressClassWithDefinedTagsAnnotation(sampleTags)
	ingressClassWithFaultyTags := getIngressClassWithDefinedTagsAnnotation(faultyTags)

	verifyGetIngressClassDefinedTags := func(ic *networkingv1.IngressClass, shouldBePresent bool, shouldError bool,
		expectedResult map[string]map[string]interface{}) {
		present, res, err := GetIngressClassDefinedTags(ic)
		Expect(present).To(Equal(shouldBePresent))
		Expect(err != nil).To(Equal(shouldError))
		Expect(res).To(Equal(expectedResult))
	}

	verifyGetIngressClassDefinedTags(ingressClassWithNoAnnotation, false, false, emptyTagsResult)
	verifyGetIngressClassDefinedTags(ingressClassWithEmptyTags, true, false, emptyTagsResult)
	verifyGetIngressClassDefinedTags(ingressClassWithSampleTags, true, false, sampleTagsResult)
	verifyGetIngressClassDefinedTags(ingressClassWithFaultyTags, true, true, nil)
}

func TestGetIngressClassFreeformTags(t *testing.T) {
	RegisterTestingT(t)

	getIngressClassWithFreeformTagsAnnotation := func(annotation string) *networkingv1.IngressClass {
		return &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{IngressClassFreeformTagsAnnotation: annotation},
			},
		}
	}

	emptyTags := ""
	sampleTags := `{"k1": "v1", "k2": "v2","k3":"v3"}`
	faultyTags := `{"k1": v1}`

	emptyTagsResult := map[string]string{}
	sampleTagsResult := map[string]string{
		"k1": "v1", "k2": "v2", "k3": "v3",
	}

	ingressClassWithNoAnnotation := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
	ingressClassWithEmptyTags := getIngressClassWithFreeformTagsAnnotation(emptyTags)
	ingressClassWithSampleTags := getIngressClassWithFreeformTagsAnnotation(sampleTags)
	ingressClassWithFaultyTags := getIngressClassWithFreeformTagsAnnotation(faultyTags)

	verifyGetIngressClassFreeformTags := func(ic *networkingv1.IngressClass, shouldError bool, expectedResult map[string]string) {
		res, err := GetIngressClassFreeformTags(ic)
		Expect(err != nil).To(Equal(shouldError))
		Expect(res).To(Equal(expectedResult))
	}

	verifyGetIngressClassFreeformTags(ingressClassWithNoAnnotation, false, emptyTagsResult)
	verifyGetIngressClassFreeformTags(ingressClassWithEmptyTags, false, emptyTagsResult)
	verifyGetIngressClassFreeformTags(ingressClassWithSampleTags, false, sampleTagsResult)
	verifyGetIngressClassFreeformTags(ingressClassWithFaultyTags, true, nil)
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

	ingressClassLister := getIngressClassLister(getIngressClassList())

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
	Expect(err).NotTo(HaveOccurred())
	Expect(ic).To(BeNil())

	ingressClassLister = getIngressClassLister(&networkingv1.IngressClassList{Items: []networkingv1.IngressClass{}})

	i.Spec.IngressClassName = nil
	ic, err = GetIngressClass(&i, ingressClassLister)
	Expect(err).ToNot(HaveOccurred())
	Expect(ic).To(BeNil())
}

func getIngressClassLister(ingressClassList *networkingv1.IngressClassList) networkingListers.IngressClassLister {
	client := fakeclientset.NewSimpleClientset()

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
	return ingressClassLister
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

	serviceName1 := "service-with-single-port"
	serviceName2 := "service-with-multiple-ports"
	namespace := "test"

	svcWithSinglePort := GetServiceResource(namespace, serviceName1, []v1.ServicePort{
		GetServicePortResource("", 443, intstr.FromInt(1000), 30224),
	})

	svcWithMultiplePorts := GetServiceResource(namespace, serviceName2, []v1.ServicePort{
		GetServicePortResource("first_svc_port", 443, intstr.FromInt(1000), 30225),
		GetServicePortResource("second_svc_port", 444, intstr.FromString("port_name_for_2000"), 30226),
		GetServicePortResource("third_svc_port", 445, intstr.FromInt(3000), 30227),
		GetServicePortResource("fourth_svc_port", 446, intstr.FromString("port_name_for_4000"), 30228),
	})

	endpointsList := &v1.EndpointsList{
		Items: []v1.Endpoints{
			GetEndpointsResource(namespace, serviceName1, []v1.EndpointPort{{Port: 1000}}),
			GetEndpointsResource(namespace, serviceName2, []v1.EndpointPort{
				{Port: 1000, Name: "first_svc_port"},
				{Port: 2000, Name: "second_svc_port"},
			}),
		},
	}

	endpointsLister := GetEndpointsListerResource(endpointsList)

	ingBackend1 := GetIngressServiceBackendResource(serviceName1, "", 443)
	runTests(endpointsLister, svcWithSinglePort, ingBackend1, namespace, false, serviceName1, 443, 1000, 30224)

	ingBackend2 := GetIngressServiceBackendResource(serviceName2, "", 443)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend2, namespace, false, serviceName2, 443, 1000, 30225)

	ingBackend3 := GetIngressServiceBackendResource(serviceName2, "first_svc_port", 0)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend3, namespace, false, serviceName2, 443, 1000, 30225)

	ingBackend4 := GetIngressServiceBackendResource(serviceName2, "", 444)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend4, namespace, false, serviceName2, 444, 2000, 30226)

	ingBackend5 := GetIngressServiceBackendResource(serviceName2, "second_svc_port", 0)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend5, namespace, false, serviceName2, 444, 2000, 30226)

	ingBackend6 := GetIngressServiceBackendResource(serviceName2, "", 445)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend6, namespace, false, serviceName2, 445, 3000, 30227)

	ingBackend7 := GetIngressServiceBackendResource(serviceName2, "third_svc_port", 0)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend7, namespace, false, serviceName2, 445, 3000, 30227)

	ingBackend8 := GetIngressServiceBackendResource(serviceName2, "", 446)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend8, namespace, false, serviceName2, 446, 0, 30228)

	ingBackend9 := GetIngressServiceBackendResource(serviceName2, "fourth_svc_port", 0)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend9, namespace, false, serviceName2, 446, 0, 30228)

	ingBackend10 := GetIngressServiceBackendResource(serviceName2, "non_existent_port", 0)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend10, namespace, true, "", 0, 0, 0)

	ingBackend11 := GetIngressServiceBackendResource("non_existent_service", "non_existent_port", 0)
	runTests(endpointsLister, svcWithMultiplePorts, ingBackend11, namespace, true, "", 0, 0, 0)
}

func runTests(endpointsLister corelisters.EndpointsLister, svc *v1.Service, ingBackend networkingv1.IngressServiceBackend,
	namespace string, expectError bool, expectedSvcName string, expectedPort int32, expectedTargetPort int32, expectedNodePort int32) {
	svcName, svcPort, targetPort, err := PathToServiceAndTargetPort(endpointsLister, svc, ingBackend, namespace, true)
	Expect(err != nil).Should(Equal(expectError))
	Expect(svcName).Should(Equal(expectedSvcName))
	Expect(svcPort).Should(Equal(expectedPort))
	Expect(targetPort).Should(Equal(expectedNodePort))

	svcName, svcPort, targetPort, err = PathToServiceAndTargetPort(endpointsLister, svc, ingBackend, namespace, false)
	Expect(err != nil).Should(Equal(expectError))
	Expect(svcName).Should(Equal(expectedSvcName))
	Expect(svcPort).Should(Equal(expectedPort))
	Expect(targetPort).Should(Equal(expectedTargetPort))
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

func TestStringSlicesHaveSameElements(t *testing.T) {
	RegisterTestingT(t)

	var nilSlice []string = nil
	emptySlice := make([]string, 0)
	exampleSlice := []string{"a", "b", "d"}
	exampleSliceGroup := [][]string{{"a", "b", "c"}, {"a", "c", "b"}, {"c", "b", "a"}, {"a", "b", "a", "c", "c"}}

	Expect(StringSlicesHaveSameElements(nilSlice, nilSlice)).Should(BeTrue())
	Expect(StringSlicesHaveSameElements(nilSlice, emptySlice)).Should(BeTrue())
	Expect(StringSlicesHaveSameElements(emptySlice, nilSlice)).Should(BeTrue())
	Expect(StringSlicesHaveSameElements(emptySlice, emptySlice)).Should(BeTrue())

	Expect(StringSlicesHaveSameElements(nilSlice, exampleSlice)).Should(BeFalse())
	Expect(StringSlicesHaveSameElements(exampleSlice, emptySlice)).Should(BeFalse())

	for _, val1 := range exampleSliceGroup {
		for _, val2 := range exampleSliceGroup {
			Expect(StringSlicesHaveSameElements(val1, val2)).Should(BeTrue())
		}
	}

	for _, v := range exampleSliceGroup {
		Expect(StringSlicesHaveSameElements(v, exampleSlice)).Should(BeFalse())
	}
}
