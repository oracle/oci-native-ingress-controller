/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package loadbalancer

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/loadbalancer"
	"github.com/oracle/oci-native-ingress-controller/pkg/oci/client"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

type LbCacheObj struct {
	LB   *loadbalancer.LoadBalancer
	Age  time.Time
	ETag string
}

type LoadBalancerClient struct {
	LbClient client.LoadBalancerInterface

	Mu    sync.Mutex
	Cache map[string]*LbCacheObj
}

func New(lbClient *loadbalancer.LoadBalancerClient) *LoadBalancerClient {
	return &LoadBalancerClient{
		LbClient: client.NewLoadBalancerClient(lbClient),
		Cache:    map[string]*LbCacheObj{},
	}
}

func (lbc *LoadBalancerClient) setCache(lb *loadbalancer.LoadBalancer, etag string) {
	lbc.Mu.Lock()
	lbc.Cache[*lb.Id] = &LbCacheObj{lb, time.Now(), etag}
	lbc.Mu.Unlock()
}

func (lbc *LoadBalancerClient) getFromCache(lbID string) *LbCacheObj {
	lbc.Mu.Lock()
	defer lbc.Mu.Unlock()
	return lbc.Cache[lbID]
}

func (lbc *LoadBalancerClient) removeFromCache(lbID string) *LbCacheObj {
	lbc.Mu.Lock()
	defer lbc.Mu.Unlock()
	return lbc.Cache[lbID]
}

func (lbc *LoadBalancerClient) getLoadBalancerBustCache(ctx context.Context, lbID string) (*loadbalancer.LoadBalancer, string, error) {
	resp, err := lbc.LbClient.GetLoadBalancer(ctx, loadbalancer.GetLoadBalancerRequest{
		LoadBalancerId: common.String(lbID),
	})
	if err != nil {
		return nil, "", err
	}

	lbc.setCache(&resp.LoadBalancer, *resp.ETag)
	return &resp.LoadBalancer, *resp.ETag, nil
}

func (lbc *LoadBalancerClient) GetBackendSetHealth(ctx context.Context, lbID string, backendSetName string) (*loadbalancer.BackendSetHealth, error) {
	resp, err := lbc.LbClient.GetBackendSetHealth(ctx, loadbalancer.GetBackendSetHealthRequest{
		LoadBalancerId: common.String(lbID),
		BackendSetName: common.String(backendSetName),
	})
	if err != nil {
		return nil, err
	}

	return &resp.BackendSetHealth, nil
}

func (lbc *LoadBalancerClient) UpdateNetworkSecurityGroups(ctx context.Context, lbId string, nsgIds []string) (loadbalancer.UpdateNetworkSecurityGroupsResponse, error) {
	_, etag, err := lbc.GetLoadBalancer(ctx, lbId)
	if err != nil {
		return loadbalancer.UpdateNetworkSecurityGroupsResponse{}, err
	}

	req := loadbalancer.UpdateNetworkSecurityGroupsRequest{
		LoadBalancerId: common.String(lbId),
		IfMatch:        common.String(etag),
		UpdateNetworkSecurityGroupsDetails: loadbalancer.UpdateNetworkSecurityGroupsDetails{
			NetworkSecurityGroupIds: nsgIds,
		},
	}

	klog.Infof("Update LB NSG IDs request: %s", util.PrettyPrint(req))

	resp, err := lbc.LbClient.UpdateNetworkSecurityGroups(ctx, req)
	if err != nil {
		return resp, err
	}

	lbID, err := lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	if err != nil {
		return resp, err
	}

	_, _, err = lbc.getLoadBalancerBustCache(ctx, lbID)
	return resp, err
}

func (lbc *LoadBalancerClient) UpdateLoadBalancerShape(ctx context.Context, req loadbalancer.UpdateLoadBalancerShapeRequest) (response loadbalancer.UpdateLoadBalancerShapeResponse, err error) {
	resp, err := lbc.LbClient.UpdateLoadBalancerShape(ctx, req)
	if err != nil {
		return resp, err
	}

	lbID, err := lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	if err != nil {
		return resp, err
	}

	_, _, err = lbc.getLoadBalancerBustCache(ctx, lbID)
	return resp, err
}

