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
	"fmt"
	"reflect"
	"sort"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/metric"
	"github.com/oracle/oci-native-ingress-controller/pkg/tlspolicy"
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

	PortConflictMessage               = "validation failure: service port %d has multiple certificate or secret configs across ingresses in the ingress class"
	MtlsConflictMessage               = "validation failure: listener port %d configured with multiple mTLS configurations"
	MtlsRequiresTlsMessage            = "validation failure: listener port %d enables mTLS without TLS"
	HealthCheckerConflictMessage      = "validation failure: incompatible health checker config across ingresses sharing backend set %s in the ingress class"
	PolicyConflictMessage             = "validation failure: incompatible policy config across ingresses sharing backend set %s in the ingress class"
	ProtocolConflictMessage           = "validation failure: incompatible protocol config across ingresses sharing listener %d in the ingress class"
	DefaultBackendSetConflictMessage  = "validation failure: incompatible default backend set across TCP ingresses sharing listener %d in the ingress class"
	SessionPersistenceEmptyMessage    = "validation failure: empty session persistence configuration for backend set %s"
	BackendTlsEnabledConflictMessage  = "validation failure: incompatible backend-tls-enabled config across ingresses sharing backend set %s in the ingress class"
	BackendTlsArtifactConflictMessage = "validation failure: incompatible backend TLS certificate or secret across ingresses sharing backend set %s in the ingress class"
	TLSPolicyInvalidAnnotationMessage = "TLSPolicyInvalidAnnotation: %s %s ingress %s: %w"
	TLSPolicyConflictMessage          = "TLSPolicyConflict: %s %s annotation %s field %s conflicts between ingress %s value %q and ingress %s value %q"
)

type TlsConfig struct {
	Artifact  string
	Type      string
	Namespace string
}

type ListenerTLSConfig struct {
	TlsConfigs []TlsConfig
}

// MtlsConfig controls client-certificate verification on a TLS listener.
type MtlsConfig struct {
	TrustedCertificateAuthorityIds []string
	VerifyDepth                    int
}

// listenerTLSCandidate is a pre-normalized listener TLS config discovered before deterministic sort and de-dupe.
type listenerTLSCandidate struct {
	IngressKey     string
	DiscoveryOrder int
	Config         TlsConfig
}

type backendTLSStatus struct {
	Enabled             bool
	HasTLSArtifactInput bool
	Config              TlsConfig
}

type tlsPolicyCandidate struct {
	IngressKey     string
	AnnotationName string
	RawValue       string
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
	BackendSets                     sets.String
	BackendSetHealthCheckerMap      map[string]*ociloadbalancer.HealthCheckerDetails
	BackendSetPolicyMap             map[string]string
	BackendSetTLSConfigMap          map[string]TlsConfig
	BackendSetTLSPolicyMap          map[string]*tlspolicy.ExplicitTLSPolicy
	BackendSetSessionPersistenceMap map[string]SessionPersistence
	Listeners                       sets.Int32
	ListenerProtocolMap             map[int32]string
	ListenerTLSConfigMap            map[int32]ListenerTLSConfig
	ListenerMtlsConfigMap           map[int32]MtlsConfig
	ListenerTLSPolicyMap            map[int32]*tlspolicy.ExplicitTLSPolicy
	ListenerDefaultBsMap            map[int32]string
	// ListenerBackendSetMap includes backend sets reachable through listener defaults,
	// path rules, and the routing-policy rules derived from those paths.
	ListenerBackendSetMap map[int32]sets.String
}

type IngressState struct {
	BackendSets sets.String
	Ports       sets.Int32
	ClassName   string
}

