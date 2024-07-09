/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package state

import (
	"encoding/json"
	"fmt"
	"reflect"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"github.com/pkg/errors"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/klog/v2"
)

const (
	ArtifactTypeSecret      = "secret"
	ArtifactTypeCertificate = "certificate"

	PortConflictMessage              = "validation failure: service port %d configured with multiple certificates or secrets"
	HealthCheckerConflictMessage     = "validation failure: conflict with health checker configured for backend set %s"
	PolicyConflictMessage            = "validation failure: conflict with policy configured for backend set %s"
	ProtocolConflictMessage          = "validation failure: conflict with protocol configured for listener %d"
	DefaultBackendSetConflictMessage = "validation failure: conflict with default backend set for TCP listener %d"
)

type TlsConfig struct {
	Artifact string
	Type     string
}
type MutualTlsPortConfig struct {
	Port        int32  `json:"port"`
	Mode        string `json:"mode"`
	Depth       int    `json:"depth,omitempty"`
	TrustCACert string `json:"trustcacert"`
}
type StateStore struct {
	IngressClassLister networkinglisters.IngressClassLister
	IngressLister      networkinglisters.IngressLister
	ServiceLister      corelisters.ServiceLister
	IngressGroupState  IngressClassState
	IngressState       map[string]IngressState
	metricsCollector   *metric.IngressCollector
}

type IngressClassState struct {
	BackendSets                sets.String
	BackendSetHealthCheckerMap map[string]*ociloadbalancer.HealthCheckerDetails
	BackendSetPolicyMap        map[string]string
	BackendSetTLSConfigMap     map[string]TlsConfig
	Listeners                  sets.Int32
	ListenerProtocolMap        map[int32]string
	ListenerTLSConfigMap       map[int32]TlsConfig
	MtlsPorts                  map[int32]MutualTlsPortConfig
	ListenerDefaultBsMap       map[int32]string
}

type IngressState struct {
	BackendSets sets.String
	Ports       sets.Int32
	ClassName   string
}

func NewStateStore(ingressClassLister networkinglisters.IngressClassLister,
	ingressLister networkinglisters.IngressLister,
	serviceLister corelisters.ServiceLister, collector *metric.IngressCollector) *StateStore {
	return &StateStore{
		IngressClassLister: ingressClassLister,
		IngressLister:      ingressLister,
		ServiceLister:      serviceLister,
		IngressGroupState:  IngressClassState{},
		IngressState:       map[string]IngressState{},
		metricsCollector:   collector,
	}
}