func (lbc *LoadBalancerClient) UpdateLoadBalancer(ctx context.Context, lbId string, displayName string, definedTags map[string]map[string]interface{},
	freeformTags map[string]string) (*loadbalancer.LoadBalancer, error) {
	_, etag, err := lbc.GetLoadBalancer(ctx, lbId)
	if err != nil {
		return nil, err
	}

	req := loadbalancer.UpdateLoadBalancerRequest{
		LoadBalancerId: common.String(lbId),
		IfMatch:        common.String(etag),
		UpdateLoadBalancerDetails: loadbalancer.UpdateLoadBalancerDetails{
			DisplayName:  common.String(displayName),
			DefinedTags:  definedTags,
			FreeformTags: freeformTags,
		},
	}

	klog.Infof("Update lb details request: %s", util.PrettyPrint(req))
	resp, err := lbc.LbClient.UpdateLoadBalancer(ctx, req)
	if err != nil {
		return nil, err
	}

	lbID, err := lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	if err != nil {
		return nil, err
	}

	lb, _, err := lbc.getLoadBalancerBustCache(ctx, lbID)
	return lb, err
}

func (lbc *LoadBalancerClient) CreateLoadBalancer(ctx context.Context, req loadbalancer.CreateLoadBalancerRequest) (*loadbalancer.LoadBalancer, error) {
	resp, err := lbc.LbClient.CreateLoadBalancer(ctx, req)
	if err != nil {
		return nil, err
	}

	lbID, err := lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	if err != nil {
		return nil, err
	}

	lb, _, err := lbc.getLoadBalancerBustCache(ctx, lbID)
	return lb, err
}

func (lbc *LoadBalancerClient) GetLoadBalancer(ctx context.Context, lbID string) (*loadbalancer.LoadBalancer, string, error) {
	lbCache := lbc.getFromCache(lbID)

	if lbCache != nil {
		// Get new load balancer state if cache value is older than LBCacheMaxAgeInMinutes, else use from cache
		now := time.Now()
		if now.Sub(lbCache.Age).Minutes() < util.LBCacheMaxAgeInMinutes {
			return lbCache.LB, lbCache.ETag, nil
		}
		klog.Infof("Refreshing LB cache for lb %s ", lbID)
	}

	return lbc.getLoadBalancerBustCache(ctx, lbID)
}

func (lbc *LoadBalancerClient) DeleteLoadBalancer(ctx context.Context, lbID string) error {

	deleteLoadBalancerRequest := loadbalancer.DeleteLoadBalancerRequest{
		LoadBalancerId: common.String(lbID),
	}

	klog.Infof("Deleting load balancer with request %s", util.PrettyPrint(deleteLoadBalancerRequest))
	resp, err := lbc.LbClient.DeleteLoadBalancer(ctx, deleteLoadBalancerRequest)
	svcErr, ok := common.IsServiceError(err)
	if ok && (svcErr.GetHTTPStatusCode() == 409 || svcErr.GetHTTPStatusCode() == 404) {
		lbc.removeFromCache(lbID)
		// already deleting or deleted so nothing to do.
		klog.Infof("Delete load balancer operation returned code %d for load balancer %s, it may be already deleted.", svcErr.GetHTTPStatusCode(), lbID)
		return nil
	}
	if err != nil {
		return err
	}

	lbc.removeFromCache(lbID)

	klog.Infof("Delete load balancer response: lb id: %s, work request id: %s, opc request id: %s.", lbID, *resp.OpcWorkRequestId, *resp.OpcRequestId)

	return err
}

