/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2025 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package certificatecleanup

import (
	"context"
	"errors"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/informers"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"

	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

var (
	caBundlesFirstPage = []certificatesmanagement.CaBundleSummary{
		{
			// will be ignored
			Name:         common.String("unrelated-to-nic"),
			Id:           common.String("unrelated-to-nic"),
			FreeformTags: map[string]string{util.CaBundleHashTagKey: "hash", deletionTimeTagKey: time.Now().UTC().Format(time.RFC3339)},
		},
		{
			// will be ignored after association check
			// ListAssociation should return a list with at least one association
			Name:         common.String(util.CertificateResourcePrefix + "-in-use"),
			Id:           common.String(util.CertificateResourcePrefix + "-in-use"),
			FreeformTags: map[string]string{util.CaBundleHashTagKey: "hash"},
		},
		{
			// will be ignored as no hash freeform tag present
			Name: common.String(util.CertificateResourcePrefix + "-not-in-use-no-hash"),
			Id:   common.String(util.CertificateResourcePrefix + "-not-in-use-no-hash"),
		},
	}

	caBundlesSecondPage = []certificatesmanagement.CaBundleSummary{
		{
			// will be marked for deletion
			Name:         common.String(util.CertificateResourcePrefix + "-not-in-use"),
			Id:           common.String(util.CertificateResourcePrefix + "-not-in-use"),
			FreeformTags: map[string]string{util.CaBundleHashTagKey: "hash"},
		},
		{
			// will be deleted
			Name:         common.String(util.CertificateResourcePrefix + "-marked-for-deletion"),
			Id:           common.String(util.CertificateResourcePrefix + "-marked-for-deletion"),
			FreeformTags: map[string]string{deletionTimeTagKey: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)},
		},
		{
			// will be ignored
			Name:         common.String(util.CertificateResourcePrefix + "-marked-for-deletion-in-future"),
			Id:           common.String(util.CertificateResourcePrefix + "-marked-for-deletion-in-future"),
			FreeformTags: map[string]string{deletionTimeTagKey: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)},
		},
		{
			// will be marked for deletion again
			Name:         common.String(util.CertificateResourcePrefix + "-marked-for-deletion-unparsable-time"),
			Id:           common.String(util.CertificateResourcePrefix + "-marked-for-deletion-unparsable-time"),
			FreeformTags: map[string]string{deletionTimeTagKey: "unparsable"},
		},
	}

	certificatesFirstPage = []certificatesmanagement.CertificateSummary{
		{
			// will be ignored
			Name:         common.String("unrelated-to-nic"),
			Id:           common.String("unrelated-to-nic"),
			FreeformTags: map[string]string{util.CertificateHashTagKey: "hash", deletionTimeTagKey: time.Now().UTC().Format(time.RFC3339)},
		},
		{
			// will be ignored after association check
			// ListAssociation should return a list with at least one association
			Name:         common.String(util.CertificateResourcePrefix + "-in-use"),
			Id:           common.String(util.CertificateResourcePrefix + "-in-use"),
			FreeformTags: map[string]string{util.CertificateHashTagKey: "hash"},
		},
		{
			// will be ignored as no hash freeform tag is present
			Name: common.String(util.CertificateResourcePrefix + "-not-in-use-no-hash"),
			Id:   common.String(util.CertificateResourcePrefix + "-not-in-use-no-hash"),
		},
	}

	certificatesSecondPage = []certificatesmanagement.CertificateSummary{
		{
			// will be marked for deletion
			Name:         common.String(util.CertificateResourcePrefix + "-not-in-use"),
			Id:           common.String(util.CertificateResourcePrefix + "-not-in-use"),
			FreeformTags: map[string]string{util.CertificateHashTagKey: "hash"},
		},
		{
			// will be deleted
			Name:         common.String(util.CertificateResourcePrefix + "-marked-for-deletion"),
			Id:           common.String(util.CertificateResourcePrefix + "-marked-for-deletion"),
			FreeformTags: map[string]string{deletionTimeTagKey: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)},
		},
		{
			// will be ignored
			Name:         common.String(util.CertificateResourcePrefix + "-marked-for-deletion-in-future"),
			Id:           common.String(util.CertificateResourcePrefix + "-marked-for-deletion-in-future"),
			FreeformTags: map[string]string{deletionTimeTagKey: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)},
		},
		{
			// will be marked for deletion again
			Name:         common.String(util.CertificateResourcePrefix + "-marked-for-deletion-unparsable-time"),
			Id:           common.String(util.CertificateResourcePrefix + "-marked-for-deletion-unparsable-time"),
			FreeformTags: map[string]string{deletionTimeTagKey: "unparsable"},
		},
	}
)

