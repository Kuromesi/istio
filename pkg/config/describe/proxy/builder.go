package proxy

import "istio.io/istio/pilot/pkg/model"

type ProxyBuilder interface {
	BuildProxy(name, namespace string) (*model.Proxy, error)
}
