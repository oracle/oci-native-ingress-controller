package ingressclass

import (
	"context"
	"fmt"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/events"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-go-sdk/v65/waf"
	"github.com/oracle/oci-native-ingress-controller/pkg/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/exception"

	"github.com/oracle/oci-native-ingress-controller/api/v1beta1"

	lb "github.com/oracle/oci-native-ingress-controller/pkg/loadbalancer"
	ociclient "github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	WAF "github.com/oracle/oci-native-ingress-controller/pkg/waf"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	networkinginformers "k8s.io/client-go/informers/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	"k8s.io/client-go/tools/cache"
)

func TestEnsureLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)

	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestEnsureLoadBalancerWithLbIdSet(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	c := inits(ctx, ingressClassList)

	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestEnsureLoadBalancerWithNotFound(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()

	ingressClassList := util.GetIngressClassListWithLBSet("notfound")
	c := inits(ctx, ingressClassList)

	ic := &ingressClassList.Items[0]
	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), ic)
	Expect(err).Should(BeNil())

}

func TestEnsureLoadBalancerWithNetworkError(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()

	ingressClassList := util.GetIngressClassListWithLBSet("networkerror")
	c := inits(ctx, ingressClassList)

	err := c.ensureLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal("Failure due to network error"))
}

func TestIngressClassAdd(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	queueSize := c.queue.Len()
	c.ingressClassAdd(&ingressClassList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}

func TestIngressUpdate(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	queueSize := c.queue.Len()
	c.ingressClassUpdate(&ingressClassList.Items[0], &ingressClassList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}
func TestIngressClassDelete(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	queueSize := c.queue.Len()
	c.ingressClassDelete(&ingressClassList.Items[0])
	Expect(c.queue.Len()).Should(Equal(queueSize + 1))
}

func TestDeleteIngressClass(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.deleteIngressClass(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestClearLoadBalancerWhenLBFound(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressClassList.Items[0].Annotations[util.IngressClassFireWallIdAnnotation] = "firewallId"
	ingressClassList.Items[0].Annotations[util.IngressClassNetworkSecurityGroupIdsAnnotation] = "nsgId"
	c := inits(ctx, ingressClassList)
	err := c.clearLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestClearLoadBalancerWhenLBNotFound(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("notfound")
	c := inits(ctx, ingressClassList)
	err := c.clearLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestClearLoadBalancerWhenNetworkError(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("networkerror")
	c := inits(ctx, ingressClassList)
	err := c.clearLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).ShouldNot(BeNil())
}

func TestDeleteLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.deleteLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestEnsureFinalizer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.ensureFinalizer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func TestSetupWebApplicationFirewall_WithPolicySet(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := "id"
	compartmentId := "ocid1.compartment.oc1..aaaaaaaaxaq3szzikh7cb53arlkdgbi4wz4g73qpnuqhdhqckr2d5rvdffya"
	annotations := map[string]string{util.IngressClassIsDefault: fmt.Sprint(false), util.IngressClassWafPolicyAnnotation: "ocid1.webappfirewallpolicy.oc1.phx.amaaaaaah4gjgpya3siqywzdmre3mv4op3rzpo"}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-withPolicy", annotations, "oci.oraclecloud.com/native-ingress-controller")
	c := inits(ctx, ingressClassList)
	err := c.setupWebApplicationFirewall(getContextWithClient(c, ctx), &ingressClassList.Items[0], &compartmentId, &id)
	Expect(err).Should(BeNil())
}

func TestSetupWebApplicationFirewall_NoPolicySet(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := "id"
	compartmentId := "ocid1.compartment.oc1..aaaaaaaaxaq3szzikh7cb53arlkdgbi4wz4g73qpnuqhdhqckr2d5rvdffya"

	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	err := c.setupWebApplicationFirewall(getContextWithClient(c, ctx), &ingressClassList.Items[0], &compartmentId, &id)
	Expect(err).Should(BeNil())
}

func TestCheckForIngressClassParameterUpdates(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassListWithLBSet("id")
	c := inits(ctx, ingressClassList)
	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			CompartmentId:    "",
			SubnetId:         "",
			LoadBalancerName: "testecho1-998",
			IsPrivate:        false,
			MinBandwidthMbps: 200,
			MaxBandwidthMbps: 400,
		},
	}
	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &icp)
	Expect(err).Should(BeNil())
}

func TestCheckForIngressClassParameterUpdatesSecurityAttributes(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	ingressClassList.Items[0].Annotations[util.IngressClassSecurityAttributesAnnotation] = `{"Oracle-ZPR":{"MaxEgressCount":{"value":"42","mode":"enforce"}}}`
	lbClient := &MockLoadBalancerClient{}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, &MockPrivateIpClient{})
	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			LoadBalancerName: "testecho1-998",
			MinBandwidthMbps: 200,
			MaxBandwidthMbps: 400,
		},
	}

	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &icp)
	Expect(err).Should(BeNil())
	Expect(lbClient.UpdateLoadBalancerRequests).Should(HaveLen(1))
	Expect(lbClient.UpdateLoadBalancerRequests[0].SecurityAttributes).ShouldNot(BeNil())
	Expect(lbClient.UpdateLoadBalancerRequests[0].SecurityAttributes["Oracle-ZPR"]).Should(HaveKey("MaxEgressCount"))
}