func (s *StateStore) BuildState(ingressClass *networkingv1.IngressClass) error {

	startBuildTime := util.GetCurrentTimeInUnixMillis()
	klog.Infof("Starting to build state for ingress class %s", ingressClass.Name)
	ingressList, err := s.IngressLister.List(labels.Everything())
	if err != nil {
		return errors.Wrap(err, "error listing ingress")
	}

	var ingressGroup []*networkingv1.Ingress
	for _, ing := range ingressList {
		if ((ing.Spec.IngressClassName == nil && ingressClass.Annotations[util.IngressClassIsDefault] == "true") ||
			(ing.Spec.IngressClassName != nil && ingressClass.Name == *ing.Spec.IngressClassName)) &&
			!util.IsIngressDeleting(ing) {
			ingressGroup = append(ingressGroup, ing)
		}
	}

	klog.Infof("Found %d ingress resources related to ingress class %s", len(ingressGroup), ingressClass.Name)
	bsTLSConfigMap := make(map[string]TlsConfig)
	listenerProtocolMap := make(map[int32]string)
	listenerTLSConfigMap := make(map[int32]TlsConfig)
	listenerDefaultBsMap := make(map[int32]string)
	bsHealthCheckerMap := make(map[string]*ociloadbalancer.HealthCheckerDetails)
	bsPolicyMap := make(map[string]string)
	allBackendSets := sets.NewString(util.DefaultBackendSetName)
	allListeners := sets.NewInt32()

	// add mtls verify configmap
	mutualTlsPortConfigMap := make(map[int32]MutualTlsPortConfig)

	bsHealthCheckerMap[DefaultIngressName] = util.GetDefaultHeathChecker()
	bsPolicyMap[DefaultIngressName] = util.DefaultBackendSetRoutingPolicy

	bsHealthCheckerMap[util.DefaultBackendSetName] = util.GetDefaultHeathChecker()
	bsPolicyMap[util.DefaultBackendSetName] = util.DefaultBackendSetRoutingPolicy

	for _, ing := range ingressGroup {
		hostSecretMap := make(map[string]string)
		tlsConfiguredHosts := sets.NewString()
		desiredPorts := sets.NewInt32()

		validateMtlsPortAnnotationJson, err := ParseMutualTlsAnnotationJSON(ing)
		klog.Infof("Ingress name: %s, validateMtlsPortAnnotationJson  %s", util.PrettyPrint(ing.Name), util.PrettyPrint(validateMtlsPortAnnotationJson))

		if err != nil {
			klog.Infof("Error parsing validateMtlsPortAnnotationJson JSON:", err)
			return nil
		}

		for _, configPort := range validateMtlsPortAnnotationJson {
			// add new mtls port to map
			mutualTlsPortConfigMap[configPort.Port] = configPort
		}
		klog.Infof(" mutualTlsPortConfigMap  %s, mutualTlsPortConfigMap  %s", util.PrettyPrint(ing.Name), util.PrettyPrint(mutualTlsPortConfigMap))

		// we always expect the default_ingress backendset
		desiredBackendSets := sets.NewString(util.DefaultBackendSetName)

		// For now, we ignore TLS spec for TCP ingresses, revisit this if required in future
		if !util.IsIngressProtocolTCP(ing) {
			for ingressItem := range ing.Spec.TLS {
				ingressTls := ing.Spec.TLS[ingressItem]
				for j := range ingressTls.Hosts {
					host := ingressTls.Hosts[j]
					tlsConfiguredHosts.Insert(host)
					hostSecretMap[host] = ingressTls.SecretName
				}
			}
		}

		for _, rule := range ing.Spec.Rules {
			host := rule.Host

			for _, path := range rule.HTTP.Paths {
				serviceName, servicePort, err := util.PathToServiceAndPort(ing.Namespace, path, s.ServiceLister)
				if err != nil {
					return errors.Wrap(err, "error finding service and port")
				}


				desiredPorts.Insert(servicePort)
				allListeners.Insert(servicePort)

				listenerPort, err := util.DetermineListenerPort(ing, &tlsConfiguredHosts, host, servicePort)
				if err != nil {
					return errors.Wrap(err, "error determining listener port")
				}

				desiredPorts.Insert(listenerPort)
				allListeners.Insert(listenerPort)

				bsName := util.GenerateBackendSetName(ing.Namespace, serviceName, servicePort)
				desiredBackendSets.Insert(bsName)
				allBackendSets.Insert(bsName)

				err = validateListenerProtocol(ing, listenerProtocolMap, listenerPort)
				if err != nil {
					return err
				}

				err = validateListenerDefaultBackendSet(ing, listenerDefaultBsMap, listenerPort, bsName)
				if err != nil {
					return err
				}

				err = validateBackendSetHealthChecker(ing, bsHealthCheckerMap, bsName)
				if err != nil {
					return err
				}

				err = validateBackendSetPolicy(ing, bsPolicyMap, bsName)
				if err != nil {
					return err
				}

				err = validateTlsConfig(ing, listenerPort, bsName, host, listenerTLSConfigMap, bsTLSConfigMap, hostSecretMap)
				if err != nil {
					return err
				}
			}
		}

		s.IngressState[ing.Name] = IngressState{
			Ports:       desiredPorts,
			BackendSets: desiredBackendSets,
			ClassName:   ingressClass.Name,
		}
	}
	s.IngressGroupState = IngressClassState{
		BackendSets:                allBackendSets,
		BackendSetHealthCheckerMap: bsHealthCheckerMap,
		BackendSetPolicyMap:        bsPolicyMap,
		BackendSetTLSConfigMap:     bsTLSConfigMap,
		Listeners:                  allListeners,
		ListenerProtocolMap:        listenerProtocolMap,
		ListenerTLSConfigMap:       listenerTLSConfigMap,
		//add mutual tls port configmap
		MtlsPorts: mutualTlsPortConfigMap,
		ListenerDefaultBsMap:       listenerDefaultBsMap,
	}

	klog.Infof("Ingress Group state %s, Ingress state %s", util.PrettyPrint(s.IngressGroupState), util.PrettyPrint(s.IngressState))
	klog.Infof("State build complete..")

	endBuildTime := util.GetCurrentTimeInUnixMillis()
	if s.metricsCollector != nil {
		s.metricsCollector.AddStateBuildTime(util.GetTimeDifferenceInSeconds(startBuildTime, endBuildTime))
	}
	return nil
}

