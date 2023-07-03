package certificate

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	. "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
)

func setup() (CertificateInterface, CertificateManagementInterface) {
	certClient := GetCertClient()
	certManageClient := GetCertManageClient()
	return certClient, certManageClient
}

func TestNew(t *testing.T) {
	RegisterTestingT(t)
	certClient, certManageClient := setup()

	client := New(certManageClient, certClient)
	Expect(client).Should(Not(BeNil()))
}

func TestScheduleCertificateDeletion(t *testing.T) {
	RegisterTestingT(t)
	certClient, certManageClient := setup()
	id := "id"
	client := New(certManageClient, certClient)
	request := certificatesmanagement.ScheduleCertificateDeletionRequest{
		CertificateId: &id,
	}
	err := client.ScheduleCertificateDeletion(context.TODO(), request)
	Expect(err).Should(BeNil())

	id = "error"
	request = certificatesmanagement.ScheduleCertificateDeletionRequest{
		CertificateId:                      &id,
		ScheduleCertificateDeletionDetails: certificatesmanagement.ScheduleCertificateDeletionDetails{},
		OpcRequestId:                       nil,
		IfMatch:                            nil,
		RequestMetadata:                    common.RequestMetadata{},
	}
	err = client.ScheduleCertificateDeletion(context.TODO(), request)
	Expect(err).Should(Not(BeNil()))
}

func TestDeleteCaBundle(t *testing.T) {
	RegisterTestingT(t)
	certClient, certManageClient := setup()
	id := "id"
	client := New(certManageClient, certClient)
	request := getDeleteCaBundleRequest(id)
	res, err := client.DeleteCaBundle(context.TODO(), request)

	Expect(err).Should(BeNil())
	Expect(res.Status).Should(Equal("200"))

	request = getDeleteCaBundleRequest("error")
	res, err = client.DeleteCaBundle(context.TODO(), request)
	Expect(err).Should(Not(BeNil()))
}

func getDeleteCaBundleRequest(id string) certificatesmanagement.DeleteCaBundleRequest {
	request := certificatesmanagement.DeleteCaBundleRequest{
		CaBundleId:      &id,
		OpcRequestId:    &id,
		IfMatch:         nil,
		RequestMetadata: common.RequestMetadata{},
	}
	return request
}
