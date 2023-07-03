package util

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/kubernetes/typed/core/v1/fake"
	fake2 "k8s.io/client-go/kubernetes/typed/networking/v1/fake"
	k8stesting "k8s.io/client-go/testing"
)

func ReadResourceAsIngressList(fileName string) *networkingv1.IngressList {
	data, err := os.ReadFile(fileName)
	if err != nil {
		log.Fatal(err)
	}
	decoder := scheme.Codecs.UniversalDeserializer()

	var ingressList []networkingv1.Ingress
	for _, resourceYAML := range strings.Split(string(data), "---") {

		if len(resourceYAML) == 0 {
			continue
		}

		obj, groupVersionKind, err := decoder.Decode([]byte(resourceYAML), nil, nil)
		if err != nil {
			log.Print(err.Error())
			continue
		}

		if groupVersionKind.Group == "networking.k8s.io" &&
			groupVersionKind.Version == "v1" &&
			groupVersionKind.Kind == "Ingress" {
			ingress := obj.(*networkingv1.Ingress)
			ingressList = append(ingressList, *ingress)
		}
	}
	return &networkingv1.IngressList{
		Items: ingressList,
	}
}
func GetServiceResource(namespace string, name string, port int32) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []v1.ServicePort{{
				Protocol: v1.ProtocolTCP,
				Port:     port,
			}},
		},
	}
}
func GetServiceListResource(namespace string, name string, port int32) *v1.ServiceList {
	testService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []v1.ServicePort{{
				Protocol: v1.ProtocolTCP,
				Port:     port,
			}},
		},
	}
	var services []v1.Service
	services = append(services, testService)

	return &v1.ServiceList{
		Items: services,
	}
}
func GetServiceListResourceWithPortName(namespace string, name string, port int32, portName string) *v1.ServiceList {
	testService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []v1.ServicePort{{
				Protocol: v1.ProtocolTCP,
				Name:     portName,
				Port:     port,
			}},
		},
	}
	var services []v1.Service
	services = append(services, testService)

	return &v1.ServiceList{
		Items: services,
	}
}

func GetIngressClassList() *networkingv1.IngressClassList {
	ingressClass := GetIngressClassResource("default-ingress-class", true, "oci.oraclecloud.com/native-ingress-controller")
	ingressClassList := &networkingv1.IngressClassList{
		Items: []networkingv1.IngressClass{*ingressClass},
	}
	return ingressClassList
}

func GetIngressClassListWithLBSet(lbId string) *networkingv1.IngressClassList {
	ingressClass := GetIngressClassResourceWithLbId("default-ingress-class", true, "oci.oraclecloud.com/native-ingress-controller", lbId)
	ingressClassList := &networkingv1.IngressClassList{
		Items: []networkingv1.IngressClass{*ingressClass},
	}
	return ingressClassList
}

func GetIngressClassListWithNginx() *networkingv1.IngressClassList {
	ingressClass := GetIngressClassResource("nginx-ingress-class", true, "nginx-ingress-controller")
	ingressClassList := &networkingv1.IngressClassList{
		Items: []networkingv1.IngressClass{*ingressClass},
	}
	return ingressClassList
}

func GetIngressClassResource(name string, isDefault bool, controller string) *networkingv1.IngressClass {
	return &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{"ingressclass.kubernetes.io/is-default-class": fmt.Sprint(isDefault)},
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controller,
		},
	}
}

func GetIngressClassResourceWithLbId(name string, isDefault bool, controller string, lbid string) *networkingv1.IngressClass {
	return &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{"ingressclass.kubernetes.io/is-default-class": fmt.Sprint(isDefault), IngressClassLoadBalancerIdAnnotation: lbid},
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controller,
		},
	}
}

func GetEndpointsResourceList(name string, namespace string) *v1.EndpointsList {
	var emptyNodeName string
	endpoint := v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{
				IP:       "6.7.8.9",
				Hostname: "",
				NodeName: &emptyNodeName,
				TargetRef: &v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "testpod",
					UID:       "999",
				},
			}},
			Ports: []v1.EndpointPort{{Port: 1000}},
		}},
	}
	endpoint2 := v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "host-es",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{
				IP:       "6.7.8.9",
				Hostname: "",
				NodeName: &emptyNodeName,
				TargetRef: &v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "testpod",
					UID:       "999",
				},
			}},
			Ports: []v1.EndpointPort{{Port: 1000}},
		}},
	}

	var endpoints []v1.Endpoints
	endpoints = append(endpoints, endpoint)
	endpoints = append(endpoints, endpoint2)

	return &v1.EndpointsList{
		Items: endpoints,
	}

}

func GetEndpointsResource(name string, namespace string) *v1.Endpoints {
	var emptyNodeName string
	return &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{IP: "6.7.8.9", NodeName: &emptyNodeName}},
			Ports:     []v1.EndpointPort{{Port: 1000}},
		}},
	}
}

func GetPodResource(name string, image string) *v1.Pod {
	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  name,
					Image: image,
				},
			},
		},
	}
	return pod
}
func GetPodResourceWithReadiness(name string, image string, ingressName string, host string, condition []v1.PodCondition) *v1.Pod {
	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  name,
					Image: image,
				},
			},
			ReadinessGates: GetPodReadinessGates(ingressName, host),
		},
		Status: v1.PodStatus{
			Phase:      "",
			Conditions: condition,
			Message:    "",
			Reason:     "",
		},
	}
	return pod
}

