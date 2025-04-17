package describe

import (
	extensions "istio.io/api/extensions/v1alpha1"
	"istio.io/api/mesh/v1alpha1"
	"istio.io/api/networking/v1alpha3"
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/config/host"
	"istio.io/istio/pkg/util/sets"
)

type ProxyDescribe interface {
	Services() ServicesDescriber
	EnvoyFilters() EnvoyFiltersDescriber
}

type proxyDescribe struct {
	ps *model.PushContext

	envoyfilters EnvoyFiltersDescriber
	services     ServicesDescriber
}

// func NewForPod(store *multicluster.ClusterStore) ProxyDescribe {
// 	store.GetByID("").Client()
// }

func NewForProxy(env *model.Environment, ps *model.PushContext, proxy *model.Proxy) ProxyDescribe {
	ctx := NewDescribeContext(env, ps, proxy)
	proxy.SetSidecarScope(ps)
	proxy.SetGatewaysForProxy(ps)

	return &proxyDescribe{
		ps:           ps,
		envoyfilters: newEnvoyFiltersDescriber(ctx),
		services:     newServicesDescriber(ctx),
	}
}

func (d *proxyDescribe) Services() ServicesDescriber {
	return d.services
}

func (d *proxyDescribe) EnvoyFilters() EnvoyFiltersDescriber {
	return d.envoyfilters
}

func DescribesServices(ctx *DescribeContext) []*model.Service {
	return ctx.Proxy.SidecarScope.Services()
}

func DescribeOutboundTrafficPolicy(ctx *DescribeContext) *v1alpha3.OutboundTrafficPolicy {
	return ctx.Proxy.SidecarScope.OutboundTrafficPolicy
}

func DescribeConsolidatedDestinationRules(ctx *DescribeContext) map[host.Name][]*model.ConsolidatedDestRule {
	return ctx.Proxy.SidecarScope.DestinationRules()
}

func DescribeVirtualServiceConfigs(ctx *DescribeContext) []config.Config {
	virtualServices := []config.Config{}
	described := sets.New[string]()
	egressListeners := ctx.Proxy.SidecarScope.EgressListeners
	for _, egressListener := range egressListeners {
		for _, virtualService := range egressListener.VirtualServices() {
			if described.Contains(virtualService.Key()) {
				continue
			}
			virtualServices = append(virtualServices, virtualService)
			described.Insert(virtualService.Key())
		}
	}
	return virtualServices
}

func DescribeSidecar(ctx *DescribeContext) *v1alpha3.Sidecar {
	return ctx.Proxy.SidecarScope.Sidecar
}

func DescribeProxyConfig(ctx *DescribeContext) *v1alpha1.ProxyConfig {
	return ctx.Environment.GetProxyConfigOrDefault(
		ctx.Proxy.ConfigNamespace,
		ctx.Proxy.Labels,
		ctx.Proxy.Metadata.Annotations,
		ctx.Environment.Mesh(),
	)
}

func DescribeTelemetries(ctx *DescribeContext) model.ComputedTelemetries {
	return ctx.PushContext.Telemetry.ApplicableTelemetries(ctx.Proxy)
}

func DescribeWasmPlugins(ctx *DescribeContext) []*extensions.WasmPlugin {
	res := make([]*extensions.WasmPlugin, 0)

	for _, plugins := range ctx.PushContext.WasmPlugins(ctx.Proxy) {
		for _, plugin := range plugins {
			res = append(res, plugin.WasmPlugin)
		}
	}
	return res
}

func DescribePeerAuthenticationConfigs(ctx *DescribeContext) []*config.Config {
	forWorkload := model.PolicyMatcherForProxy(ctx.Proxy)
	return ctx.PushContext.AuthnPolicies.GetPeerAuthenticationsForWorkload(forWorkload)
}

func DescribeRequestAuthenticationConfigs(ctx *DescribeContext) []*config.Config {
	forWorkload := model.PolicyMatcherForProxy(ctx.Proxy)
	return ctx.PushContext.AuthnPolicies.GetJwtPoliciesForWorkload(forWorkload)
}

func DescribeMutualTLSMode(ctx *DescribeContext) model.MutualTLSMode {
	return ctx.PushContext.AuthnPolicies.GetNamespaceMutualTLSMode(ctx.Proxy.ConfigNamespace)
}

