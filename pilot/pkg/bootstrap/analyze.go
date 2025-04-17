package bootstrap

import (
	"encoding/json"
	"net/http"

	"istio.io/istio/pkg/cluster"
	"istio.io/istio/pkg/config/describe"
	proxyutils "istio.io/istio/pkg/config/describe/proxy"
	"k8s.io/client-go/kubernetes"
)

func (s *Server) initAnalyze() {
	s.httpMux.HandleFunc("/analyze/pod", s.analyzePushContextForPod)
}

func (s *Server) analyzePushContextForPod(w http.ResponseWriter, req *http.Request) {
	ps := s.environment.PushContext()
	name := req.URL.Query().Get("name")
	namespace := req.URL.Query().Get("namespace")
	clusterId := req.URL.Query().Get("cluster")
	if name == "" || namespace == "" || clusterId == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("name, namespace, and cluster are required"))
		return
	}

	var err error
	var kubeClient kubernetes.Interface
	if clusterId == string(s.clusterID) {
		kubeClient = s.kubeClient.Kube()
	} else {
		kubeClient = s.multiclusterController.GetRemoteKubeClient(cluster.ID(clusterId))
	}

	if kubeClient == nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("no kube client found for cluster " + clusterId))
		return
	}

	nb := proxyutils.NewProxyBuilder(s.environment, kubeClient)
	proxy, err := nb.BuildProxy(name, namespace)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	analyzeContext := describe.NewDescribeContext(s.environment, ps, proxy)
	res := describe.DescribeAll(analyzeContext)
	ret, err := json.MarshalIndent(res, "", "    ")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	w.Write(ret)
	w.WriteHeader(http.StatusOK)
}
