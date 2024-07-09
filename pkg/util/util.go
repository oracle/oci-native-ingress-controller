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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/util/sets"
	"strconv"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/pkg/errors"

	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"
)

const (
	IngressClassLoadBalancerIdAnnotation = "oci-native-ingress.oraclecloud.com/id"
	IngressClassIsDefault                = "ingressclass.kubernetes.io/is-default-class"

	PodReadinessConditionPrefix = "podreadiness.ingress.oraclecloud.com"

	IngressControllerFinalizer = "oci.oraclecloud.com/ingress-controller-protection"

	IngressListenerTlsCertificateAnnotation = "oci-native-ingress.oraclecloud.com/certificate-ocid"
	IngressBackendTlsEnabledAnnotation      = "oci-native-ingress.oraclecloud.com/backend-tls-enabled"

	//verify client certificate , only use the lisentner bound CA certificate to verify the client certificate .
	/*
	 mutual-tls-authentication: '[{"port": 80, "mode": "passthrough"}, {"port": 443, "mode": "verify","depth":1 }]'
	*/
	IngressListenerMutualTlsVerifyAnnotation = "oci-native-ingress.oraclecloud.com/mutual-tls-authentication"

	// IngressProtocolAnntoation - HTTP only for now
	// HTTP, HTTP2, TCP - accepted.
	IngressProtocolAnnotation = "oci-native-ingress.oraclecloud.com/protocol"

	IngressPolicyAnnotation          = "oci-native-ingress.oraclecloud.com/policy"
	IngressClassWafPolicyAnnotation  = "oci-native-ingress.oraclecloud.com/waf-policy-ocid"
	IngressClassFireWallIdAnnotation = "oci-native-ingress.oraclecloud.com/firewall-id"

	IngressHealthCheckProtocolAnnotation             = "oci-native-ingress.oraclecloud.com/healthcheck-protocol"
	IngressHealthCheckPortAnnotation                 = "oci-native-ingress.oraclecloud.com/healthcheck-port"
	IngressHealthCheckPathAnnotation                 = "oci-native-ingress.oraclecloud.com/healthcheck-path"
	IngressHealthCheckIntervalMillisecondsAnnotation = "oci-native-ingress.oraclecloud.com/healthcheck-interval-milliseconds"
	IngressHealthCheckTimeoutMillisecondsAnnotation  = "oci-native-ingress.oraclecloud.com/healthcheck-timeout-milliseconds"
	IngressHealthCheckRetriesAnnotation              = "oci-native-ingress.oraclecloud.com/healthcheck-retries"
	IngressHealthCheckReturnCodeAnnotation           = "oci-native-ingress.oraclecloud.com/healthcheck-return-code"
	IngressHealthCheckResponseBodyRegexAnnotation    = "oci-native-ingress.oraclecloud.com/healthcheck-response-regex"
	IngressHealthCheckForcePlainTextAnnotation       = "oci-native-ingress.oraclecloud.com/healthcheck-force-plaintext"
	IngressHttpListenerPortAnnotation                = "oci-native-ingress.oraclecloud.com/http-listener-port"
	IngressHttpsListenerPortAnnotation               = "oci-native-ingress.oraclecloud.com/https-listener-port"

	ProtocolTCP                            = "TCP"
	ProtocolHTTP                           = "HTTP"
	ProtocolHTTP2                          = "HTTP2"
	ProtocolHTTP2DefaultCipherSuite        = "oci-default-http2-ssl-cipher-suite-v1"
	DefaultBackendSetName                  = "default_ingress"
	DefaultHealthCheckProtocol             = ProtocolTCP
	DefaultHealthCheckPort                 = 0
	ZeroPort                               = 0
	DefaultHealthCheckTimeOutMilliSeconds  = 3000
	DefaultHealthCheckIntervalMilliSeconds = 10000
	DefaultHealthCheckRetries              = 3
	DefaultHealthCheckReturnCode           = 200
	DefaultBackendSetRoutingPolicy         = "LEAST_CONNECTIONS"

	CertificateCacheMaxAgeInMinutes = 10
	LBCacheMaxAgeInMinutes          = 1
	WAFCacheMaxAgeInMinutes         = 5

	MutualTlsAuthenticationVerify = "verify"
)

var ErrIngressClassNotReady = errors.New("ingress class not ready")

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

func GetIngressClassWafPolicy(ic *networkingv1.IngressClass) string {
	value, ok := ic.Annotations[IngressClassWafPolicyAnnotation]
	if !ok {
		return ""
	}

	return value
}