func DescribeAuthorizationPolicies(ctx *DescribeContext) model.AuthorizationPoliciesResult {
	forWorkload := model.PolicyMatcherForProxy(ctx.Proxy)
	return ctx.PushContext.AuthzPolicies.ListAuthorizationPolicies(forWorkload)
}

func DescribeClusterLocalHosts(ctx *DescribeContext) model.ClusterLocalHosts {
	return ctx.PushContext.ClusterLocalHosts()
}

func DescribeGateways(ctx *DescribeContext) *model.MergedGateway {
	return ctx.Proxy.MergedGateway
}

func DescribeEnvoyFilters(ctx *DescribeContext) []*networking.EnvoyFilter {
	var matched []*model.EnvoyFilterWrapper
	if ctx.PushContext.Mesh.RootNamespace != "" {
		matched = ctx.PushContext.GetMatchedEnvoyFilters(ctx.Proxy, ctx.PushContext.Mesh.RootNamespace)
	}

	matched = append(matched, ctx.PushContext.GetMatchedEnvoyFilters(ctx.Proxy, ctx.Proxy.ConfigNamespace)...)
	ret := []*networking.EnvoyFilter{}
	for _, ef := range matched {
		ret = append(ret, ef.FullSpec())
	}
	return ret
}

type ProxyInfo struct {
	Type      model.NodeType
	Namespace string
	Name      string
}

type DescribeAllResponse struct {
	Proxy                        ProxyInfo                                   `json:"proxy"`
	OutboundTrafficPolicy        *v1alpha3.OutboundTrafficPolicy             `json:"outboundTrafficPolicy"`
	Sidecar                      *v1alpha3.Sidecar                           `json:"sidecar"`
	ProxyConfig                  *v1alpha1.ProxyConfig                       `json:"proxyConfig"`
	ConsolidatedDestinationRules map[host.Name][]*model.ConsolidatedDestRule `json:"consolidatedDestinationRules"`
	VirtualServiceConfigs        []config.Config                             `json:"virtualServiceConfigs"`
	EnvoyFilters                 []*networking.EnvoyFilter                   `json:"envoyFilters"`

	Telemetries                  model.ComputedTelemetries         `json:"telemetries"`
	PeerAuthenticationConfigs    []*config.Config                  `json:"peerAuthenticationConfigs"`
	RequestAuthenticationConfigs []*config.Config                  `json:"requestAuthenticationConfigs"`
	MutualTlsMode                model.MutualTLSMode               `json:"mutualTlsMode"`
	AuthorizationPolicies        model.AuthorizationPoliciesResult `json:"authorizationPolicies"`
	ClusterLocalHosts            model.ClusterLocalHosts           `json:"clusterLocalHosts"`
	Gateways                     *model.MergedGateway              `json:"gateways"`
	WasmPlugins                  []*extensions.WasmPlugin          `json:"wasmPlugins"`
	Services                     []*model.Service                  `json:"services"`
}

func DescribeAll(ctx *DescribeContext) DescribeAllResponse {
	proxy := ProxyInfo{
		Type:      ctx.Proxy.Type,
		Namespace: ctx.Proxy.ConfigNamespace,
		Name:      ctx.Proxy.ID,
	}
	return DescribeAllResponse{
		Proxy:                        proxy,
		Services:                     DescribesServices(ctx),
		OutboundTrafficPolicy:        DescribeOutboundTrafficPolicy(ctx),
		ConsolidatedDestinationRules: DescribeConsolidatedDestinationRules(ctx),
		VirtualServiceConfigs:        DescribeVirtualServiceConfigs(ctx),
		Sidecar:                      DescribeSidecar(ctx),
		ProxyConfig:                  DescribeProxyConfig(ctx),
		Telemetries:                  DescribeTelemetries(ctx),
		WasmPlugins:                  DescribeWasmPlugins(ctx),
		PeerAuthenticationConfigs:    DescribePeerAuthenticationConfigs(ctx),
		RequestAuthenticationConfigs: DescribeRequestAuthenticationConfigs(ctx),
		MutualTlsMode:                DescribeMutualTLSMode(ctx),
		AuthorizationPolicies:        DescribeAuthorizationPolicies(ctx),
		ClusterLocalHosts:            DescribeClusterLocalHosts(ctx),
		Gateways:                     DescribeGateways(ctx),
		EnvoyFilters:                 DescribeEnvoyFilters(ctx),
	}
}
