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
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
)

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
