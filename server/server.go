package server

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/util"
)

type Server struct {
	logger   *util.Logger
	provider provider.Provider
	callback Callback
	router   *mux.Router
}

func NewServer(logger *util.Logger, provider provider.Provider, callback Callback) *Server {
	if logger == nil || provider == nil || callback == nil {
		panic("server.NewServer: invalid argument")
	}
	svr := &Server{logger: logger, provider: provider, callback: callback}
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
}
