/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package util

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/klog/v2"

	"bitbucket.oci.oraclecorp.com/oke/oci-native-ingress-controller/api/v1beta1"
)

const (
	IngressClassLoadBalancerIdAnnotation = "oci-native-ingress.oraclecloud.com/id"

	PodReadinessConditionPrefix = "podreadiness.ingress.oraclecloud.com"

	IngressControllerFinalizer = "oci.oraclecloud.com/ingress-controller-protection"

	IngressListenerTlsCertificateAnnotation = "oci-native-ingress.oraclecloud.com/certificate-ocid"

	// IngressProtocolAnntoation - HTTP only for now
	// HTTP, HTTP2 - accepted.
	IngressProtocolAnnotation = "oci-native-ingress.oraclecloud.com/protocol"

	IngressPolicyAnnotation = "oci-native-ingress.oraclecloud.com/policy"

	IngressHealthCheckProtocolAnnotation             = "oci-native-ingress.oraclecloud.com/healthcheck-protocol"
	IngressHealthCheckPortAnnotation                 = "oci-native-ingress.oraclecloud.com/healthcheck-port"
	IngressHealthCheckPathAnnotation                 = "oci-native-ingress.oraclecloud.com/healthcheck-path"
	IngressHealthCheckIntervalMillisecondsAnnotation = "oci-native-ingress.oraclecloud.com/healthcheck-interval-milliseconds"
	IngressHealthCheckTimeoutMillisecondsAnnotation  = "oci-native-ingress.oraclecloud.com/healthcheck-timeout-milliseconds"
	IngressHealthCheckRetriesAnnotation              = "oci-native-ingress.oraclecloud.com/healthcheck-retries"
	IngressHealthCheckReturnCodeAnnotation           = "oci-native-ingress.oraclecloud.com/healthcheck-return-code"
	IngressHealthCheckResponseBodyRegexAnnotation    = "oci-native-ingress.oraclecloud.com/healthcheck-response-regex"
	IngressHealthCheckForcePlainTextAnnotation       = "oci-native-ingress.oraclecloud.com/healthcheck-force-plaintext"

	ProtocolTCP                            = "TCP"
	ProtocolHTTP                           = "HTTP"
	ProtocolHTTP2                          = "HTTP2"
	ProtocolHTTP2DefaultCipherSuite        = "oci-default-http2-ssl-cipher-suite-v1"
	DefaultHealthCheckProtocol             = ProtocolTCP
	DefaultHealthCheckPort                 = 0
	DefaultHealthCheckTimeOutMilliSeconds  = 3000
	DefaultHealthCheckIntervalMilliSeconds = 10000
	DefaultHealthCheckRetries              = 3
	DefaultHealthCheckReturnCode           = 200
	DefaultBackendSetRoutingPolicy         = "LEAST_CONNECTIONS"

	CertificateCacheMaxAgeInMinutes = 10
	LBCacheMaxAgeInMinutes          = 1
)

func GetIngressClassCompartmentId(p *v1beta1.IngressClassParameters, defaultCompartment string) string {
	if strings.TrimSpace(p.Spec.CompartmentId) == "" {
		return defaultCompartment
	}

	return p.Spec.CompartmentId
}

func GetIngressClassLoadBalancerName(ic *networkingv1.IngressClass, p *v1beta1.IngressClassParameters) string {
	if strings.TrimSpace(p.Spec.LoadBalancerName) != "" {
		return p.Spec.LoadBalancerName
	}

	return fmt.Sprintf("k8s-%s", ic.Name)
}

func GetIngressClassSubnetId(p *v1beta1.IngressClassParameters, defaultSubnet string) string {
	if strings.TrimSpace(p.Spec.SubnetId) == "" {
		return defaultSubnet
	}

	return p.Spec.SubnetId
}

func GetIngressPolicy(i *networkingv1.Ingress) string {
	value, ok := i.Annotations[IngressPolicyAnnotation]
	if !ok {
		return DefaultBackendSetRoutingPolicy
	}

	return value
}

func GetIngressProtocol(i *networkingv1.Ingress) string {
	protocol, ok := i.Annotations[IngressProtocolAnnotation]
	if !ok {
		return ProtocolHTTP
	}
	return strings.ToUpper(protocol)
}