func TestCheckForIngressClassParameterUpdatesMissingSecurityAttributesAnnotationSkipsEmptyReadback(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassListWithLBSet("id")
	delete(ingressClassList.Items[0].Annotations, util.IngressClassSecurityAttributesAnnotation)
	lbResponse := util.SampleLoadBalancerResponse()
	lbResponse.LoadBalancer.DisplayName = common.String("k8s-default-ingress-class")
	lbResponse.LoadBalancer.FreeformTags = map[string]string{}
	lbResponse.LoadBalancer.SecurityAttributes = map[string]map[string]interface{}{}
	lbClient := &MockLoadBalancerClient{GetLoadBalancerResponse: &lbResponse}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, &MockPrivateIpClient{})
	icp := v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			MinBandwidthMbps: 100,
			MaxBandwidthMbps: 400,
		},
	}

	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &icp)
	Expect(err).Should(BeNil())
	Expect(lbClient.UpdateLoadBalancerRequests).Should(BeEmpty())
}

func TestCheckForNetworkSecurityGroupsUpdate(t *testing.T) {
	RegisterTestingT(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingress-class-with-nsg",
		map[string]string{
			util.IngressClassNetworkSecurityGroupIdsAnnotation: "id1,id2,  id3",
			util.IngressClassLoadBalancerIdAnnotation:          "id",
		}, "oci.oraclecloud.com/native-ingress-controller")
	c := inits(ctx, ingressClassList)

	err := c.checkForNetworkSecurityGroupsUpdate(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).To(BeNil())
}

func TestCreateLoadBalancerWithSecurityAttributes(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassIsDefault:                    fmt.Sprint(false),
		util.IngressClassSecurityAttributesAnnotation: `{"Oracle-ZPR":{"MaxEgressCount":{"value":"42","mode":"enforce"}}}`,
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-security-attributes", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbClient := &MockLoadBalancerClient{}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, &MockPrivateIpClient{})

	_, err := c.createLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{})
	Expect(err).Should(BeNil())
	Expect(lbClient.CreateLoadBalancerRequests).Should(HaveLen(1))
	Expect(lbClient.CreateLoadBalancerRequests[0].SecurityAttributes).ShouldNot(BeNil())
	Expect(lbClient.CreateLoadBalancerRequests[0].SecurityAttributes["Oracle-ZPR"]).Should(HaveKey("MaxEgressCount"))
}

