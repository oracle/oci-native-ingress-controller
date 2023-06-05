/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/oracle/oci-go-sdk/v65/common"
	sdkAuth "github.com/oracle/oci-go-sdk/v65/common/auth"
	"k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/types"
)

const httpClientTimeout = 20 * time.Second

func GetConfigurationProvider(ctx context.Context, opts types.IngressOpts) (common.ConfigurationProvider, error) {
	auth, err := RetrieveAuthConfig(ctx, opts, opts.LeaseLockNamespace)
	if err != nil {
		klog.Fatalf("Unable to handle authentication parameters", err)
		return nil, err
	}
	return getConfProviderFromAuth(auth)
}

func getConfProviderFromAuth(authCfg *types.Auth) (common.ConfigurationProvider, error) {
	klog.Infof("Fetching auth config provider for type: %v", authCfg.Type)
	switch authCfg.Type {
	case types.Instance:
		return sdkAuth.InstancePrincipalConfigurationProviderWithCustomClient(setHTTPClientTimeout(httpClientTimeout))

	case types.User:
		cfg := authCfg.Config
		return common.NewRawConfigurationProvider(cfg.TenancyID, cfg.UserID,
			cfg.Region, cfg.Fingerprint, cfg.PrivateKey, &cfg.Passphrase), nil

	case types.WorkloadIdentity:
		return sdkAuth.OkeWorkloadIdentityConfigurationProvider()
	default:
		return nil, fmt.Errorf("unable to determine OCI principal type for configuration provider")
	}
}

func setHTTPClientTimeout(
	timeout time.Duration) func(common.HTTPRequestDispatcher) (common.HTTPRequestDispatcher, error) {

	return func(dispatcher common.HTTPRequestDispatcher) (common.HTTPRequestDispatcher, error) {
		switch client := dispatcher.(type) {
		case *http.Client:
			client.Timeout = timeout
			return dispatcher, nil
		default:
			return nil, fmt.Errorf("unable to modify unknown HTTP client type")
		}
	}
}

func RetrieveAuthConfig(ctx context.Context, opts types.IngressOpts, namespace string) (*types.Auth, error) {
	authType := opts.AuthType
	principalType, err := types.MapToPrincipalType(authType)
	if err != nil {
		return nil, fmt.Errorf("invalid auth principal type, %v", authType)
	}

	var auth = &types.Auth{
		Type: principalType,
	}

	if principalType == types.User {
		authConfigSecretName := opts.AuthSecretName

		// read it from k8s api
		secret, err := readK8sSecret(ctx, namespace, authConfigSecretName)
		if err != nil {
			klog.Fatalf("Error while reading secret from k8s api", err)
			return nil, fmt.Errorf("error retrieving secret: %v", authConfigSecretName)
		}

		klog.Infof("secret is retrieved from kubernetes api: %s", authConfigSecretName)

		if len(secret.Data) == 0 || len(secret.Data["config"]) == 0 {
			klog.Fatalf("Empty Configuration is found in the secret %s", authConfigSecretName)
			return nil, fmt.Errorf("auth config data is empty: %v", authConfigSecretName)
		}
		authCfg, err := ParseAuthConfig(secret, authConfigSecretName)
		if err != nil {
			klog.Fatalf("Missing auth config data: %s", authConfigSecretName)
			return nil, fmt.Errorf("missing auth config data: %v", err)
		}

		err = authCfg.Validate()
		if err != nil {
			klog.Fatalf("Missing auth config data %s", authConfigSecretName)
			return nil, fmt.Errorf("missing auth config data: %v", err)
		}
		auth.Config = *authCfg
	}
	return auth, nil
}

func ParseAuthConfig(secret *v1.Secret, authConfigSecretName string) (*types.AuthConfig, error) {
	authYaml := &types.AuthConfigYaml{}
	err := yaml.Unmarshal(secret.Data["config"], &authYaml)
	if err != nil {
		klog.Fatalf("Invalid auth config data %s", authConfigSecretName)
		return nil, fmt.Errorf("invalid auth config data: %v", authConfigSecretName)
	}

	if len(secret.Data["private-key"]) > 0 {
		authYaml.Auth["privateKey"] = string(secret.Data["private-key"])
	} else {
		klog.Fatalf("Invalid user auth private key %s", authConfigSecretName)
		return nil, fmt.Errorf("invalid user auth config data: %v", authConfigSecretName)
	}

	authCfgYaml, _ := yaml.Marshal(authYaml.Auth)
	authCfg := &types.AuthConfig{}
	err = yaml.Unmarshal(authCfgYaml, &authCfg)
	if err != nil {
		klog.Fatalf("Invalid auth config data %s", authConfigSecretName)
		return nil, fmt.Errorf("invalid auth config data: %v", authConfigSecretName)
	}
	return authCfg, nil
}

func readK8sSecret(ctx context.Context, namespace string,
	secretName string) (*v1.Secret, error) {
	clusterCfg, err := rest.InClusterConfig()
	if err != nil {
		return &v1.Secret{}, fmt.Errorf("can not get cluster config. error: %v", err)
	}

	clientSet, err := kubernetes.NewForConfig(clusterCfg)
	if err != nil {
		return &v1.Secret{}, fmt.Errorf("can not initialize kubernetes client. error: %v", err)
	}

	k8client := clientSet.CoreV1()
	return k8client.Secrets(namespace).Get(ctx, secretName, metaV1.GetOptions{})
}