func GetIngressClassLoadBalancerId(ic *networkingv1.IngressClass) string {
	id, ok := ic.Annotations[IngressClassLoadBalancerIdAnnotation]
	if !ok {
		return ""
	}

	return id
}

func GetListenerTlsCertificateOcid(i *networkingv1.Ingress) *string {
	value, ok := i.Annotations[IngressListenerTlsCertificateAnnotation]
	if !ok {
		return nil
	}
	return &value
}

func GetIngressHealthCheckProtocol(i *networkingv1.Ingress) string {
	protocol, ok := i.Annotations[IngressHealthCheckProtocolAnnotation]
	if !ok {
		return DefaultHealthCheckProtocol
	}

	return strings.ToUpper(protocol)
}

func GetIngressHealthCheckPath(i *networkingv1.Ingress) string {
	value, ok := i.Annotations[IngressHealthCheckPathAnnotation]
	if !ok {
		return ""
	}

	return value
}

func GetIngressHealthCheckPort(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHealthCheckPortAnnotation]
	if !ok {
		return DefaultHealthCheckPort, nil
	}

	return strconv.Atoi(value)
}

func GetIngressHealthCheckIntervalMilliseconds(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHealthCheckIntervalMillisecondsAnnotation]
	if !ok {
		return DefaultHealthCheckIntervalMilliSeconds, nil
	}

	return strconv.Atoi(value)
}

func GetIngressHealthCheckTimeoutMilliseconds(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHealthCheckTimeoutMillisecondsAnnotation]
	if !ok {
		return DefaultHealthCheckTimeOutMilliSeconds, nil
	}

	return strconv.Atoi(value)
}

func GetIngressHealthCheckRetries(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHealthCheckRetriesAnnotation]
	if !ok {
		return DefaultHealthCheckRetries, nil
	}

	return strconv.Atoi(value)
}

func GetIngressHealthCheckReturnCode(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHealthCheckReturnCodeAnnotation]
	if !ok {
		return DefaultHealthCheckReturnCode, nil
	}

	return strconv.Atoi(value)
}

func GetIngressHealthCheckResponseBodyRegex(i *networkingv1.Ingress) string {
	value, ok := i.Annotations[IngressHealthCheckResponseBodyRegexAnnotation]
	if !ok {
		return ""
	}

	return value
}

func GetIngressHealthCheckForcePlainText(i *networkingv1.Ingress) bool {
	annotation := IngressHealthCheckForcePlainTextAnnotation
	value, ok := i.Annotations[annotation]
	if !ok || strings.TrimSpace(value) == "" {
		return false
	}

	result, err := strconv.ParseBool(value)
	if err != nil {
		klog.Errorf("Error parsing value %s for flag %s as boolean. Setting the default value as 'false'", value, annotation)
		return false
	}

	return result
}

func PathToRoutePolicyName(ingressName string, host string, path networkingv1.HTTPIngressPath) string {
	h := sha256.New()
	h.Write([]byte(ingressName))
	h.Write([]byte(host))
	h.Write([]byte(*path.PathType))
	h.Write([]byte(path.Path))
	h.Write([]byte(path.Backend.Service.Name))
	return fmt.Sprintf("k8s_%.10s", hex.EncodeToString(h.Sum(nil)))
}

func GenerateBackendSetName(namespace string, serviceName string, port int32) string {
	servicePort := strconv.Itoa(int(port))
	h := sha256.New()
	h.Write([]byte(namespace))
	h.Write([]byte(serviceName))
	h.Write([]byte(servicePort))
	bsName := fmt.Sprintf("bs_%.15s", hex.EncodeToString(h.Sum(nil)))
	return bsName
}

func GenerateListenerName(servicePort int32) string {
	return fmt.Sprintf("%s_%d", "route", servicePort)
}

func GetPodReadinessCondition(ingressName string, host string, path networkingv1.HTTPIngressPath) corev1.PodConditionType {
	return corev1.PodConditionType(fmt.Sprintf("%s/%s", PodReadinessConditionPrefix, PathToRoutePolicyName(ingressName, host, path)))
}

