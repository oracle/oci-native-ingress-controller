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
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	. "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"k8s.io/klog/v2"
)

const (
	certificateServiceTimeout = 2 * time.Minute
)

type CertificatesClient struct {
	ManagementClient   CertificateManagementInterface
	CertificatesClient CertificateInterface

	certMu        sync.Mutex
	caMu          sync.Mutex
	CertCache     map[string]*CertCacheObj
	CaBundleCache map[string]*CaBundleCacheObj
}

func New(managementClient CertificateManagementInterface,
	certificateClient CertificateInterface) *CertificatesClient {
	return &CertificatesClient{
		ManagementClient:   managementClient,
		CertificatesClient: certificateClient,
		CertCache:          map[string]*CertCacheObj{},
		CaBundleCache:      map[string]*CaBundleCacheObj{},
	}
}

func (certificatesClient *CertificatesClient) SetCertCache(cert *certificatesmanagement.Certificate, etag string) {
	certificatesClient.certMu.Lock()
	certificatesClient.CertCache[*cert.Id] = &CertCacheObj{Cert: cert, Age: time.Now(), Etag: etag}
	certificatesClient.certMu.Unlock()
}

func (certificatesClient *CertificatesClient) GetFromCertCache(certId string) *CertCacheObj {
	certificatesClient.certMu.Lock()
	defer certificatesClient.certMu.Unlock()
	return certificatesClient.CertCache[certId]
}

func (certificatesClient *CertificatesClient) SetCaBundleCache(caBundle *certificatesmanagement.CaBundle, etag string) {
	certificatesClient.caMu.Lock()
	certificatesClient.CaBundleCache[*caBundle.Id] = &CaBundleCacheObj{CaBundle: caBundle, Age: time.Now(), Etag: etag}
	certificatesClient.caMu.Unlock()
}

func (certificatesClient *CertificatesClient) GetFromCaBundleCache(id string) *CaBundleCacheObj {
	certificatesClient.caMu.Lock()
	defer certificatesClient.caMu.Unlock()
	return certificatesClient.CaBundleCache[id]
}

func (certificatesClient *CertificatesClient) CreateCertificate(ctx context.Context,
	req certificatesmanagement.CreateCertificateRequest) (*certificatesmanagement.Certificate, string, error) {
	klog.Infof("Creating certicate %s in compartment %s", *req.Name, *req.CompartmentId)
	resp, err := certificatesClient.ManagementClient.CreateCertificate(ctx, req)
	if err != nil {
		klog.Errorf("Error creating certificate %s, %s ", *req.Name, err.Error())
		return nil, "", err
	}

	return certificatesClient.waitForActiveCertificate(ctx, *resp.Certificate.Id)
}

func (certificatesClient *CertificatesClient) CreateCaBundle(ctx context.Context,
	req certificatesmanagement.CreateCaBundleRequest) (*certificatesmanagement.CaBundle, string, error) {
	klog.Infof("Creating cabundle %s in compartment %s", *req.Name, *req.CompartmentId)
	resp, err := certificatesClient.ManagementClient.CreateCaBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error creating ca bundle %s, %s ", *req.Name, err.Error())
		return nil, "", err
	}

	return certificatesClient.waitForActiveCaBundle(ctx, *resp.CaBundle.Id)
}

func (certificatesClient *CertificatesClient) GetCertificate(ctx context.Context,
	req certificatesmanagement.GetCertificateRequest) (*certificatesmanagement.Certificate, string, error) {
	klog.Infof("Getting certificate for ocid %s ", *req.CertificateId)
	resp, err := certificatesClient.ManagementClient.GetCertificate(ctx, req)
	if err != nil {
		klog.Errorf("Error getting certificate %s, %s ", *req.CertificateId, err.Error())
		return nil, "", err
	}

	return &resp.Certificate, *resp.Etag, nil
}

