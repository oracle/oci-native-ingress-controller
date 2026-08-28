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
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/tlspolicy"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"k8s.io/apimachinery/pkg/util/sets"
)

type tlsPolicyResourceType string

const (
	tlsPolicyResourceListener   tlsPolicyResourceType = "listener"
	tlsPolicyResourceBackendSet tlsPolicyResourceType = "backend set"
)

type TLSPolicy struct {
	CipherSuiteName string
	Protocols       []string
}

type ExplicitTLSPolicy = tlspolicy.ExplicitTLSPolicy

type TLSPolicyDefaults struct {
	Listener      TLSPolicy
	HTTP2Listener TLSPolicy
	BackendSet    TLSPolicy
}

const (
	listenerTLS12CipherSuite      = "oci-tls-12-ssl-cipher-suite-v3"
	listenerTLS13CipherSuite      = "oci-tls-13-ssl-cipher-suite-v3"
	http2ListenerTLS12CipherSuite = "oci-default-http2-ssl-cipher-suite-v1"
	http2ListenerTLS13CipherSuite = "oci-default-http2-tls-13-ssl-cipher-suite-v1"
)

const (
	tls12ProtocolKey   = "TLSv1.2"
	tls13ProtocolKey   = "TLSv1.3"
	tls1213ProtocolKey = "TLSv1.2,TLSv1.3"
)

var LockedDefaultTLSPolicy = TLSPolicyDefaults{
	Listener: TLSPolicy{
		CipherSuiteName: "oci-tls-12-13-ssl-cipher-suite-v3",
		Protocols:       []string{"TLSv1.2", "TLSv1.3"},
	},
	HTTP2Listener: TLSPolicy{
		CipherSuiteName: util.ProtocolHTTP2DefaultCipherSuite,
		Protocols:       []string{"TLSv1.2", "TLSv1.3"},
	},
	BackendSet: TLSPolicy{
		CipherSuiteName: util.ProtocolHTTP2DefaultCipherSuite,
		Protocols:       []string{"TLSv1.2", "TLSv1.3"},
	},
}

var protocolCompatibleListenerCipherSuites = map[string]string{
	tls1213ProtocolKey: LockedDefaultTLSPolicy.Listener.CipherSuiteName,
	tls13ProtocolKey:   listenerTLS13CipherSuite,
	tls12ProtocolKey:   listenerTLS12CipherSuite,
}

var protocolCompatibleHTTP2CipherSuites = map[string]string{
	tls1213ProtocolKey: LockedDefaultTLSPolicy.HTTP2Listener.CipherSuiteName,
	tls13ProtocolKey:   http2ListenerTLS13CipherSuite,
	tls12ProtocolKey:   http2ListenerTLS12CipherSuite,
}

var protocolCompatibleBackendSetCipherSuites = map[string]string{
	tls1213ProtocolKey: LockedDefaultTLSPolicy.BackendSet.CipherSuiteName,
	tls13ProtocolKey:   http2ListenerTLS13CipherSuite,
	tls12ProtocolKey:   http2ListenerTLS12CipherSuite,
}

// ParseExplicitTLSPolicyAnnotation parses a NIC listener or backend-set SSL
// policy annotation into a validated explicit TLS policy. It returns nil when
// the annotation is absent and delegates validation to pkg/tlspolicy.
func ParseExplicitTLSPolicyAnnotation(annotations map[string]string, annotationName string) (*ExplicitTLSPolicy, error) {
	return tlspolicy.ParseExplicitTLSPolicyAnnotation(annotations, annotationName)
}

// ParseExplicitTLSPolicyAnnotationValue parses and validates a raw TLS policy
// annotation value. It is exposed from the ingress package for tests and
// callers that already resolved the annotation string.
func ParseExplicitTLSPolicyAnnotationValue(annotationName string, value string) (*ExplicitTLSPolicy, error) {
	return tlspolicy.ParseExplicitTLSPolicyAnnotationValue(annotationName, value)
}

