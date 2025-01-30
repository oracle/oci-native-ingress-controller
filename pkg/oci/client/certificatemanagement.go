package client

import (
	"context"

	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
)

type CertificateManagementInterface interface {
	CreateCertificate(ctx context.Context, request certificatesmanagement.CreateCertificateRequest) (certificatesmanagement.CreateCertificateResponse, error)
	GetCertificate(ctx context.Context, request certificatesmanagement.GetCertificateRequest) (certificatesmanagement.GetCertificateResponse, error)
	ListCertificates(ctx context.Context, request certificatesmanagement.ListCertificatesRequest) (certificatesmanagement.ListCertificatesResponse, error)
	UpdateCertificate(ctx context.Context, request certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error)
	ScheduleCertificateDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateDeletionRequest) (certificatesmanagement.ScheduleCertificateDeletionResponse, error)
	ListCertificateVersions(ctx context.Context, request certificatesmanagement.ListCertificateVersionsRequest) (certificatesmanagement.ListCertificateVersionsResponse, error)
	ScheduleCertificateVersionDeletion(ctx context.Context, request certificatesmanagement.ScheduleCertificateVersionDeletionRequest) (certificatesmanagement.ScheduleCertificateVersionDeletionResponse, error)
	CreateCaBundle(ctx context.Context, request certificatesmanagement.CreateCaBundleRequest) (certificatesmanagement.CreateCaBundleResponse, error)
	GetCaBundle(ctx context.Context, request certificatesmanagement.GetCaBundleRequest) (certificatesmanagement.GetCaBundleResponse, error)
	ListCaBundles(ctx context.Context, request certificatesmanagement.ListCaBundlesRequest) (certificatesmanagement.ListCaBundlesResponse, error)
	UpdateCaBundle(ctx context.Context, request certificatesmanagement.UpdateCaBundleRequest) (certificatesmanagement.UpdateCaBundleResponse, error)
	DeleteCaBundle(ctx context.Context, request certificatesmanagement.DeleteCaBundleRequest) (certificatesmanagement.DeleteCaBundleResponse, error)
}

type CertificateManagementClient struct {
	managementClient *certificatesmanagement.CertificatesManagementClient
}

func NewCertificateManagementClient(managementClient *certificatesmanagement.CertificatesManagementClient) CertificateManagementClient {
	return CertificateManagementClient{
		managementClient: managementClient,
	}
}

func (client CertificateManagementClient) CreateCertificate(ctx context.Context,
	request certificatesmanagement.CreateCertificateRequest) (certificatesmanagement.CreateCertificateResponse, error) {
	return client.managementClient.CreateCertificate(ctx, request)
}

func (client CertificateManagementClient) GetCertificate(ctx context.Context,
	request certificatesmanagement.GetCertificateRequest) (certificatesmanagement.GetCertificateResponse, error) {
	return client.managementClient.GetCertificate(ctx, request)
}

func (client CertificateManagementClient) ListCertificates(ctx context.Context,
	request certificatesmanagement.ListCertificatesRequest) (certificatesmanagement.ListCertificatesResponse, error) {
	return client.managementClient.ListCertificates(ctx, request)
}

func (client CertificateManagementClient) UpdateCertificate(ctx context.Context,
	request certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error) {
	return client.managementClient.UpdateCertificate(ctx, request)
}

func (client CertificateManagementClient) ScheduleCertificateDeletion(ctx context.Context,
	request certificatesmanagement.ScheduleCertificateDeletionRequest) (certificatesmanagement.ScheduleCertificateDeletionResponse, error) {
	return client.managementClient.ScheduleCertificateDeletion(ctx, request)
}

func (client CertificateManagementClient) ListCertificateVersions(ctx context.Context,
	request certificatesmanagement.ListCertificateVersionsRequest) (certificatesmanagement.ListCertificateVersionsResponse, error) {
	return client.managementClient.ListCertificateVersions(ctx, request)
}

func (client CertificateManagementClient) ScheduleCertificateVersionDeletion(ctx context.Context,
	request certificatesmanagement.ScheduleCertificateVersionDeletionRequest) (certificatesmanagement.ScheduleCertificateVersionDeletionResponse, error) {
	return client.managementClient.ScheduleCertificateVersionDeletion(ctx, request)
}

func (client CertificateManagementClient) CreateCaBundle(ctx context.Context,
	request certificatesmanagement.CreateCaBundleRequest) (certificatesmanagement.CreateCaBundleResponse, error) {
	return client.managementClient.CreateCaBundle(ctx, request)
}

func (client CertificateManagementClient) GetCaBundle(ctx context.Context,
	request certificatesmanagement.GetCaBundleRequest) (certificatesmanagement.GetCaBundleResponse, error) {
	return client.managementClient.GetCaBundle(ctx, request)
}

func (client CertificateManagementClient) ListCaBundles(ctx context.Context,
	request certificatesmanagement.ListCaBundlesRequest) (certificatesmanagement.ListCaBundlesResponse, error) {
	return client.managementClient.ListCaBundles(ctx, request)
}

func (client CertificateManagementClient) UpdateCaBundle(ctx context.Context,
	request certificatesmanagement.UpdateCaBundleRequest) (certificatesmanagement.UpdateCaBundleResponse, error) {
	return client.managementClient.UpdateCaBundle(ctx, request)
}

func (client CertificateManagementClient) DeleteCaBundle(ctx context.Context,
	request certificatesmanagement.DeleteCaBundleRequest) (certificatesmanagement.DeleteCaBundleResponse, error) {
	return client.managementClient.DeleteCaBundle(ctx, request)
}
