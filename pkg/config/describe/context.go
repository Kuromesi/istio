package describe

import "istio.io/istio/pilot/pkg/model"

type DescribeContext struct {
	PushContext *model.PushContext
	Proxy       *model.Proxy
	Environment *model.Environment
}

func NewDescribeContext(env *model.Environment, ps *model.PushContext, proxy *model.Proxy) *DescribeContext {
	proxy.SetSidecarScope(ps)
	proxy.SetGatewaysForProxy(ps)
	return &DescribeContext{
		Environment: env,
		PushContext: ps,
		Proxy:       proxy,
	}
}
