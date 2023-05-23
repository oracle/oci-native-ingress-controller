/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package types

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

type OCIPrincipalType string

const (
	Instance         OCIPrincipalType = "instance"
	User             OCIPrincipalType = "user"
	WorkloadIdentity OCIPrincipalType = "workloadIdentity"
)

type IngressOpts struct {
	KubeConfig         string
	LeaseLockName      string
	LeaseLockNamespace string
	LeaseID            string
	CompartmentId      string
	SubnetId           string
	ControllerClass    string
	AuthType           string
	AuthSecretName     string
}

func MapToPrincipalType(authType string) (OCIPrincipalType, error) {
	switch authType {
	case string(Instance):
		return Instance, nil
	case string(User):
		return User, nil
	case string(WorkloadIdentity):
		return WorkloadIdentity, nil
	default:
		return "", fmt.Errorf("unknown OCI principal type: %v", authType)
	}
}

type Auth struct {
	Type   OCIPrincipalType
	Config AuthConfig
}

type AuthConfig struct {
	Region      string `yaml:"region"`
	TenancyID   string `yaml:"tenancy"`
	UserID      string `yaml:"user"`
	PrivateKey  string `yaml:"privateKey"`
	Fingerprint string `yaml:"fingerprint"`
	Passphrase  string `yaml:"passphrase"`
}

type AuthConfigYaml struct {
	Auth map[string]string `yaml:"auth,omitempty"`
}

func (config *AuthConfig) Validate() error {
	return validateConfig(config).ToAggregate()
}

func validateConfig(c *AuthConfig) field.ErrorList {
	errs := field.ErrorList{}
	if len(c.TenancyID) == 0 {
		errs = append(errs, field.Required(field.NewPath("Auth", "Tenancy"),
			"Tenancy is required for user principal"))
	}
	if len(c.Region) == 0 {
		errs = append(errs, field.Required(field.NewPath("Auth", "Region"),
			"Region is required for user principal"))
	}
	if len(c.Fingerprint) == 0 {
		errs = append(errs, field.Required(field.NewPath("Auth", "Fingerprint"),
			"Fingerprint is required for user principal"))
	}
	if len(c.UserID) == 0 {
		errs = append(errs, field.Required(field.NewPath("Auth", "UserID"),
			"UserID is required for user principal"))
	}
	if len(c.PrivateKey) == 0 {
		errs = append(errs, field.Required(field.NewPath("Auth", "PrivateKey"),
			"PrivateKey is required for user principal"))
	}
	return errs
}
