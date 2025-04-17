package proxy

import (
	"encoding/json"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"istio.io/istio/pkg/cluster"
	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/kube/multicluster"
	"istio.io/istio/pkg/model"
	"k8s.io/client-go/rest"

	pilotmodel "istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/bootstrap/platform"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/util/sets"
	"istio.io/istio/pkg/version"
)

func SetIstioVersion(meta *model.BootstrapNodeMetadata) *model.BootstrapNodeMetadata {
	if meta.IstioVersion == "" {
		meta.IstioVersion = version.Info.Version
	}
	return meta
}

// Extracts instance labels for the platform into model.NodeMetadata.Labels
// only if not running on Kubernetes
func extractInstanceLabels(plat platform.Environment, meta *model.BootstrapNodeMetadata) {
	if plat == nil || meta == nil || plat.IsKubernetes() {
		return
	}
	instanceLabels := plat.Labels()
	if meta.StaticLabels == nil {
		meta.StaticLabels = map[string]string{}
	}
	for k, v := range instanceLabels {
		meta.StaticLabels[k] = v
	}
}

func removeDuplicates(values []string) []string {
	set := sets.New[string]()
	newValues := make([]string, 0, len(values))
	for _, v := range values {
		if !set.InsertContains(v) {
			newValues = append(newValues, v)
		}
	}
	return newValues
}

type setMetaFunc func(m map[string]any, key string, val string)

func extractMetadata(envs []string, prefix string, set setMetaFunc, meta map[string]any) {
	metaPrefixLen := len(prefix)
	for _, e := range envs {
		if !shouldExtract(e, prefix) {
			continue
		}
		v := e[metaPrefixLen:]
		if !isEnvVar(v) {
			continue
		}
		metaKey, metaVal := parseEnvVar(v)
		set(meta, metaKey, metaVal)
	}
}

func shouldExtract(envVar, prefix string) bool {
	return strings.HasPrefix(envVar, prefix)
}

func isEnvVar(str string) bool {
	return strings.Contains(str, "=")
}

func parseEnvVar(varStr string) (string, string) {
	parts := strings.SplitN(varStr, "=", 2)
	if len(parts) != 2 {
		return varStr, ""
	}
	return parts[0], parts[1]
}

func jsonStringToMap(jsonStr string) (m map[string]string) {
	err := json.Unmarshal([]byte(jsonStr), &m)
	if err != nil {
		log.Warnf("Env variable with value %q failed json unmarshal: %v", jsonStr, err)
	}
	return
}

func extractAttributesMetadata(envVars []string, plat platform.Environment, meta *model.BootstrapNodeMetadata) {
	for _, varStr := range envVars {
		name, val := parseEnvVar(varStr)
		switch name {
		case "ISTIO_METAJSON_LABELS":
			m := jsonStringToMap(val)
			if len(m) > 0 {
				meta.Labels = m
				meta.StaticLabels = m
			}
		case "POD_NAME":
			meta.InstanceName = val
		case "POD_NAMESPACE":
			meta.Namespace = val
		case "SERVICE_ACCOUNT":
			meta.ServiceAccount = val
		}
	}
	if plat != nil && len(plat.Metadata()) > 0 {
		meta.PlatformMetadata = plat.Metadata()
	}
}

func InitProxyMetadata(node *corev3.Node) (*pilotmodel.Proxy, error) {
	meta, err := pilotmodel.ParseMetadata(node.Metadata)
	if err != nil {
		return nil, status.New(codes.InvalidArgument, err.Error()).Err()
	}
	proxy, err := pilotmodel.ParseServiceNodeWithMetadata(node.Id, meta)
	if err != nil {
		return nil, status.New(codes.InvalidArgument, err.Error()).Err()
	}
	// Update the config namespace associated with this proxy
	proxy.ConfigNamespace = pilotmodel.GetProxyConfigNamespace(proxy)
	proxy.XdsNode = node
	return proxy, nil
}

func newKubeClientWithRevision(kubeconfig, configContext, revision string, impersonateConfig rest.ImpersonationConfig) (kube.CLIClient, error) {
	rc, err := kube.DefaultRestConfig(kubeconfig, configContext, func(config *rest.Config) {
		// We are running a one-off command locally, so we don't need to worry too much about rate limiting
		// Bumping this up greatly decreases install time
		config.QPS = 50
		config.Burst = 100
		config.Impersonate = impersonateConfig
	})
	if err != nil {
		return nil, err
	}
	return kube.NewCLIClient(kube.NewClientConfigForRestConfig(rc), kube.WithRevision(revision), kube.WithCluster(cluster.ID(configContext)))
}

func NewCLIClient(store multicluster.ClusterStore, cluster cluster.ID) (kube.CLIClient, error) {
	cfg := store.GetByID(cluster).Client.RESTConfig()
	return kube.NewCLIClient(kube.NewClientConfigForRestConfig(cfg), kube.WithCluster(cluster))
}

func NewCLIClientFromREST(clusterId cluster.ID, cfg *rest.Config) (kube.CLIClient, error) {
	return kube.NewCLIClient(kube.NewClientConfigForRestConfig(cfg), kube.WithCluster(clusterId))
}
