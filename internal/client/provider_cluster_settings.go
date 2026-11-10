package client

// Provider-specific cluster settings (ankra-hq41xb.15): the k3s components
// kept disabled, UpCloud NAT gateway and ingress load balancer plans, the
// UpCloud zone catalogue, the OVH gateway model and the Hetzner external
// cloud provider convergence. Every path is spelled as a literal so the
// route census (cluster_routes_test.go) can check it.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// K3sDisabledComponents is the k3s packaged components the platform keeps
// disabled on a running cluster (k3s --disable), and how the stored list
// reaches the nodes.
type K3sDisabledComponents struct {
	K3sDisabledComponents []string `json:"k3s_disabled_components" yaml:"k3s_disabled_components"`
	ResourceIDs           []string `json:"resource_ids" yaml:"resource_ids"`
	ControlPlaneCount     int      `json:"control_plane_count" yaml:"control_plane_count"`
	Applies               string   `json:"applies" yaml:"applies"`
}

type k3sDisabledComponentsUpdate struct {
	K3sDisabledComponents []string `json:"k3s_disabled_components"`
}

// K3sDisabledComponentsKinds are the cluster kinds that serve the route.
var K3sDisabledComponentsKinds = []string{
	"ankracloud", "aws", "digitalocean", "hetzner", "morpheus", "ovh", "proxmox", "scaleway", "upcloud",
}

func k3sDisabledComponentsPath(kind string) (string, error) {
	switch kind {
	case "ankracloud":
		return "/api/v1/clusters/ankracloud/%s/k3s-disabled-components", nil
	case "aws":
		return "/api/v1/clusters/aws/%s/k3s-disabled-components", nil
	case "digitalocean":
		return "/api/v1/clusters/digitalocean/%s/k3s-disabled-components", nil
	case "hetzner":
		return "/api/v1/clusters/hetzner/%s/k3s-disabled-components", nil
	case "morpheus":
		return "/api/v1/clusters/morpheus/%s/k3s-disabled-components", nil
	case "ovh":
		return "/api/v1/clusters/ovh/%s/k3s-disabled-components", nil
	case "proxmox":
		return "/api/v1/clusters/proxmox/%s/k3s-disabled-components", nil
	case "scaleway":
		return "/api/v1/clusters/scaleway/%s/k3s-disabled-components", nil
	case "upcloud":
		return "/api/v1/clusters/upcloud/%s/k3s-disabled-components", nil
	}
	return "", fmt.Errorf("cluster kind %q has no k3s disabled components route", kind)
}

// GetK3sDisabledComponents reads the stored disable list of a cluster of the
// given provider kind.
func (c *Client) GetK3sDisabledComponents(ctx context.Context, kind string, clusterID string) (*K3sDisabledComponents, error) {
	pathFormat, kindError := k3sDisabledComponentsPath(kind)
	if kindError != nil {
		return nil, kindError
	}
	var components K3sDisabledComponents
	if getError := c.sendJSONContext(ctx, http.MethodGet, c.servicesURL(pathFormat, nil, clusterID), nil, &components); getError != nil {
		return nil, getError
	}
	return &components, nil
}

// SetK3sDisabledComponents replaces the stored disable list. The platform
// wants the complete list, which has to contain every entry already stored:
// a list that drops one answers 409.
func (c *Client) SetK3sDisabledComponents(ctx context.Context, kind string, clusterID string, components []string) (*K3sDisabledComponents, error) {
	pathFormat, kindError := k3sDisabledComponentsPath(kind)
	if kindError != nil {
		return nil, kindError
	}
	if components == nil {
		components = []string{}
	}
	var stored K3sDisabledComponents
	if putError := c.sendJSONContext(ctx, http.MethodPut, c.servicesURL(pathFormat, nil, clusterID),
		k3sDisabledComponentsUpdate{K3sDisabledComponents: components}, &stored); putError != nil {
		return nil, putError
	}
	return &stored, nil
}

func credentialQuery(credentialID string) url.Values {
	query := url.Values{}
	query.Set("credential_id", credentialID)
	return query
}

// UpcloudGatewayPlan is one UpCloud NAT gateway plan. HighAvailability is a
// plan that runs two or more gateway servers.
type UpcloudGatewayPlan struct {
	Name              string   `json:"name" yaml:"name"`
	BandwidthMbps     int      `json:"bandwidth_mbps" yaml:"bandwidth_mbps"`
	ServerNumber      int      `json:"server_number" yaml:"server_number"`
	HighAvailability  bool     `json:"high_availability" yaml:"high_availability"`
	MaxConnections    int      `json:"max_connections" yaml:"max_connections"`
	VPNTunnelAmount   int      `json:"vpn_tunnel_amount" yaml:"vpn_tunnel_amount"`
	SupportedFeatures []string `json:"supported_features" yaml:"supported_features"`
}

// UpcloudGatewayPlans is the live NAT gateway plan catalogue, the plan a
// create without one gets, and the notice a day-2 plan change carries.
type UpcloudGatewayPlans struct {
	Plans            []UpcloudGatewayPlan `json:"plans" yaml:"plans"`
	DefaultPlan      string               `json:"default_plan" yaml:"default_plan"`
	PlanChangeNotice string               `json:"plan_change_notice" yaml:"plan_change_notice"`
}

