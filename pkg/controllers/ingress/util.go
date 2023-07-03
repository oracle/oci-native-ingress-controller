/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package ingress

import (
	"reflect"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

func compareHealthCheckers(healthCheckerDetails *ociloadbalancer.HealthCheckerDetails, healthChecker *ociloadbalancer.HealthChecker) bool {
	if reflect.DeepEqual(healthCheckerDetails.Protocol, healthChecker.Protocol) {
		if *healthChecker.Protocol == util.ProtocolTCP {
			return compareTcpHealthCheckerAttributes(healthCheckerDetails, healthChecker)
		} else if *healthChecker.Protocol == util.ProtocolHTTP {
			return compareHttpHealthCheckerAttributes(healthCheckerDetails, healthChecker)
		}
	}
	return false
}

func compareTcpHealthCheckerAttributes(healthCheckerDetails *ociloadbalancer.HealthCheckerDetails, healthChecker *ociloadbalancer.HealthChecker) bool {
	return reflect.DeepEqual(healthCheckerDetails.Port, healthChecker.Port) &&
		reflect.DeepEqual(healthCheckerDetails.IntervalInMillis, healthChecker.IntervalInMillis) &&
		reflect.DeepEqual(healthCheckerDetails.TimeoutInMillis, healthChecker.TimeoutInMillis) &&
		reflect.DeepEqual(healthCheckerDetails.Retries, healthChecker.Retries)
}

func compareHttpHealthCheckerAttributes(healthCheckerDetails *ociloadbalancer.HealthCheckerDetails, healthChecker *ociloadbalancer.HealthChecker) bool {
	return compareTcpHealthCheckerAttributes(healthCheckerDetails, healthChecker) &&
		reflect.DeepEqual(healthCheckerDetails.UrlPath, healthChecker.UrlPath) &&
		reflect.DeepEqual(healthCheckerDetails.ReturnCode, healthChecker.ReturnCode) &&
		reflect.DeepEqual(healthCheckerDetails.ResponseBodyRegex, healthChecker.ResponseBodyRegex) &&
		reflect.DeepEqual(healthCheckerDetails.IsForcePlainText, healthChecker.IsForcePlainText)
}
