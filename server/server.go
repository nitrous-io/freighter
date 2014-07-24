package server

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/pkg/shell"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/util"
)

type Server struct {
	logger   *util.Logger
	provider provider.Provider
	router   *mux.Router

	// collection of current attached shells
	shells map[string]*shell.Shell
}

func NewServer(logger *util.Logger, provider provider.Provider) *Server {
	if logger == nil || provider == nil {
		panic("server.NewServer: invalid argument")
	}
	svr := &Server{
		logger:   logger,
		provider: provider,
		shells:   map[string]*shell.Shell{},
	}

	svr.router = mux.NewRouter()
	svr.drawRoutes()
	return svr
}

func (s *Server) Start(host string, port uint16) {
	http.Handle("/", s.router)
	svr := &http.Server{Addr: fmt.Sprintf("%s:%d", host, port)}
	err := svr.ListenAndServe()
	s.logger.FatalExit(err)
}

func (s *Server) Router() *mux.Router {
	return s.router
}

func (s *Server) route(route string, handler http.HandlerFunc, methods ...string) {
	h := func(w http.ResponseWriter, r *http.Request) {
		s.logger.Infof("Request: %v %v", r.Method, r.URL.String())
		handler(w, r)
	}
	s.router.HandleFunc(route, h).Methods(methods...)
}

func (s *Server) drawRoutes() {
	s.route("/", s.home, "GET")
	s.route("/containers", s.listContainers, "GET")
	s.route("/containers", s.createContainer, "POST")
	s.route("/containers/{id}", s.getContainerInfo, "GET")
	s.route("/containers/{id}", s.terminateContainer, "DELETE")
	s.route("/containers/{id}/start", s.startContainer, "POST")
	s.route("/containers/{id}/stop", s.stopContainer, "POST")
	s.route("/containers/{id}/restart", s.restartContainer, "POST")
	s.route("/containers/{id}/export", s.exportContainer, "GET")
	s.route("/containers/{id}/public_keys", s.listPublicKeys, "GET")
	s.route("/containers/{id}/public_keys", s.addPublicKeys, "POST", "PUT")
	s.route("/containers/{id}/public_keys", s.clearPublicKeys, "DELETE")
	s.route("/containers/{id}/public_keys/{key_fingerprint}", s.removePublicKey, "DELETE")
	s.route("/containers/{id}/logs", s.getContainerLogs, "GET")

	s.route("/containers/{id}/shells", s.wrapError(s.createShell), "POST")
	s.route("/shells/{id}/attach", s.wrapError(s.attachShell), "POST")
	s.route("/shells/{id}/resize", s.wrapError(s.resizeShell), "POST")

	s.route("/registry/auth", s.loginRegistry, "POST")
	s.route("/registry/auth", s.logoutRegistry, "DELETE")

	s.route("/images", s.listImages, "GET")
	s.route("/images", s.buildImage, "POST")

	// volumes
	s.route("/volumes", s.wrapError(s.listVolumes), "GET")
	s.route("/volumes", s.wrapError(s.createVolume), "POST")
	s.route("/volumes/{slug}", s.wrapError(s.removeVolume), "DELETE")
	s.route("/volumes/{slug}", s.wrapError(s.getVolumeInfo), "GET")

	s.route("/info", s.wrapError(s.serverInfo), "GET")
}
