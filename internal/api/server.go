package api

import (
	"net/http"

	"github.com/verl-project/rayjob-submitter/internal/config"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Server struct {
	cfg            config.Config
	typedClient    *kubernetes.Clientset
	dynamicClient  dynamic.Interface
	rayJobTemplate *unstructured.Unstructured
	mux            *http.ServeMux
}

func NewServer(cfg config.Config, typedClient *kubernetes.Clientset, dynamicClient dynamic.Interface, rayJobTemplate *unstructured.Unstructured) *Server {
	s := &Server{
		cfg:            cfg,
		typedClient:    typedClient,
		dynamicClient:  dynamicClient,
		rayJobTemplate: rayJobTemplate,
		mux:            http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return authMiddleware(s.cfg, s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("POST /api/v1/rayjobs", s.submitRayJob)
	s.mux.HandleFunc("GET /api/v1/rayjobs/{job_id}", s.getRayJob)
	s.mux.HandleFunc("GET /api/v1/rayjobs/{job_id}/watch", s.watchRayJob)
	s.mux.HandleFunc("GET /api/v1/rayjobs/{job_id}/logs/stream", s.streamRayJobLogs)
	s.mux.HandleFunc("DELETE /api/v1/workflows/{github_run_id}", s.cleanupWorkflow)
}