func inits() (context.Context, *certificate.CertificatesClient) {
	certClient := GetCertClient()
	certManageClient := GetCertManageClient()

	certificatesClient := &certificate.CertificatesClient{
		ManagementClient:   certManageClient,
		CertificatesClient: certClient,
		CertCache:          map[string]*ociclient.CertCacheObj{},
		CaBundleCache:      map[string]*ociclient.CaBundleCacheObj{},
	}

	wrapperClient := client.NewWrapperClient(nil, nil, nil, certificatesClient, nil)
	fakeClient := &client.ClientProvider{
		K8sClient:           nil,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}

	ctx, _ := client.GetClientContext(nil, nil, fakeClient, "", "")
	return ctx, certificatesClient
}

func TestRun(t *testing.T) {
	RegisterTestingT(t)

	controllerClass := "oci.oraclecloud.com/native-ingress-controller"
	fakeClient := fakeclientset.NewSimpleClientset()
	util.UpdateFakeClientCall(fakeClient, "list", "ingressclasses", &networkingv1.IngressClassList{
		Items: []networkingv1.IngressClass{
			*util.GetIngressClassResource("ingressClass1", true, controllerClass),
			*util.GetIngressClassResource("ingressClass2", false, controllerClass),
		},
	})
	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()
	saInformer := informerFactory.Core().V1().ServiceAccounts()
	saInformer.Lister()
	informerFactory.Start(context.TODO().Done())
	cache.WaitForCacheSync(context.TODO().Done(), ingressClassInformer.Informer().HasSynced)
	cache.WaitForCacheSync(context.TODO().Done(), saInformer.Informer().HasSynced)

	_, certClient := inits()
	wrapperClient := client.NewWrapperClient(fakeClient, nil, nil, certClient, nil)
	client := &client.ClientProvider{
		K8sClient:           fakeClient,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	cleanupTask := NewTask(controllerClass, ingressClassInformer, saInformer, true,
		"compartmentId", time.Hour, client, nil)

	cleanupTask.run()
}

func TestCleanupCompartment(t *testing.T) {
	RegisterTestingT(t)
	ctx, _ := inits()

	err := cleanupCompartment(ctx, "compartmentId", time.Hour)
	Expect(err).To(BeNil())

	err = cleanupCompartment(ctx, "error", time.Hour)
	Expect(err).ToNot(BeNil())
}

func GetCertManageClient() ociclient.CertificateManagementInterface {
	return &MockCertificateManagerClient{}
}

type MockCertificateManagerClient struct {
}

func (m MockCertificateManagerClient) CreateCertificate(ctx context.Context, request certificatesmanagement.CreateCertificateRequest) (certificatesmanagement.CreateCertificateResponse, error) {
	return certificatesmanagement.CreateCertificateResponse{}, nil
}

func (m MockCertificateManagerClient) GetCertificate(ctx context.Context, request certificatesmanagement.GetCertificateRequest) (certificatesmanagement.GetCertificateResponse, error) {
	return certificatesmanagement.GetCertificateResponse{
		Etag: common.String("etag"),
		Certificate: certificatesmanagement.Certificate{
			LifecycleState: certificatesmanagement.CertificateLifecycleStateActive,
			Id:             common.String("id"),
		},
	}, nil
}

func (m MockCertificateManagerClient) ListCertificates(ctx context.Context, request certificatesmanagement.ListCertificatesRequest) (certificatesmanagement.ListCertificatesResponse, error) {
	if *request.CompartmentId == "error" {
		return certificatesmanagement.ListCertificatesResponse{}, errors.New("error listing certificates")
	}

	if request.Page == nil {
		return certificatesmanagement.ListCertificatesResponse{
			OpcNextPage: common.String("next-page"),
			CertificateCollection: certificatesmanagement.CertificateCollection{
				Items: certificatesSecondPage,
			},
		}, nil
	}

	return certificatesmanagement.ListCertificatesResponse{
		CertificateCollection: certificatesmanagement.CertificateCollection{
			Items: certificatesFirstPage,
		},
	}, nil
}

func (m MockCertificateManagerClient) UpdateCertificate(ctx context.Context, request certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error) {
	if *request.CertificateId == "error" {
		return certificatesmanagement.UpdateCertificateResponse{}, errors.New("cert update error")
	}

	return certificatesmanagement.UpdateCertificateResponse{}, nil
}

func (m MockCertificateManagerClient) ScheduleCertificateDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateDeletionRequest) (certificatesmanagement.ScheduleCertificateDeletionResponse, error) {
	var err error
	if *request.CertificateId == "error" {
		err = errors.New("cert error deletion")
	}
	return certificatesmanagement.ScheduleCertificateDeletionResponse{}, err
}