func (certificatesClient *CertificatesClient) ListCertificates(ctx context.Context,
	req certificatesmanagement.ListCertificatesRequest) (*certificatesmanagement.CertificateCollection, *string, error) {
	klog.Infof("Listing certificates with request %s", util.PrettyPrint(req))
	resp, err := certificatesClient.ManagementClient.ListCertificates(ctx, req)
	if err != nil {
		klog.Errorf("Error listing certificates for request %s, %s ", util.PrettyPrint(req), err.Error())
		return nil, nil, err
	}

	return &resp.CertificateCollection, resp.OpcNextPage, nil
}

func (certificatesClient *CertificatesClient) UpdateCertificate(ctx context.Context,
	req certificatesmanagement.UpdateCertificateRequest) (*certificatesmanagement.Certificate, string, error) {
	_, err := certificatesClient.ManagementClient.UpdateCertificate(ctx, req)
	if err != nil {
		if !util.IsServiceError(err, 409) {
			klog.Errorf("Error updating certificate %s: %s", *req.CertificateId, err)
		} else {
			klog.Errorf("Error updating certificate %s due to 409-Conflict", *req.CertificateId)
		}
		return nil, "", err
	}

	return certificatesClient.waitForActiveCertificate(ctx, *req.CertificateId)
}

func (certificatesClient *CertificatesClient) ScheduleCertificateDeletion(ctx context.Context,
	req certificatesmanagement.ScheduleCertificateDeletionRequest) error {
	_, err := certificatesClient.ManagementClient.ScheduleCertificateDeletion(ctx, req)
	if err != nil {
		klog.Errorf("Error scheduling certificate for deletion, certificateId %s, %s ", *req.CertificateId, err.Error())
		return err
	}
	return nil
}

func (certificatesClient *CertificatesClient) ListCertificateVersions(ctx context.Context,
	req certificatesmanagement.ListCertificateVersionsRequest) (*certificatesmanagement.CertificateVersionCollection, *string, error) {
	resp, err := certificatesClient.ManagementClient.ListCertificateVersions(ctx, req)
	if err != nil {
		klog.Errorf("Error listing certificate versions for request %s, %s ", util.PrettyPrint(req), err.Error())
		return nil, nil, err
	}

	return &resp.CertificateVersionCollection, resp.OpcNextPage, nil
}

func (certificatesClient *CertificatesClient) ScheduleCertificateVersionDeletion(ctx context.Context,
	req certificatesmanagement.ScheduleCertificateVersionDeletionRequest) (*certificatesmanagement.Certificate, string, error) {
	klog.Infof("Scheduling version %d of Certificate %s for deletion", *req.CertificateVersionNumber, *req.CertificateId)
	_, err := certificatesClient.ManagementClient.ScheduleCertificateVersionDeletion(ctx, req)
	if err != nil {
		klog.Errorf("Error scheduling certificate version for deletion, certificateId %s, version %d, %s ",
			*req.CertificateId, *req.CertificateVersionNumber, err.Error())
		return nil, "", err
	}

	return certificatesClient.waitForActiveCertificate(ctx, *req.CertificateId)
}

func (certificatesClient *CertificatesClient) waitForActiveCertificate(ctx context.Context,
	certificateId string) (*certificatesmanagement.Certificate, string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, certificateServiceTimeout)
	defer cancel()

	for {
		resp, err := certificatesClient.ManagementClient.GetCertificate(timeoutCtx, certificatesmanagement.GetCertificateRequest{
			CertificateId: &certificateId,
		})
		if err != nil {
			return nil, "", err
		}

		if resp.Certificate.LifecycleState == certificatesmanagement.CertificateLifecycleStateActive {
			return &resp.Certificate, *resp.Etag, nil
		}

		if resp.Certificate.LifecycleState != certificatesmanagement.CertificateLifecycleStateUpdating &&
			resp.Certificate.LifecycleState != certificatesmanagement.CertificateLifecycleStateCreating {
			return nil, "", fmt.Errorf("certificate %s went into an unexpected state %s while updating",
				*resp.Certificate.Id, resp.Certificate.LifecycleState)
		}

		klog.Infof("Certificate %s still not active, waiting", certificateId)
		time.Sleep(3 * time.Second)
	}
}