func resolveTLSPolicy(resourceType tlsPolicyResourceType, listenerProtocol string,
	sslConfig *ociloadbalancer.SslConfigurationDetails, explicitPolicy *ExplicitTLSPolicy,
	isSSLConfigCreate bool, currentSSLConfig *ociloadbalancer.SslConfiguration) (*TLSPolicy, error) {
	if sslConfig == nil {
		return nil, nil
	}

	defaultPolicy, err := lockedDefaultTLSPolicy(resourceType, listenerProtocol)
	if err != nil {
		return nil, err
	}

	if explicitPolicy != nil {
		policy, err := overlayExplicitTLSPolicy(resourceType, listenerProtocol, defaultPolicy, explicitPolicy)
		if err != nil {
			return nil, err
		}
		applyTLSPolicyToSSLConfig(sslConfig, policy)
		return policy, nil
	}

	if isSSLConfigCreate {
		applyTLSPolicyToSSLConfig(sslConfig, defaultPolicy)
		return defaultPolicy, nil
	}

	return nil, preserveTLSPolicyFields(string(resourceType), sslConfig, currentSSLConfig)
}

func lockedDefaultTLSPolicy(resourceType tlsPolicyResourceType, listenerProtocol string) (*TLSPolicy, error) {
	switch resourceType {
	case tlsPolicyResourceListener:
		if util.IsListenerProtocolUsingHTTP2CipherSuite(listenerProtocol) {
			return copyTLSPolicy(LockedDefaultTLSPolicy.HTTP2Listener), nil
		}
		return copyTLSPolicy(LockedDefaultTLSPolicy.Listener), nil
	case tlsPolicyResourceBackendSet:
		return copyTLSPolicy(LockedDefaultTLSPolicy.BackendSet), nil
	default:
		return nil, fmt.Errorf("unknown TLS policy resource type %q", resourceType)
	}
}

func overlayExplicitTLSPolicy(resourceType tlsPolicyResourceType, listenerProtocol string, defaultPolicy *TLSPolicy, explicitPolicy *ExplicitTLSPolicy) (*TLSPolicy, error) {
	policy := copyTLSPolicy(*defaultPolicy)
	if explicitPolicy.HasProtocols {
		policy.Protocols = append([]string(nil), explicitPolicy.Protocols...)
	}
	if explicitPolicy.HasCipherSuiteName {
		policy.CipherSuiteName = explicitPolicy.CipherSuiteName
		return policy, nil
	}
	if explicitPolicy.HasProtocols {
		cipherSuiteName, err := protocolCompatibleDefaultCipherSuite(resourceType, listenerProtocol, policy.Protocols)
		if err != nil {
			return nil, err
		}
		policy.CipherSuiteName = cipherSuiteName
	}
	return policy, nil
}

func protocolCompatibleDefaultCipherSuite(resourceType tlsPolicyResourceType, listenerProtocol string, protocols []string) (string, error) {
	protocolKey := tlsProtocolKey(protocols)
	var cipherSuites map[string]string

	switch resourceType {
	case tlsPolicyResourceListener:
		if util.IsListenerProtocolUsingHTTP2CipherSuite(listenerProtocol) {
			cipherSuites = protocolCompatibleHTTP2CipherSuites
		} else {
			cipherSuites = protocolCompatibleListenerCipherSuites
		}
	case tlsPolicyResourceBackendSet:
		cipherSuites = protocolCompatibleBackendSetCipherSuites
	default:
		return "", fmt.Errorf("unknown TLS policy resource type %q", resourceType)
	}

	if cipherSuiteName, ok := cipherSuites[protocolKey]; ok {
		return cipherSuiteName, nil
	}
	return "", unsupportedProtocolOnlyDefaultError(resourceType, protocols)
}

func unsupportedProtocolOnlyDefaultError(resourceType tlsPolicyResourceType, protocols []string) error {
	return fmt.Errorf("%s: %s protocols-only TLS policy with protocols %v has no confirmed safe default cipher suite; provide cipherSuiteName explicitly",
		tlspolicy.InvalidAnnotationReason, resourceType, protocols)
}

func listenerHasMultipleCertificates(listener *ociloadbalancer.Listener) bool {
	return listener != nil && listener.SslConfiguration != nil && len(listener.SslConfiguration.CertificateIds) > 1
}

