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
	"context"
	"fmt"
	"reflect"
	"strings"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type TLSSecretData struct {
	// This would hold server certificate and any chain of trust.
	CaCertificateChain *string
	ServerCertificate  *string
	PrivateKey         *string
}

func getTlsSecretContent(namespace string, secretName string, client kubernetes.Interface) (*TLSSecretData, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	caCertificateChain := string(secret.Data["ca.crt"])
	serverCertificate := string(secret.Data["tls.crt"])
	privateKey := string(secret.Data["tls.key"])
	return &TLSSecretData{CaCertificateChain: &caCertificateChain, ServerCertificate: &serverCertificate, PrivateKey: &privateKey}, nil
}

func getCertificateNameFromSecret(secretName string) string {
	if secretName == "" {
		return ""
	}
	return fmt.Sprintf("ic-%s", secretName)
}

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

func isTrustAuthorityCaBundle(id string) bool {
	return strings.Contains(id, "cabundle")
}