func (certificatesClient *CertificatesClient) GetCaBundle(ctx context.Context,
	req certificatesmanagement.GetCaBundleRequest) (*certificatesmanagement.CaBundle, string, error) {
	klog.Infof("Getting ca bundle with ocid %s ", *req.CaBundleId)
	resp, err := certificatesClient.ManagementClient.GetCaBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error getting certificate %s, %s ", *req.CaBundleId, err.Error())
		return nil, "", err
	}

	return &resp.CaBundle, *resp.Etag, nil
}

func (certificatesClient *CertificatesClient) ListCaBundles(ctx context.Context,
	req certificatesmanagement.ListCaBundlesRequest) (*certificatesmanagement.CaBundleCollection, error) {
	klog.Infof("Getting ca bundles using request %s ", util.PrettyPrint(req))
	resp, err := certificatesClient.ManagementClient.ListCaBundles(ctx, req)
	if err != nil {
		klog.Errorf("Error listing ca bundles for request %s, %s ", util.PrettyPrint(req), err.Error())
		return nil, err
	}

	return &resp.CaBundleCollection, nil
}

func (certificatesClient *CertificatesClient) UpdateCaBundle(ctx context.Context,
	req certificatesmanagement.UpdateCaBundleRequest) (*certificatesmanagement.CaBundle, string, error) {
	_, err := certificatesClient.ManagementClient.UpdateCaBundle(ctx, req)
	if err != nil {
		if !util.IsServiceError(err, 409) {
			klog.Errorf("Error updating ca bundle %s: %s", *req.CaBundleId, err)
		} else {
			klog.Errorf("Error updating ca bundle %s due to 409-Conflict", *req.CaBundleId)
		}
		return nil, "", err
	}

	return certificatesClient.waitForActiveCaBundle(ctx, *req.CaBundleId)
}

func (certificatesClient *CertificatesClient) DeleteCaBundle(ctx context.Context,
	req certificatesmanagement.DeleteCaBundleRequest) (*http.Response, error) {
	klog.Infof("Deleting ca bundle with ocid %s ", *req.CaBundleId)
	resp, err := certificatesClient.ManagementClient.DeleteCaBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error deleting ca bundle %s, %s ", *req.CaBundleId, err.Error())
		return nil, err
	}

	return resp.HTTPResponse(), nil
}

func (certificatesClient *CertificatesClient) waitForActiveCaBundle(ctx context.Context,
	caBundleId string) (*certificatesmanagement.CaBundle, string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, certificateServiceTimeout)
	defer cancel()

	for {
		resp, err := certificatesClient.ManagementClient.GetCaBundle(timeoutCtx, certificatesmanagement.GetCaBundleRequest{
			CaBundleId: &caBundleId,
		})
		if err != nil {
			return nil, "", err
		}

		if resp.CaBundle.LifecycleState == certificatesmanagement.CaBundleLifecycleStateActive {
			return &resp.CaBundle, *resp.Etag, nil
		}

		if resp.CaBundle.LifecycleState != certificatesmanagement.CaBundleLifecycleStateUpdating &&
			resp.CaBundle.LifecycleState != certificatesmanagement.CaBundleLifecycleStateCreating {
			return nil, "", fmt.Errorf("ca bundle %s went into an unexpected state %s while updating",
				*resp.CaBundle.Id, resp.CaBundle.LifecycleState)
		}

		klog.Infof("cabundle %s still not active, waiting", caBundleId)
		time.Sleep(3 * time.Second)
	}
}

func (certificatesClient *CertificatesClient) GetCertificateBundle(ctx context.Context,
	req certificates.GetCertificateBundleRequest) (certificates.CertificateBundle, error) {
	klog.Infof("Getting certificate bundle for certificate ocid %s ", *req.CertificateId)
	resp, err := certificatesClient.CertificatesClient.GetCertificateBundle(ctx, req)
	if err != nil {
		klog.Errorf("Error getting certificate bundle for certificate %s, %s ", *req.CertificateId, err.Error())
		return nil, err
	}

	return resp.CertificateBundle, nil
}
