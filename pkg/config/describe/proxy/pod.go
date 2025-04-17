// contains copy from pkg/bootstrap/config.go

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	meshAPI "istio.io/api/mesh/v1alpha1"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/bootstrap"
	"istio.io/istio/pkg/bootstrap/platform"
	"istio.io/istio/pkg/kube/labels"
	"istio.io/istio/pkg/log"
	istiomodel "istio.io/istio/pkg/model"
	"istio.io/istio/pkg/ptr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/cmd/set/env"
)

const (
	serviceNodeSeparator = "~"
)

type nodeBuilder struct {
	kubeClient kubernetes.Interface
	env        *model.Environment
}

type buildNodeContext struct {
	pod         *corev1.Pod
	nodeType    model.NodeType
	proxyConfig *meshAPI.ProxyConfig
	serviceNode string
	containers  []corev1.Container
}

func NewProxyBuilder(env *model.Environment, kubeClient kubernetes.Interface) ProxyBuilder {
	return &nodeBuilder{
		env:        env,
		kubeClient: kubeClient,
	}
}

func (b *nodeBuilder) BuildProxy(name, namespace string) (*model.Proxy, error) {
	pod, err := b.kubeClient.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ctx := &buildNodeContext{
		pod: pod,
	}

	containers := []corev1.Container{}
	containers = append(containers, ctx.pod.Spec.Containers...)
	containers = append(containers, ctx.pod.Spec.InitContainers...)
	ctx.containers = containers

	proxyConfig := b.env.GetProxyConfigOrDefault(pod.Namespace, pod.Labels, pod.Annotations, b.env.Mesh())
	ctx.proxyConfig = proxyConfig

	nodeType := b.nodeType(ctx)
	if !istiomodel.IsApplicationNodeType(nodeType) {
		return nil, fmt.Errorf("invalid node type in the service node %q", nodeType)
	}

	ctx.nodeType = nodeType

	node, err := b.getNodeMetaData(ctx)
	if err != nil {
		return nil, err
	}

	metadata, err := model.ParseMetadata(node.Metadata.ToStruct())
	if err != nil {
		return nil, err
	}

	buildedNode := &corev3.Node{
		Id:       b.serviceNode(ctx),
		Cluster:  b.getServiceCluster(ctx),
		Metadata: metadata.ToStruct(),
		Locality: node.Locality,
	}
	return InitProxyMetadata(buildedNode)
}

func (b *nodeBuilder) nodeType(ctx *buildNodeContext) model.NodeType {
	nodeType := model.NodeType("")

	for _, c := range ctx.containers {
		if c.Name == "istio-proxy" {
			args := c.Args
			if len(args) > 1 {
				nodeType = model.NodeType(args[1])
				break
			}
		}
	}

	return nodeType
}

func (b *nodeBuilder) serviceNode(ctx *buildNodeContext) string {
	pod := ctx.pod

	ip := ""
	if len(pod.Status.PodIPs) > 0 {
		ip = pod.Status.PodIPs[0].IP
	}

	dnsDomain := pod.Namespace + ".svc." + b.env.DomainSuffix

	return strings.Join([]string{
		string(ctx.nodeType), ip, b.nodeId(pod), dnsDomain,
	}, serviceNodeSeparator)
}

func (b *nodeBuilder) nodeId(pod *corev1.Pod) string {
	return pod.Name + "." + pod.Namespace
}

func (b *nodeBuilder) getServiceCluster(ctx *buildNodeContext) string {
	switch name := ctx.proxyConfig.ClusterName.(type) {
	case *meshAPI.ProxyConfig_ServiceCluster:
		return serviceClusterOrDefault(name.ServiceCluster, ctx.pod.Namespace, ctx.pod.Labels)

	case *meshAPI.ProxyConfig_TracingServiceName_:
		workloadName := "istio-proxy"

		switch name.TracingServiceName {
		case meshAPI.ProxyConfig_APP_LABEL_AND_NAMESPACE:
			return serviceClusterOrDefault("istio-proxy", ctx.pod.Namespace, ctx.pod.Labels)
		case meshAPI.ProxyConfig_CANONICAL_NAME_ONLY:
			cs, _ := labels.CanonicalService(ctx.pod.Labels, workloadName)
			return serviceClusterOrDefault(cs, ctx.pod.Namespace, ctx.pod.Labels)
		case meshAPI.ProxyConfig_CANONICAL_NAME_AND_NAMESPACE:
			cs, _ := labels.CanonicalService(ctx.pod.Labels, workloadName)
			if ctx.pod.Namespace != "" {
				return cs + "." + ctx.pod.Namespace
			}
			return serviceClusterOrDefault(cs, ctx.pod.Namespace, ctx.pod.Labels)
		default:
			return serviceClusterOrDefault("istio-proxy", ctx.pod.Namespace, ctx.pod.Labels)
		}

	default:
		return serviceClusterOrDefault("istio-proxy", ctx.pod.Namespace, ctx.pod.Labels)
	}
}

