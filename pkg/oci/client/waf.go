package client

import (
	"context"

	"github.com/oracle/oci-go-sdk/v65/waf"
)

type WafInterface interface {
	GetWebAppFirewall(ctx context.Context, request waf.GetWebAppFirewallRequest) (response waf.GetWebAppFirewallResponse, err error)
	CreateWebAppFirewall(ctx context.Context, request waf.CreateWebAppFirewallRequest) (response waf.CreateWebAppFirewallResponse, err error)
	DeleteWebAppFirewall(ctx context.Context, request waf.DeleteWebAppFirewallRequest) (response waf.DeleteWebAppFirewallResponse, err error)
}

type WAFClient struct {
	wafClient *waf.WafClient
}

func NewWafClient(wafClient *waf.WafClient) WAFClient {
	return WAFClient{
		wafClient: wafClient,
	}
}

func (W WAFClient) GetWebAppFirewall(ctx context.Context, request waf.GetWebAppFirewallRequest) (response waf.GetWebAppFirewallResponse, err error) {
	return W.wafClient.GetWebAppFirewall(ctx, request)
}

func (W WAFClient) CreateWebAppFirewall(ctx context.Context, request waf.CreateWebAppFirewallRequest) (response waf.CreateWebAppFirewallResponse, err error) {
	return W.wafClient.CreateWebAppFirewall(ctx, request)
}

func (W WAFClient) DeleteWebAppFirewall(ctx context.Context, request waf.DeleteWebAppFirewallRequest) (response waf.DeleteWebAppFirewallResponse, err error) {
	return W.wafClient.DeleteWebAppFirewall(ctx, request)
}
