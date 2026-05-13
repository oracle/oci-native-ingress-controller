/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */
package routingpolicy

import (
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/common"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/klog/v2"
)

func TestByPath_Less(t *testing.T) {
	RegisterTestingT(t)
	ingressName := "testIngress"
	hostName := "testHost"
	path := "/testPath"
	ExactPathType := networkingv1.PathType("Exact")
	PrefixPathType := networkingv1.PathType("Prefix")
	byPath := ByPath{
		{
			IngressName:    ingressName,
			Host:           hostName,
			BackendSetName: "bs_fae011fdb569ae4",
			Path: &networkingv1.HTTPIngressPath{
				Path:     path,
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
		},
		{
			IngressName:    ingressName,
			Host:           hostName,
			BackendSetName: "bs_a76b859286a9e74",
			Path: &networkingv1.HTTPIngressPath{
				Path:     path,
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
		},
	}

	Expect(byPath.Less(0, 1)).Should(Equal(true))

	byPath = ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/efgh",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			}},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
		},
	}
	Expect(byPath.Less(0, 1)).Should(Equal(true))

	byPath = ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			}},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &PrefixPathType,
				Backend:  networkingv1.IngressBackend{},
			},
		},
	}
	Expect(byPath.Less(0, 1)).Should(Equal(true))

	byPath = ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host: "xyz.com",
		},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host: "abcd.com",
		},
	}
	Expect(byPath.Less(0, 1)).Should(Equal(true))

	byPath = ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:           "abcd.com",
			BackendSetName: "bs_xyz",
		},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:           "abcd.com",
			BackendSetName: "bs_abcd",
		},
	}
	Expect(byPath.Less(0, 1)).Should(Equal(true))

	byPath = ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:           "abcd.com",
			BackendSetName: "bs_abcd",
			IngressName:    "ingress_xyz",
		},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:           "abcd.com",
			BackendSetName: "bs_abcd",
			IngressName:    "ingress_abcd",
		},
	}
	Expect(byPath.Less(0, 1)).Should(Equal(true))
}

func TestByPath_LessKeepsNilPathsFirst(t *testing.T) {
	RegisterTestingT(t)

	pathType := networkingv1.PathTypeExact
	nonNilPath := &networkingv1.HTTPIngressPath{
		Path:     "/abcd",
		PathType: &pathType,
	}

	byPath := ByPath{
		{Path: nil},
		{Path: nonNilPath},
	}
	Expect(byPath.Less(0, 1)).Should(BeTrue())

	byPath = ByPath{
		{Path: nonNilPath},
		{Path: nil},
	}
	Expect(byPath.Less(0, 1)).Should(BeFalse())

	byPath = ByPath{
		{Path: nil},
		{Path: nil},
	}
	Expect(byPath.Less(0, 1)).Should(BeFalse())

	byPath = ByPath{
		nil,
		{Path: nonNilPath},
	}
	Expect(byPath.Less(0, 1)).Should(BeTrue())

	byPath = ByPath{
		{Path: nonNilPath},
		nil,
	}
	Expect(byPath.Less(0, 1)).Should(BeFalse())

	byPath = ByPath{
		nil,
		nil,
	}
	Expect(byPath.Less(0, 1)).Should(BeFalse())
}

func TestBuildRoutingRulesSkipsNilListenerPaths(t *testing.T) {
	RegisterTestingT(t)

	pathType := networkingv1.PathTypePrefix
	validPath := &networkingv1.HTTPIngressPath{
		Path:     "/valid",
		PathType: &pathType,
	}
	paths := []*listenerPath{
		nil,
		{Path: nil},
		{
			IngressName:    "ingress",
			ListenerPort:   80,
			Host:           "example.com",
			BackendSetName: "backend_set",
			Path:           validPath,
		},
	}

	rules := buildRoutingRules("route_80", paths)

	Expect(rules).Should(HaveLen(1))
	Expect(*rules[0].Name).Should(Equal(util.PathToRoutePolicyName("ingress", "example.com", *validPath)))
	Expect(*rules[0].Condition).Should(Equal(PathToRoutePolicyCondition(80, "example.com", *validPath)))
}

