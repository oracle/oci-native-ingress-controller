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
	"github.com/pkg/errors"
	"k8s.io/klog/v2"
	"strings"

	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	corelisters "k8s.io/client-go/listers/core/v1"
)

type listenerPath struct {
	IngressName    string
	Host           string
	BackendSetName string
	Path           *networkingv1.HTTPIngressPath
}

type ByPath []*listenerPath

func (pathArray ByPath) Len() int { return len(pathArray) }

func (pathArray ByPath) Less(i, j int) bool {
	if pathArray[i].Path.Path == pathArray[j].Path.Path {
		if *pathArray[i].Path.PathType == *pathArray[j].Path.PathType {
			if strings.EqualFold(pathArray[i].Host, pathArray[j].Host) {
				if pathArray[i].BackendSetName == pathArray[j].BackendSetName {
					return pathArray[i].IngressName > pathArray[j].IngressName
				}
				return pathArray[i].BackendSetName > pathArray[j].BackendSetName
			}
			return pathArray[i].Host > pathArray[j].Host
		}
		return *pathArray[i].Path.PathType < *pathArray[j].Path.PathType
	}
	return pathArray[i].Path.Path > pathArray[j].Path.Path
}

func (pathArray ByPath) Swap(i, j int) { pathArray[i], pathArray[j] = pathArray[j], pathArray[i] }

func PathToRoutePolicyCondition(host string, path networkingv1.HTTPIngressPath) string {
	var conditions []string

	if host != "" {
		if host[:2] == "*." {
			conditions = append(conditions, fmt.Sprintf("http.request.headers[(i 'Host')][0] ew '%s'", host[1:]))
		} else {
			conditions = append(conditions, fmt.Sprintf("http.request.headers[(i 'Host')] eq '%s'", host))
		}

	}
	if *path.PathType == networkingv1.PathTypeExact {
		conditions = append(conditions, fmt.Sprintf("http.request.url.path eq '%s'", path.Path))
	} else {
		conditions = append(conditions, fmt.Sprintf("http.request.url.path sw '%s'", path.Path))
	}

	if len(conditions) == 1 {
		return conditions[0]
	}

	return fmt.Sprintf("all(%s , %s)", conditions[0], conditions[1])
}

func processRoutingPolicy(ingresses []*networkingv1.Ingress, serviceLister corelisters.ServiceLister,
	listenerPaths map[string][]*listenerPath, desiredRoutingPolicies sets.String) error {
	for _, ingress := range ingresses {
		tlsConfiguredHosts := sets.NewString()
		for tlsIdx := range ingress.Spec.TLS {
			ingressTls := ingress.Spec.TLS[tlsIdx]
			for hostIdx := range ingressTls.Hosts {
				tlsConfiguredHosts.Insert(ingressTls.Hosts[hostIdx])
			}
		}

		for _, rule := range ingress.Spec.Rules {
			host := rule.Host

			for _, path := range rule.HTTP.Paths {
				serviceName, servicePort, err := util.PathToServiceAndPort(ingress.Namespace, path, serviceLister)
				if err != nil {
					return fmt.Errorf("for ingress %s, encountered error: %w", klog.KObj(ingress), err)
				}

				listenerPort, err := util.DetermineListenerPort(ingress, &tlsConfiguredHosts, host, servicePort)
				if err != nil {
					err = errors.Wrap(err, "error determining listener port")
					return fmt.Errorf("for ingress %s, encountered error: %w", klog.KObj(ingress), err)
				}

				listenerName := util.GenerateListenerName(listenerPort)
				rulePath := path
				listenerPaths[listenerName] = append(listenerPaths[listenerName], &listenerPath{
					IngressName:    ingress.Name,
					Host:           host,
					Path:           &rulePath,
					BackendSetName: util.GenerateBackendSetName(ingress.Namespace, serviceName, servicePort),
				})
				desiredRoutingPolicies.Insert(listenerName)
			}
		}
	}
	return nil
}

func filterIngressesForRoutingPolicy(ingressClass *networkingv1.IngressClass, allIngresses []*networkingv1.Ingress) []*networkingv1.Ingress {
	var ingresses []*networkingv1.Ingress
	for _, ingress := range allIngresses {
		// skip if the ingress is in deleting state or if protocol is TCP
		if util.IsIngressDeleting(ingress) || util.IsIngressProtocolTCP(ingress) {
			continue
		}
		if ingress.Spec.IngressClassName == nil && ingressClass.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
			// ingress has on class name defined and our ingress class is default
			ingresses = append(ingresses, ingress)
		}
		if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName == ingressClass.Name {
			ingresses = append(ingresses, ingress)
		}
	}

	return ingresses
}
