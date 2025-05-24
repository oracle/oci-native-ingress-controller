package client

import (
	"context"

	"github.com/oracle/oci-go-sdk/v65/loadbalancer"
)

type LoadBalancerInterface interface {
	GetLoadBalancer(ctx context.Context, request loadbalancer.GetLoadBalancerRequest) (loadbalancer.GetLoadBalancerResponse, error)
	CreateLoadBalancer(ctx context.Context, request loadbalancer.CreateLoadBalancerRequest) (loadbalancer.CreateLoadBalancerResponse, error)
	UpdateLoadBalancer(ctx context.Context, request loadbalancer.UpdateLoadBalancerRequest) (response loadbalancer.UpdateLoadBalancerResponse, err error)
	UpdateLoadBalancerShape(ctx context.Context, request loadbalancer.UpdateLoadBalancerShapeRequest) (response loadbalancer.UpdateLoadBalancerShapeResponse, err error)
	UpdateNetworkSecurityGroups(ctx context.Context, request loadbalancer.UpdateNetworkSecurityGroupsRequest) (loadbalancer.UpdateNetworkSecurityGroupsResponse, error)
	DeleteLoadBalancer(ctx context.Context, request loadbalancer.DeleteLoadBalancerRequest) (loadbalancer.DeleteLoadBalancerResponse, error)

	GetWorkRequest(ctx context.Context, request loadbalancer.GetWorkRequestRequest) (loadbalancer.GetWorkRequestResponse, error)

	CreateBackendSet(ctx context.Context, request loadbalancer.CreateBackendSetRequest) (loadbalancer.CreateBackendSetResponse, error)
	UpdateBackendSet(ctx context.Context, request loadbalancer.UpdateBackendSetRequest) (loadbalancer.UpdateBackendSetResponse, error)
	DeleteBackendSet(ctx context.Context, request loadbalancer.DeleteBackendSetRequest) (loadbalancer.DeleteBackendSetResponse, error)
	GetBackendSet(ctx context.Context, request loadbalancer.GetBackendSetRequest) (loadbalancer.GetBackendSetResponse, error) // Added

	GetBackendSetHealth(ctx context.Context, request loadbalancer.GetBackendSetHealthRequest) (loadbalancer.GetBackendSetHealthResponse, error)

	CreateRoutingPolicy(ctx context.Context, request loadbalancer.CreateRoutingPolicyRequest) (loadbalancer.CreateRoutingPolicyResponse, error)
	UpdateRoutingPolicy(ctx context.Context, request loadbalancer.UpdateRoutingPolicyRequest) (loadbalancer.UpdateRoutingPolicyResponse, error)
	DeleteRoutingPolicy(ctx context.Context, request loadbalancer.DeleteRoutingPolicyRequest) (loadbalancer.DeleteRoutingPolicyResponse, error)

	CreateListener(ctx context.Context, request loadbalancer.CreateListenerRequest) (loadbalancer.CreateListenerResponse, error)
	UpdateListener(ctx context.Context, request loadbalancer.UpdateListenerRequest) (loadbalancer.UpdateListenerResponse, error)
	DeleteListener(ctx context.Context, request loadbalancer.DeleteListenerRequest) (loadbalancer.DeleteListenerResponse, error)
}

type LBClient struct {
	lbClient *loadbalancer.LoadBalancerClient
}

func NewLoadBalancerClient(lbClient *loadbalancer.LoadBalancerClient) LBClient {
	return LBClient{
		lbClient: lbClient,
	}
}

func (client LBClient) GetLoadBalancer(ctx context.Context,
	request loadbalancer.GetLoadBalancerRequest) (loadbalancer.GetLoadBalancerResponse, error) {
	return client.lbClient.GetLoadBalancer(ctx, request)
}

func (client LBClient) CreateLoadBalancer(ctx context.Context,
	request loadbalancer.CreateLoadBalancerRequest) (loadbalancer.CreateLoadBalancerResponse, error) {
	return client.lbClient.CreateLoadBalancer(ctx, request)
}