// SessionPersistence holds desired session persistence config for a backend set.
// Exactly one of the pointers should be non-nil. If both are nil and the session
// persistence annotation is present on the ingress, this is treated as a validation error.
// If no annotation is present, both may be nil (persistence disabled).
type SessionPersistence struct {
	AppCookie *ociloadbalancer.SessionPersistenceConfigurationDetails
	LbCookie  *ociloadbalancer.LbCookieSessionPersistenceConfigurationDetails
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
	backendTLSStatusMap := make(map[string]backendTLSStatus)
	bsTLSPolicyCandidateMap := make(map[string][]tlsPolicyCandidate)
	listenerProtocolMap := make(map[int32]string)
	listenerTLSCandidateMap := make(map[int32][]listenerTLSCandidate)
	listenerMtlsConfigMap := make(map[int32]MtlsConfig)
	listenerTLSPolicyCandidateMap := make(map[int32][]tlsPolicyCandidate)
	listenerDefaultBsMap := make(map[int32]string)
	listenerBackendSetMap := make(map[int32]sets.String)
	bsHealthCheckerMap := make(map[string]*ociloadbalancer.HealthCheckerDetails)
	bsPolicyMap := make(map[string]string)
	bsSessionPersistenceMap := make(map[string]SessionPersistence)
	allBackendSets := sets.NewString(util.DefaultBackendSetName)
	allListeners := sets.NewInt32()

	bsHealthCheckerMap[util.DefaultBackendSetName] = util.GetDefaultHeathChecker()
	bsPolicyMap[util.DefaultBackendSetName] = util.DefaultBackendSetRoutingPolicy

	for _, ing := range ingressGroup {
		nextListenerTLSDiscoveryOrder := 0
		hostSecretMap := make(map[string]string)
		tlsConfiguredHosts := sets.NewString()
		desiredPorts := sets.NewInt32()
		// we always expect the default_ingress backendset
		desiredBackendSets := sets.NewString(util.DefaultBackendSetName)

		// For now, TLS spec is only applied to HTTP-family ingresses.
		if util.IsIngressProtocolHTTPBased(ing) {
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
			if !util.HasHTTPPaths(rule) {
				klog.V(4).InfoS("skipping ingress rule without HTTP paths while building state", "ingress", klog.KObj(ing), "host", host)
				continue
			}

			for _, path := range rule.HTTP.Paths {
				if !util.HasServiceBackend(path) {
					util.LogAndPublishIngressBackendValidationWarning(nil, ing, host, path, " while building state")
					continue
				}
				serviceName, servicePort, err := util.PathToServiceAndPort(ing.Namespace, path, s.ServiceLister)
				if err != nil {
					return errors.Wrap(err, "error finding service and port")
				}

				listenerPort, err := util.DetermineListenerPort(ing, &tlsConfiguredHosts, host, servicePort)
				if err != nil {
					return errors.Wrap(err, "error determining listener port")
				}

				desiredPorts.Insert(listenerPort)
				allListeners.Insert(listenerPort)

				bsName := util.GenerateBackendSetName(ing.Namespace, serviceName, servicePort)
				desiredBackendSets.Insert(bsName)
				allBackendSets.Insert(bsName)
				appendListenerBackendSet(listenerBackendSetMap, listenerPort, bsName)
				appendTLSPolicyCandidate(listenerTLSPolicyCandidateMap, listenerPort, ing, util.IngressListenerSslConfigAnnotation)
				appendTLSPolicyCandidate(bsTLSPolicyCandidateMap, bsName, ing, util.IngressBackendSetSslConfigAnnotation)

				err = validateListenerProtocol(ing, listenerProtocolMap, listenerPort)
				if err != nil {
					return err
				}
				err = validateListenerDefaultBackendSet(ing, listenerDefaultBsMap, listenerPort, bsName)
				if err != nil {
					return err
				}
				if err = validateMtlsConfig(ing, listenerPort, listenerTLSCandidateMap, listenerMtlsConfigMap); err != nil {
					return err
				}
				appendListenerBackendSet(listenerBackendSetMap, listenerPort, listenerDefaultBsMap[listenerPort])

				err = validateBackendSetHealthChecker(ing, bsHealthCheckerMap, bsName)
				if err != nil {
					return err
				}

				err = validateBackendSetPolicy(ing, bsPolicyMap, bsName)
				if err != nil {
					return err
				}

				err = validateBackendSetSessionPersistence(ing, bsSessionPersistenceMap, bsName)
				if err != nil {
					return err
				}

				err = validateTlsConfig(
					ing,
					listenerPort,
					bsName,
					host,
					listenerTLSCandidateMap,
					bsTLSConfigMap,
					backendTLSStatusMap,
					hostSecretMap,
					&nextListenerTLSDiscoveryOrder,
				)
				if err != nil {
					return err
				}
			}
		}

		s.IngressState[getIngressStateKey(ing.Namespace, ing.Name)] = IngressState{
			Ports:       desiredPorts,
			BackendSets: desiredBackendSets,
			ClassName:   ingressClass.Name,
		}
	}
	listenerTLSConfigMap := buildListenerTLSConfigMap(listenerTLSCandidateMap)
	listenerTLSPolicyMap, err := buildListenerTLSPolicyMap(listenerTLSPolicyCandidateMap, listenerTLSConfigMap)
	if err != nil {
		return err
	}
	bsTLSPolicyMap, err := buildBackendSetTLSPolicyMap(bsTLSPolicyCandidateMap, bsTLSConfigMap)
	if err != nil {
		return err
	}
	s.IngressGroupState = IngressClassState{
		BackendSets:                     allBackendSets,
		BackendSetHealthCheckerMap:      bsHealthCheckerMap,
		BackendSetPolicyMap:             bsPolicyMap,
		BackendSetTLSConfigMap:          bsTLSConfigMap,
		BackendSetTLSPolicyMap:          bsTLSPolicyMap,
		BackendSetSessionPersistenceMap: bsSessionPersistenceMap,
		Listeners:                       allListeners,
		ListenerProtocolMap:             listenerProtocolMap,
		ListenerTLSConfigMap:            listenerTLSConfigMap,
		ListenerMtlsConfigMap:           listenerMtlsConfigMap,
		ListenerTLSPolicyMap:            listenerTLSPolicyMap,
		ListenerDefaultBsMap:            listenerDefaultBsMap,
		ListenerBackendSetMap:           listenerBackendSetMap,
	}

	klog.Infof("Ingress Group state %s, Ingress state %s", util.PrettyPrint(s.IngressGroupState), util.PrettyPrint(s.IngressState))
	klog.Infof("State build complete..")

	endBuildTime := util.GetCurrentTimeInUnixMillis()
	if s.metricsCollector != nil {
		s.metricsCollector.AddStateBuildTime(util.GetTimeDifferenceInSeconds(startBuildTime, endBuildTime))
	}
	return nil
}