func (b *nodeBuilder) getEnvVarValue(pod *corev1.Pod, c *corev1.Container, e *corev1.EnvVar) string {
	if e.ValueFrom != nil {
		if e.ValueFrom.SecretKeyRef != nil {
			return ""
		}

		if e.ValueFrom.ConfigMapKeyRef != nil {
			return ""
		}

		if e.ValueFrom.FieldRef != nil {
			switch e.ValueFrom.FieldRef.FieldPath {
			case "spec.nodeName":
				return pod.Spec.NodeName
			case "status.podIP":
				return pod.Status.PodIP
			case "spec.serviceAccountName":
				return pod.Spec.ServiceAccountName

			}
		}

		val, err := env.GetEnvVarRefValue(b.kubeClient, pod.Namespace, nil, e.ValueFrom, pod, c)
		if err != nil {
			if strings.HasPrefix(e.Name, bootstrap.IstioMetaPrefix) ||
				strings.HasPrefix(e.Name, bootstrap.IstioMetaJSONPrefix) {
				log.Warnf("failed to get value for env variable %s, error: %v", e.Name, err)
			}
		}
		return val
	}

	return e.Value
}

// GetNodeMetaData function uses an environment variable contract
// ISTIO_METAJSON_* env variables contain json_string in the value.
// The name of variable is ignored.
// ISTIO_META_* env variables are passed through
func (b *nodeBuilder) getNodeMetaData(ctx *buildNodeContext) (*model.Node, error) {
	var err error
	platform := &platform.Unknown{}

	meta := &model.BootstrapNodeMetadata{}
	untypedMeta := map[string]any{}

	for k, v := range ctx.proxyConfig.GetProxyMetadata() {
		if strings.HasPrefix(k, bootstrap.IstioMetaPrefix) {
			untypedMeta[strings.TrimPrefix(k, bootstrap.IstioMetaPrefix)] = v
		}
	}

	envs := []string{}
	for _, c := range ctx.containers {
		for _, e := range c.Env {
			if val := b.getEnvVarValue(ctx.pod, &c, &e); val != "" {
				envs = append(envs, fmt.Sprintf("%s=%s", e.Name, val))
			}
		}
	}
	extractMetadata(envs, bootstrap.IstioMetaPrefix, func(m map[string]any, key string, val string) {
		m[key] = val
	}, untypedMeta)

	extractMetadata(envs, bootstrap.IstioMetaJSONPrefix, func(m map[string]any, key string, val string) {
		err := json.Unmarshal([]byte(val), &m)
		if err != nil {
			log.Warnf("Env variable %s [%s] failed json unmarshal: %v", key, val, err)
		}
	}, untypedMeta)

	j, err := json.Marshal(untypedMeta)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(j, meta); err != nil {
		return nil, err
	}

	meta = SetIstioVersion(meta)

	// Support multiple network interfaces, removing duplicates.
	podIps := []string{}
	for _, ip := range ctx.pod.Status.PodIPs {
		podIps = append(podIps, ip.IP)
	}
	meta.InstanceIPs = removeDuplicates(podIps)

	meta.ProxyConfig = (*model.NodeMetaProxyConfig)(ctx.proxyConfig)

	extractAttributesMetadata(envs, platform, meta)
	// Add all instance labels with lower precedence than pod labels
	extractInstanceLabels(platform, meta)

	// Add all pod labels found from filesystem
	// These are typically volume mounted by the downward API
	lbls := ctx.pod.Labels
	meta.Labels = map[string]string{}
	for k, v := range meta.StaticLabels {
		meta.Labels[k] = v
	}
	for k, v := range lbls {
		// ignore `pod-template-hash` label
		if k == bootstrap.DefaultDeploymentUniqueLabelKey {
			continue
		}
		meta.Labels[k] = v
	}

	// Add all pod annotations found from filesystem
	// These are typically volume mounted by the downward API
	annos := ctx.pod.Annotations
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	for k, v := range annos {
		meta.Annotations[k] = v
	}

	var l *corev3.Locality
	if meta.Labels[model.LocalityLabel] == "" && platform != nil {
		// The locality string was not set, try to get locality from platform
		l = platform.Locality()
	} else {
		// replace "." with "/"
		localityString := model.GetLocalityLabel(meta.Labels[model.LocalityLabel])
		if localityString != "" {
			// override the label with the sanitized value
			meta.Labels[model.LocalityLabel] = localityString
		}
		l = istiomodel.ConvertLocality(localityString)
	}

	if meta.MetadataDiscovery == nil {
		// If it's disabled, set it if ambient is enabled
		meta.MetadataDiscovery = ptr.Of(meta.EnableHBONE)
		log.Debugf("metadata discovery is disabled, setting it to %s based on if ambient HBONE is enabled", meta.EnableHBONE)
	}

	return &model.Node{
		ID:          ctx.serviceNode,
		Metadata:    meta,
		RawMetadata: untypedMeta,
		Locality:    l,
	}, nil
}

func serviceClusterOrDefault(name string, namespace string, labels map[string]string) string {
	if name != "" && name != "istio-proxy" {
		return name
	}
	if app, ok := labels["app"]; ok {
		return app + "." + namespace
	}
	if namespace != "" {
		return "istio-proxy." + namespace
	}
	return "istio-proxy"
}
