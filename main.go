package main

import (
	"context"
	"fmt"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/spf13/pflag"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"istio.io/istio/istioctl/pkg/util/configdump"
	"istio.io/istio/pilot/pkg/bootstrap"
	"istio.io/istio/pilot/pkg/features"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/networking/plugin/authn"
	"istio.io/istio/pilot/pkg/networking/plugin/authz"
	"istio.io/istio/pilot/pkg/serviceregistry/provider"
	"istio.io/istio/pkg/cluster"
	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/config/describe"
	"istio.io/istio/pkg/ctrlz"
	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/log"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

func main() {
	args := bootstrap.NewPilotArgs(func(p *bootstrap.PilotArgs) {
		// Set Defaults
		p.CtrlZOptions = ctrlz.DefaultOptions()
		// TODO replace with mesh config?
		p.InjectionOptions = bootstrap.InjectionOptions{
			InjectionDirectory: "./var/lib/istio/inject",
		}
	})
	initFlags(args)
	server, err := bootstrap.NewAnalyzeServer(args)
	fmt.Println(err)
	stop := make(chan struct{})

	server.Start(stop)
	log.Infof("waiting configs to be pushed for %s", features.DebounceMax.String())
	time.Sleep(features.DebounceMax)

	ps := server.Environment().PushContext()
	psbytes, err := ps.MarshalFull()
	fmt.Println(string(psbytes))

	close(stop)

	c, err := newKubeClientWithRevision("/root/.kube/cluster-sb", "cluster-S4b1zd", "", rest.ImpersonationConfig{})
	ef, err := server.KubeClient().Istio().NetworkingV1alpha3().EnvoyFilters("scheduler").List(context.TODO(), v1.ListOptions{})
	fmt.Println(ef)
	proxy, err := describe.PodToProxy(c, types.NamespacedName{Namespace: "scheduler", Name: "httpbin-77b69b5689-w5lrq"})
	proxy.SetGatewaysForProxy(ps)
	describer := describe.NewForProxy(ps, proxy)
	matched := describer.EnvoyFilters().Attached()
	svcs := describer.Services().Visible()
	fmt.Println(svcs)
	efs := ps.EnvoyFilters(proxy)
	fmt.Println(matched)
	fmt.Println(efs)
	ps.WasmPlugins(proxy)
	proxy.SidecarScope.Services()
	authz.NewBuilder(authz.ActionType(authz.Local), ps, proxy, false)
	authn.NewBuilder(ps, proxy)
}

func NodeInfoForPod(kubeClient kube.CLIClient, pod *corev1.Pod) (*corev3.Node, error) { // nolint: lll
	byConfigDump, err := kubeClient.EnvoyDo(context.TODO(), pod.ObjectMeta.Name, pod.ObjectMeta.Namespace, "GET", "config_dump")
	if err != nil {
		return nil, fmt.Errorf("failed to execute command on sidecar: %v", err)
	}
	cd := configdump.Wrapper{}
	err = cd.UnmarshalJSON(byConfigDump)
	if err != nil {
		return nil, fmt.Errorf("can't parse sidecar config_dump for %v: %v", err, pod.ObjectMeta.Name)
	}
	d, _ := cd.GetBootstrapConfigDump()
	return d.Bootstrap.Node, nil
}

func initProxyMetadata(node *corev3.Node) (*model.Proxy, error) {
	meta, err := model.ParseMetadata(node.Metadata)
	if err != nil {
		return nil, status.New(codes.InvalidArgument, err.Error()).Err()
	}
	proxy, err := model.ParseServiceNodeWithMetadata(node.Id, meta)
	if err != nil {
		return nil, status.New(codes.InvalidArgument, err.Error()).Err()
	}
	// Update the config namespace associated with this proxy
	proxy.ConfigNamespace = model.GetProxyConfigNamespace(proxy)
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

func initFlags(serverArgs *bootstrap.PilotArgs) {
	pflag.StringSliceVar(&serverArgs.RegistryOptions.Registries, "registries",
		[]string{string(provider.Kubernetes)},
		fmt.Sprintf("Comma separated list of platform service registries to read from (choose one or more from {%s, %s})",
			provider.Kubernetes, provider.Mock))
	pflag.StringVar(&serverArgs.RegistryOptions.ClusterRegistriesNamespace, "clusterRegistriesNamespace",
		serverArgs.RegistryOptions.ClusterRegistriesNamespace, "Namespace for ConfigMap which stores clusters configs")
	pflag.StringVar(&serverArgs.RegistryOptions.KubeConfig, "kubeconfig", "",
		"Use a Kubernetes configuration file instead of in-cluster configuration")
	pflag.StringVar(&serverArgs.MeshConfigFile, "meshConfig", "./etc/istio/config/mesh",
		"File name for Istio mesh configuration. If not specified, a default mesh will be used.")
	pflag.StringVar(&serverArgs.NetworksConfigFile, "networksConfig", "./etc/istio/config/meshNetworks",
		"File name for Istio mesh networks configuration. If not specified, a default mesh networks will be used.")
	pflag.StringVarP(&serverArgs.Namespace, "namespace", "n", bootstrap.PodNamespace,
		"Select a namespace where the controller resides. If not set, uses ${POD_NAMESPACE} environment variable")
	pflag.StringVar(&serverArgs.CniNamespace, "cniNamespace", bootstrap.PodNamespace,
		"Select a namespace where the istio-cni resides. If not set, uses ${POD_NAMESPACE} environment variable")
	pflag.DurationVar(&serverArgs.ShutdownDuration, "shutdownDuration", 10*time.Second,
		"Duration the discovery server needs to terminate gracefully")

	// RegistryOptions Controller options
	pflag.StringVar(&serverArgs.RegistryOptions.FileDir, "configDir", "",
		"Directory to watch for updates to config yaml files. If specified, the files will be used as the source of config, rather than a CRD client.")
	pflag.StringVar(&serverArgs.RegistryOptions.KubeOptions.DomainSuffix, "domain", constants.DefaultClusterLocalDomain,
		"DNS domain suffix")
	pflag.StringVar((*string)(&serverArgs.RegistryOptions.KubeOptions.ClusterID), "clusterID", features.ClusterName,
		"The ID of the cluster that this Istiod instance resides")
	pflag.StringToStringVar(&serverArgs.RegistryOptions.KubeOptions.ClusterAliases, "clusterAliases", map[string]string{},
		"Alias names for clusters")

	pflag.BoolVar(&serverArgs.ServerOptions.EnableProfiling, "profile", true,
		"Enable profiling via web interface host:port/debug/pprof")

	// Use TLS certificates if provided.
	pflag.StringVar(&serverArgs.ServerOptions.TLSOptions.CaCertFile, "caCertFile", "",
		"File containing the x509 Server CA Certificate")
	pflag.StringVar(&serverArgs.ServerOptions.TLSOptions.CertFile, "tlsCertFile", "",
		"File containing the x509 Server Certificate")
	pflag.StringVar(&serverArgs.ServerOptions.TLSOptions.KeyFile, "tlsKeyFile", "",
		"File containing the x509 private key matching --tlsCertFile")

	pflag.Float32Var(&serverArgs.RegistryOptions.KubeOptions.KubernetesAPIQPS, "kubernetesApiQPS", 80.0,
		"Maximum QPS when communicating with the kubernetes API")

	pflag.IntVar(&serverArgs.RegistryOptions.KubeOptions.KubernetesAPIBurst, "kubernetesApiBurst", 160,
		"Maximum burst for throttle when communicating with the kubernetes API")
}