func GetPodReadinessGates(name string, host string) []v1.PodReadinessGate {
	var gates []v1.PodReadinessGate

	cond := GetPodReadinessCondition(name, host, GetHTTPPath())
	gates = append(gates, v1.PodReadinessGate{
		ConditionType: cond,
	})
	return gates
}

func GetHTTPPath() networkingv1.HTTPIngressPath {
	pathType := networkingv1.PathType("Exact")
	return networkingv1.HTTPIngressPath{
		Path:     "/testecho1",
		PathType: &pathType,
		Backend: networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: "testecho1",
				Port: networkingv1.ServiceBackendPort{},
			},
		},
	}

}

func GetPodResourceList(name string, image string) *v1.PodList {
	pod := v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  name,
					Image: image,
				},
			},
		},
	}

	var pods []v1.Pod
	pods = append(pods, pod)

	return &v1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PodList",
			APIVersion: "v1",
		},
		Items: pods,
	}
}

func UpdateFakeClientCall(client *fakeclientset.Clientset, action string, resource string, object runtime.Object) {
	client.NetworkingV1().(*fake2.FakeNetworkingV1).
		PrependReactor(action, resource, func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, object, nil
		})
}

func SampleLoadBalancerResponse() ociloadbalancer.GetLoadBalancerResponse {
	etag := "testTag"
	lbId := "id"
	backendSetName := GenerateBackendSetName("default", "testecho1", 80)
	name := "testecho1-999"
	port := 80
	ip := "127.89.90.90"
	backend := ociloadbalancer.Backend{
		Name:      &name,
		IpAddress: &ip,
		Port:      &port,
		Weight:    nil,
		Drain:     nil,
		Backup:    nil,
		Offline:   nil,
	}
	var backends []ociloadbalancer.Backend
	backends = append(backends, backend)

	healthChecker := &ociloadbalancer.HealthChecker{
		Protocol:          common.String(ProtocolHTTP),
		UrlPath:           common.String("/health"),
		Port:              common.Int(8080),
		ReturnCode:        common.Int(200),
		Retries:           common.Int(3),
		TimeoutInMillis:   common.Int(3000),
		IntervalInMillis:  common.Int(10000),
		ResponseBodyRegex: common.String("*"),
		IsForcePlainText:  common.Bool(true),
	}
	policy := "LEAST_CONNECTIONS"
	var ipAddresses []ociloadbalancer.IpAddress
	ipAddress := ociloadbalancer.IpAddress{
		IpAddress:  &ip,
		IsPublic:   nil,
		ReservedIp: nil,
	}
	ipAddresses = append(ipAddresses, ipAddress)

	var rules []ociloadbalancer.RoutingRule
	routeN := "route_80"
	cond := "cond"
	routeName := routeN
	rule := ociloadbalancer.RoutingRule{
		Name:      &routeName,
		Condition: &cond,
		Actions:   nil,
	}
	rules = append(rules, rule)
	plcy := ociloadbalancer.RoutingPolicy{
		Name:                     &routeN,
		ConditionLanguageVersion: "",
		Rules:                    rules,
	}
	policies := map[string]ociloadbalancer.RoutingPolicy{
		routeN: plcy,
	}
	proto := ProtocolHTTP
	listener := ociloadbalancer.Listener{
		Name:                    &routeN,
		DefaultBackendSetName:   nil,
		Port:                    &port,
		Protocol:                &proto,
		HostnameNames:           nil,
		PathRouteSetName:        nil,
		SslConfiguration:        nil,
		ConnectionConfiguration: nil,
		RuleSetNames:            nil,
		RoutingPolicyName:       &routeN,
	}
	var res = ociloadbalancer.GetLoadBalancerResponse{
		RawResponse: nil,
		LoadBalancer: ociloadbalancer.LoadBalancer{
			Id:          &lbId,
			IpAddresses: ipAddresses,
			Listeners: map[string]ociloadbalancer.Listener{
				routeN: listener,
			},
			BackendSets: map[string]ociloadbalancer.BackendSet{
				backendSetName: {
					Name:                                    &backendSetName,
					Policy:                                  &policy,
					Backends:                                backends,
					HealthChecker:                           healthChecker,
					SslConfiguration:                        nil,
					SessionPersistenceConfiguration:         nil,
					LbCookieSessionPersistenceConfiguration: nil,
				},
			},
			PathRouteSets:   nil,
			FreeformTags:    nil,
			DefinedTags:     nil,
			SystemTags:      nil,
			RuleSets:        nil,
			RoutingPolicies: policies,
		},
		OpcRequestId: nil,
		ETag:         &etag,
	}
	return res
}

func GetSampleSecret(configName string, privateKey string, data string, privateKeyData string) *v1.Secret {
	dat, _ := base64.StdEncoding.DecodeString(data)

	namespace := "test"
	name := "oci-config"
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string][]byte{
			configName: dat,
			privateKey: []byte(privateKeyData),
		},
	}
	return secret
}

func GetSampleCertSecret() *v1.Secret {
	namespace := "test"
	name := "oci-cert"
	s := "some-random"
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string][]byte{
			"ca.crt":  []byte(s),
			"tls.crt": []byte(s),
			"tls.key": []byte(s),
		},
	}
	return secret
}

func FakeClientGetCall(client *fakeclientset.Clientset, action string, resource string, obj runtime.Object) {
	client.CoreV1().(*fake.FakeCoreV1).
		PrependReactor(action, resource, func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, obj, nil
		})
}
