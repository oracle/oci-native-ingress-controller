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
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	"k8s.io/klog/v2"

	"github.com/oracle/oci-native-ingress-controller/pkg/certificate"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
)

func getCaBundlesToBeMarkedOrCleanedUp(client *certificate.CertificatesClient, compartmentId string) ([]string, []string, error) {
	unmarkedCaBundles := []string{}
	caBundlesToBeDeleted := []string{}

	caBundleListRequest := certificatesmanagement.ListCaBundlesRequest{
		CompartmentId:  common.String(compartmentId),
		LifecycleState: certificatesmanagement.ListCaBundlesLifecycleStateActive,
		Limit:          common.Int(10),
	}

	for {
		caBundles, nextPage, err := client.ListCaBundles(context.TODO(), caBundleListRequest)
		if err != nil {
			return nil, nil, err
		}

		for _, caBundleSummary := range caBundles.Items {
			// we are not interested in ca bundles that don't have our prefix
			if !strings.HasPrefix(*caBundleSummary.Name, util.CertificateResourcePrefix) {
				continue
			}

			deletionTimeVal, ok := caBundleSummary.FreeformTags[deletionTimeTagKey]
			// if the deletionTimestamp tag is absent and resource-hash tag is present, we may need to mark this for deletion, we check for associations later
			if !ok && caBundleSummary.FreeformTags[util.CaBundleHashTagKey] != "" {
				unmarkedCaBundles = append(unmarkedCaBundles, *caBundleSummary.Id)
				continue
			} else if !ok {
				continue
			}

			shouldBeDeleted, err := hasDeletionTimePassed(deletionTimeVal)
			// failed to parse the freeform tag appropriately, will mark for deletion again if required
			if err != nil {
				klog.Infof("for ca bundle %s, deletionTimestamp tag unproccessable, will see if requires marking", *caBundleSummary.Name)
				unmarkedCaBundles = append(unmarkedCaBundles, *caBundleSummary.Id)
				continue
			}

			// the deletionTimestamp tag has a time that's in the past, can delete this ca bundle
			if shouldBeDeleted {
				klog.Infof("unused ca bundle %s needs to be deleted", *caBundleSummary.Name)
				caBundlesToBeDeleted = append(caBundlesToBeDeleted, *caBundleSummary.Id)
			}
		}

		if nextPage == nil {
			break
		}

		caBundleListRequest.Page = nextPage
	}

	// we filter out any ca bundles that are actually being used, all other relevant unmarked ca bundles will be marked with a deletionTimestamp
	caBundlesToBeMarked, err := filterOutCertificateResourcesWithAssociations(client, unmarkedCaBundles, compartmentId)
	if err != nil {
		return nil, nil, err
	}

	return caBundlesToBeMarked, caBundlesToBeDeleted, nil
}

func markCaBundlesWithDeletionTime(client *certificate.CertificatesClient, caBundles []string, gracePeriod time.Duration) {
	deletionTime := time.Now().UTC().Add(gracePeriod).Format(time.RFC3339)

	for _, caBundleId := range caBundles {
		updateCaBundleRequest := certificatesmanagement.UpdateCaBundleRequest{
			CaBundleId: common.String(caBundleId),
			UpdateCaBundleDetails: certificatesmanagement.UpdateCaBundleDetails{
				FreeformTags: map[string]string{
					deletionTimeTagKey: deletionTime,
				},
			},
		}

		klog.Infof("marking ca bundle %s with deletionTimestamp %s", caBundleId, deletionTime)
		_, _, err := client.UpdateCaBundle(context.TODO(), updateCaBundleRequest)
		if err != nil {
			klog.Errorf("unable to mark ca bundle %s for future deletion: %s", caBundleId, err.Error())
		}
	}
}

func deleteExpiredCaBundles(client *certificate.CertificatesClient, caBundles []string) {
	for _, caBundleId := range caBundles {
		deleteCaBundleRequest := certificatesmanagement.DeleteCaBundleRequest{
			CaBundleId: common.String(caBundleId),
		}

		klog.Infof("deleting unused ca bundle %s", caBundleId)
		_, err := client.DeleteCaBundle(context.TODO(), deleteCaBundleRequest)
		if err != nil {
			klog.Errorf("unable to delete unused ca bundle %s: %s", caBundleId, err.Error())
		}
	}
}

