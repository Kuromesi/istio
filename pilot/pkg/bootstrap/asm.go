package bootstrap

import (
	"fmt"
	"net/http"

	"istio.io/istio/pilot/pkg/keycertbundle"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/networking/core"
	"istio.io/istio/pilot/pkg/server"
	"istio.io/istio/pilot/pkg/serviceregistry/aggregate"
	"istio.io/istio/pilot/pkg/xds"
	"istio.io/istio/pkg/filewatcher"
	kubelib "istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/kube/kclient"
	"istio.io/istio/pkg/kube/namespace"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/monitoring"
	corev1 "k8s.io/api/core/v1"
)

// NewServer creates a new Server instance based on the provided arguments.
func NewAnalyzeServer(args *PilotArgs, initFuncs ...func(*Server)) (*Server, error) {
	e := model.NewEnvironment()
	e.DomainSuffix = args.RegistryOptions.KubeOptions.DomainSuffix

	ac := aggregate.NewController(aggregate.Options{
		MeshHolder: e,
	})
	e.ServiceDiscovery = ac

	exporter, err := monitoring.RegisterPrometheusExporter(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("could not set up prometheus exporter: %v", err)
	}
	s := &Server{
		clusterID:               getClusterID(args),
		environment:             e,
		fileWatcher:             filewatcher.NewWatcher(),
		httpMux:                 http.NewServeMux(),
		monitoringMux:           http.NewServeMux(),
		readinessProbes:         make(map[string]readinessProbe),
		readinessFlags:          &readinessFlags{},
		server:                  server.New(),
		shutdownDuration:        args.ShutdownDuration,
		internalStop:            make(chan struct{}),
		istiodCertBundleWatcher: keycertbundle.NewWatcher(),
		webhookInfo:             &webhookInfo{},
		metricsExporter:         exporter,
		krtDebugger:             args.KrtDebugger,
	}

	// Apply custom initialization functions.
	for _, fn := range initFuncs {
		fn(s)
	}
	// Initialize workload Trust Bundle before XDS Server
	s.XDSServer = xds.NewDiscoveryServer(e, args.RegistryOptions.KubeOptions.ClusterAliases, args.KrtDebugger)
	configGen := core.NewConfigGenerator(s.XDSServer.Cache)

	// Apply the arguments to the configuration.
	if err := s.initKubeClient(args); err != nil {
		return nil, fmt.Errorf("error initializing kube client: %v", err)
	}

	s.initMeshConfiguration(args, s.fileWatcher)
	// Setup Kubernetes watch filters
	// Because this relies on meshconfig, it needs to be outside initKubeClient
	if s.kubeClient != nil {
		// Build a namespace watcher. This must have no filter, since this is our input to the filter itself.
		namespaces := kclient.New[*corev1.Namespace](s.kubeClient)
		filter := namespace.NewDiscoveryNamespacesFilter(namespaces, s.environment.Watcher, s.internalStop)
		s.kubeClient = kubelib.SetObjectFilter(s.kubeClient, filter)
	}

	s.initMeshNetworks(args, s.fileWatcher)
	s.initMeshHandlers(configGen.MeshConfigChanged)
	s.environment.Init()
	if err := s.environment.InitNetworksManager(s.XDSServer); err != nil {
		return nil, err
	}

	if err := s.initAnalyzerControllers(args); err != nil {
		return nil, err
	}

	InitGenerators(s.XDSServer, configGen, args.Namespace, s.clusterID, s.internalDebugMux)

	// This should be called only after controllers are initialized.
	s.initRegistryEventHandlers()

	s.initDiscoveryService()

	// This must be last, otherwise we will not know which informers to register
	if s.kubeClient != nil {
		s.addStartFunc("kube client", func(stop <-chan struct{}) error {
			s.kubeClient.RunAndWait(stop)
			return nil
		})
	}

	return s, nil
}

func (s *Server) initAnalyzerControllers(args *PilotArgs) error {
	log.Info("initializing controllers")
	s.initMulticluster(args)

	s.initSDSServer()

	if err := s.initConfigController(args); err != nil {
		return fmt.Errorf("error initializing config controller: %v", err)
	}
	if err := s.initServiceControllers(args); err != nil {
		return fmt.Errorf("error initializing service controllers: %v", err)
	}
	return nil
}

func (s *Server) Environment() *model.Environment {
	return s.environment
}
