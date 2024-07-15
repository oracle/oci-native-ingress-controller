package util

import (
	"context"
	"encoding/base64"
	"fmt"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
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

const (
	ToBeDeletedTaint = "ToBeDeletedByClusterAutoscaler"
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
				NodePort: 30223,
			}},
			ExternalTrafficPolicy: v1.ServiceExternalTrafficPolicyTypeLocal,
		},
	}
	testService2 := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "host-es",
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []v1.ServicePort{{
				Protocol: v1.ProtocolTCP,
				Port:     8080,
				NodePort: 30224,
			}},
			ExternalTrafficPolicy: v1.ServiceExternalTrafficPolicyTypeLocal,
		},
	}
	testService3 := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-with-named-target-port",
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []v1.ServicePort{
				{
					Protocol:   v1.ProtocolTCP,
					Port:       8081,
					NodePort:   30225,
					TargetPort: intstr.FromString("test_port_name"),
					Name:       "port_8081",
				},
				{
					Protocol:   v1.ProtocolTCP,
					Port:       8082,
					NodePort:   30226,
					TargetPort: intstr.FromInt(1001),
					Name:       "port_8082",
				},
			},
			ExternalTrafficPolicy: v1.ServiceExternalTrafficPolicyTypeLocal,
		},
	}
	var services []v1.Service
	services = append(services, testService)
	services = append(services, testService2)
	services = append(services, testService3)

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
			Annotations: map[string]string{IngressClassIsDefault: fmt.Sprint(isDefault)},
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controller,
		},
	}
}

func GetIngressClassResourceWithAnnotation(name string, annotation map[string]string, controller string) *networkingv1.IngressClassList {
	ingressClass := GetIngressClassResource("default-ingress-class", true, "oci.oraclecloud.com/native-ingress-controller")
	ic := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotation,
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controller,
		},
	}
	ingressClassList := &networkingv1.IngressClassList{
		Items: []networkingv1.IngressClass{*ic, *ingressClass},
	}
	return ingressClassList
}

func GetIngressClassResourceWithLbId(name string, isDefault bool, controller string, lbid string) *networkingv1.IngressClass {
	return &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{IngressClassIsDefault: fmt.Sprint(isDefault), IngressClassLoadBalancerIdAnnotation: lbid},
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controller,
		},
	}
}

func GetIngressResource(name string) *networkingv1.Ingress {
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func GetEndpointsResourceList(name string, namespace string, allCase bool) *v1.EndpointsList {
	if allCase {
		return GetEndpointsResourceListAllCase(name,
			namespace)
	}
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
	endpoint3 := v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "service-with-named-target-port",
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
			Ports: []v1.EndpointPort{{Port: 1000, Name: "port_8081"}, {Port: 1001, Name: "port_8082"}},
		}},
	}

	var endpoints []v1.Endpoints
	endpoints = append(endpoints, endpoint)
	endpoints = append(endpoints, endpoint2)
	endpoints = append(endpoints, endpoint3)

	return &v1.EndpointsList{
		Items: endpoints,
	}

}

func GetEndpointsResourceListAllCase(name string, namespace string) *v1.EndpointsList {
	var emptyNodeName string
	endpoint := v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{
				IP:       "6.7.8.0",
				Hostname: "",
				NodeName: &emptyNodeName,
				TargetRef: &v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "testpod0",
					UID:       "990",
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
				IP:       "6.7.8.1",
				Hostname: "",
				NodeName: &emptyNodeName,
				TargetRef: &v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "testpod1",
					UID:       "991",
				},
			}},
			Ports: []v1.EndpointPort{{Port: 1001}},
		}},
	}
	endpoint3 := v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{
				IP:       "6.7.8.2",
				Hostname: "",
				NodeName: &emptyNodeName,
				TargetRef: &v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "testpod2",
					UID:       "992",
				},
			}},
			Ports: []v1.EndpointPort{{Port: 1004}},
			NotReadyAddresses: []v1.EndpointAddress{{
				IP:       "6.7.8.3",
				Hostname: "",
				NodeName: &emptyNodeName,
				TargetRef: &v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "testpod3",
					UID:       "993",
				},
			}},
		}},
	}
	endpoint4 := v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "service-with-named-target-port",
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
			Ports: []v1.EndpointPort{{Port: 1000, Name: "port_8081"}, {Port: 1001, Name: "port_8082"}},
		}},
	}

	var endpoints []v1.Endpoints
	endpoints = append(endpoints, endpoint)
	endpoints = append(endpoints, endpoint2)
	endpoints = append(endpoints, endpoint3)
	endpoints = append(endpoints, endpoint4)

	return &v1.EndpointsList{
		Items: endpoints,
	}

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
			NodeName: "10.0.10.166",
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
	backendSetName2 := GenerateBackendSetName("default", "service-with-named-target-port", 8081)
	backendSetName3 := GenerateBackendSetName("default", "service-with-named-target-port", 8082)
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
		DefaultBackendSetName:   common.String(DefaultBackendSetName),
		Port:                    &port,
		Protocol:                &proto,
		HostnameNames:           nil,
		PathRouteSetName:        nil,
		SslConfiguration:        nil,
		ConnectionConfiguration: nil,
		RuleSetNames:            nil,
		RoutingPolicyName:       &routeN,
	}
	minimumBandwidthInMbps := 100
	maximumBandwidthInMbps := 400
	var res = ociloadbalancer.GetLoadBalancerResponse{
		RawResponse: nil,
		LoadBalancer: ociloadbalancer.LoadBalancer{
			DisplayName: &name,
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
				backendSetName2: {
					Name:                                    &backendSetName2,
					Policy:                                  &policy,
					Backends:                                backends,
					HealthChecker:                           healthChecker,
					SslConfiguration:                        nil,
					SessionPersistenceConfiguration:         nil,
					LbCookieSessionPersistenceConfiguration: nil,
				},
				backendSetName3: {
					Name:                                    &backendSetName3,
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
			ShapeDetails: &ociloadbalancer.ShapeDetails{
				MinimumBandwidthInMbps: &minimumBandwidthInMbps,
				MaximumBandwidthInMbps: &maximumBandwidthInMbps,
			},
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

func GetSampleCertSecret(namespace, name, caChain, cert, key string) *v1.Secret {
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string][]byte{
			"tls.crt": []byte(cert),
			"tls.key": []byte(key),
		},
	}

	if caChain != "" {
		secret.Data["ca.crt"] = []byte(caChain)
	}

	return secret
}

func FakeClientGetCall(client *fakeclientset.Clientset, action string, resource string, obj runtime.Object) {
	client.CoreV1().(*fake.FakeCoreV1).
		PrependReactor(action, resource, func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, obj, nil
		})
}

