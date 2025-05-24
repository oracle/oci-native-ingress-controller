/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IngressClassParametersSpec defines the desired state of IngressClassParameters
type IngressClassParametersSpec struct {
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:MinLength=1
	CompartmentId string `json:"compartmentId,omitempty"`

	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:MinLength=1
	SubnetId string `json:"subnetId,omitempty"`

	LoadBalancerName string `json:"loadBalancerName,omitempty"`

	IsPrivate bool `json:"isPrivate,omitempty"`

	ReservedPublicAddressId string `json:"reservedPublicAddressId,omitempty"`

	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=8000
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:default:=10
	MinBandwidthMbps int `json:"minBandwidthMbps,omitempty"`

	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=8000
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:default:=100
	MaxBandwidthMbps int `json:"maxBandwidthMbps,omitempty"`

	// LbCookieSessionPersistenceConfiguration defines the LB cookie-based session persistence settings.
	// Note: This is mutually exclusive with SessionPersistenceConfiguration (application cookie stickiness).
	// +optional
	LbCookieSessionPersistenceConfiguration *LbCookieSessionPersistenceConfigurationDetails `json:"lbCookieSessionPersistenceConfiguration,omitempty"`

	// DefaultListenerIdleTimeoutInSeconds specifies the default idle timeout in seconds for listeners created for this IngressClass.
	// If not set, the OCI Load Balancing service default will be used (e.g., 60 seconds for HTTP, 300 for TCP).
	// Refer to OCI documentation for specific default values and ranges.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7200
	// Example: OCI LB allows 1-7200 for TCP listeners. Adjust if needed.
	// +optional
	DefaultListenerIdleTimeoutInSeconds *int64 `json:"defaultListenerIdleTimeoutInSeconds,omitempty"`
}

// LbCookieSessionPersistenceConfigurationDetails defines the configuration for LbCookieSessionPersistence.
// These fields correspond to the OCI LoadBalancer LbCookieSessionPersistenceConfigurationDetails.
type LbCookieSessionPersistenceConfigurationDetails struct {
	// The name of the cookie used to detect a session initiated by the backend server.
	// If unspecified, the cookie name will be chosen by the OCI Load Balancing service.
	// Example: `X-Oracle-OCILB-Cookie`
	// +optional
	CookieName *string `json:"cookieName,omitempty"`

	// Whether the load balancer is prevented from directing traffic from a persistent session client to
	// a different backend server if the original server is unavailable. Defaults to false.
	// Example: `true`
	// +optional
	IsDisableFallback *bool `json:"isDisableFallback,omitempty"`

	// The maximum time, in seconds, to independently maintain a session sticking connection.
	// Access to this server will be prevented after the session timeout occurs.
	// If unspecified, the OCI Load Balancing service default will be used.
	// Example: `300`
	// +optional
	TimeoutInSeconds *int `json:"timeoutInSeconds,omitempty"`

	// Whether the session cookie should be secure. For a secure cookie, the `Set-Cookie` header
	// includes the `Secure` attribute.
	// Example: `true`
	// +optional
	IsSecure *bool `json:"isSecure,omitempty"`

	// Whether the session cookie should be HttpOnly. For a HttpOnly cookie, the `Set-Cookie` header
	// includes the `HttpOnly` attribute.
	// Example: `true`
	// +optional
	IsHttpOnly *bool `json:"isHttpOnly,omitempty"`

	// The domain of the session cookie.
	// Example: `example.com`
	// +optional
	Domain *string `json:"domain,omitempty"`

	// The path of the session cookie.
	// Example: `/`
	// +optional
	Path *string `json:"path,omitempty"`
}

// IngressClassParametersStatus defines the observed state of IngressClassParameters
type IngressClassParametersStatus struct {
}

// IngressClassParameters is the Schema for the IngressClassParameterss API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="LoadBalancerName",type=string,JSONPath=`.spec.loadBalancerName`
// +kubebuilder:printcolumn:name="Compartment",type=string,JSONPath=`.spec.compartmentID`
// +kubebuilder:printcolumn:name="Private",type=boolean,JSONPath=`.spec.isPrivate`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type IngressClassParameters struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IngressClassParametersSpec   `json:"spec,omitempty"`
	Status IngressClassParametersStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// IngressClassParametersList contains a list of IngressClassParameters
type IngressClassParametersList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IngressClassParameters `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IngressClassParameters{}, &IngressClassParametersList{})
}
