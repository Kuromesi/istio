package proxy

import (
	"context"
	"fmt"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"istio.io/istio/istioctl/pkg/util/configdump"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/kube"
	"k8s.io/apimachinery/pkg/types"
)

type configDumpBuilder struct {
	kubeClient kube.CLIClient
}

func NewConfigDumpNodeBuilder(c kube.CLIClient) ProxyBuilder {
	return &configDumpBuilder{kubeClient: c}
}

func (b *configDumpBuilder) BuildProxy(name, namespace string) (*model.Proxy, error) {
	return podToProxy(b.kubeClient, types.NamespacedName{Name: name, Namespace: namespace})
}

func podToProxy(kubeClient kube.CLIClient, namespacedName types.NamespacedName) (*model.Proxy, error) {
	node, err := nodeInfoForPod(kubeClient, namespacedName)
	if err != nil {
		return nil, err
	}
	return InitProxyMetadata(node)
}

func nodeInfoForPod(kubeClient kube.CLIClient, namespacedName types.NamespacedName) (*corev3.Node, error) { // nolint: lll
	byConfigDump, err := kubeClient.EnvoyDo(context.TODO(), namespacedName.Name, namespacedName.Namespace, "GET", "config_dump")
	if err != nil {
		return nil, fmt.Errorf("failed to execute command on sidecar: %v", err)
	}
	cd := configdump.Wrapper{}
	err = cd.UnmarshalJSON(byConfigDump)
	if err != nil {
		return nil, fmt.Errorf("can't parse sidecar config_dump for %v: %v", err, namespacedName.Name)
	}
	d, _ := cd.GetBootstrapConfigDump()
	return d.Bootstrap.Node, nil
}