func GetNodesList() *v1.NodeList {
	var conditions []v1.NodeCondition
	cond := v1.NodeCondition{
		Type:   v1.NodeReady,
		Status: v1.ConditionTrue,
	}
	conditions = append(conditions, cond)
	var nodeAddress []v1.NodeAddress
	nodeAdd1 := v1.NodeAddress{
		Type:    v1.NodeInternalIP,
		Address: "10.0.10.166",
	}
	nodeAddress = append(nodeAddress, nodeAdd1)

	nodeA := v1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "10.0.10.166",
		},
		Status: v1.NodeStatus{
			Conditions: conditions,
			Addresses:  nodeAddress,
		},
	}
	var taints []v1.Taint
	taint := v1.Taint{
		Key: ToBeDeletedTaint,
	}
	taints = append(taints, taint)

	nodeB := v1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nodeB",
		},
		Spec: v1.NodeSpec{
			Taints: taints,
		},
	}

	var conditions2 []v1.NodeCondition
	cond2 := v1.NodeCondition{
		Type:   v1.NodeReady,
		Status: v1.ConditionFalse,
	}
	conditions2 = append(conditions2, cond2)

	nodeC := v1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nodeC",
		},
		Status: v1.NodeStatus{
			Conditions: conditions2,
		},
	}
	nodeD := v1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nodeD",
		},
	}

	var nodes []v1.Node
	nodes = append(nodes, nodeA)
	nodes = append(nodes, nodeB)
	nodes = append(nodes, nodeC)
	nodes = append(nodes, nodeD)

	return &v1.NodeList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeList",
			APIVersion: "v1",
		},
		Items: nodes,
	}
}

func GetServicePortResource(name string, port int32, targetPort intstr.IntOrString, nodePort int32) v1.ServicePort {
	return v1.ServicePort{
		Name:       name,
		Port:       port,
		TargetPort: targetPort,
		NodePort:   nodePort,
	}
}

func GetServiceResource(namespace string, name string, ports []v1.ServicePort) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Selector:              map[string]string{"app": name},
			Ports:                 ports,
			ExternalTrafficPolicy: v1.ServiceExternalTrafficPolicyTypeLocal,
		},
	}
}

func GetEndpointsResource(namespace string, name string, ports []v1.EndpointPort) v1.Endpoints {
	var emptyNodeName string
	return v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{IP: "6.7.8.9", NodeName: &emptyNodeName}},
			Ports:     ports,
		}},
	}
}

func GetIngressServiceBackendResource(name string, portName string, portNumber int32) networkingv1.IngressServiceBackend {
	return networkingv1.IngressServiceBackend{
		Name: name,
		Port: networkingv1.ServiceBackendPort{
			Name:   portName,
			Number: portNumber,
		},
	}
}

func GetEndpointsListerResource(endpointsList *v1.EndpointsList) corelisters.EndpointsLister {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fakeclientset.NewSimpleClientset()
	UpdateFakeClientCall(client, "list", "endpoints", endpointsList)
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	endpointInformer := informerFactory.Core().V1().Endpoints()
	endpointsLister := endpointInformer.Lister()
	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), endpointInformer.Informer().HasSynced)

	return endpointsLister
}