func TestCreateLoadBalancer_WithReservedPrivateIPv4(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassIsDefault:                   fmt.Sprint(false),
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.privateip.oc1.iad.testreservedprivateip",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-private-ip", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbClient := &MockLoadBalancerClient{}
	privateIPClient := &MockPrivateIpClient{
		PrivateIp: &ociclient.PrivateIp{
			Id:        common.String("ocid1.privateip.oc1.iad.testreservedprivateip"),
			IpAddress: common.String("10.0.0.10"),
			Lifetime:  ociclient.PrivateIPLifetimeReserved,
		},
	}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, privateIPClient)

	_, err := c.createLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate: true,
		},
	})
	Expect(err).Should(BeNil())
	Expect(lbClient.CreateLoadBalancerRequests).Should(HaveLen(1))
	Expect(lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.IsPrivate).ShouldNot(BeNil())
	Expect(*lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.IsPrivate).Should(BeTrue())
	Expect(lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.ReservedIps).Should(HaveLen(1))
	Expect(*lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.ReservedIps[0].Id).Should(Equal("ocid1.privateip.oc1.iad.testreservedprivateip"))
}

func TestCreateLoadBalancer_RejectsReservedPrivateIPForPublicLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassIsDefault:                   fmt.Sprint(false),
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.privateip.oc1.iad.testreservedprivateip",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-both-ips", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbClient := &MockLoadBalancerClient{}
	privateIPClient := &MockPrivateIpClient{
		PrivateIp: &ociclient.PrivateIp{
			Id:        common.String("ocid1.privateip.oc1.iad.testreservedprivateip"),
			IpAddress: common.String("10.0.0.10"),
			Lifetime:  ociclient.PrivateIPLifetimeReserved,
		},
	}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, privateIPClient)

	_, err := c.createLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate: false,
		},
	})
	Expect(err).ShouldNot(BeNil())
	Expect(err.Error()).Should(ContainSubstring("reserved private IP is only supported for private load balancers"))
	Expect(lbClient.CreateLoadBalancerRequests).Should(BeEmpty())
	Expect(privateIPClient.RequestedIds).Should(BeEmpty())
}

func TestCreateLoadBalancer_WithReservedPublicAndPrivateIPsOnPrivateLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassIsDefault:                   fmt.Sprint(false),
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.privateip.oc1.iad.testreservedprivateip",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-both-ips", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbClient := &MockLoadBalancerClient{}
	privateIPClient := &MockPrivateIpClient{
		PrivateIp: &ociclient.PrivateIp{
			Id:        common.String("ocid1.privateip.oc1.iad.testreservedprivateip"),
			IpAddress: common.String("10.0.0.10"),
			Lifetime:  ociclient.PrivateIPLifetimeReserved,
		},
	}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, privateIPClient)

	_, err := c.createLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate:               true,
			ReservedPublicAddressId: "ocid1.publicip.oc1.iad.testreservedpublicip",
		},
	})
	Expect(err).Should(BeNil())
	Expect(lbClient.CreateLoadBalancerRequests).Should(HaveLen(1))
	Expect(lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.IsPrivate).ShouldNot(BeNil())
	Expect(*lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.IsPrivate).Should(BeTrue())
	Expect(lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.ReservedIps).Should(HaveLen(2))
	Expect(*lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.ReservedIps[0].Id).Should(Equal("ocid1.publicip.oc1.iad.testreservedpublicip"))
	Expect(*lbClient.CreateLoadBalancerRequests[0].CreateLoadBalancerDetails.ReservedIps[1].Id).Should(Equal("ocid1.privateip.oc1.iad.testreservedprivateip"))
}

func TestCreateLoadBalancer_RejectsReservedPrivateIPv6(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassIsDefault:                   fmt.Sprint(false),
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.ipv6.oc1.iad.testreservedipv6",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-ipv6", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbClient := &MockLoadBalancerClient{}
	privateIPClient := &MockPrivateIpClient{}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, privateIPClient)

	_, err := c.createLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate: true,
		},
	})
	Expect(err).ShouldNot(BeNil())
	Expect(err.Error()).Should(ContainSubstring("reserved private IPv6 is not supported"))
	Expect(lbClient.CreateLoadBalancerRequests).Should(BeEmpty())
	Expect(privateIPClient.RequestedIds).Should(BeEmpty())
}

