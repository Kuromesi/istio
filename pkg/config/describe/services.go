package describe

import (
	"istio.io/istio/pilot/pkg/model"
)

type ServicesDescriber interface {
	Visible() []*model.Service
}

type servicesDescriber struct {
	*DescribeContext
}

func newServicesDescriber(ctx *DescribeContext) ServicesDescriber {
	return &servicesDescriber{
		DescribeContext: ctx,
	}
}

func (d *servicesDescriber) Visible() []*model.Service {
	return d.Proxy.SidecarScope.Services()
}
