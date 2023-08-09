package waf

import (
	"context"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/waf"
	"github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type CacheObj struct {
	WAF  *waf.WebAppFirewall
	Age  time.Time
	ETag string
}

type Client struct {
	WafClient client.WafInterface
	Mu        sync.Mutex
	Cache     map[string]*CacheObj
}

func New(wafClient *waf.WafClient) *Client {
	return &Client{
		WafClient: client.NewWafClient(wafClient),
		Cache:     map[string]*CacheObj{},
	}
}

// CreateFirewall Creates the firewall from loadBalancerId, CompartmentId, PolicyId and ingressClassName
func (W Client) CreateFirewall(lbId *string, compartmentID *string, policyId *string, ingressClassName string) (waf.CreateWebAppFirewallResponse, error) {
	req := waf.CreateWebAppFirewallRequest{CreateWebAppFirewallDetails: waf.CreateWebAppFirewallLoadBalancerDetails{
		FreeformTags:           map[string]string{"oci-native-ingress-controller-resource": "firewall"},
		LoadBalancerId:         lbId,
		WebAppFirewallPolicyId: policyId,
		CompartmentId:          compartmentID,
		DisplayName:            common.String(ingressClassName)},
	}
	// Send the request using the service client
	r, err := W.CreateWebAppFirewall(context.Background(), req)
	if err != nil {
		klog.Infof("Error creating firewall for ingressClass %s %s", ingressClassName, err)
	}
	return r, err
}

func (W Client) GetFireWallId(kubeClient kubernetes.Interface, ic *networkingv1.IngressClass, compartmentId *string, lbId *string) (waf.CreateWebAppFirewallResponse, error, error, bool) {
	policyId := util.GetIngressClassWafPolicy(ic)
	fireWallId := util.GetIngressClassFireWallId(ic)
	if policyId == "" {
		if fireWallId != "" {
			// cleanup firewall
			W.DeleteWebAppFirewallWithId(fireWallId)
			util.PatchIngressClassWithAnnotation(kubeClient, ic, util.IngressClassFireWallIdAnnotation, "")
			klog.Infof("Web Firewall cleaned up %s", fireWallId)
		}
		return waf.CreateWebAppFirewallResponse{}, nil, nil, true
	}
	if fireWallId != "" {
		// check policy ocid for the existing firewall == policy in ingressclass
		firewallPolicyId, err := W.GetWebAppFirewallWithId(fireWallId)
		if err == nil && firewallPolicyId == policyId {
			return waf.CreateWebAppFirewallResponse{}, nil, nil, true
		}

	}
	// create firewall
	firewall, err := W.CreateFirewall(lbId, compartmentId, common.String(policyId), ic.Name)
	if err != nil && !apierrors.IsConflict(err) {
		klog.Error("Unable to create web app firewall", err)
		return waf.CreateWebAppFirewallResponse{}, nil, err, true
	}
	return firewall, err, nil, false
}

func (W Client) GetWebAppFirewall(ctx context.Context, request waf.GetWebAppFirewallRequest) (response waf.GetWebAppFirewallResponse, err error) {
	return W.WafClient.GetWebAppFirewall(ctx, request)
}

func (W Client) CreateWebAppFirewall(ctx context.Context, request waf.CreateWebAppFirewallRequest) (response waf.CreateWebAppFirewallResponse, err error) {
	return W.WafClient.CreateWebAppFirewall(ctx, request)
}

func (W Client) GetWebAppFirewallWithId(id string) (string, error) {
	// fetch from cache, if not available then burst
	firewallCache := W.getFromCache(id)

	if firewallCache != nil {
		// Get new waf state if cache value is older than WAFCacheMaxAgeInMinutes, else use from cache
		now := time.Now()
		if now.Sub(firewallCache.Age).Minutes() < util.WAFCacheMaxAgeInMinutes {
			firewall := *firewallCache.WAF
			return *firewall.GetWebAppFirewallPolicyId(), nil
		}
		klog.Infof("Refreshing WAF cache for waf %s ", id)
	}

	resp, err := W.GetWebAppFirewallWithIdBurstCache(id)
	if err != nil {
		return "", err
	}
	return resp, nil
}

func (W Client) setCache(waf waf.WebAppFirewall, etag string) {
	W.Mu.Lock()
	W.Cache[*waf.GetId()] = &CacheObj{&waf, time.Now(), etag}
	W.Mu.Unlock()
}

func (W Client) getFromCache(wafId string) *CacheObj {
	W.Mu.Lock()
	defer W.Mu.Unlock()
	return W.Cache[wafId]
}

func (W Client) removeFromCache(wafId string) *CacheObj {
	W.Mu.Lock()
	defer W.Mu.Unlock()
	return W.Cache[wafId]
}

func (W Client) GetWebAppFirewallWithIdBurstCache(id string) (string, error) {
	req := waf.GetWebAppFirewallRequest{
		WebAppFirewallId: common.String(id),
	}

	// Send the request using the service client
	resp, err := W.GetWebAppFirewall(context.Background(), req)
	if err != nil {
		klog.Errorf("Error fetching web app firewall for %s %s", id, err.Error())
		return "", err
	}
	W.setCache(resp.WebAppFirewall, *resp.Etag)

	return *resp.GetWebAppFirewallPolicyId(), nil
}

func (W Client) DeleteWebAppFirewall(ctx context.Context, request waf.DeleteWebAppFirewallRequest) (response waf.DeleteWebAppFirewallResponse, err error) {
	return W.WafClient.DeleteWebAppFirewall(ctx, request)
}

func (W Client) DeleteWebAppFirewallWithId(id string) {
	req := waf.DeleteWebAppFirewallRequest{
		WebAppFirewallId: common.String(id),
	}
	_, err := W.DeleteWebAppFirewall(context.TODO(), req)
	if err != nil {
		klog.Infof("Error deleting web app firewall for %s %s", id, err.Error())
	}
}