func getCertificatesToBeMarkedOrCleanedUp(client *certificate.CertificatesClient, compartmentId string) ([]string, []string, error) {
	unmarkedCertificates := []string{}
	certificatesToBeDeleted := []string{}

	certificateListRequest := certificatesmanagement.ListCertificatesRequest{
		CompartmentId:  common.String(compartmentId),
		LifecycleState: certificatesmanagement.ListCertificatesLifecycleStateActive,
		Limit:          common.Int(10),
	}

	for {
		certificates, nextPage, err := client.ListCertificates(context.TODO(), certificateListRequest)
		if err != nil {
			return nil, nil, err
		}

		for _, certificateSummary := range certificates.Items {
			// we are not interested in certificates that don't have our prefix
			if !strings.HasPrefix(*certificateSummary.Name, util.CertificateResourcePrefix) {
				continue
			}

			deletionTimeVal, ok := certificateSummary.FreeformTags[deletionTimeTagKey]
			// if the deletionTimestamp tag is absent and resource-hash tag is present, we may need to mark this for deletion, we check for associations later
			if !ok && certificateSummary.FreeformTags[util.CertificateHashTagKey] != "" {
				unmarkedCertificates = append(unmarkedCertificates, *certificateSummary.Id)
				continue
			} else if !ok {
				continue
			}

			shouldBeDeleted, err := hasDeletionTimePassed(deletionTimeVal)
			// failed to parse the freeform tag appropriately, will mark for deletion again if required
			if err != nil {
				klog.Infof("for certificate %s, deletionTimestamp tag unproccessable, will see if requires marking", *certificateSummary.Name)
				unmarkedCertificates = append(unmarkedCertificates, *certificateSummary.Id)
				continue
			}

			// the deletionTimestamp tag has a time that's in the past, can delete this certificate
			if shouldBeDeleted {
				klog.Infof("unused certificate %s needs to be deleted", *certificateSummary.Name)
				certificatesToBeDeleted = append(certificatesToBeDeleted, *certificateSummary.Id)
			}
		}

		if nextPage == nil {
			break
		}

		certificateListRequest.Page = nextPage
	}

	// we filter out any certificates that are actually being used, all other relevant unmarked certificates will be marked with a deletionTimestamp
	certificatesToBeMarked, err := filterOutCertificateResourcesWithAssociations(client, unmarkedCertificates, compartmentId)
	if err != nil {
		return nil, nil, err
	}

	return certificatesToBeMarked, certificatesToBeDeleted, nil
}

func markCertificatesWithDeletionTime(client *certificate.CertificatesClient, certificates []string, gracePeriod time.Duration) {
	deletionTime := time.Now().UTC().Add(gracePeriod).Format(time.RFC3339)

	for _, certificateId := range certificates {
		updateCertificateRequest := certificatesmanagement.UpdateCertificateRequest{
			CertificateId: common.String(certificateId),
			UpdateCertificateDetails: certificatesmanagement.UpdateCertificateDetails{
				FreeformTags: map[string]string{
					deletionTimeTagKey: deletionTime,
				},
			},
		}

		klog.Infof("marking certificate %s with deletionTimestamp %s", certificateId, deletionTime)
		_, _, err := client.UpdateCertificate(context.TODO(), updateCertificateRequest)
		if err != nil {
			klog.Errorf("unable to mark certificate %s for future deletion: %s", certificateId, err.Error())
		}
	}
}

func deleteExpiredCertificates(client *certificate.CertificatesClient, certificates []string) {
	for _, certificateId := range certificates {
		deleteCertificateRequest := certificatesmanagement.ScheduleCertificateDeletionRequest{
			CertificateId: common.String(certificateId),
			ScheduleCertificateDeletionDetails: certificatesmanagement.ScheduleCertificateDeletionDetails{
				TimeOfDeletion: &common.SDKTime{Time: time.Now().UTC().Add(3 * 24 * time.Hour)},
			},
		}

		klog.Infof("scheduling unused certificate %s for deletion", certificateId)
		err := client.ScheduleCertificateDeletion(context.TODO(), deleteCertificateRequest)
		if err != nil {
			klog.Errorf("unable to delete unused certificate %s: %s", certificateId, err.Error())
		}
	}
}

func hasDeletionTimePassed(deletionTimeString string) (bool, error) {
	deletionTime, err := time.Parse(time.RFC3339, deletionTimeString)
	if err != nil {
		return false, err
	}

	if deletionTime.UTC().Compare(time.Now().UTC()) < 0 {
		return true, nil
	}

	return false, nil
}

func filterOutCertificateResourcesWithAssociations(client *certificate.CertificatesClient, certificateResourceIds []string, compartmentId string) ([]string, error) {
	certificateResourcesWithNoAssociations := []string{}

	for _, certificateResourceId := range certificateResourceIds {
		listAssociationRequest := certificatesmanagement.ListAssociationsRequest{
			CertificatesResourceId: common.String(certificateResourceId),
			CompartmentId:          common.String(compartmentId),
		}

		associationCollection, err := client.ListAssociations(context.TODO(), listAssociationRequest)
		if err != nil {
			return nil, err
		}

		if len(associationCollection.Items) == 0 {
			klog.Infof("certificate resource %s has no associations", certificateResourceId)
			certificateResourcesWithNoAssociations = append(certificateResourcesWithNoAssociations, certificateResourceId)
		}
	}

	return certificateResourcesWithNoAssociations, nil
}