func validateTlsConfig(ingress *networkingv1.Ingress, listenerPort int32, bsName string, host string, listenerTLSCandidateMap map[int32][]listenerTLSCandidate,
	bsTLSConfigMap map[string]TlsConfig, bsTLSStatusMap map[string]backendTLSStatus, hostSecretMap map[string]string, discoveryOrder *int) error {
	bsTLSEnabled := util.GetBackendTlsEnabled(ingress)
	certificateIds := util.GetListenerTlsCertificateOcids(ingress)
	ingressKey := getIngressStateKey(ingress.Namespace, ingress.Name)
	backendTLSConfig := TlsConfig{}
	hasTLSArtifactInput := false

	if len(certificateIds) > 0 && util.IsIngressProtocolHTTPBased(ingress) {
		for _, certificateId := range certificateIds {
			config := TlsConfig{
				Type:      ArtifactTypeCertificate,
				Artifact:  certificateId,
				Namespace: ingress.Namespace,
			}
			appendListenerTLSCandidate(listenerTLSCandidateMap, listenerPort, ingressKey, discoveryOrder, config)
		}

		hasTLSArtifactInput = true
		backendTLSConfig = TlsConfig{
			Type:      ArtifactTypeCertificate,
			Artifact:  certificateIds[0],
			Namespace: ingress.Namespace,
		}
	}

	if host != "" {
		secretName, ok := hostSecretMap[host]

		if ok && secretName != "" {
			hasTLSArtifactInput = true
			config := TlsConfig{
				Type:      ArtifactTypeSecret,
				Artifact:  secretName,
				Namespace: ingress.Namespace,
			}
			appendListenerTLSCandidate(listenerTLSCandidateMap, listenerPort, ingressKey, discoveryOrder, config)
			backendTLSConfig = config
		}
	}

	return updateBackendTlsStatus(bsTLSEnabled, hasTLSArtifactInput, bsTLSStatusMap, bsTLSConfigMap, bsName, backendTLSConfig)
}

