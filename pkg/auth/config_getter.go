package auth

import (
	"context"
	"github.com/oracle/oci-go-sdk/v65/common"
	sdkAuth "github.com/oracle/oci-go-sdk/v65/common/auth"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"github.com/pkg/errors"
	auth1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

var serviceAccountTokenExpiry int64 = int64(21600) // 6 hours

type ConfigGetter interface {
	GetConfigurationProvider() (common.ConfigurationProvider, error)
	GetKey() string
}

type workloadIdentityConfigGetter struct {
	saTokenProvider *ServiceAccountTokenProvider
	parentRptPath   string
}

var _ ConfigGetter = &workloadIdentityConfigGetter{}

func NewWorkloadIdentityConfigGetter(saTokenProvider *ServiceAccountTokenProvider, rptPath string) *workloadIdentityConfigGetter {
	return &workloadIdentityConfigGetter{
		saTokenProvider: saTokenProvider,
		parentRptPath:   rptPath,
	}
}

func (o workloadIdentityConfigGetter) GetConfigurationProvider() (common.ConfigurationProvider, error) {
	cp, err := sdkAuth.OkeWorkloadIdentityConfigurationProviderWithServiceAccountTokenProvider(o.saTokenProvider)
	if err != nil {
		klog.ErrorS(err, "failed to get workload identity configuration provider")
		return nil, err
	}

	if o.parentRptPath != "" {
		cp, err = sdkAuth.ResourcePrincipalV3ConfiguratorBuilder(cp).WithParentRPSTURL("").WithParentRPTURL(o.parentRptPath).Build()
		if err != nil {
			klog.ErrorS(err, "failed to get resource Principal configuration provider")
			return nil, err
		}
	}
	return cp, nil
}

func (o workloadIdentityConfigGetter) GetKey() string {
	key, _ := o.saTokenProvider.ServiceAccountNamespacedName()
	return key
}

type ServiceAccountTokenProvider struct {
	saName    string
	namespace string
	saLister  corelisters.ServiceAccountLister
	client    kubernetes.Interface
}

func NewServiceAccountTokenProvider(saName string, namespace string, saLister corelisters.ServiceAccountLister, client kubernetes.Interface) *ServiceAccountTokenProvider {
	return &ServiceAccountTokenProvider{saName: saName, namespace: namespace, saLister: saLister, client: client}
}

func (provider *ServiceAccountTokenProvider) ServiceAccountToken() (string, error) {
	if provider.saName == "" {
		return "", errors.New("error fetching service account, empty string provided via" + util.IngressClassServiceAccountName)
	}
	if _, err := provider.saLister.ServiceAccounts(provider.namespace).Get(provider.saName); err != nil {
		return "", errors.Wrapf(err, "error fetching service account token")
	}
	tokenRequest := auth1.TokenRequest{Spec: auth1.TokenRequestSpec{ExpirationSeconds: &serviceAccountTokenExpiry}}
	saToken, err := provider.client.CoreV1().ServiceAccounts(provider.namespace).CreateToken(context.TODO(), provider.saName, &tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return "", errors.Wrapf(err, "error creating service account token")
	}
	return saToken.Status.Token, nil
}

func (provider *ServiceAccountTokenProvider) ServiceAccountNamespacedName() (string, error) {
	return types.NamespacedName{Namespace: provider.saName, Name: provider.namespace}.String(), nil
}