func validateTlsConfig(ingress *networkingv1.Ingress, listenerPort int32, bsName string, host string, listenerTLSConfigMap map[int32]TlsConfig,
	bsTLSConfigMap map[string]TlsConfig, hostSecretMap map[string]string) error {
	bsTLSEnabled := util.GetBackendTlsEnabled(ingress)
	certificateId := util.GetListenerTlsCertificateOcid(ingress)

	if certificateId != nil && !util.IsIngressProtocolTCP(ingress) {
		tlsPortDetail, ok := listenerTLSConfigMap[listenerPort]
		if ok {
			err := validatePortInUse(tlsPortDetail, "", certificateId, listenerPort)
			if err != nil {
				return errors.Wrap(err, "validating certificates")
			}
		}
		config := TlsConfig{
			Type:     ArtifactTypeCertificate,
			Artifact: *certificateId,
		}
		listenerTLSConfigMap[listenerPort] = config
		updateBackendTlsStatus(bsTLSEnabled, bsTLSConfigMap, bsName, config)
	}

	if host != "" {
		secretName, ok := hostSecretMap[host]

		if ok && secretName != "" {
			tlsPortDetail, ok := listenerTLSConfigMap[listenerPort]
			if ok {
				err := validatePortInUse(tlsPortDetail, secretName, nil, listenerPort)
				if err != nil {
					return errors.Wrap(err, "validating secrets")
				}
			}
			config := TlsConfig{
				Type:     ArtifactTypeSecret,
				Artifact: secretName,
			}
			listenerTLSConfigMap[listenerPort] = config
			updateBackendTlsStatus(bsTLSEnabled, bsTLSConfigMap, bsName, config)
		}
	}

	return nil
}

func updateBackendTlsStatus(bsTLSEnabled bool, bsTLSConfigMap map[string]TlsConfig, bsName string, config TlsConfig) {
	if bsTLSEnabled {
		bsTLSConfigMap[bsName] = config
	} else {
		config := TlsConfig{
			Type:     "",
			Artifact: "",
		}
		bsTLSConfigMap[bsName] = config
	}
}

func validateBackendSetHealthChecker(ingressResource *networkingv1.Ingress,
	bsHealthCheckerMap map[string]*ociloadbalancer.HealthCheckerDetails, bsName string) error {
	defaultHealthChecker := util.GetDefaultHeathChecker()
	healthChecker, err := util.GetHealthChecker(ingressResource)
	if err != nil {
		return err
	}
	healthCheckerCurrent, ok := bsHealthCheckerMap[bsName]
	if ok && !reflect.DeepEqual(healthChecker, defaultHealthChecker) && !reflect.DeepEqual(healthChecker, healthCheckerCurrent) {
		return fmt.Errorf(HealthCheckerConflictMessage, bsName)
	}
	bsHealthCheckerMap[bsName] = healthChecker
	return nil
}

func validateBackendSetPolicy(ingressResource *networkingv1.Ingress, bsPolicyMap map[string]string, bsName string) error {
	policy := util.GetIngressPolicy(ingressResource)

	policyCurrent, ok := bsPolicyMap[bsName]
	if ok && policyCurrent != policy {
		return fmt.Errorf(PolicyConflictMessage, bsName)
	}
	bsPolicyMap[bsName] = policy
	return nil
}

func validateListenerProtocol(ingressResource *networkingv1.Ingress, listenerProtocolMap map[int32]string, listenerPort int32) error {
	protocol := util.GetIngressProtocol(ingressResource)

	protocolCurrent, ok := listenerProtocolMap[listenerPort]
	if ok && protocolCurrent != protocol {
		return fmt.Errorf(ProtocolConflictMessage, listenerPort)
	}
	listenerProtocolMap[listenerPort] = protocol
	return nil
}

// backendSetName is ignored if ingress protocol is not TCP, uses default_ingress in that scenario
func validateListenerDefaultBackendSet(ingressResource *networkingv1.Ingress,
	listenerDefaultBsMap map[int32]string, listenerPort int32, backendSetName string) error {
	if !util.IsIngressProtocolTCP(ingressResource) {
		backendSetName = util.DefaultBackendSetName
	}

	defaultBackendSetCurrent, ok := listenerDefaultBsMap[listenerPort]
	if ok && defaultBackendSetCurrent != backendSetName {
		return fmt.Errorf(DefaultBackendSetConflictMessage, listenerPort)
	}
	listenerDefaultBsMap[listenerPort] = backendSetName
	return nil
}