func updateBackendTlsStatus(bsTLSEnabled bool, hasTLSArtifactInput bool, bsTLSStatusMap map[string]backendTLSStatus,
	bsTLSConfigMap map[string]TlsConfig, bsName string, config TlsConfig) error {
	current, ok := bsTLSStatusMap[bsName]
	if ok {
		if current.Enabled != bsTLSEnabled {
			return fmt.Errorf(BackendTlsEnabledConflictMessage, bsName)
		}
		if bsTLSEnabled && current.HasTLSArtifactInput && hasTLSArtifactInput && current.Config != config {
			return fmt.Errorf(BackendTlsArtifactConflictMessage, bsName)
		}
		if hasTLSArtifactInput && !current.HasTLSArtifactInput {
			current.HasTLSArtifactInput = true
			current.Config = config
			bsTLSStatusMap[bsName] = current
		}
	} else {
		bsTLSStatusMap[bsName] = backendTLSStatus{
			Enabled:             bsTLSEnabled,
			HasTLSArtifactInput: hasTLSArtifactInput,
			Config:              config,
		}
	}

	if hasTLSArtifactInput {
		if bsTLSEnabled {
			bsTLSConfigMap[bsName] = config
		} else {
			bsTLSConfigMap[bsName] = TlsConfig{}
		}
	}
	return nil
}