func (lbc *LoadBalancerClient) createRoutingPolicy(
	ctx context.Context,
	lbID string,
	policyName string,
	rules []loadbalancer.RoutingRule) error {

	lb, _, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	_, ok := lb.RoutingPolicies[policyName]
	if ok {
		// already created so nothing to do.
		klog.Infof("Routing policy %s already present in load balancer %s. Skipping creation..", policyName, lbID)
		return nil
	}

	createPolicyRequest := loadbalancer.CreateRoutingPolicyRequest{
		LoadBalancerId: lb.Id,
		CreateRoutingPolicyDetails: loadbalancer.CreateRoutingPolicyDetails{
			Name:                     common.String(policyName),
			ConditionLanguageVersion: loadbalancer.CreateRoutingPolicyDetailsConditionLanguageVersionV1,
			Rules:                    rules,
		},
	}

	klog.Infof("Creating routing policy with request: %s", util.PrettyPrint(createPolicyRequest))
	resp, err := lbc.LbClient.CreateRoutingPolicy(ctx, createPolicyRequest)

	if util.IsServiceError(err, 409) {
		klog.Infof("Create routing policy operation returned code %d for load balancer %s. Routing policy %s may be already present.", 409, lbID, policyName)
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Create routing policy response: name: %s, work request id: %s, opc request id: %s.", policyName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) DeleteRoutingPolicy(
	ctx context.Context,
	lbID string,
	policyName string) error {

	deleteRoutingPolicyRequest := loadbalancer.DeleteRoutingPolicyRequest{
		LoadBalancerId:    &lbID,
		RoutingPolicyName: &policyName,
	}

	klog.Infof("Delete routing policy with request %s ", util.PrettyPrint(deleteRoutingPolicyRequest))
	resp, err := lbc.LbClient.DeleteRoutingPolicy(ctx, deleteRoutingPolicyRequest)

	if util.IsServiceError(err, 404) {
		klog.Infof("Delete routing policy operation returned code %d for load balancer %s. Routing policy %s may be already deleted.", 404, lbID, policyName)
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Delete routing policy response: name: %s, work request id: %s, opc request id: %s.", policyName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) DeleteBackendSet(ctx context.Context, lbID string, backendSetName string) error {

	lb, _, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	_, ok := lb.BackendSets[backendSetName]
	if !ok {
		// nothing to delete since it already doesn't exist.
		klog.Infof("Backend set %s not found in load balancer %s. Skipping deletion..", backendSetName, lbID)
		return nil
	}

	backendSetDeleteRequest := loadbalancer.DeleteBackendSetRequest{
		LoadBalancerId: lb.Id,
		BackendSetName: common.String(backendSetName),
	}

	klog.Infof("Deleting backend set with request %s", util.PrettyPrint(backendSetDeleteRequest))
	resp, err := lbc.LbClient.DeleteBackendSet(ctx, backendSetDeleteRequest)
	if util.IsServiceError(err, 404) {
		// it was already deleted so nothing to do.
		klog.Infof("Delete backend set operation returned code %d for load balancer %s. Backend set %s may be already deleted.", 404, lbID, backendSetName)
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Delete backend set response: name: %s, work request id: %s, opc request id: %s.", backendSetName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) DeleteListener(ctx context.Context, lbID string, listenerName string) error {

	lb, _, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	_, ok := lb.Listeners[listenerName]
	if !ok {
		// nothing to delete since it already doesn't exist.
		klog.Infof("Listener %s not found in load balancer %s. Skipping deletion..", listenerName, lbID)
		return nil
	}

	deleteListenerRequest := loadbalancer.DeleteListenerRequest{
		LoadBalancerId: lb.Id,
		ListenerName:   common.String(listenerName),
	}

	klog.Infof("Deleting listener with request %s", util.PrettyPrint(deleteListenerRequest))
	resp, err := lbc.LbClient.DeleteListener(ctx, deleteListenerRequest)
	if util.IsServiceError(err, 404) {
		// it was already deleted so nothing to do.
		klog.Infof("Delete listener operation returned code %d for load balancer %s. Listener %s may be already deleted.", 404, lbID, listenerName)
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Delete listener response: name: %s, work request id: %s, opc request id: %s.", listenerName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) CreateBackendSet(
	ctx context.Context,
	lbID string,
	backendSetName string,
	policy string,
	healthChecker *loadbalancer.HealthCheckerDetails,
	sslConfig *loadbalancer.SslConfigurationDetails) error {

	lb, _, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	_, ok := lb.BackendSets[backendSetName]
	if ok {
		klog.Infof("Backend set %s already present in load balancer %s. Skipping creation..", backendSetName, lbID)
		return nil
	}

	createBackendSetRequest := loadbalancer.CreateBackendSetRequest{
		LoadBalancerId: lb.Id,
		CreateBackendSetDetails: loadbalancer.CreateBackendSetDetails{
			Name:             common.String(backendSetName),
			Policy:           common.String(policy),
			HealthChecker:    healthChecker,
			SslConfiguration: sslConfig,
		},
	}

	klog.Infof("Creating backend set with request: %s", util.PrettyPrint(createBackendSetRequest))
	resp, err := lbc.LbClient.CreateBackendSet(ctx, createBackendSetRequest)

	if util.IsServiceError(err, 409) {
		klog.Infof("Create backend set operation returned code %d for load balancer %s. Backend set %s may be already present.", 409, lbID, backendSetName)
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Create backend set response: name: %s, work request id: %s, opc request id: %s.", backendSetName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) EnsureRoutingPolicy(
	ctx context.Context,
	lbID string,
	listenerName string,
	rules []loadbalancer.RoutingRule) error {

	lb, _, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	found := false
	for name := range lb.RoutingPolicies {
		if name == listenerName {
			found = true
			break
		}
	}

	if !found {
		err = lbc.createRoutingPolicy(ctx, lbID, listenerName, rules)
		if err != nil {
			return err
		}
	}

	err = lbc.setRoutingPolicyOnListener(ctx, lbID, listenerName)
	if err != nil {
		return err
	}

	return lbc.updateRoutingPolicyRules(ctx, lbID, listenerName, rules)
}

func (lbc *LoadBalancerClient) updateRoutingPolicyRules(ctx context.Context, lbID string, policyName string, rules []loadbalancer.RoutingRule) error {
	lb, etag, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	var actualRoutingRules []loadbalancer.RoutingRule
	for name, routingPolicy := range lb.RoutingPolicies {
		if name == policyName {
			actualRoutingRules = routingPolicy.Rules
			break
		}
	}

	needsUpdate := false
	if len(actualRoutingRules) == len(rules) {
		for i, lbRule := range actualRoutingRules {
			if !reflect.DeepEqual(lbRule, rules[i]) {
				klog.Infof("Rules %s and %s are different.", *lbRule.Name, *rules[i].Name)
				klog.Infof("Conditions %s and %s.", *lbRule.Condition, *rules[i].Condition)
				needsUpdate = true
				break
			}
		}
	} else {
		klog.Infof("Found difference between expected and existing rule count. This requires an update.")
		needsUpdate = true
	}

	if !needsUpdate {
		klog.Infof("No difference in routing rules. No routing policy update required.")
		return nil
	}

	klog.Infof("Routing policy %s for lb %s needs update.", policyName, lbID)

	updateRoutingPolicyRequest := loadbalancer.UpdateRoutingPolicyRequest{
		IfMatch:           common.String(etag),
		LoadBalancerId:    lb.Id,
		RoutingPolicyName: common.String(policyName),
		UpdateRoutingPolicyDetails: loadbalancer.UpdateRoutingPolicyDetails{
			Rules: rules,
		},
	}

	klog.Infof("Updating routing policy with request: %s", util.PrettyPrint(updateRoutingPolicyRequest))
	resp, err := lbc.LbClient.UpdateRoutingPolicy(ctx, updateRoutingPolicyRequest)
	if err != nil {
		return err
	}

	klog.Infof("Update routing policy response: name: %s, work request id: %s, opc request id: %s.", policyName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

// UpdateBackends updates Backends for backendSetName, while preserving sslConfig, policy, and healthChecker details
func (lbc *LoadBalancerClient) UpdateBackends(ctx context.Context, lbID string, backendSetName string, backends []loadbalancer.BackendDetails) error {
	lb, etag, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	backendSet, ok := lb.BackendSets[backendSetName]
	if !ok {
		return fmt.Errorf("backendset %s was not found", backendSetName)
	}

	actual := sets.NewString()
	for _, backend := range backendSet.Backends {
		actual.Insert(fmt.Sprintf("%s:%d", *backend.IpAddress, *backend.Port))
	}

	desired := sets.NewString()
	for _, backend := range backends {
		desired.Insert(fmt.Sprintf("%s:%d", *backend.IpAddress, *backend.Port))
	}

	if desired.Equal(actual) {
		klog.V(4).InfoS("no backend update required", "lbName", *lb.DisplayName, "lbID", *lb.Id)
		return nil
	}

	var sslConfig *loadbalancer.SslConfigurationDetails
	if backendSet.SslConfiguration != nil {
		sslConfig = &loadbalancer.SslConfigurationDetails{
			TrustedCertificateAuthorityIds: backendSet.SslConfiguration.TrustedCertificateAuthorityIds,
		}
	}

	healthCheckerDetails := &loadbalancer.HealthCheckerDetails{
		Protocol:          backendSet.HealthChecker.Protocol,
		UrlPath:           backendSet.HealthChecker.UrlPath,
		Port:              backendSet.HealthChecker.Port,
		ReturnCode:        backendSet.HealthChecker.ReturnCode,
		Retries:           backendSet.HealthChecker.Retries,
		TimeoutInMillis:   backendSet.HealthChecker.TimeoutInMillis,
		IntervalInMillis:  backendSet.HealthChecker.IntervalInMillis,
		ResponseBodyRegex: backendSet.HealthChecker.ResponseBodyRegex,
		IsForcePlainText:  backendSet.HealthChecker.IsForcePlainText,
	}

	policy := *backendSet.Policy

	return lbc.UpdateBackendSet(ctx, lbID, etag, *backendSet.Name, policy, healthCheckerDetails, sslConfig, backends)
}

// UpdateBackendSetDetails updates sslConfig, policy, and healthChecker details for backendSet, while preserving individual backends
func (lbc *LoadBalancerClient) UpdateBackendSetDetails(ctx context.Context, lbID string, etag string,
	backendSet *loadbalancer.BackendSet, sslConfig *loadbalancer.SslConfigurationDetails,
	healthCheckerDetails *loadbalancer.HealthCheckerDetails, policy string) error {

	backends := make([]loadbalancer.BackendDetails, len(backendSet.Backends))
	for i := range backendSet.Backends {
		backend := backendSet.Backends[i]
		backends[i] = loadbalancer.BackendDetails{
			IpAddress: backend.IpAddress,
			Port:      backend.Port,
			Weight:    backend.Weight,
			Drain:     backend.Drain,
			Backup:    backend.Backup,
			Offline:   backend.Offline,
		}
	}

	return lbc.UpdateBackendSet(ctx, lbID, etag, *backendSet.Name, policy, healthCheckerDetails, sslConfig, backends)
}

func (lbc *LoadBalancerClient) UpdateBackendSet(ctx context.Context, lbID string, etag string, backendSetName string,
	policy string, healthCheckerDetails *loadbalancer.HealthCheckerDetails, sslConfig *loadbalancer.SslConfigurationDetails,
	backends []loadbalancer.BackendDetails) error {
	updateBackendSetRequest := loadbalancer.UpdateBackendSetRequest{
		IfMatch:        common.String(etag),
		LoadBalancerId: common.String(lbID),
		BackendSetName: common.String(backendSetName),
		UpdateBackendSetDetails: loadbalancer.UpdateBackendSetDetails{
			Policy:           common.String(policy),
			HealthChecker:    healthCheckerDetails,
			SslConfiguration: sslConfig,
			Backends:         backends,
		},
	}

	klog.Infof("Updating backend set with request: %s", util.PrettyPrint(updateBackendSetRequest))
	resp, err := lbc.LbClient.UpdateBackendSet(ctx, updateBackendSetRequest)
	if err != nil {
		return err
	}

	klog.Infof("Update backend set response: name: %s, work request id: %s, opc request id: %s.",
		*updateBackendSetRequest.BackendSetName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) setRoutingPolicyOnListener(
	ctx context.Context,
	lbID string,
	routingPolicyName string) error {

	lb, etag, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	var l loadbalancer.Listener
	for _, listener := range lb.Listeners {
		if *listener.Name == routingPolicyName {
			if listener.RoutingPolicyName == nil {
				l = listener
				break
			}
			if *listener.RoutingPolicyName == routingPolicyName {
				// nothing to do since it already is set on the listener
				return nil
			}

			l = listener
			break
		}
	}

	if l.Name == nil {
		return fmt.Errorf("listener %s not found", routingPolicyName)
	}

	return lbc.UpdateListener(ctx, lb.Id, etag, l, &routingPolicyName, nil, l.Protocol, nil)
}

func (lbc *LoadBalancerClient) UpdateListener(ctx context.Context, lbId *string, etag string, l loadbalancer.Listener, routingPolicyName *string,
	sslConfigurationDetails *loadbalancer.SslConfigurationDetails, protocol *string, defaultBackendSet *string) error {

	if sslConfigurationDetails == nil && l.SslConfiguration != nil {
		sslConfigurationDetails = &loadbalancer.SslConfigurationDetails{
			CertificateIds: l.SslConfiguration.CertificateIds,
		}
	}

	if protocol == nil || *protocol == "" {
		protocol = l.Protocol
	}

	if defaultBackendSet == nil || *defaultBackendSet == "" {
		defaultBackendSet = l.DefaultBackendSetName
	}

	if *protocol == util.ProtocolHTTP2 {
		sslConfigurationDetails.CipherSuiteName = common.String(util.ProtocolHTTP2DefaultCipherSuite)
	}

	updateListenerRequest := loadbalancer.UpdateListenerRequest{
		IfMatch:        common.String(etag),
		LoadBalancerId: lbId,
		ListenerName:   l.Name,
		UpdateListenerDetails: loadbalancer.UpdateListenerDetails{
			Port:                  l.Port,
			Protocol:              protocol,
			DefaultBackendSetName: defaultBackendSet,
			SslConfiguration:      sslConfigurationDetails,
			RoutingPolicyName:     routingPolicyName,
		},
	}

	klog.Infof("Updating listener with request: %s", util.PrettyPrint(updateListenerRequest))
	resp, err := lbc.LbClient.UpdateListener(ctx, updateListenerRequest)
	if err != nil {
		return err
	}

	klog.Infof("Update listener response: name: %s, work request id: %s, opc request id: %s.", *l.Name, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) CreateListener(ctx context.Context, lbID string, listenerPort int, listenerProtocol string,
	defaultBackendSet string, sslConfig *loadbalancer.SslConfigurationDetails) error {

	lb, _, err := lbc.GetLoadBalancer(ctx, lbID)
	if err != nil {
		return err
	}

	listenerName := util.GenerateListenerName(int32(listenerPort))

	_, ok := lb.Listeners[listenerName]
	if ok {
		klog.Infof("Listener %s already present in load balancer %s. Skipping creation..", listenerName, lbID)
		return nil
	}

	if listenerProtocol == util.ProtocolHTTP2 {
		if sslConfig == nil {
			return fmt.Errorf("no TLS configuration provided for a HTTP2 listener at port %d", listenerPort)
		}

		sslConfig.CipherSuiteName = common.String(util.ProtocolHTTP2DefaultCipherSuite)
	}

	createListenerRequest := loadbalancer.CreateListenerRequest{
		LoadBalancerId: lb.Id,
		CreateListenerDetails: loadbalancer.CreateListenerDetails{
			DefaultBackendSetName: common.String(defaultBackendSet),
			Port:                  common.Int(listenerPort),
			Protocol:              common.String(listenerProtocol),
			Name:                  common.String(listenerName),
			SslConfiguration:      sslConfig,
		},
	}

	klog.Infof("Creating listener with request %s", util.PrettyPrint(createListenerRequest))
	resp, err := lbc.LbClient.CreateListener(ctx, createListenerRequest)

	if util.IsServiceError(err, 409) {
		klog.Infof("Create listener operation returned code %d for load balancer %s. Listener %s may be already present.", 409, lbID, listenerName)
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Create listener response: name: %s, work request id: %s, opc request id: %s.", listenerName, *resp.OpcWorkRequestId, *resp.OpcRequestId)
	_, err = lbc.waitForWorkRequest(ctx, *resp.OpcWorkRequestId)
	return err
}

func (lbc *LoadBalancerClient) waitForWorkRequest(ctx context.Context, workRequestID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	for {
		resp, err := lbc.LbClient.GetWorkRequest(ctx, loadbalancer.GetWorkRequestRequest{
			WorkRequestId: common.String(workRequestID),
		})
		if err != nil {
			return "", err
		}

		if resp.LifecycleState == loadbalancer.WorkRequestLifecycleStateSucceeded {
			lbc.getLoadBalancerBustCache(ctx, *resp.LoadBalancerId)
			return *resp.LoadBalancerId, nil
		}

		if resp.LifecycleState == loadbalancer.WorkRequestLifecycleStateFailed {
			return "", fmt.Errorf("work request failed: %+v", resp.ErrorDetails)
		}

		time.Sleep(10 * time.Second)
	}
}