func TestCreateLoadBalancer_RejectsEmptyReservedPrivateIPAnnotation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassIsDefault:                   fmt.Sprint(false),
		util.IngressClassReservedPrivateIpAnnotation: "   ",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-empty-private-ip", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbClient := &MockLoadBalancerClient{}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, &MockPrivateIpClient{})

	_, err := c.createLoadBalancer(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{})
	Expect(err).ShouldNot(BeNil())
	Expect(err.Error()).Should(ContainSubstring("must not be empty"))
	Expect(lbClient.CreateLoadBalancerRequests).Should(BeEmpty())
}

func TestCheckForIngressClassParameterUpdates_RejectsReservedPrivateIPMutation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassLoadBalancerIdAnnotation:    "id",
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.privateip.oc1.iad.updatedprivateip",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-updated-private-ip", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbResponse := util.SampleLoadBalancerResponse()
	lbResponse.LoadBalancer.IpAddresses = []ociloadbalancer.IpAddress{{
		IpAddress:  common.String("10.0.0.10"),
		IsPublic:   common.Bool(false),
		ReservedIp: &ociloadbalancer.ReservedIp{Id: common.String("ocid1.privateip.oc1.iad.existingprivateip")},
	}}
	lbClient := &MockLoadBalancerClient{GetLoadBalancerResponse: &lbResponse}
	privateIPClient := &MockPrivateIpClient{
		PrivateIp: &ociclient.PrivateIp{
			Id:        common.String("ocid1.privateip.oc1.iad.updatedprivateip"),
			IpAddress: common.String("10.0.0.11"),
			Lifetime:  ociclient.PrivateIPLifetimeReserved,
		},
	}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, privateIPClient)

	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate: true,
		},
	})
	Expect(err).ShouldNot(BeNil())
	Expect(err.Error()).Should(ContainSubstring("reserved private IP setting is immutable"))
	Expect(privateIPClient.RequestedIds).Should(BeEmpty())
}

func TestCheckForIngressClassParameterUpdates_RejectsReservedPrivateIPForPublicLoadBalancer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassLoadBalancerIdAnnotation:    "id",
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.privateip.oc1.iad.existingprivateip",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-public-lb-private-ip", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbResponse := util.SampleLoadBalancerResponse()
	lbResponse.LoadBalancer.IpAddresses = []ociloadbalancer.IpAddress{{
		IpAddress:  common.String("10.0.0.10"),
		IsPublic:   common.Bool(false),
		ReservedIp: &ociloadbalancer.ReservedIp{Id: common.String("ocid1.privateip.oc1.iad.existingprivateip")},
	}}
	privateIPClient := &MockPrivateIpClient{Err: fmt.Errorf("unexpected private IP lookup")}
	c := initsWithCustomClients(ctx, ingressClassList, &MockLoadBalancerClient{GetLoadBalancerResponse: &lbResponse}, privateIPClient)

	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate: false,
		},
	})
	Expect(err).ShouldNot(BeNil())
	Expect(err.Error()).Should(ContainSubstring("reserved private IP is only supported for private load balancers"))
	Expect(privateIPClient.RequestedIds).Should(BeEmpty())
}