func validateMtlsConfig(ingress *networkingv1.Ingress, listenerPort int32,
	listenerTLSCandidateMap map[int32][]listenerTLSCandidate, listenerMtlsConfigMap map[int32]MtlsConfig) error {
	parsed, err := util.GetListenerMtlsConfig(ingress)
	if err != nil {
		return fmt.Errorf("validation failure: listener port %d has invalid mTLS configuration: %w", listenerPort, err)
	}

	incoming := MtlsConfig{}
	if parsed != nil {
		incoming.TrustedCertificateAuthorityIds = append([]string(nil), parsed.TrustedCertificateAuthorityIds...)
		incoming.VerifyDepth = parsed.VerifyDepth
		if len(listenerTLSCandidateMap[listenerPort]) == 0 {
			return fmt.Errorf(MtlsRequiresTlsMessage, listenerPort)
		}
	}

	current, configured := listenerMtlsConfigMap[listenerPort]
	if configured && !reflect.DeepEqual(current, incoming) {
		return fmt.Errorf(MtlsConflictMessage, listenerPort)
	}
	listenerMtlsConfigMap[listenerPort] = incoming
	return nil
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

func validateBackendSetSessionPersistence(ingressResource *networkingv1.Ingress,
	bsPersistenceMap map[string]SessionPersistence, bsName string) error {
	appCookie, lbCookie, err := util.GetSessionPersistenceConfigs(ingressResource)
	if err != nil {
		return fmt.Errorf("invalid session persistence configuration on ingress %s/%s: %w", ingressResource.Namespace, ingressResource.Name, err)
	}

	// Ensure mutual exclusivity (only one or none)
	if appCookie != nil && lbCookie != nil {
		// Prefer LB cookie if both provided; log and continue
		return fmt.Errorf("Provide only one of LB cookie or App cookie config for %s.", bsName)
	}

	// If annotation is present but both configs are nil, treat as validation error
	if util.HasSessionPersistenceAnnotation(ingressResource) && appCookie == nil && lbCookie == nil {
		return fmt.Errorf(SessionPersistenceEmptyMessage, bsName)
	}

	incoming := SessionPersistence{AppCookie: appCookie, LbCookie: lbCookie}

	current, ok := bsPersistenceMap[bsName]
	if ok {
		// Reconcile conflicts instead of erroring out
		if !reflect.DeepEqual(current, incoming) {
			// If either side has lbCookie, prefer lbCookie (LB-managed persistence)
			if current.LbCookie != nil || incoming.LbCookie != nil {
				// If current already lbCookie, keep it; else adopt incoming lbCookie
				if current.LbCookie != nil {
					klog.Warningf("session persistence conflict for %s; keeping existing lbCookie configuration", bsName)
					// keep current as-is
				} else {
					klog.Warningf("session persistence conflict for %s; adopting lbCookie configuration from ingress %s", bsName, ingressResource.Name)
					current = SessionPersistence{LbCookie: incoming.LbCookie}
				}
			} else if current.AppCookie != nil || incoming.AppCookie != nil {
				// Both sides appCookie but may differ on cookieName; keep existing to avoid churn
				if current.AppCookie != nil {
					klog.Warningf("session persistence conflict (appCookie) for %s; keeping existing configuration", bsName)
					// keep current
				} else {
					klog.Warningf("session persistence conflict (appCookie) for %s; adopting configuration from ingress %s", bsName, ingressResource.Name)
					current = SessionPersistence{AppCookie: incoming.AppCookie}
				}
			} else {
				// Both nil or one nil and other nil: end up nil
				current = SessionPersistence{}
			}
			bsPersistenceMap[bsName] = current
			return nil
		}
		// No change; keep current
		bsPersistenceMap[bsName] = current
		return nil
	}

	// First writer wins
	bsPersistenceMap[bsName] = incoming
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

func (s *StateStore) GetIngressBackendSets(namespace string, ingressName string) sets.String {
	ingress, ok := s.IngressState[getIngressStateKey(namespace, ingressName)]
	if ok {
		return ingress.BackendSets
	}
	return nil
}

func (s *StateStore) GetIngressPorts(namespace string, ingressName string) sets.Int32 {
	ingress, ok := s.IngressState[getIngressStateKey(namespace, ingressName)]
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

func (s *StateStore) GetBackendSetsForListener(listenerPort int32) sets.String {
	backendSets, ok := s.IngressGroupState.ListenerBackendSetMap[listenerPort]
	if ok {
		return sets.NewString(backendSets.List()...)
	}
	return sets.NewString()
}

func (s *StateStore) GetTLSConfigForListener(port int32) []TlsConfig {
	portTLSConfig, ok := s.IngressGroupState.ListenerTLSConfigMap[port]
	if ok {
		// Return a copy so callers cannot mutate state-store internals.
		tlsConfigs := make([]TlsConfig, len(portTLSConfig.TlsConfigs))
		copy(tlsConfigs, portTLSConfig.TlsConfigs)
		return tlsConfigs
	}
	return nil
}

func (s *StateStore) GetMtlsConfigForListener(port int32) MtlsConfig {
	config := s.IngressGroupState.ListenerMtlsConfigMap[port]
	config.TrustedCertificateAuthorityIds = append([]string(nil), config.TrustedCertificateAuthorityIds...)
	return config
}

func (s *StateStore) GetTLSPolicyForListener(port int32) *tlspolicy.ExplicitTLSPolicy {
	return copyExplicitTLSPolicy(s.IngressGroupState.ListenerTLSPolicyMap[port])
}

func (s *StateStore) GetTLSConfigForBackendSet(bsName string) TlsConfig {
	bsTLSConfig, ok := s.IngressGroupState.BackendSetTLSConfigMap[bsName]
	if ok {
		return bsTLSConfig
	}
	return TlsConfig{}
}

func (s *StateStore) GetTLSPolicyForBackendSet(bsName string) *tlspolicy.ExplicitTLSPolicy {
	return copyExplicitTLSPolicy(s.IngressGroupState.BackendSetTLSPolicyMap[bsName])
}

func (s *StateStore) GetBackendSetSessionPersistence(bsName string) (*ociloadbalancer.SessionPersistenceConfigurationDetails, *ociloadbalancer.LbCookieSessionPersistenceConfigurationDetails) {
	p, ok := s.IngressGroupState.BackendSetSessionPersistenceMap[bsName]
	if ok {
		return p.AppCookie, p.LbCookie
	}
	return nil, nil
}

func (s *StateStore) GetAllBackendSetForIngressClass() sets.String {
	return s.IngressGroupState.BackendSets
}

func (s *StateStore) GetAllListenersForIngressClass() sets.Int32 {
	return s.IngressGroupState.Listeners
}

func appendTLSPolicyCandidate[K comparable](candidateMap map[K][]tlsPolicyCandidate, target K, ingress *networkingv1.Ingress, annotationName string) {
	if ingress == nil || ingress.Annotations == nil {
		return
	}
	value, ok := ingress.Annotations[annotationName]
	if !ok {
		return
	}
	candidateMap[target] = append(candidateMap[target], tlsPolicyCandidate{
		IngressKey:     getIngressStateKey(ingress.Namespace, ingress.Name),
		AnnotationName: annotationName,
		RawValue:       value,
	})
}

func appendListenerBackendSet(listenerBackendSetMap map[int32]sets.String, listenerPort int32, bsName string) {
	backendSets, ok := listenerBackendSetMap[listenerPort]
	if !ok {
		backendSets = sets.NewString()
		listenerBackendSetMap[listenerPort] = backendSets
	}
	backendSets.Insert(bsName)
}

func appendListenerTLSCandidate(listenerTLSCandidateMap map[int32][]listenerTLSCandidate, listenerPort int32,
	ingressKey string, discoveryOrder *int, config TlsConfig) {
	listenerTLSCandidateMap[listenerPort] = append(listenerTLSCandidateMap[listenerPort], listenerTLSCandidate{
		IngressKey:     ingressKey,
		DiscoveryOrder: *discoveryOrder,
		Config:         config,
	})
	*discoveryOrder++
}

// buildListenerTLSConfigMap orders listener TLS configs deterministically by ingress key,
// discovery order, and config value. The order is for stable state across reconciles, not certificate priority.
func buildListenerTLSConfigMap(listenerTLSCandidateMap map[int32][]listenerTLSCandidate) map[int32]ListenerTLSConfig {
	listenerTLSConfigMap := make(map[int32]ListenerTLSConfig, len(listenerTLSCandidateMap))
	for port, candidates := range listenerTLSCandidateMap {
		sortedCandidates := make([]listenerTLSCandidate, len(candidates))
		copy(sortedCandidates, candidates)

		sort.SliceStable(sortedCandidates, func(i, j int) bool {
			leftCandidate := sortedCandidates[i]
			rightCandidate := sortedCandidates[j]
			if leftCandidate.IngressKey != rightCandidate.IngressKey {
				return leftCandidate.IngressKey < rightCandidate.IngressKey
			}
			if leftCandidate.DiscoveryOrder != rightCandidate.DiscoveryOrder {
				return leftCandidate.DiscoveryOrder < rightCandidate.DiscoveryOrder
			}
			if leftCandidate.Config.Artifact != rightCandidate.Config.Artifact {
				return leftCandidate.Config.Artifact < rightCandidate.Config.Artifact
			}
			if leftCandidate.Config.Type != rightCandidate.Config.Type {
				return leftCandidate.Config.Type < rightCandidate.Config.Type
			}
			return leftCandidate.Config.Namespace < rightCandidate.Config.Namespace
		})

		tlsConfigs := dedupeListenerTLSConfigs(sortedCandidates)
		if len(tlsConfigs) > 0 {
			listenerTLSConfigMap[port] = ListenerTLSConfig{TlsConfigs: tlsConfigs}
		}
	}
	return listenerTLSConfigMap
}

func buildListenerTLSPolicyMap(candidateMap map[int32][]tlsPolicyCandidate,
	listenerTLSConfigMap map[int32]ListenerTLSConfig) (map[int32]*tlspolicy.ExplicitTLSPolicy, error) {
	policyMap := make(map[int32]*tlspolicy.ExplicitTLSPolicy)
	ports := make([]int32, 0, len(candidateMap))
	for port := range candidateMap {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool {
		return ports[i] < ports[j]
	})

	for _, port := range ports {
		listenerTLSConfig := listenerTLSConfigMap[port]
		if len(listenerTLSConfig.TlsConfigs) == 0 {
			continue
		}
		policy, err := mergeExplicitTLSPolicyCandidates("listener", fmt.Sprintf("%d", port), candidateMap[port])
		if err != nil {
			return nil, err
		}
		if policy != nil {
			policyMap[port] = policy
		}
	}
	return policyMap, nil
}

func buildBackendSetTLSPolicyMap(candidateMap map[string][]tlsPolicyCandidate,
	bsTLSConfigMap map[string]TlsConfig) (map[string]*tlspolicy.ExplicitTLSPolicy, error) {
	policyMap := make(map[string]*tlspolicy.ExplicitTLSPolicy)
	backendSetNames := make([]string, 0, len(candidateMap))
	for bsName := range candidateMap {
		backendSetNames = append(backendSetNames, bsName)
	}
	sort.Strings(backendSetNames)

	for _, bsName := range backendSetNames {
		if !hasTLSConfig(bsTLSConfigMap[bsName]) {
			continue
		}
		policy, err := mergeExplicitTLSPolicyCandidates("backend set", bsName, candidateMap[bsName])
		if err != nil {
			return nil, err
		}
		if policy != nil {
			policyMap[bsName] = policy
		}
	}
	return policyMap, nil
}

func mergeExplicitTLSPolicyCandidates(resourceKind string, target string, candidates []tlsPolicyCandidate) (*tlspolicy.ExplicitTLSPolicy, error) {
	sortedCandidates := make([]tlsPolicyCandidate, len(candidates))
	copy(sortedCandidates, candidates)
	sort.SliceStable(sortedCandidates, func(i, j int) bool {
		if sortedCandidates[i].IngressKey != sortedCandidates[j].IngressKey {
			return sortedCandidates[i].IngressKey < sortedCandidates[j].IngressKey
		}
		if sortedCandidates[i].AnnotationName != sortedCandidates[j].AnnotationName {
			return sortedCandidates[i].AnnotationName < sortedCandidates[j].AnnotationName
		}
		return sortedCandidates[i].RawValue < sortedCandidates[j].RawValue
	})

	var merged *tlspolicy.ExplicitTLSPolicy
	var cipherOwner string
	var protocolsOwner string
	seenCandidates := make(map[tlsPolicyCandidate]struct{}, len(sortedCandidates))
	for _, candidate := range sortedCandidates {
		if _, ok := seenCandidates[candidate]; ok {
			continue
		}
		seenCandidates[candidate] = struct{}{}

		policy, err := tlspolicy.ParseExplicitTLSPolicyAnnotationValue(candidate.AnnotationName, candidate.RawValue)
		if err != nil {
			return nil, fmt.Errorf(TLSPolicyInvalidAnnotationMessage, resourceKind, target, candidate.IngressKey, err)
		}
		if policy == nil {
			continue
		}
		if merged == nil {
			merged = &tlspolicy.ExplicitTLSPolicy{}
		}
		if policy.HasCipherSuiteName {
			if merged.HasCipherSuiteName && merged.CipherSuiteName != policy.CipherSuiteName {
				return nil, fmt.Errorf(TLSPolicyConflictMessage, resourceKind, target, candidate.AnnotationName, "cipherSuiteName",
					cipherOwner, merged.CipherSuiteName, candidate.IngressKey, policy.CipherSuiteName)
			}
			if !merged.HasCipherSuiteName {
				cipherOwner = candidate.IngressKey
			}
			merged.HasCipherSuiteName = true
			merged.CipherSuiteName = policy.CipherSuiteName
		}
		if policy.HasProtocols {
			if merged.HasProtocols && !reflect.DeepEqual(merged.Protocols, policy.Protocols) {
				return nil, fmt.Errorf(TLSPolicyConflictMessage, resourceKind, target, candidate.AnnotationName, "protocols",
					protocolsOwner, merged.Protocols, candidate.IngressKey, policy.Protocols)
			}
			if !merged.HasProtocols {
				protocolsOwner = candidate.IngressKey
			}
			merged.HasProtocols = true
			merged.Protocols = append([]string(nil), policy.Protocols...)
		}
	}
	return copyExplicitTLSPolicy(merged), nil
}

func hasTLSConfig(config TlsConfig) bool {
	return config.Type != "" && config.Artifact != ""
}

func copyExplicitTLSPolicy(policy *tlspolicy.ExplicitTLSPolicy) *tlspolicy.ExplicitTLSPolicy {
	if policy == nil {
		return nil
	}
	return &tlspolicy.ExplicitTLSPolicy{
		HasCipherSuiteName: policy.HasCipherSuiteName,
		CipherSuiteName:    policy.CipherSuiteName,
		HasProtocols:       policy.HasProtocols,
		Protocols:          append([]string(nil), policy.Protocols...),
	}
}

func dedupeListenerTLSConfigs(candidates []listenerTLSCandidate) []TlsConfig {
	tlsConfigs := make([]TlsConfig, 0, len(candidates))
	seen := make(map[TlsConfig]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.Config]; ok {
			continue
		}
		seen[candidate.Config] = struct{}{}
		tlsConfigs = append(tlsConfigs, candidate.Config)
	}
	return tlsConfigs
}

func getIngressStateKey(namespace string, ingressName string) string {
	return fmt.Sprintf("%s/%s", namespace, ingressName)
}