func TestByPath_Swap(t *testing.T) {
	RegisterTestingT(t)
	ExactPathType := networkingv1.PathType("Exact")
	byPath := ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:           "abcd.com",
			BackendSetName: "bs_abcd",
			IngressName:    "ingress_xyz",
		},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:           "abcd.com",
			BackendSetName: "bs_abcd",
			IngressName:    "ingress_abcd",
		},
	}

	var byPathCopy []*listenerPath
	byPathCopy = append(byPathCopy, byPath[0])
	byPathCopy = append(byPathCopy, byPath[1])
	Expect(byPath[0]).Should(Equal(byPathCopy[0]))
	Expect(byPath[1]).Should(Equal(byPathCopy[1]))

	byPath.Swap(0, 1)
	Expect(byPath[0]).Should(Equal(byPathCopy[1]))
	Expect(byPath[1]).Should(Equal(byPathCopy[0]))
}

func TestByPath_Len(t *testing.T) {
	RegisterTestingT(t)
	ExactPathType := networkingv1.PathType("Exact")
	byPath := ByPath{
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			}},
		{
			Path: &networkingv1.HTTPIngressPath{
				Path:     "/abcd",
				PathType: &ExactPathType,
				Backend:  networkingv1.IngressBackend{},
			},
		},
	}
	Expect(byPath.Len()).Should(Equal(2))
}

func TestPathToRoutePolicyCondition(t *testing.T) {
	RegisterTestingT(t)
	PathTypeExact := networkingv1.PathType("Exact")
	PathTypePrefix := networkingv1.PathType("Prefix")
	PrefixPath := "/PrefixPath"
	ExactPath := "/ExactPath"
	HostFooBar := "foo.bar.com"
	HostWildCard := "*.example.com"

	var tests = []TestPathToRoutingPolicy{
		{
			Path: networkingv1.HTTPIngressPath{
				Path:     PrefixPath,
				PathType: &PathTypePrefix,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:         HostFooBar,
			LisneterPort: 80,
			Expected: fmt.Sprintf("all(any(http.request.headers[(i 'Host')] eq '%s', http.request.headers[(i 'Host')] eq '%s:%d') , http.request.url.path sw '%s')",
				HostFooBar, HostFooBar, 80, PrefixPath),
		},
		{
			Path: networkingv1.HTTPIngressPath{
				Path:     PrefixPath,
				PathType: &PathTypePrefix,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:         HostWildCard,
			LisneterPort: 80,
			Expected: fmt.Sprintf("all(any(http.request.headers[(i 'Host')][0] ew '%s', http.request.headers[(i 'Host')][0] ew '%s:%d') , http.request.url.path sw '%s')",
				HostWildCard[1:], HostWildCard[1:], 80, PrefixPath),
		},
		{
			Path: networkingv1.HTTPIngressPath{
				Path:     ExactPath,
				PathType: &PathTypeExact,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:         "",
			LisneterPort: 80,
			Expected:     fmt.Sprintf("http.request.url.path eq '%s'", ExactPath),
		},
	}

	for i := range tests {
		klog.Infof("Running test %s ", util.PrettyPrint(tests[i]))
		value := PathToRoutePolicyCondition(tests[i].LisneterPort, tests[i].Host, tests[i].Path)
		klog.Infof("Result: %s ", value)
		Expect(value).Should(Equal(tests[i].Expected))
	}
}

func TestPathToRoutePolicyConditionNilPathTypeAndShortHost(t *testing.T) {
	RegisterTestingT(t)

	path := networkingv1.HTTPIngressPath{Path: "/"}

	condition := PathToRoutePolicyCondition(80, "a", path)

	Expect(condition).Should(ContainSubstring("any(http.request.headers[(i 'Host')] eq 'a'"))
	Expect(condition).Should(ContainSubstring("http.request.url.path sw '/"))
}

func TestProcessRoutingPolicySkipsNonHTTPAndNonServiceBackends(t *testing.T) {
	RegisterTestingT(t)

	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: v1.ObjectMeta{
			Name:      "ingress",
			Namespace: "default",
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{Host: "tcp.example.com"},
				{
					Host: "http.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/resource",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Resource: &corev1.TypedLocalObjectReference{Name: "unsupported-resource"},
									},
								},
								{
									Path:     "/missing-service",
									PathType: &pathType,
								},
								{
									Path:     "/service",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "service",
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
	listenerPaths := map[string][]*listenerPath{}
	desiredRoutingPolicies := sets.NewString()
	fakeRecorder := events.NewFakeRecorder(10)

	err := processRoutingPolicy([]*networkingv1.Ingress{ingress}, nil, listenerPaths, desiredRoutingPolicies, fakeRecorder)

	Expect(err).NotTo(HaveOccurred())
	Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(util.UnsupportedBackendReason)))
	Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(util.MissingServiceBackendReason)))
	Expect(desiredRoutingPolicies.Has("route_80")).Should(BeTrue())
	Expect(listenerPaths).Should(HaveKey("route_80"))
	Expect(listenerPaths["route_80"]).Should(HaveLen(1))
	Expect(listenerPaths["route_80"][0].Host).Should(Equal("http.example.com"))
	Expect(listenerPaths["route_80"][0].Path.Path).Should(Equal("/service"))
	Expect(listenerPaths["route_80"][0].BackendSetName).Should(Equal(util.GenerateBackendSetName("default", "service", 80)))
}