func TestCheckForIngressClassParameterUpdates_RejectsReservedPublicIPMutation(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassLoadBalancerIdAnnotation: "id",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-updated-public-ip", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbResponse := util.SampleLoadBalancerResponse()
	lbResponse.LoadBalancer.IpAddresses = []ociloadbalancer.IpAddress{{
		IpAddress:  common.String("129.146.1.10"),
		IsPublic:   common.Bool(true),
		ReservedIp: &ociloadbalancer.ReservedIp{Id: common.String("ocid1.publicip.oc1.iad.existingpublicip")},
	}}
	lbClient := &MockLoadBalancerClient{GetLoadBalancerResponse: &lbResponse}
	c := initsWithCustomClients(ctx, ingressClassList, lbClient, &MockPrivateIpClient{})

	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			ReservedPublicAddressId: "ocid1.publicip.oc1.iad.updatedpublicip",
		},
	})
	Expect(err).ShouldNot(BeNil())
	Expect(err.Error()).Should(ContainSubstring("reserved public IP setting is immutable"))
}

func TestCheckForIngressClassParameterUpdates_DoesNotLookupReservedPrivateIPWhenUnchanged(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	annotations := map[string]string{
		util.IngressClassLoadBalancerIdAnnotation:    "id",
		util.IngressClassReservedPrivateIpAnnotation: "ocid1.privateip.oc1.iad.existingprivateip",
	}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-with-existing-private-ip", annotations, "oci.oraclecloud.com/native-ingress-controller")
	lbResponse := util.SampleLoadBalancerResponse()
	lbResponse.LoadBalancer.IpAddresses = []ociloadbalancer.IpAddress{{
		IpAddress:  common.String("10.0.0.10"),
		IsPublic:   common.Bool(false),
		ReservedIp: &ociloadbalancer.ReservedIp{Id: common.String("ocid1.privateip.oc1.iad.existingprivateip")},
	}}
	privateIPClient := &MockPrivateIpClient{Err: fmt.Errorf("unexpected private IP lookup")}
	c := initsWithCustomClients(ctx, ingressClassList, &MockLoadBalancerClient{GetLoadBalancerResponse: &lbResponse}, privateIPClient)

	err := c.checkForIngressClassParameterUpdates(getContextWithClient(c, ctx), &ingressClassList.Items[0], &v1beta1.IngressClassParameters{
		Spec: v1beta1.IngressClassParametersSpec{
			IsPrivate:        true,
			LoadBalancerName: "testecho1-999",
			MinBandwidthMbps: 100,
			MaxBandwidthMbps: 400,
		},
	})
	Expect(err).Should(BeNil())
	Expect(privateIPClient.RequestedIds).Should(BeEmpty())
}

func TestDeleteFinalizer(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingressClassList := util.GetIngressClassList()
	c := inits(ctx, ingressClassList)
	var finalizers []string
	finalizer := "oci.oraclecloud.com/ingress-controller-protection"
	finalizers = append(finalizers, finalizer)
	ingressClass := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "name",
			Annotations: map[string]string{util.IngressClassIsDefault: fmt.Sprint("isDefault")},
			Finalizers:  finalizers,
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: "controller",
		},
	}
	err := c.deleteFinalizer(getContextWithClient(c, ctx), ingressClass) // with finalizer
	Expect(err).Should(BeNil())
	err = c.deleteFinalizer(getContextWithClient(c, ctx), &ingressClassList.Items[0])
	Expect(err).Should(BeNil())
}

func getContextWithClient(c *Controller, ctx context.Context) context.Context {
	wc, err := c.client.GetClient(&MockConfigGetter{})
	Expect(err).To(BeNil())
	ctx = context.WithValue(ctx, util.WrapperClient, wc)
	return ctx
}

func inits(ctx context.Context, ingressClassList *networkingv1.IngressClassList) *Controller {
	return initsWithCustomClients(ctx, ingressClassList, &MockLoadBalancerClient{}, &MockPrivateIpClient{})
}

