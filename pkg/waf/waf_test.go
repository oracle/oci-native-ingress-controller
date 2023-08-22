package waf

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	networkingv1 "k8s.io/api/networking/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	"github.com/oracle/oci-go-sdk/v65/waf"
	"github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
)

const policyId = "ocid1.webappfirewallpolicy.oc1.phx.amaaaaaah4gjgpya3siqywzdmre3mv4op3rzpo"

func setupClient() (*fakeclientset.Clientset, *Client, *networkingv1.IngressClassList) {
	client := GetWafClient()

	wafClient := &Client{
		WafClient: client,
		Mu:        sync.Mutex{},
		Cache:     map[string]*CacheObj{},
	}

	k8client := fakeclientset.NewSimpleClientset()
	annotations := map[string]string{"ingressclass.kubernetes.io/is-default-class": fmt.Sprint(false), util.IngressClassWafPolicyAnnotation: policyId}
	ingressClassList := util.GetIngressClassResourceWithAnnotation("ingressclass-withPolicy", annotations, "oci.oraclecloud.com/native-ingress-controller")

	util.UpdateFakeClientCall(k8client, "list", "ingressclasses", ingressClassList)
	util.UpdateFakeClientCall(k8client, "patch", "ingressclasses", &ingressClassList.Items[0])

	return k8client, wafClient, ingressClassList
}

func TestClient_GetFireWallId(t *testing.T) {
	RegisterTestingT(t)
	k8client, wafClient, ingressClassList := setupClient()

	compartmentId := "ocid1.compartment.oc1..aaaaaaaaxaq3szzikh7cb53arlkdgbi4wz4g73qpnuqhdhqckr2d5rvdffya"

	// Only PolicyId set in ingressClass
	wafClient.GetFireWallId(k8client, &ingressClassList.Items[0], common.String(compartmentId), common.String("id"))

	// PolicyId and FireWall Set
	annotations := map[string]string{"ingressclass.kubernetes.io/is-default-class": fmt.Sprint(false), util.IngressClassWafPolicyAnnotation: policyId, util.IngressClassFireWallIdAnnotation: "SetFirewall"}
	ingressClassList = util.GetIngressClassResourceWithAnnotation("ingressclass-withPolicy", annotations, "oci.oraclecloud.com/native-ingress-controller")
	wafClient.GetFireWallId(k8client, &ingressClassList.Items[0], common.String(compartmentId), common.String("id"))

	// Only FireWall Set
	annotations = map[string]string{"ingressclass.kubernetes.io/is-default-class": fmt.Sprint(false), util.IngressClassFireWallIdAnnotation: "SetFirewall"}
	ingressClassList = util.GetIngressClassResourceWithAnnotation("ingressclass-withPolicy", annotations, "oci.oraclecloud.com/native-ingress-controller")
	wafClient.GetFireWallId(k8client, &ingressClassList.Items[0], common.String(compartmentId), common.String("id"))

	// None Set
	ingressClassList = util.GetIngressClassList()
	wafClient.GetFireWallId(k8client, &ingressClassList.Items[0], common.String(compartmentId), common.String("id"))

}

func GetWafClient() client.WafInterface {
	return &MockWafClient{}
}

type MockWafClient struct {
}

func (m MockWafClient) GetWebAppFirewall(ctx context.Context, request waf.GetWebAppFirewallRequest) (response waf.GetWebAppFirewallResponse, err error) {
	return waf.GetWebAppFirewallResponse{
		RawResponse: nil,
		WebAppFirewall: waf.WebAppFirewallLoadBalancer{
			Id:                     common.String("fireWallId"),
			WebAppFirewallPolicyId: common.String(policyId),
		},
		Etag:         common.String("etag"),
		OpcRequestId: nil,
	}, nil
}

func (m MockWafClient) CreateWebAppFirewall(ctx context.Context, request waf.CreateWebAppFirewallRequest) (response waf.CreateWebAppFirewallResponse, err error) {
	return waf.CreateWebAppFirewallResponse{
		RawResponse: nil,
		WebAppFirewall: waf.WebAppFirewallLoadBalancer{
			Id: common.String("fireWallId"),
		},
		Etag:             common.String("etag"),
		OpcWorkRequestId: nil,
		OpcRequestId:     common.String("id"),
	}, nil
}

func (m MockWafClient) DeleteWebAppFirewall(ctx context.Context, request waf.DeleteWebAppFirewallRequest) (response waf.DeleteWebAppFirewallResponse, err error) {
	return waf.DeleteWebAppFirewallResponse{}, nil
}