func GetIngressClassFireWallId(ic *networkingv1.IngressClass) string {
	value, ok := ic.Annotations[IngressClassFireWallIdAnnotation]
	if !ok {
		return ""
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

func GetBackendTlsEnabled(i *networkingv1.Ingress) bool {
	annotation := IngressBackendTlsEnabledAnnotation
	value, ok := i.Annotations[annotation]

	if !ok || strings.TrimSpace(value) == "" {
		return true
	}

	result, err := strconv.ParseBool(value)
	if err != nil {
		klog.Errorf("Error parsing value %s for flag %s as boolean. Setting the default value as 'true'", value, annotation)
		return true
	}

	return result
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

func GetIngressHttpListenerPort(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHttpListenerPortAnnotation]
	if !ok {
		return ZeroPort, nil
	}

	return strconv.Atoi(value)
}

func GetIngressHttpsListenerPort(i *networkingv1.Ingress) (int, error) {
	value, ok := i.Annotations[IngressHttpsListenerPortAnnotation]
	if !ok {
		return ZeroPort, nil
	}

	return strconv.Atoi(value)
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
			if ic.Annotations[IngressClassIsDefault] == "true" {
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

func GetCurrentTimeInUnixMillis() int64 {
	return time.Now().UnixMilli()
}

// GetTimeDifferenceInSeconds returns time difference in seconds of two timestamp values passed in Milliseconds
func GetTimeDifferenceInSeconds(startTime, endTime int64) float64 {
	return float64(endTime-startTime) / 1000
}

func PatchIngressClassWithAnnotation(client kubernetes.Interface, ic *networkingv1.IngressClass, annotationName string, annotationValue string) (error, bool) {

	patchBytes := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, annotationName, annotationValue))

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		_, err := client.NetworkingV1().IngressClasses().Patch(context.TODO(), ic.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		return err
	})

	if apierrors.IsConflict(err) {
		return errors.Wrapf(err, "updateMaxRetries(%d) limit was reached while attempting to add load balancer id annotation", retry.DefaultBackoff.Steps), true
	}

	if err != nil {
		return err, true
	}
	return nil, false
}

func GetTargetPortForService(svc *corev1.Service, namespace string, name string, port int32, portName string, isNodeportRequired bool) (int32, int32, error) {
	for _, p := range svc.Spec.Ports {
		if (p.Port != 0 && p.Port == port) || p.Name == portName {

			if !isNodeportRequired {
				if p.TargetPort.Type != intstr.Int {
					return 0, 0, fmt.Errorf("service %s/%s has non-integer ports: %s", namespace, name, p.Name)
				}
				return p.Port, p.TargetPort.IntVal, nil
			} else {
				if p.NodePort == 0 {
					return 0, 0, fmt.Errorf("service %s/%s has no nodeports: %s", namespace, name, p.Name)
				}
				return p.Port, p.NodePort, nil
			}

		}
	}

	return 0, 0, fmt.Errorf("service %s/%s does not have port: %s (%d)", namespace, name, portName, port)
}

func PathToServiceAndTargetPort(svc *corev1.Service, svcBackend networkingv1.IngressServiceBackend, namespace string, isNodePortRequired bool) (string, int32, int32, error) {

	svcPort, targetPort, err := GetTargetPortForService(svc, namespace, svcBackend.Name, svcBackend.Port.Number, svcBackend.Port.Name, isNodePortRequired)
	if err != nil {
		return "", 0, 0, err
	}
	return svcBackend.Name, svcPort, targetPort, nil
}

func ExtractServices(path networkingv1.HTTPIngressPath, svcLister corelisters.ServiceLister, ingress *networkingv1.Ingress) (networkingv1.IngressServiceBackend, *corev1.Service, error) {
	pSvc, err := getIngressBackend(path)
	if err != nil {
		return networkingv1.IngressServiceBackend{}, nil, err
	}

	svc, err := svcLister.Services(ingress.Namespace).Get(pSvc.Name)
	if err != nil {
		return networkingv1.IngressServiceBackend{}, nil, err
	}
	return pSvc, svc, nil
}

func getIngressBackend(path networkingv1.HTTPIngressPath) (networkingv1.IngressServiceBackend, error) {
	if path.Backend.Service == nil {
		return networkingv1.IngressServiceBackend{}, fmt.Errorf("backend service is not defined for ingress")
	}
	pSvc := *path.Backend.Service
	return pSvc, nil
}

func GetEndpoints(lister corelisters.EndpointsLister, namespace string, service string) ([]corev1.EndpointAddress, error) {
	endpoints, err := lister.Endpoints(namespace).Get(service)
	if err != nil {
		return nil, err
	}

	var addresses []corev1.EndpointAddress
	for _, endpoint := range endpoints.Subsets {
		for _, address := range endpoint.Addresses {
			if address.TargetRef == nil || address.TargetRef.Kind != "Pod" {
				continue
			}

			addresses = append(addresses, address)
		}
		for _, address := range endpoint.NotReadyAddresses {
			if address.TargetRef == nil || address.TargetRef.Kind != "Pod" {
				continue
			}

			addresses = append(addresses, address)
		}
	}

	return addresses, nil
}

// HandleErr checks if an error happened and makes sure we will retry later.
func HandleErr(queue workqueue.RateLimitingInterface, err error, message string, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		queue.Forget(key)
		return
	}

	if errors.Is(err, ErrIngressClassNotReady) {
		queue.AddAfter(key, 10*time.Second)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if queue.NumRequeues(key) < 5 {
		klog.Infof(message+" %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		queue.AddRateLimited(key)
		return
	}

	queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	klog.Infof("Dropping items %q out of the queue: %v", key, err)
}

func GetIngressesForClass(lister networkinglisters.IngressLister, ingressClass *networkingv1.IngressClass) ([]*networkingv1.Ingress, error) {

	ingresses, err := lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// Filter ingresses down to this ingress class we want to sync.
	var result []*networkingv1.Ingress
	for _, ingress := range ingresses {
		if ingress.Spec.IngressClassName == nil && ingressClass.Annotations[IngressClassIsDefault] == "true" {
			// ingress has on class name defined and our ingress class is default
			result = append(result, ingress)
		}
		if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName == ingressClass.Name {
			result = append(result, ingress)
		}
	}
	return result, nil
}

func NewBackend(ip string, port int32) ociloadbalancer.BackendDetails {
	return ociloadbalancer.BackendDetails{
		IpAddress: common.String(ip),
		Port:      common.Int(int(port)),
		Weight:    common.Int(1),
		Drain:     common.Bool(false),
		Backup:    common.Bool(false),
		Offline:   common.Bool(false),
	}
}

func RetrievePods(endpointLister corelisters.EndpointsLister, podLister corelisters.PodLister, namespace string, svcName string) ([]*corev1.Pod, error) {
	epAddress, err := GetEndpoints(endpointLister, namespace, svcName)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch endpoints for %s/%s: %w", namespace, svcName, err)
	}

	var pods []*corev1.Pod
	for _, epAddr := range epAddress {
		pod, err := podLister.Pods(namespace).Get(epAddr.TargetRef.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch pod %s/%s: %w", namespace, epAddr.TargetRef.Name, err)
		}
		pods = append(pods, pod)
	}
	return pods, nil
}


func GetMutualTlsVerifyAnnotation(i *networkingv1.Ingress) string {
	mtlsVerifyPorts, ok := i.Annotations[IngressListenerMutualTlsVerifyAnnotation]
	if !ok {
		return ""
	}

	return strings.ToLower(mtlsVerifyPorts)
}
func DetermineListenerPort(ingress *networkingv1.Ingress, tlsConfiguredHosts *sets.String, host string, servicePort int32) (int32, error) {
	annotatedHttpPort, err := GetIngressHttpListenerPort(ingress)
	if err != nil {
		return 0, fmt.Errorf("error parsing Ingress Http Listener Port: %w", err)
	}
	annotatedHttpsPort, err := GetIngressHttpsListenerPort(ingress)
	if err != nil {
		return 0, fmt.Errorf("error parsing Ingress Https Listener Port: %w", err)
	}

	isCertOcidPresent := GetListenerTlsCertificateOcid(ingress) != nil

	listenerPort := servicePort
	if isCertOcidPresent || tlsConfiguredHosts.Has(host) {
		if annotatedHttpsPort != ZeroPort {
			listenerPort = int32(annotatedHttpsPort)
		}
	} else if annotatedHttpPort != ZeroPort {
		listenerPort = int32(annotatedHttpPort)
	}

	return listenerPort, nil
}

// b1 and b2 are assumed non-nil
func IsBackendServiceEqual(b1 *networkingv1.IngressBackend, b2 *networkingv1.IngressBackend) bool {
	if b1.Service == b2.Service {
		return true
	}
	if b1.Service == nil || b2.Service == nil || *b1.Service != *b2.Service {
		return false
	}
	return true
}

func IsIngressProtocolTCP(ingress *networkingv1.Ingress) bool {
	return GetIngressProtocol(ingress) == ProtocolTCP
}