func initsWithCustomClients(ctx context.Context, ingressClassList *networkingv1.IngressClassList, lbClient ociclient.LoadBalancerInterface, privateIPClient ociclient.PrivateIpInterface) *Controller {
	wafClient := getWafClient()

	loadBalancerClient := &lb.LoadBalancerClient{
		LbClient: lbClient,
		Mu:       sync.Mutex{},
		Cache:    map[string]*lb.LbCacheObj{},
	}

	firewallClient := &WAF.Client{
		WafClient: wafClient,
		Mu:        sync.Mutex{},
		Cache:     map[string]*WAF.CacheObj{},
	}

	ingressClassInformer, saInformer, k8client := setUp(ctx, ingressClassList)
	wrapperClient := client.NewWrapperClient(k8client, firewallClient, loadBalancerClient, privateIPClient, nil, nil)
	mockClient := &client.ClientProvider{
		K8sClient:           k8client,
		DefaultConfigGetter: &MockConfigGetter{},
		Cache:               NewMockCacheStore(wrapperClient),
	}
	fakeRecorder := events.NewFakeRecorder(10)
	c := NewController("", "", "oci.oraclecloud.com/native-ingress-controller", ingressClassInformer, saInformer, mockClient, nil, fakeRecorder)
	return c
}

func setUp(ctx context.Context, ingressClassList *networkingv1.IngressClassList) (networkinginformers.IngressClassInformer, coreinformers.ServiceAccountInformer, *fakeclientset.Clientset) {
	objects := make([]runtime.Object, 0, len(ingressClassList.Items))
	hasDeleteFinalizerTestClass := false
	for i := range ingressClassList.Items {
		if ingressClassList.Items[i].Name == "name" {
			hasDeleteFinalizerTestClass = true
		}
		objects = append(objects, ingressClassList.Items[i].DeepCopy())
	}
	if !hasDeleteFinalizerTestClass {
		objects = append(objects, (&networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "name"}}).DeepCopy())
	}
	fakeClient := fakeclientset.NewSimpleClientset(objects...)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ingressClassInformer := informerFactory.Networking().V1().IngressClasses()
	ingressClassInformer.Lister()

	saInformer := informerFactory.Core().V1().ServiceAccounts()

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), ingressClassInformer.Informer().HasSynced)

	return ingressClassInformer, saInformer, fakeClient
}

func getWafClient() ociclient.WafInterface {
	return &MockWafClient{}
}

type MockWafClient struct {
}

func (m MockWafClient) GetWebAppFirewall(ctx context.Context, request waf.GetWebAppFirewallRequest) (waf.GetWebAppFirewallResponse, error) {
	return waf.GetWebAppFirewallResponse{}, nil
}

func (m MockWafClient) CreateWebAppFirewall(ctx context.Context, request waf.CreateWebAppFirewallRequest) (waf.CreateWebAppFirewallResponse, error) {

	return waf.CreateWebAppFirewallResponse{
		RawResponse: nil,
		WebAppFirewall: waf.WebAppFirewallLoadBalancer{
			Id: common.String("fireWallId"),
		},
		OpcRequestId: common.String("id"),
	}, nil
}

func (m MockWafClient) DeleteWebAppFirewall(ctx context.Context, request waf.DeleteWebAppFirewallRequest) (waf.DeleteWebAppFirewallResponse, error) {
	return waf.DeleteWebAppFirewallResponse{}, nil
}

type MockLoadBalancerClient struct {
	CreateLoadBalancerRequests []ociloadbalancer.CreateLoadBalancerRequest
	UpdateLoadBalancerRequests []ociloadbalancer.UpdateLoadBalancerRequest
	GetLoadBalancerResponse    *ociloadbalancer.GetLoadBalancerResponse
}

func (m *MockLoadBalancerClient) GetLoadBalancer(ctx context.Context, request ociloadbalancer.GetLoadBalancerRequest) (ociloadbalancer.GetLoadBalancerResponse, error) {
	if *request.LoadBalancerId == "networkerror" {
		return ociloadbalancer.GetLoadBalancerResponse{}, NetworkError{}
	}
	if *request.LoadBalancerId == "notfound" {
		return ociloadbalancer.GetLoadBalancerResponse{}, &exception.NotFoundServiceError{}
	}
	if m.GetLoadBalancerResponse != nil {
		return *m.GetLoadBalancerResponse, nil
	}

	res := util.SampleLoadBalancerResponse()
	return res, nil
}