func GetIngressClass(ingress *networkingv1.Ingress, ingressClassLister networkinglisters.IngressClassLister) (*networkingv1.IngressClass, error) {
	icList, err := ingressClassLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	if ingress.Spec.IngressClassName == nil {
		// find default ingress class since ingress has no class defined.
		for _, ic := range icList {
			if ic.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
				klog.InfoS("Found default ingress class: %s ", ic.Name)
				return ic, nil
			}
		}

		return nil, nil
	}

	for _, ic := range icList {
		if ic.Name == *ingress.Spec.IngressClassName {
			return ic, nil
		}
	}

	return nil, errors.New("ingress class not found for ingress")
}

func PathToServiceAndPort(ingressNamespace string, path networkingv1.HTTPIngressPath, serviceLister corelisters.ServiceLister) (string, int32, error) {
	if path.Backend.Service == nil {
		return "", 0, fmt.Errorf("backend service is not defined for ingress")
	}

	pSvc := *path.Backend.Service
	if pSvc.Port.Number != 0 {
		return pSvc.Name, pSvc.Port.Number, nil

	}

	// else name is used so lets map it to port
	svc, err := serviceLister.Services(ingressNamespace).Get(pSvc.Name)
	if err != nil {
		return "", 0, err
	}

	for _, port := range svc.Spec.Ports {
		if port.Name == pSvc.Port.Name {
			return pSvc.Name, port.Port, nil
		}
	}

	return "", 0, fmt.Errorf("port named %s for service backend %s was not found", pSvc.Port.Name, pSvc.Name)
}

func GetHealthChecker(i *networkingv1.Ingress) (*ociloadbalancer.HealthCheckerDetails, error) {
	interval, err := GetIngressHealthCheckIntervalMilliseconds(i)
	if err != nil {
		return nil, fmt.Errorf("error parsing health check interval: %w", err)
	}

	timeout, err := GetIngressHealthCheckTimeoutMilliseconds(i)
	if err != nil {
		return nil, fmt.Errorf("error parsing health check timeout: %w", err)
	}

	retries, err := GetIngressHealthCheckRetries(i)
	if err != nil {
		return nil, fmt.Errorf("error parsing health check retries: %w", err)
	}

	port, err := GetIngressHealthCheckPort(i)
	if err != nil {
		return nil, fmt.Errorf("error parsing health check retries: %w", err)
	}

	protocol := GetIngressHealthCheckProtocol(i)

	switch protocol {
	case ProtocolTCP:

		return &ociloadbalancer.HealthCheckerDetails{
			Protocol:         common.String(ProtocolTCP),
			Port:             common.Int(port),
			TimeoutInMillis:  common.Int(timeout),
			IntervalInMillis: common.Int(interval),
			Retries:          common.Int(retries),
		}, nil

	case ProtocolHTTP:

		returnCode, err := GetIngressHealthCheckReturnCode(i)
		if err != nil {
			return nil, fmt.Errorf("error parsing health check return code: %w", err)
		}

		responseBodyRegex := GetIngressHealthCheckResponseBodyRegex(i)
		urlPath := GetIngressHealthCheckPath(i)
		isForcePlainText := GetIngressHealthCheckForcePlainText(i)

		return &ociloadbalancer.HealthCheckerDetails{
			Protocol:          common.String(ProtocolHTTP),
			Port:              common.Int(port),
			UrlPath:           common.String(urlPath),
			TimeoutInMillis:   common.Int(timeout),
			IntervalInMillis:  common.Int(interval),
			Retries:           common.Int(retries),
			ReturnCode:        common.Int(returnCode),
			ResponseBodyRegex: common.String(responseBodyRegex),
			IsForcePlainText:  common.Bool(isForcePlainText),
		}, nil

	default:
		return nil, fmt.Errorf("%s unknown protocol found for healthcheck", protocol)
	}
}

func GetDefaultHeathChecker() *ociloadbalancer.HealthCheckerDetails {
	return &ociloadbalancer.HealthCheckerDetails{
		Protocol:         common.String(DefaultHealthCheckProtocol),
		Port:             common.Int(DefaultHealthCheckPort),
		TimeoutInMillis:  common.Int(DefaultHealthCheckTimeOutMilliSeconds),
		IntervalInMillis: common.Int(DefaultHealthCheckIntervalMilliSeconds),
		Retries:          common.Int(DefaultHealthCheckRetries),
	}
}

func IsIngressDeleting(i *networkingv1.Ingress) bool {
	return i.DeletionTimestamp != nil
}

func PrettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", " ")
	return string(s)
}
