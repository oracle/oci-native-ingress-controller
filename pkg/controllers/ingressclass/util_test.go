/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2024 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package ingressclass

import (
	. "github.com/onsi/gomega"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	networkingv1 "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestIsDefinedTagsEqual(t *testing.T) {
	RegisterTestingT(t)

	emptyTags := util.DefinedTagsType{}
	dt1 := util.DefinedTagsType{"N1": {"k1": "v1"}, "n2": {"K2": "V2", "k3": "v3"}}
	dt2 := util.DefinedTagsType{"n1": {"K1": "v1"}, "N2": {"k2": "V2", "K3": "v3"}}
	dt3 := util.DefinedTagsType{"n1": {"k1": "v1"}, "n2": {"k2": "v2", "k3": "v3"}}

	Expect(isDefinedTagsEqual(nil, emptyTags)).Should(BeTrue())
	Expect(isDefinedTagsEqual(dt1, dt2)).Should(BeTrue())
	Expect(isDefinedTagsEqual(dt1, dt3)).Should(BeFalse())
	Expect(isDefinedTagsEqual(dt2, dt3)).Should(BeFalse())
}

func TestGetImplicitDefaultTagsForNewLoadBalancer(t *testing.T) {
	RegisterTestingT(t)

	actualDefinedTags := util.DefinedTagsType{"n1": {"k1": "v1", "KI1": "vi1"}, "n2": {"k2": "V2", "K3": "v3"}, "n3": {"k4": "v4"}}
	suppliedDefinedTags := util.DefinedTagsType{"N1": {"k1": "v1"}, "n2": {"K2": "V2", "k3": "v3"}}
	expectedImplicitDefaultTags := util.DefinedTagsType{"n1": {"KI1": "vi1"}, "n3": {"k4": "v4"}}

	Expect(expectedImplicitDefaultTags).Should(Equal(getImplicitDefaultTagsForNewLoadBalancer(actualDefinedTags, suppliedDefinedTags)))
}

func TestGetUpdatedDefinedAndImplicitDefaultTags(t *testing.T) {
	RegisterTestingT(t)

	actualTags := util.DefinedTagsType{"n1": {"k1": "v1", "KI1": "vi1"}, "n2": {"K2": "V2", "k3": "v3"}, "n3": {"K4": "v5"}}

	ingressClass := &networkingv1.IngressClass{
		ObjectMeta: v1.ObjectMeta{
			Annotations: map[string]string{
				util.IngressClassDefinedTagsAnnotation:         `{"n1": {"k1": "v1"}, "N2": {"k2": "v3", "K3": "V3"}}`,
				util.IngressClassImplicitDefaultTagsAnnotation: `{"n1": {"KI1": "vi1"}, "n2": {"K2": "V2"}, "n3": {"k4": "V4"}}`,
			},
		},
	}

	expectedDefinedTags := util.DefinedTagsType{"n1": {"k1": "v1", "ki1": "vi1"}, "n2": {"k2": "v3", "k3": "V3"}, "n3": {"k4": "v5"}}
	expectedDefaultTags := util.DefinedTagsType{"n1": {"KI1": "vi1"}, "n3": {"K4": "v5"}}

	definedTags, defaultTags, err := getUpdatedDefinedAndImplicitDefaultTags(actualTags, ingressClass)
	Expect(err).To(BeNil())
	Expect(expectedDefinedTags).Should(Equal(definedTags))
	Expect(expectedDefaultTags).Should(Equal(defaultTags))
}

func TestGetLowerCaseDefinedTags(t *testing.T) {
	RegisterTestingT(t)

	emptyTags := util.DefinedTagsType{}
	definedTags := util.DefinedTagsType{"N1": {"k1": "v1"}, "n2": {"K2": "V2", "k3": "v3"}}
	expectedTags := util.DefinedTagsType{"n1": {"k1": "v1"}, "n2": {"k2": "V2", "k3": "v3"}}

	Expect(getLowerCaseDefinedTags(nil)).Should(Equal(emptyTags))
	Expect(getLowerCaseDefinedTags(definedTags)).Should(Equal(expectedTags))
}

func TestContainsDefinedTagIgnoreCase(t *testing.T) {
	RegisterTestingT(t)

	definedTags := util.DefinedTagsType{"n1": {"k1": "v1"}, "n2": {"k2": "V2", "k3": "v3"}}

	Expect(containsDefinedTagIgnoreCase(nil, "namespace", "key")).Should(BeFalse())
	Expect(containsDefinedTagIgnoreCase(definedTags, "N1", "k1")).Should(BeTrue())
	Expect(containsDefinedTagIgnoreCase(definedTags, "n2", "K3")).Should(BeTrue())
	Expect(containsDefinedTagIgnoreCase(definedTags, "n4", "k1")).Should(BeFalse())
	Expect(containsDefinedTagIgnoreCase(definedTags, "n2", "k1")).Should(BeFalse())
}

func TestInsertDefinedTag(t *testing.T) {
	RegisterTestingT(t)

	definedtags := util.DefinedTagsType{}
	insertDefinedTag(definedtags, "n1", "k1", "v1")
	insertDefinedTag(definedtags, "n1", "K2", "V2")
	insertDefinedTag(definedtags, "N2", "k3", "v3")
	insertDefinedTag(definedtags, "n1", "k4", "V4")

	expectedTags := util.DefinedTagsType{"n1": {"k1": "v1", "K2": "V2", "k4": "V4"}, "N2": {"k3": "v3"}}
	Expect(expectedTags).Should(Equal(definedtags))
}