func listenerSSLConfigHasMultipleCertificates(protocol string, sslConfig *ociloadbalancer.SslConfigurationDetails) bool {
	return protocol != util.ProtocolTCP && sslConfig != nil && len(sslConfig.CertificateIds) > 1
}

func applyTLSPolicyToSSLConfig(sslConfig *ociloadbalancer.SslConfigurationDetails, policy *TLSPolicy) {
	if sslConfig == nil || policy == nil {
		return
	}
	sslConfig.CipherSuiteName = common.String(policy.CipherSuiteName)
	sslConfig.Protocols = append([]string(nil), policy.Protocols...)
}

func preserveListenerTLSPolicy(dst *ociloadbalancer.SslConfigurationDetails, current *ociloadbalancer.SslConfiguration) error {
	return preserveTLSPolicyFields("listener", dst, current)
}

func preserveBackendSetTLSPolicy(dst *ociloadbalancer.SslConfigurationDetails, current *ociloadbalancer.SslConfiguration) error {
	return preserveTLSPolicyFields("backend set", dst, current)
}

func preserveTLSPolicyFields(resourceKind string, dst *ociloadbalancer.SslConfigurationDetails, current *ociloadbalancer.SslConfiguration) error {
	if dst == nil || current == nil {
		return nil
	}
	if dst.CipherSuiteName == nil && current.CipherSuiteName != nil {
		if err := tlspolicy.EnsureRequestableCipherSuite(resourceKind, current.CipherSuiteName); err != nil {
			return err
		}
		dst.CipherSuiteName = common.String(*current.CipherSuiteName)
	}
	if len(dst.Protocols) == 0 {
		dst.Protocols = append([]string(nil), current.Protocols...)
	}
	return nil
}

func listenerSslConfigNeedsUpdate(calculatedConfig *ociloadbalancer.SslConfigurationDetails,
	currentListener *ociloadbalancer.Listener, comparePolicy bool) bool {
	if currentListener == nil {
		return calculatedConfig != nil
	}

	currentConfig := currentListener.SslConfiguration
	if calculatedConfig == nil {
		return listenerHasSSLArtifacts(currentConfig)
	}
	if currentConfig == nil {
		return true
	}
	if !reflect.DeepEqual(currentConfig.CertificateIds, calculatedConfig.CertificateIds) {
		return true
	}
	currentMtlsEnabled := currentConfig.VerifyPeerCertificate != nil && *currentConfig.VerifyPeerCertificate
	desiredMtlsEnabled := calculatedConfig.VerifyPeerCertificate != nil && *calculatedConfig.VerifyPeerCertificate
	if currentMtlsEnabled != desiredMtlsEnabled {
		return true
	}
	if desiredMtlsEnabled && (!sets.NewString(currentConfig.TrustedCertificateAuthorityIds...).Equal(sets.NewString(calculatedConfig.TrustedCertificateAuthorityIds...)) ||
		!reflect.DeepEqual(currentConfig.VerifyDepth, calculatedConfig.VerifyDepth)) {
		return true
	}
	if comparePolicy && (!reflect.DeepEqual(currentConfig.CipherSuiteName, calculatedConfig.CipherSuiteName) ||
		!tlsProtocolsEqual(currentConfig.Protocols, calculatedConfig.Protocols)) {
		return true
	}
	return false
}

func listenerHasSSLArtifacts(currentConfig *ociloadbalancer.SslConfiguration) bool {
	return currentConfig != nil && (len(currentConfig.CertificateIds) > 0 || currentConfig.CertificateName != nil)
}

func tlsProtocolKey(protocols []string) string {
	protocolsCopy := append([]string(nil), protocols...)
	sort.Strings(protocolsCopy)
	return strings.Join(protocolsCopy, ",")
}

func tlsProtocolsEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy := append([]string(nil), a...)
	bCopy := append([]string(nil), b...)
	sort.Strings(aCopy)
	sort.Strings(bCopy)
	for i := range aCopy {
		if aCopy[i] != bCopy[i] {
			return false
		}
	}
	return true
}

func copyTLSPolicy(policy TLSPolicy) *TLSPolicy {
	return &TLSPolicy{
		CipherSuiteName: policy.CipherSuiteName,
		Protocols:       append([]string(nil), policy.Protocols...),
	}
}