type NetworkError struct {
}

func (n NetworkError) Error() string {
	return "Failure due to network error"
}

func (m *MockLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerRequest) (response ociloadbalancer.UpdateLoadBalancerResponse, err error) {
	m.UpdateLoadBalancerRequests = append(m.UpdateLoadBalancerRequests, request)
	return ociloadbalancer.UpdateLoadBalancerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m *MockLoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, request ociloadbalancer.UpdateLoadBalancerShapeRequest) (response ociloadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return ociloadbalancer.UpdateLoadBalancerShapeResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m *MockLoadBalancerClient) UpdateNetworkSecurityGroups(ctx context.Context,
	request ociloadbalancer.UpdateNetworkSecurityGroupsRequest) (ociloadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	return ociloadbalancer.UpdateNetworkSecurityGroupsResponse{
		RawResponse:      nil,
		OpcWorkRequestId: common.String("id"),
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m *MockLoadBalancerClient) CreateLoadBalancer(ctx context.Context, request ociloadbalancer.CreateLoadBalancerRequest) (ociloadbalancer.CreateLoadBalancerResponse, error) {
	m.CreateLoadBalancerRequests = append(m.CreateLoadBalancerRequests, request)
	id := "id"
	return ociloadbalancer.CreateLoadBalancerResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &id,
		OpcRequestId:     &id,
	}, nil
}

func (m *MockLoadBalancerClient) DeleteLoadBalancer(ctx context.Context, request ociloadbalancer.DeleteLoadBalancerRequest) (ociloadbalancer.DeleteLoadBalancerResponse, error) {
	return ociloadbalancer.DeleteLoadBalancerResponse{
		OpcRequestId:     common.String("OpcRequestId"),
		OpcWorkRequestId: common.String("OpcWorkRequestId"),
	}, nil
}

func (m *MockLoadBalancerClient) GetWorkRequest(ctx context.Context, request ociloadbalancer.GetWorkRequestRequest) (ociloadbalancer.GetWorkRequestResponse, error) {
	id := "id"
	requestId := "opcrequestid"
	return ociloadbalancer.GetWorkRequestResponse{
		RawResponse: nil,
		WorkRequest: ociloadbalancer.WorkRequest{
			Id:             &id,
			LoadBalancerId: &id,
			Type:           nil,
			LifecycleState: ociloadbalancer.WorkRequestLifecycleStateSucceeded,
		},
		OpcRequestId: &requestId,
	}, nil
}

func (m *MockLoadBalancerClient) CreateBackendSet(ctx context.Context, request ociloadbalancer.CreateBackendSetRequest) (ociloadbalancer.CreateBackendSetResponse, error) {
	return ociloadbalancer.CreateBackendSetResponse{}, nil
}

func (m *MockLoadBalancerClient) UpdateBackendSet(ctx context.Context, request ociloadbalancer.UpdateBackendSetRequest) (ociloadbalancer.UpdateBackendSetResponse, error) {
	reqId := "opcrequestid"
	res := ociloadbalancer.UpdateBackendSetResponse{
		RawResponse:      nil,
		OpcWorkRequestId: &reqId,
		OpcRequestId:     &reqId,
	}
	return res, nil
}

func (m *MockLoadBalancerClient) DeleteBackendSet(ctx context.Context, request ociloadbalancer.DeleteBackendSetRequest) (ociloadbalancer.DeleteBackendSetResponse, error) {
	return ociloadbalancer.DeleteBackendSetResponse{}, nil
}