func (s *StateStore) GetBackendSetHealthChecker(bsName string) *ociloadbalancer.HealthCheckerDetails {
	return s.IngressGroupState.BackendSetHealthCheckerMap[bsName]
}

func (s *StateStore) GetBackendSetPolicy(bsName string) string {
	return s.IngressGroupState.BackendSetPolicyMap[bsName]
}

func (s *StateStore) GetIngressBackendSets(ingressName string) sets.String {
	ingress, ok := s.IngressState[ingressName]
	if ok {
		return ingress.BackendSets
	}
	return nil
}

func (s *StateStore) GetIngressPorts(ingressName string) sets.Int32 {
	ingress, ok := s.IngressState[ingressName]
	if ok {
		return ingress.Ports
	}
	return nil
}

func (s *StateStore) GetListenerProtocol(listenerPort int32) string {
	return s.IngressGroupState.ListenerProtocolMap[listenerPort]
}

func (s *StateStore) GetListenerDefaultBackendSet(listenerPort int32) string {
	return s.IngressGroupState.ListenerDefaultBsMap[listenerPort]
}

func (s *StateStore) GetTLSConfigForListener(port int32) (string, string) {
	portTLSConfig, ok := s.IngressGroupState.ListenerTLSConfigMap[port]
	if ok {
		return portTLSConfig.Artifact, portTLSConfig.Type
	}
	return "", ""
}

// func (s *StateStore) GetMutualTlsPortConfigForListener(port int32) MutualTlsPortConfig {
// 	portMtlsConfig, ok := s.IngressGroupState.MtlsPorts[port]
// 	if ok {
// 		return portMtlsConfig
// 	}
// 	return MutualTlsPortConfig{}
// }

func (s *StateStore) GetMutualTlsPortConfigForListener(port int32) (string, int, string) {
	portMtlsConfig, ok := s.IngressGroupState.MtlsPorts[port]
	if ok {
		return portMtlsConfig.Mode, portMtlsConfig.Depth, portMtlsConfig.TrustCACert
	}
	return "", 0, ""
}

func (s *StateStore) GetTLSConfigForBackendSet(bsName string) (string, string) {
	bsTLSConfig, ok := s.IngressGroupState.BackendSetTLSConfigMap[bsName]
	if ok {
		return bsTLSConfig.Artifact, bsTLSConfig.Type
	}
	return "", ""
}

func (s *StateStore) GetAllBackendSetForIngressClass() sets.String {
	return s.IngressGroupState.BackendSets
}

func (s *StateStore) GetAllListenersForIngressClass() sets.Int32 {
	return s.IngressGroupState.Listeners
}

func validatePortInUse(listenerTLSConfig TlsConfig, secretName string, certificateId *string, servicePort int32) error {
	existing := listenerTLSConfig.Artifact
	artifactType := listenerTLSConfig.Type
	if (artifactType == ArtifactTypeSecret && certificateId != nil) ||
		(artifactType == ArtifactTypeCertificate && secretName != "") ||
		(artifactType == ArtifactTypeSecret && existing != "" && existing != secretName) ||
		(artifactType == ArtifactTypeCertificate && certificateId != nil && existing != *certificateId) {
		return fmt.Errorf(PortConflictMessage, servicePort)
	}
	return nil
}

func ParseMutualTlsAnnotationJSON(ing *networkingv1.Ingress) ([]MutualTlsPortConfig, error) {

	mtlsPortsAnnotation := util.GetMutualTlsVerifyAnnotation(ing)
	klog.Infof("Ingress name ParseMutualTlsAnnotationJSON %s, mtlsPortsAnnotation  %s", util.PrettyPrint(ing.Name), util.PrettyPrint(mtlsPortsAnnotation))

	s := mtlsPortsAnnotation
	//check if s is empty
	if s == "" {
		return []MutualTlsPortConfig{}, nil
	}
	// Check if the string conforms to JSON format
	if !isValidJSON(s) {
		return nil, fmt.Errorf("Original string does not conform to JSON format")
	}

	// Parse JSON string
	var mtls []MutualTlsPortConfig
	err := json.Unmarshal([]byte(s), &mtls)
	if err != nil {
		return nil, err
	}

	// Remove sub-objects without the 'port' field
	var validMtls []MutualTlsPortConfig
	for _, config := range mtls {
		if config.Port != 0 {
			validMtls = append(validMtls, config)
		}
	}

	return validMtls, nil
}

// Check if the string conforms to JSON format
func isValidJSON(s string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js) == nil
}
