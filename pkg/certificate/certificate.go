/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package certificate

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"k8s.io/klog/v2"
)

type certCacheObj struct {
	cert *certificatesmanagement.Certificate
	age  time.Time
}

type caBundleCacheObj struct {
	caBundle *certificatesmanagement.CaBundle
	age      time.Time
}

type CertificatesClient struct {
	managementClient   *certificatesmanagement.CertificatesManagementClient
	certificatesClient *certificates.CertificatesClient

	certMu        sync.Mutex
	caMu          sync.Mutex
	certCache     map[string]*certCacheObj
	caBundleCache map[string]*caBundleCacheObj
}

func New(managementClient *certificatesmanagement.CertificatesManagementClient,
	certificateClient *certificates.CertificatesClient) *CertificatesClient {
	return &CertificatesClient{
		managementClient:   managementClient,
		certificatesClient: certificateClient,
		certCache:          map[string]*certCacheObj{},
		caBundleCache:      map[string]*caBundleCacheObj{},
	}
}

func (certificatesClient *CertificatesClient) setCertCache(cert *certificatesmanagement.Certificate) {
	certificatesClient.certMu.Lock()
	certificatesClient.certCache[*cert.Id] = &certCacheObj{cert, time.Now()}
	certificatesClient.certMu.Unlock()
}

func (certificatesClient *CertificatesClient) getFromCertCache(certId string) *certCacheObj {
	certificatesClient.certMu.Lock()
	defer certificatesClient.certMu.Unlock()
	return certificatesClient.certCache[certId]
}

func (certificatesClient *CertificatesClient) setCaBundleCache(caBundle *certificatesmanagement.CaBundle) {
	certificatesClient.caMu.Lock()
	certificatesClient.caBundleCache[*caBundle.Id] = &caBundleCacheObj{caBundle, time.Now()}
	certificatesClient.caMu.Unlock()
}

func (certificatesClient *CertificatesClient) getFromCaBundleCache(id string) *caBundleCacheObj {
	certificatesClient.caMu.Lock()
	defer certificatesClient.caMu.Unlock()
	return certificatesClient.caBundleCache[id]
}

func (certificatesClient *CertificatesClient) CreateCertificate(ctx context.Context,
	req certificatesmanagement.CreateCertificateRequest) (*certificatesmanagement.Certificate, error) {
	resp, err := certificatesClient.managementClient.CreateCertificate(ctx, req)
	if err != nil {
		klog.Errorf("Error creating certificate %s, %s ", *req.Name, err.Error())
		return nil, err
	}

	return &resp.Certificate, nil
}

func (certificatesClient *CertificatesClient) CreateCaBundle(ctx context.Context,
	req certificatesmanagement.CreateCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	resp, err := certificatesClient.managementClient.CreateCaBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error creating ca bundle %s, %s ", *req.Name, err.Error())
		return nil, err
	}

	return &resp.CaBundle, nil
}

func (certificatesClient *CertificatesClient) GetCertificate(ctx context.Context,
	req certificatesmanagement.GetCertificateRequest) (*certificatesmanagement.Certificate, error) {
	klog.Infof("Getting certificate for ocid %s ", *req.CertificateId)
	resp, err := certificatesClient.managementClient.GetCertificate(ctx, req)
	if err != nil {
		klog.Errorf("Error getting certificate %s, %s ", *req.CertificateId, err.Error())
		return nil, err
	}

	return &resp.Certificate, nil
}

func (certificatesClient *CertificatesClient) ListCertificates(ctx context.Context,
	req certificatesmanagement.ListCertificatesRequest) (*certificatesmanagement.CertificateCollection, *string, error) {
	klog.Infof("Listing certificates with request %s", util.PrettyPrint(req))
	resp, err := certificatesClient.managementClient.ListCertificates(ctx, req)
	if err != nil {
		klog.Errorf("Error listing certificates for request %s, %s ", util.PrettyPrint(req), err.Error())
		return nil, nil, err
	}

	return &resp.CertificateCollection, resp.OpcNextPage, nil
}

func (certificatesClient *CertificatesClient) ScheduleCertificateDeletion(ctx context.Context,
	req certificatesmanagement.ScheduleCertificateDeletionRequest) error {
	_, err := certificatesClient.managementClient.ScheduleCertificateDeletion(ctx, req)
	if err != nil {
		klog.Errorf("Error scheduling certificate for deletion, certificateId %s, %s ", *req.CertificateId, err.Error())
		return err
	}
	return nil
}

func (certificatesClient *CertificatesClient) GetCaBundle(ctx context.Context,
	req certificatesmanagement.GetCaBundleRequest) (*certificatesmanagement.CaBundle, error) {
	klog.Infof("Getting ca bundle with ocid %s ", *req.CaBundleId)
	resp, err := certificatesClient.managementClient.GetCaBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error getting certificate %s, %s ", *req.CaBundleId, err.Error())
		return nil, err
	}

	return &resp.CaBundle, nil
}

func (certificatesClient *CertificatesClient) ListCaBundles(ctx context.Context,
	req certificatesmanagement.ListCaBundlesRequest) (*certificatesmanagement.CaBundleCollection, error) {
	klog.Infof("Getting ca bundles using request %s ", util.PrettyPrint(req))
	resp, err := certificatesClient.managementClient.ListCaBundles(ctx, req)
	if err != nil {
		klog.Errorf("Error listing ca bundles for request %s, %s ", util.PrettyPrint(req), err.Error())
		return nil, err
	}

	return &resp.CaBundleCollection, nil
}

func (certificatesClient *CertificatesClient) DeleteCaBundle(ctx context.Context,
	req certificatesmanagement.DeleteCaBundleRequest) (*http.Response, error) {
	klog.Infof("Deleting ca bundle with ocid %s ", *req.CaBundleId)
	resp, err := certificatesClient.managementClient.DeleteCaBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error deleting ca bundle %s, %s ", *req.CaBundleId, err.Error())
		return nil, err
	}

	return resp.HTTPResponse(), nil
}

func (certificatesClient *CertificatesClient) GetCertificateBundle(ctx context.Context,
	req certificates.GetCertificateBundleRequest) (certificates.CertificateBundle, error) {
	klog.Infof("Getting certificate bundle for certificate ocid %s ", *req.CertificateId)
	resp, err := certificatesClient.certificatesClient.GetCertificateBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error getting certificate bundle for certificate %s, %s ", *req.CertificateId, err.Error())
		return nil, err
	}

	return resp.CertificateBundle, nil
}
