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
	"encoding/json"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"reflect"
	"strings"
)

var (
	tagVariables = []string{
		"${iam.principal.name}",
		"${iam.principal.type}",
		"${oci.datetime}",
	}
)

func isDefinedTagsEqual(dt1, dt2 util.DefinedTagsType) bool {
	return reflect.DeepEqual(getLowerCaseDefinedTags(dt1), getLowerCaseDefinedTags(dt2))
}

func getImplicitDefaultTagsForNewLoadBalancer(actualDefinedTags, suppliedDefinedTags util.DefinedTagsType) util.DefinedTagsType {
	defaultTags := util.DefinedTagsType{}
	lowerCaseSuppliedDefinedTags := getLowerCaseDefinedTags(suppliedDefinedTags)

	klog.Infof("Calculating implicit default tags where actualTags: %+v, suppliedTags: %+v",
		actualDefinedTags, suppliedDefinedTags)

	for namespace, _ := range actualDefinedTags {
		for key, value := range actualDefinedTags[namespace] {
			if !containsDefinedTagIgnoreCase(lowerCaseSuppliedDefinedTags, namespace, key) {
				insertDefinedTag(defaultTags, namespace, key, value)
			}
		}
	}

	return defaultTags
}

// Return values - (new tags content for LB, new default tags annotation content, error)
func getUpdatedDefinedAndImplicitDefaultTags(actualTags util.DefinedTagsType,
	ic *networkingv1.IngressClass) (util.DefinedTagsType, util.DefinedTagsType, error) {
	// Updated value for LB's actual Defined Tags
	updatedDefinedTags := util.DefinedTagsType{}
	// Updated value to be placed into the util.IngressClassImplicitDefaultTagsAnnotation annotation
	updatedDefaultTags := util.DefinedTagsType{}

	// Defined Tags supplied by customer
	definedTagsAnnotationPresent, definedTags, err := util.GetIngressClassDefinedTags(ic)
	if err != nil {
		return nil, nil, err
	}

	// Currently stored default tags on the IngressClass
	defaultTagsAnnotationPresent, defaultTags, err := util.GetIngressClassImplicitDefaultTags(ic)
	if err != nil {
		return nil, nil, err
	}

	// If no defined tag annotations are present, we process this as if the LoadBalancer just got created
	// This can happen in two cases usually
	//  (a) migrating from an NIC version that did not have tagging support
	//  (b) filling id annotation on the IngressClass on creation to import a pre-existing load balancer, but not filling the defined-tags annotation
	// We fill the implicit-default-tag annotation with the current tags on LB and preserve them
	// Here, definedTags and defaultTags both will be empty
	if !definedTagsAnnotationPresent && !defaultTagsAnnotationPresent {
		return actualTags, actualTags, nil
	}

	klog.Infof("Calculating defined/default tags where actualTags: %+v, suppliedDefinedTags: %+v, implicitDefaultTags: %+v",
		actualTags, definedTags, defaultTags)

	// Preserve default tags if they are present on LB and not overriden in supplied tags
	lcDefinedTags := getLowerCaseDefinedTags(definedTags)
	lcDefaultTags := getLowerCaseDefinedTags(defaultTags)
	for namespace, _ := range actualTags {
		for key, value := range actualTags[namespace] {
			if !containsDefinedTagIgnoreCase(lcDefinedTags, namespace, key) &&
				containsDefinedTagIgnoreCase(lcDefaultTags, namespace, key) {
				insertDefinedTag(updatedDefinedTags, namespace, key, value)
				insertDefinedTag(updatedDefaultTags, namespace, key, value)
			}
		}
	}

	// Add supplied defined tags
	// We use only lower-case (namespace, key) pairs to avoid case-related conflicts
	// If the supplied tag value has a Tag Variable, and the tag is already present on LB we will not try to update it
	lcActualTags := getLowerCaseDefinedTags(actualTags)
	lcUpdatedDefinedTags := getLowerCaseDefinedTags(updatedDefinedTags)
	for namespace, _ := range lcDefinedTags {
		for key, value := range lcDefinedTags[namespace] {
			if definedTagValueHasTagVariable(value) && containsDefinedTagIgnoreCase(lcActualTags, namespace, key) {
				klog.Infof("Supplied value of Tag %s.%s has tag-variable(s) and is already present on LB, will not be updated",
					namespace, key)
				insertDefinedTag(lcUpdatedDefinedTags, namespace, key, lcActualTags[namespace][key])
			} else {
				insertDefinedTag(lcUpdatedDefinedTags, namespace, key, value)
			}
		}
	}

	klog.Infof("Calculated defined/default tags for IngressClass %s: definedTags: %+v, implicitDefaultTags: %+v",
		ic.Name, lcUpdatedDefinedTags, updatedDefaultTags)
	return lcUpdatedDefinedTags, updatedDefaultTags, nil
}

func updateImplicitDefaultTagsAnnotation(client kubernetes.Interface, ic *networkingv1.IngressClass,
	defaultTags util.DefinedTagsType) error {
	defaultTagsBytes, err := json.Marshal(defaultTags)
	if err != nil {
		return err
	}

	patchError, notComplete := util.PatchIngressClassWithAnnotation(client, ic,
		util.IngressClassImplicitDefaultTagsAnnotation, string(defaultTagsBytes))
	if notComplete {
		return patchError
	}

	return nil
}

func getLowerCaseDefinedTags(tags util.DefinedTagsType) util.DefinedTagsType {
	lowerCaseTags := util.DefinedTagsType{}

	for k, _ := range tags {
		lowerCaseTags[strings.ToLower(k)] = map[string]interface{}{}
		for ik, iv := range tags[k] {
			lowerCaseTags[strings.ToLower(k)][strings.ToLower(ik)] = iv
		}
	}

	return lowerCaseTags
}

// Checks if (namespace, key) pair exists in a lower-cased definedTags map, ignore case of (namespace, key)
func containsDefinedTagIgnoreCase(lowerCaseTags util.DefinedTagsType, namespace string, key string) bool {
	if lowerCaseTags == nil {
		return false
	}

	containsNamespace := false
	containsKey := false

	_, containsNamespace = lowerCaseTags[strings.ToLower(namespace)]
	if containsNamespace {
		_, containsKey = lowerCaseTags[strings.ToLower(namespace)][strings.ToLower(key)]
	}

	return containsNamespace && containsKey
}

func insertDefinedTag(definedTags util.DefinedTagsType, namespace string, key string, value interface{}) {
	if definedTags == nil {
		return
	}

	_, ok := definedTags[namespace]
	if !ok {
		definedTags[namespace] = map[string]interface{}{}
	}

	definedTags[namespace][key] = value
}

func definedTagValueHasTagVariable(value interface{}) bool {
	stringValue, ok := value.(string)
	if ok {
		for _, tagVar := range tagVariables {
			if strings.Contains(stringValue, tagVar) {
				return true
			}
		}
	}

	return false
}