type TestPathToRoutingPolicy struct {
	Path         networkingv1.HTTPIngressPath
	Host         string
	LisneterPort int32
	Expected     string
}

func TestFilterIngressesForRoutingPolicy(t *testing.T) {
	RegisterTestingT(t)

	defaultIngressClass := util.GetIngressClassResource("default_ingressclass", true, "")
	otherIngressClass := util.GetIngressClassResource("other_ingressclass", false, "")

	ingressWithoutIngressClass := util.GetIngressResource("ingress_without_ingressclass")
	ingressWithDefaultIngressClass := util.GetIngressResource("ingress_with_default_ingressclass")
	ingressWithDefaultIngressClass.Spec.IngressClassName = common.String("default_ingressclass")

	ingressWithOtherIngressClass := util.GetIngressResource("ingress_with_other_ingressclass")
	ingressWithOtherIngressClass.Spec.IngressClassName = common.String("other_ingressclass")

	deletingIngress := util.GetIngressResource("deleting_ingress")
	deletingIngress.DeletionTimestamp = &v1.Time{Time: time.Now()}

	ingressWithTCP := util.GetIngressResource("ingress_with_tcp")
	ingressWithTCP.Annotations = map[string]string{
		"oci-native-ingress.oraclecloud.com/protocol": "TCP",
	}

	testHelper := func(ingressClass *networkingv1.IngressClass, ingress *networkingv1.Ingress, expectedCount int) {
		result := filterIngressesForRoutingPolicy(ingressClass, []*networkingv1.Ingress{ingress})
		Expect(len(result)).Should(Equal(expectedCount))
	}

	testHelper(defaultIngressClass, ingressWithoutIngressClass, 1)
	testHelper(defaultIngressClass, ingressWithDefaultIngressClass, 1)
	testHelper(defaultIngressClass, ingressWithOtherIngressClass, 0)
	testHelper(defaultIngressClass, deletingIngress, 0)
	testHelper(defaultIngressClass, ingressWithTCP, 0)

	testHelper(otherIngressClass, ingressWithoutIngressClass, 0)
	testHelper(otherIngressClass, ingressWithDefaultIngressClass, 0)
	testHelper(otherIngressClass, ingressWithOtherIngressClass, 1)
	testHelper(otherIngressClass, deletingIngress, 0)
	testHelper(otherIngressClass, ingressWithTCP, 0)
}