// ListUpcloudGatewayPlans reads the NAT gateway plans the credential's
// account can create (GET /api/v1/clusters/upcloud/gateway-plans).
func (c *Client) ListUpcloudGatewayPlans(ctx context.Context, credentialID string) (*UpcloudGatewayPlans, error) {
	var plans UpcloudGatewayPlans
	address := c.servicesURL("/api/v1/clusters/upcloud/gateway-plans", credentialQuery(credentialID))
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &plans); getError != nil {
		return nil, getError
	}
	return &plans, nil
}

// UpcloudGatewayPlanChange is the platform's 202 answer to a plan change:
// the running gateways handed to the reconciler, the gateways of a stopped
// cluster that take the plan at the next start, and the egress interruption
// the change causes.
type UpcloudGatewayPlanChange struct {
	GatewayPlan         string   `json:"gateway_plan" yaml:"gateway_plan"`
	PlanChangeRequestID string   `json:"plan_change_request_id" yaml:"plan_change_request_id"`
	ResourceIDs         []string `json:"resource_ids" yaml:"resource_ids"`
	DeferredResourceIDs []string `json:"deferred_resource_ids" yaml:"deferred_resource_ids"`
	Notice              string   `json:"notice" yaml:"notice"`
}

type upcloudGatewayPlanUpdate struct {
	GatewayPlan string `json:"gateway_plan"`
}

// SetUpcloudGatewayPlan changes the plan of the cluster's NAT gateways in
// place (PUT /api/v1/clusters/upcloud/{cluster_id}/gateway-plan). It
// interrupts the cluster's outbound internet access while UpCloud rebuilds
// the gateway.
func (c *Client) SetUpcloudGatewayPlan(ctx context.Context, clusterID string, plan string) (*UpcloudGatewayPlanChange, error) {
	var change UpcloudGatewayPlanChange
	address := c.servicesURL("/api/v1/clusters/upcloud/%s/gateway-plan", nil, clusterID)
	if putError := c.sendJSONContext(ctx, http.MethodPut, address, upcloudGatewayPlanUpdate{GatewayPlan: plan}, &change); putError != nil {
		return nil, putError
	}
	return &change, nil
}

// UpcloudLoadBalancerPlan is one UpCloud managed load balancer plan.
type UpcloudLoadBalancerPlan struct {
	Name                 string `json:"name" yaml:"name"`
	ServerNumber         int    `json:"server_number" yaml:"server_number"`
	PerServerMaxSessions int    `json:"per_server_max_sessions" yaml:"per_server_max_sessions"`
	HighlyAvailable      bool   `json:"highly_available" yaml:"highly_available"`
}

// ListUpcloudLoadBalancerPlans reads UpCloud's managed load balancer plans
// with the given credential (GET /api/v1/clusters/upcloud/load-balancer-plans).
func (c *Client) ListUpcloudLoadBalancerPlans(ctx context.Context, credentialID string) ([]UpcloudLoadBalancerPlan, error) {
	plans := []UpcloudLoadBalancerPlan{}
	address := c.servicesURL("/api/v1/clusters/upcloud/load-balancer-plans", credentialQuery(credentialID))
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &plans); getError != nil {
		return nil, getError
	}
	return plans, nil
}

// UpcloudLoadBalancer is one live load balancer of a cluster. Ingress marks
// the balancer in front of Ankra's Traefik, the one the recorded plan
// governs.
type UpcloudLoadBalancer struct {
	UUID             string `json:"uuid" yaml:"uuid"`
	Name             string `json:"name" yaml:"name"`
	Plan             string `json:"plan" yaml:"plan"`
	OperationalState string `json:"operational_state" yaml:"operational_state"`
	Ingress          bool   `json:"ingress" yaml:"ingress"`
}

// UpcloudClusterLoadBalancerPlan is the plan Ankra has recorded for the
// cluster's ingress load balancer beside the plans its live balancers run
// on. LoadBalancerPlan is nil when none was ever recorded; LoadBalancers is
// nil, not empty, when UpCloud could not be read.
type UpcloudClusterLoadBalancerPlan struct {
	LoadBalancerPlan      *string                   `json:"load_balancer_plan" yaml:"load_balancer_plan"`
	Managed               bool                      `json:"managed" yaml:"managed"`
	LoadBalancersObserved bool                      `json:"load_balancers_observed" yaml:"load_balancers_observed"`
	LoadBalancers         []UpcloudLoadBalancer     `json:"load_balancers" yaml:"load_balancers"`
	ObservationError      *string                   `json:"observation_error" yaml:"observation_error"`
	AvailablePlans        []UpcloudLoadBalancerPlan `json:"available_plans" yaml:"available_plans"`
}

