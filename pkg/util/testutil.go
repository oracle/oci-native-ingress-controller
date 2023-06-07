package util

import (
	"fmt"
	"log"
	"os"
	"strings"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
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

func GetIngressClassList() *networkingv1.IngressClassList {
	ingressClass := GetIngressClassResource("default-ingress-class", true, "oci.oraclecloud.com/native-ingress-controller")
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
