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
	"github.com/oracle/oci-native-ingress-controller/pkg/testutil"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			Host:     HostFooBar,
			Expected: fmt.Sprintf("all(http.request.headers[(i 'Host')] eq '%s' , http.request.url.path sw '%s')", HostFooBar, PrefixPath),
		},
		{
			Path: networkingv1.HTTPIngressPath{
				Path:     PrefixPath,
				PathType: &PathTypePrefix,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:     HostWildCard,
			Expected: fmt.Sprintf("all(http.request.headers[(i 'Host')][0] ew '%s' , http.request.url.path sw '%s')", HostWildCard[1:], PrefixPath),
		},
		{
			Path: networkingv1.HTTPIngressPath{
				Path:     ExactPath,
				PathType: &PathTypeExact,
				Backend:  networkingv1.IngressBackend{},
			},
			Host:     "",
			Expected: fmt.Sprintf("http.request.url.path eq '%s'", ExactPath),
		},
	}

	for i := range tests {
		klog.Infof("Running test %s ", util.PrettyPrint(tests[i]))
		value := PathToRoutePolicyCondition(tests[i].Host, tests[i].Path)
		klog.Infof("Result: %s ", value)
		Expect(value).Should(Equal(tests[i].Expected))
	}
}

type TestPathToRoutingPolicy struct {
	Path     networkingv1.HTTPIngressPath
	Host     string
	Expected string
}

func TestFilterIngressesForRoutingPolicy(t *testing.T) {
	RegisterTestingT(t)

	defaultIngressClass := testutil.GetIngressClassResource("default_ingressclass", true, "")
	otherIngressClass := testutil.GetIngressClassResource("other_ingressclass", false, "")

	ingressWithoutIngressClass := testutil.GetIngressResource("ingress_without_ingressclass")
	ingressWithDefaultIngressClass := testutil.GetIngressResource("ingress_with_default_ingressclass")
	ingressWithDefaultIngressClass.Spec.IngressClassName = common.String("default_ingressclass")

	ingressWithOtherIngressClass := testutil.GetIngressResource("ingress_with_other_ingressclass")
	ingressWithOtherIngressClass.Spec.IngressClassName = common.String("other_ingressclass")

	deletingIngress := testutil.GetIngressResource("deleting_ingress")
	deletingIngress.DeletionTimestamp = &v1.Time{Time: time.Now()}

	ingressWithTCP := testutil.GetIngressResource("ingress_with_tcp")
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