func (client LBClient) UpdateLoadBalancerShape(ctx context.Context, request loadbalancer.UpdateLoadBalancerShapeRequest) (response loadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	return client.lbClient.UpdateLoadBalancerShape(ctx, request)
}

func (client LBClient) UpdateLoadBalancer(ctx context.Context, request loadbalancer.UpdateLoadBalancerRequest) (response loadbalancer.UpdateLoadBalancerResponse, err error) {
	return client.lbClient.UpdateLoadBalancer(ctx, request)
}

func (client LBClient) UpdateNetworkSecurityGroups(ctx context.Context, request loadbalancer.UpdateNetworkSecurityGroupsRequest) (loadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	return client.lbClient.UpdateNetworkSecurityGroups(ctx, request)
}

func (client LBClient) GetBackendSet(ctx context.Context,
	request loadbalancer.GetBackendSetRequest) (loadbalancer.GetBackendSetResponse, error) {
	return client.lbClient.GetBackendSet(ctx, request)
}

func (client LBClient) DeleteLoadBalancer(ctx context.Context,
	request loadbalancer.DeleteLoadBalancerRequest) (loadbalancer.DeleteLoadBalancerResponse, error) {
	return client.lbClient.DeleteLoadBalancer(ctx, request)
}

func (client LBClient) GetWorkRequest(ctx context.Context,
	request loadbalancer.GetWorkRequestRequest) (loadbalancer.GetWorkRequestResponse, error) {
	return client.lbClient.GetWorkRequest(ctx, request)
}

func (client LBClient) CreateBackendSet(ctx context.Context,
	request loadbalancer.CreateBackendSetRequest) (loadbalancer.CreateBackendSetResponse, error) {
	return client.lbClient.CreateBackendSet(ctx, request)
}

func (client LBClient) UpdateBackendSet(ctx context.Context,
	request loadbalancer.UpdateBackendSetRequest) (loadbalancer.UpdateBackendSetResponse, error) {
	return client.lbClient.UpdateBackendSet(ctx, request)
}

func (client LBClient) DeleteBackendSet(ctx context.Context,
	request loadbalancer.DeleteBackendSetRequest) (loadbalancer.DeleteBackendSetResponse, error) {
	return client.lbClient.DeleteBackendSet(ctx, request)
}

func (client LBClient) GetBackendSetHealth(ctx context.Context,
	request loadbalancer.GetBackendSetHealthRequest) (loadbalancer.GetBackendSetHealthResponse, error) {
	return client.lbClient.GetBackendSetHealth(ctx, request)
}

func (client LBClient) CreateRoutingPolicy(ctx context.Context,
	request loadbalancer.CreateRoutingPolicyRequest) (loadbalancer.CreateRoutingPolicyResponse, error) {
	return client.lbClient.CreateRoutingPolicy(ctx, request)
}

func (client LBClient) UpdateRoutingPolicy(ctx context.Context,
	request loadbalancer.UpdateRoutingPolicyRequest) (loadbalancer.UpdateRoutingPolicyResponse, error) {
	return client.lbClient.UpdateRoutingPolicy(ctx, request)
}

func (client LBClient) DeleteRoutingPolicy(ctx context.Context,
	request loadbalancer.DeleteRoutingPolicyRequest) (loadbalancer.DeleteRoutingPolicyResponse, error) {
	return client.lbClient.DeleteRoutingPolicy(ctx, request)
}

func (client LBClient) CreateListener(ctx context.Context,
	request loadbalancer.CreateListenerRequest) (loadbalancer.CreateListenerResponse, error) {
	return client.lbClient.CreateListener(ctx, request)
}

func (client LBClient) UpdateListener(ctx context.Context,
	request loadbalancer.UpdateListenerRequest) (loadbalancer.UpdateListenerResponse, error) {
	return client.lbClient.UpdateListener(ctx, request)
}

func (client LBClient) DeleteListener(ctx context.Context,
	request loadbalancer.DeleteListenerRequest) (loadbalancer.DeleteListenerResponse, error) {
	return client.lbClient.DeleteListener(ctx, request)
}