func (m MockCertificateManagerClient) ListCertificateVersions(ctx context.Context, request certificatesmanagement.ListCertificateVersionsRequest) (certificatesmanagement.ListCertificateVersionsResponse, error) {
	return certificatesmanagement.ListCertificateVersionsResponse{}, nil
}

func (m MockCertificateManagerClient) ScheduleCertificateVersionDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateVersionDeletionRequest) (certificatesmanagement.ScheduleCertificateVersionDeletionResponse, error) {
	return certificatesmanagement.ScheduleCertificateVersionDeletionResponse{}, nil
}

func (m MockCertificateManagerClient) CreateCaBundle(ctx context.Context, request certificatesmanagement.CreateCaBundleRequest) (certificatesmanagement.CreateCaBundleResponse, error) {
	return certificatesmanagement.CreateCaBundleResponse{}, nil
}

func (m MockCertificateManagerClient) GetCaBundle(ctx context.Context, request certificatesmanagement.GetCaBundleRequest) (certificatesmanagement.GetCaBundleResponse, error) {
	return certificatesmanagement.GetCaBundleResponse{
		Etag: common.String("etag"),
		CaBundle: certificatesmanagement.CaBundle{
			LifecycleState: certificatesmanagement.CaBundleLifecycleStateActive,
			Id:             common.String("id"),
		},
	}, nil
}

func (m MockCertificateManagerClient) ListCaBundles(ctx context.Context, request certificatesmanagement.ListCaBundlesRequest) (certificatesmanagement.ListCaBundlesResponse, error) {
	if *request.CompartmentId == "error" {
		return certificatesmanagement.ListCaBundlesResponse{}, errors.New("error listing ca bundles")
	}

	if request.Page == nil {
		return certificatesmanagement.ListCaBundlesResponse{
			OpcNextPage: common.String("next-page"),
			CaBundleCollection: certificatesmanagement.CaBundleCollection{
				Items: caBundlesSecondPage,
			},
		}, nil
	}

	return certificatesmanagement.ListCaBundlesResponse{
		CaBundleCollection: certificatesmanagement.CaBundleCollection{
			Items: caBundlesFirstPage,
		},
	}, nil
}

func (m MockCertificateManagerClient) UpdateCaBundle(ctx context.Context, request certificatesmanagement.UpdateCaBundleRequest) (certificatesmanagement.UpdateCaBundleResponse, error) {
	if *request.CaBundleId == "error" {
		return certificatesmanagement.UpdateCaBundleResponse{}, errors.New("caBundle update error")
	}

	return certificatesmanagement.UpdateCaBundleResponse{}, nil
}

func (m MockCertificateManagerClient) DeleteCaBundle(ctx context.Context, request certificatesmanagement.DeleteCaBundleRequest) (certificatesmanagement.DeleteCaBundleResponse, error) {
	var err error
	if *request.CaBundleId == "error" {
		err = errors.New("cabundle error deletion")
	}
	return certificatesmanagement.DeleteCaBundleResponse{}, err
}

