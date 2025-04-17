package xds

import (
	"fmt"
	"strings"

	"istio.io/istio/pilot/pkg/model"
	proxyutils "istio.io/istio/pkg/config/describe/proxy"
)

type connBuilder struct {
	connections []*Connection
}

func NewConnBuilder(connections []*Connection) proxyutils.ProxyBuilder {
	return &connBuilder{
		connections: connections,
	}
}

func (b *connBuilder) BuildProxy(name, namespace string) (*model.Proxy, error) {
	proxyId := name + "." + namespace
	for _, conn := range b.connections {
		if strings.Contains(conn.ID(), proxyId) {
			return conn.Proxy(), nil
		}
	}
	return nil, fmt.Errorf("proxy %s not found", fmt.Sprintf("%s.%s", name, namespace))
}