func (m *MockLoadBalancerClient) GetBackendSetHealth(ctx context.Context, request ociloadbalancer.GetBackendSetHealthRequest) (ociloadbalancer.GetBackendSetHealthResponse, error) {
	backendCount := 1
	return ociloadbalancer.GetBackendSetHealthResponse{
		RawResponse: nil,
		BackendSetHealth: ociloadbalancer.BackendSetHealth{
			Status:                    ociloadbalancer.BackendSetHealthStatusOk,
			WarningStateBackendNames:  nil,
			CriticalStateBackendNames: nil,
			UnknownStateBackendNames:  nil,
			TotalBackendCount:         &backendCount,
		},
		OpcRequestId: nil,
		ETag:         nil,
	}, nil
}

func (m *MockLoadBalancerClient) CreateRoutingPolicy(ctx context.Context, request ociloadbalancer.CreateRoutingPolicyRequest) (ociloadbalancer.CreateRoutingPolicyResponse, error) {
	return ociloadbalancer.CreateRoutingPolicyResponse{}, nil
}

func (m *MockLoadBalancerClient) UpdateRoutingPolicy(ctx context.Context, request ociloadbalancer.UpdateRoutingPolicyRequest) (ociloadbalancer.UpdateRoutingPolicyResponse, error) {
	return ociloadbalancer.UpdateRoutingPolicyResponse{}, nil
}

func (m *MockLoadBalancerClient) DeleteRoutingPolicy(ctx context.Context, request ociloadbalancer.DeleteRoutingPolicyRequest) (ociloadbalancer.DeleteRoutingPolicyResponse, error) {
	return ociloadbalancer.DeleteRoutingPolicyResponse{}, nil
}

func (m *MockLoadBalancerClient) CreateListener(ctx context.Context, request ociloadbalancer.CreateListenerRequest) (ociloadbalancer.CreateListenerResponse, error) {
	return ociloadbalancer.CreateListenerResponse{}, nil
}

func (m *MockLoadBalancerClient) UpdateListener(ctx context.Context, request ociloadbalancer.UpdateListenerRequest) (ociloadbalancer.UpdateListenerResponse, error) {
	return ociloadbalancer.UpdateListenerResponse{}, nil
}

func (m *MockLoadBalancerClient) DeleteListener(ctx context.Context, request ociloadbalancer.DeleteListenerRequest) (ociloadbalancer.DeleteListenerResponse, error) {
	return ociloadbalancer.DeleteListenerResponse{}, nil
}

type MockPrivateIpClient struct {
	PrivateIp    *ociclient.PrivateIp
	Err          error
	RequestedIds []string
}

func (m *MockPrivateIpClient) GetPrivateIp(ctx context.Context, request ociclient.GetPrivateIpRequest) (ociclient.GetPrivateIpResponse, error) {
	privateIpID := ""
	if request.PrivateIpId != nil {
		privateIpID = *request.PrivateIpId
	}
	m.RequestedIds = append(m.RequestedIds, privateIpID)
	if m.Err != nil {
		return ociclient.GetPrivateIpResponse{}, m.Err
	}
	if m.PrivateIp == nil {
		return ociclient.GetPrivateIpResponse{}, fmt.Errorf("private IP %s not found", privateIpID)
	}

	return ociclient.GetPrivateIpResponse{
		PrivateIp: core.PrivateIp(*m.PrivateIp),
	}, nil
}

// MockConfigGetter is a mock implementation of the ConfigGetter interface for testing purposes.
type MockConfigGetter struct {
	ConfigurationProvider common.ConfigurationProvider
	Key                   string
	Error                 error
}

// NewMockConfigGetter creates a new instance of MockConfigGetter.
func NewMockConfigGetter(configurationProvider common.ConfigurationProvider, key string, err error) *MockConfigGetter {
	return &MockConfigGetter{
		ConfigurationProvider: configurationProvider,
		Key:                   key,
		Error:                 err,
	}
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

func NewMockCacheStore(client *client.WrapperClient) *MockCacheStore {
	return &MockCacheStore{
		client: client,
	}
}

func (m *MockCacheStore) GetByKey(key string) (item interface{}, exists bool, err error) {
	return m.client, true, nil
}
