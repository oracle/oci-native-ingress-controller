package client

import (
	"context"
	"net/http"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
)

type CertificateInterface interface {
	GetCertificateBundle(ctx context.Context,
		request certificates.GetCertificateBundleRequest) (certificates.GetCertificateBundleResponse, error)
	SetCertCache(cert *certificatesmanagement.Certificate)
	GetFromCertCache(certId string) *CertCacheObj
	SetCaBundleCache(caBundle *certificatesmanagement.CaBundle)
	GetFromCaBundleCache(id string) *CaBundleCacheObj
	CreateCertificate(ctx context.Context,
		req certificatesmanagement.CreateCertificateRequest) (*certificatesmanagement.Certificate, error)
	CreateCaBundle(ctx context.Context,
		req certificatesmanagement.CreateCaBundleRequest) (*certificatesmanagement.CaBundle, error)
	GetCertificate(ctx context.Context,
		req certificatesmanagement.GetCertificateRequest) (*certificatesmanagement.Certificate, error)
	ListCertificates(ctx context.Context,
		req certificatesmanagement.ListCertificatesRequest) (*certificatesmanagement.CertificateCollection, *string, error)
	ScheduleCertificateDeletion(ctx context.Context,
		req certificatesmanagement.ScheduleCertificateDeletionRequest) error
	GetCaBundle(ctx context.Context,
		req certificatesmanagement.GetCaBundleRequest) (*certificatesmanagement.CaBundle, error)
	ListCaBundles(ctx context.Context,
		req certificatesmanagement.ListCaBundlesRequest) (*certificatesmanagement.CaBundleCollection, error)
	DeleteCaBundle(ctx context.Context,
		req certificatesmanagement.DeleteCaBundleRequest) (*http.Response, error)
}

type CertCacheObj struct {
	Cert *certificatesmanagement.Certificate
	Age  time.Time
	ETag string
}

type CaBundleCacheObj struct {
	CaBundle *certificatesmanagement.CaBundle
	Age      time.Time
}

type CertificateClient struct {
	certificatesClient *certificates.CertificatesClient
}

func (client CertificateClient) SetCertCache(cert *certificatesmanagement.Certificate) {
	client.setCertCache(cert)
}

func (client CertificateClient) GetFromCertCache(certId string) *CertCacheObj {
	return client.getFromCertCache(certId)
}

func (client CertificateClient) SetCaBundleCache(caBundle *certificatesmanagement.CaBundle) {
	client.setCaBundleCache(caBundle)
}

func (client CertificateClient) GetFromCaBundleCache(id string) *CaBundleCacheObj {
	return client.GetFromCaBundleCache(id)
}

func (client CertificateClient) setCertCache(cert *certificatesmanagement.Certificate) {
	client.setCertCache(cert)
}

func (client CertificateClient) getFromCertCache(certId string) *CertCacheObj {
	return client.getFromCertCache(certId)
}

func (client CertificateClient) setCaBundleCache(caBundle *certificatesmanagement.CaBundle) {
	client.setCaBundleCache(caBundle)
}

func (client CertificateClient) getFromCaBundleCache(id string) *CaBundleCacheObj {
	return client.getFromCaBundleCache(id)
}

func (client CertificateClient) CreateCertificate(ctx context.Context, req certificatesmanagement.CreateCertificateRequest) (*certificatesmanagement.Certificate, error) {
	return client.CreateCertificate(ctx, req)
}

func (client CertificateClient) CreateCaBundle(ctx context.Context, req certificatesmanagement.CreateCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	return client.CreateCaBundle(ctx, req)
}

func (client CertificateClient) GetCertificate(ctx context.Context, req certificatesmanagement.GetCertificateRequest) (*certificatesmanagement.Certificate, error) {
	return client.GetCertificate(ctx, req)
}

func (client CertificateClient) ListCertificates(ctx context.Context, req certificatesmanagement.ListCertificatesRequest) (*certificatesmanagement.CertificateCollection, *string, error) {
	return client.ListCertificates(ctx, req)
}

func (client CertificateClient) ScheduleCertificateDeletion(ctx context.Context, req certificatesmanagement.ScheduleCertificateDeletionRequest) error {
	return client.ScheduleCertificateDeletion(ctx, req)
}

func (client CertificateClient) GetCaBundle(ctx context.Context, req certificatesmanagement.GetCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	return client.GetCaBundle(ctx, req)
}

func (client CertificateClient) ListCaBundles(ctx context.Context, req certificatesmanagement.ListCaBundlesRequest) (*certificatesmanagement.CaBundleCollection, error) {
	return client.ListCaBundles(ctx, req)
}

func (client CertificateClient) DeleteCaBundle(ctx context.Context, req certificatesmanagement.DeleteCaBundleRequest) (*http.Response, error) {
	return client.DeleteCaBundle(ctx, req)
}

func NewCertificateClient(certificatesClient *certificates.CertificatesClient) CertificateClient {
	return CertificateClient{
		certificatesClient: certificatesClient,
	}
}

func (client CertificateClient) GetCertificateBundle(ctx context.Context,
	request certificates.GetCertificateBundleRequest) (certificates.GetCertificateBundleResponse, error) {
	return client.certificatesClient.GetCertificateBundle(ctx, request)
}