func (m MockCertificateManagerClient) ListAssociations(ctx context.Context, request certificatesmanagement.ListAssociationsRequest) (certificatesmanagement.ListAssociationsResponse, error) {
	if *request.CertificatesResourceId == "error" {
		return certificatesmanagement.ListAssociationsResponse{}, errors.New("error listing associations")
	}

	associationCollectionItems := []certificatesmanagement.AssociationSummary{}
	if *request.CertificatesResourceId == util.CertificateResourcePrefix+"-in-use" {
		associationCollectionItems = append(associationCollectionItems, certificatesmanagement.AssociationSummary{Name: common.String("association")})
	}

	return certificatesmanagement.ListAssociationsResponse{
		AssociationCollection: certificatesmanagement.AssociationCollection{Items: associationCollectionItems},
	}, nil
}

func GetCertClient() ociclient.CertificateInterface {
	return &MockCertificateClient{}
}

type MockCertificateClient struct {
}

func (m MockCertificateClient) GetCertificateBundle(ctx context.Context, request certificates.GetCertificateBundleRequest) (certificates.GetCertificateBundleResponse, error) {
	return certificates.GetCertificateBundleResponse{}, nil
}

func (m MockCertificateClient) SetCertCache(cert *certificatesmanagement.Certificate) {
}

func (m MockCertificateClient) GetFromCertCache(certId string) *ociclient.CertCacheObj {
	return nil
}

func (m MockCertificateClient) SetCaBundleCache(caBundle *certificatesmanagement.CaBundle) {
}

func (m MockCertificateClient) GetFromCaBundleCache(id string) *ociclient.CaBundleCacheObj {
	return nil
}

func (m MockCertificateClient) CreateCertificate(ctx context.Context, req certificatesmanagement.CreateCertificateRequest) (*certificatesmanagement.Certificate, error) {
	return nil, nil
}

func (m MockCertificateClient) CreateCaBundle(ctx context.Context, req certificatesmanagement.CreateCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	return nil, nil
}

func (m MockCertificateClient) GetCertificate(ctx context.Context, req certificatesmanagement.GetCertificateRequest) (*certificatesmanagement.Certificate, error) {
	return &certificatesmanagement.Certificate{}, nil
}

func (m MockCertificateClient) ListCertificates(ctx context.Context, req certificatesmanagement.ListCertificatesRequest) (*certificatesmanagement.CertificateCollection, *string, error) {
	return nil, nil, nil
}

func (m MockCertificateClient) ScheduleCertificateDeletion(ctx context.Context, req certificatesmanagement.ScheduleCertificateDeletionRequest) error {
	return nil
}

func (m MockCertificateClient) GetCaBundle(ctx context.Context, req certificatesmanagement.GetCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	return nil, nil
}

func (m MockCertificateClient) ListCaBundles(ctx context.Context, req certificatesmanagement.ListCaBundlesRequest) (*certificatesmanagement.CaBundleCollection, error) {
	return nil, nil
}

func (m MockCertificateClient) DeleteCaBundle(ctx context.Context, req certificatesmanagement.DeleteCaBundleRequest) (*http.Response, error) {
	return nil, nil
}

// MockConfigGetter is a mock implementation of the ConfigGetter interface for testing purposes.
type MockConfigGetter struct {
	ConfigurationProvider common.ConfigurationProvider
	Key                   string
	Error                 error
}

func (m *MockConfigGetter) GetConfigurationProvider() (common.ConfigurationProvider, error) {
	return m.ConfigurationProvider, m.Error
}

func (m *MockConfigGetter) GetKey() string {
	return m.Key
}

type MockCacheStore struct {
	client *client.WrapperClient
}

func (m *MockCacheStore) Add(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) Update(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) Delete(obj interface{}) error {
	return nil
}

func (m *MockCacheStore) List() []interface{} {
	return nil
}

func (m *MockCacheStore) ListKeys() []string {
	return nil
}

func (m *MockCacheStore) Get(obj interface{}) (item interface{}, exists bool, err error) {
	return nil, true, nil
}

func (m *MockCacheStore) Replace(i []interface{}, s string) error {
	return nil
}

func (m *MockCacheStore) Resync() error {
	return nil
}

func (m *MockCacheStore) GetByKey(key string) (item interface{}, exists bool, err error) {
	return m.client, true, nil
}

func NewMockCacheStore(client *client.WrapperClient) *MockCacheStore {
	return &MockCacheStore{
		client: client,
	}
}