// GetUpcloudClusterLoadBalancerPlan reads the recorded and live ingress load
// balancer plans (GET /api/v1/clusters/upcloud/{cluster_id}/load-balancer-plan).
func (c *Client) GetUpcloudClusterLoadBalancerPlan(ctx context.Context, clusterID string) (*UpcloudClusterLoadBalancerPlan, error) {
	var plan UpcloudClusterLoadBalancerPlan
	address := c.servicesURL("/api/v1/clusters/upcloud/%s/load-balancer-plan", nil, clusterID)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &plan); getError != nil {
		return nil, getError
	}
	return &plan, nil
}

// UpcloudLoadBalancerPlanChange is the answer to a plan change. Changed is
// false when the request named the plan already recorded.
type UpcloudLoadBalancerPlanChange struct {
	LoadBalancerPlan         string   `json:"load_balancer_plan" yaml:"load_balancer_plan"`
	PreviousLoadBalancerPlan *string  `json:"previous_load_balancer_plan" yaml:"previous_load_balancer_plan"`
	Changed                  bool     `json:"changed" yaml:"changed"`
	ResourceIDs              []string `json:"resource_ids" yaml:"resource_ids"`
}

type upcloudLoadBalancerPlanUpdate struct {
	LoadBalancerPlan string `json:"load_balancer_plan"`
}

// SetUpcloudClusterLoadBalancerPlan records a new plan for the cluster's
// ingress load balancer (PUT /api/v1/clusters/upcloud/{cluster_id}/load-balancer-plan).
func (c *Client) SetUpcloudClusterLoadBalancerPlan(ctx context.Context, clusterID string, plan string) (*UpcloudLoadBalancerPlanChange, error) {
	var change UpcloudLoadBalancerPlanChange
	address := c.servicesURL("/api/v1/clusters/upcloud/%s/load-balancer-plan", nil, clusterID)
	if putError := c.sendJSONContext(ctx, http.MethodPut, address, upcloudLoadBalancerPlanUpdate{LoadBalancerPlan: plan}, &change); putError != nil {
		return nil, putError
	}
	return &change, nil
}

// UpcloudZone is one public UpCloud zone a credential's account can deploy in.
type UpcloudZone struct {
	ID          string `json:"id" yaml:"id"`
	Description string `json:"description" yaml:"description"`
}

// ListUpcloudZones reads the zones the credential's account can deploy in,
// live from UpCloud (GET /api/v1/clusters/upcloud/zones).
func (c *Client) ListUpcloudZones(ctx context.Context, credentialID string) ([]UpcloudZone, error) {
	zones := []UpcloudZone{}
	address := c.servicesURL("/api/v1/clusters/upcloud/zones", credentialQuery(credentialID))
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &zones); getError != nil {
		return nil, getError
	}
	return zones, nil
}

// OvhGatewayModelChange is the answer to an OVH gateway model change.
type OvhGatewayModelChange struct {
	GatewayModel string   `json:"gateway_model" yaml:"gateway_model"`
	ResourceIDs  []string `json:"resource_ids" yaml:"resource_ids"`
}

type ovhGatewayModelUpdate struct {
	GatewayModel string `json:"gateway_model"`
}

// SetOvhGatewayModel changes the model of an OVH cluster's managed gateway
// (PUT /api/v1/clusters/ovh/{cluster_id}/gateway-model).
func (c *Client) SetOvhGatewayModel(ctx context.Context, clusterID string, model string) (*OvhGatewayModelChange, error) {
	var change OvhGatewayModelChange
	address := c.servicesURL("/api/v1/clusters/ovh/%s/gateway-model", nil, clusterID)
	if putError := c.sendJSONContext(ctx, http.MethodPut, address, ovhGatewayModelUpdate{GatewayModel: model}, &change); putError != nil {
		return nil, putError
	}
	return &change, nil
}

// HetznerExternalCloudProviderConvergence is what a convergence stored:
// which Kubernetes node definitions it flipped to external_cloud_provider
// true and which already stored it. VerifiedNodeCount is nil when no
// definition needed flipping and the node census was not read.
type HetznerExternalCloudProviderConvergence struct {
	ExternalCloudProvider      bool     `json:"external_cloud_provider" yaml:"external_cloud_provider"`
	ConvergedResourceIDs       []string `json:"converged_resource_ids" yaml:"converged_resource_ids"`
	AlreadyExternalResourceIDs []string `json:"already_external_resource_ids" yaml:"already_external_resource_ids"`
	VerifiedNodeCount          *int     `json:"verified_node_count" yaml:"verified_node_count"`
	Applies                    string   `json:"applies" yaml:"applies"`
}

// ConvergeHetznerExternalCloudProvider stores external_cloud_provider=true
// on the node definitions of a Hetzner k3s cluster whose nodes already run
// for the hcloud cloud controller manager
// (POST /api/v1/clusters/hetzner/{cluster_id}/external-cloud-provider/converge).
// Only stored definitions change; no node is restarted.
func (c *Client) ConvergeHetznerExternalCloudProvider(ctx context.Context, clusterID string) (*HetznerExternalCloudProviderConvergence, error) {
	var convergence HetznerExternalCloudProviderConvergence
	address := c.servicesURL("/api/v1/clusters/hetzner/%s/external-cloud-provider/converge", nil, clusterID)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, nil, &convergence); postError != nil {
		return nil, postError
	}
	return &convergence, nil
}
