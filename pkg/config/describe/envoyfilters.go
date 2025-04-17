package describe

import (
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/model"
)

type EnvoyFiltersDescriber interface {
	Attached() []*networking.EnvoyFilter
}

type envoyFiltersDescriber struct {
	*DescribeContext
}

func newEnvoyFiltersDescriber(ctx *DescribeContext) EnvoyFiltersDescriber {
	return &envoyFiltersDescriber{
		DescribeContext: ctx,
	}
}

func (d *envoyFiltersDescriber) Attached() []*networking.EnvoyFilter {
	var matched []*model.EnvoyFilterWrapper
	if d.PushContext.Mesh.RootNamespace != "" {
		matched = d.PushContext.GetMatchedEnvoyFilters(d.Proxy, d.PushContext.Mesh.RootNamespace)
	}

	matched = append(matched, d.PushContext.GetMatchedEnvoyFilters(d.Proxy, d.Proxy.ConfigNamespace)...)
	ret := []*networking.EnvoyFilter{}
	for _, ef := range matched {
		ret = append(ret, ef.FullSpec())
	}
	return ret
}
