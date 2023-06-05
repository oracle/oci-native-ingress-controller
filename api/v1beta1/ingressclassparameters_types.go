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
