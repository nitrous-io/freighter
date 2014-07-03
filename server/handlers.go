package server

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/provider"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.respond(w, http.StatusOK, jsonStatusOK)
}

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	list, err := s.provider.List()
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"containers": list})
}

func (s *Server) createContainer(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	image := r.FormValue("image")
	callbackURL := r.FormValue("callback_url")
	state := r.FormValue("state")

	if name == "" || !provider.IsValidName(name) {
		s.respond(w, 422, []byte(`{"error":"name is not valid"}`))
		return
	} else if image == "" {
		s.respond(w, 422, []byte(`{"error":"image is not valid"}`))
		return
	}

	list, err := s.provider.List()
	if err != nil {
		s.handleError(w, err)
		return
	}
	for _, c := range list {
		if c.Name == name {
			s.respond(w, 422, []byte(`{"error":"name is already taken"}`))
			return
		}
	}

	go func() {
		info, err := s.provider.Create(name, image)
		s.invokeCallback(callbackURL, state, map[string]interface{}{
			"result":    "created",
			"container": info,
		}, err)
	}()

	s.respond(w, http.StatusAccepted, jsonStatusAccepted)
}

func (s *Server) getContainerInfo(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	info, err := s.provider.GetInfo(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"container": info})
}

func (s *Server) terminateContainer(w http.ResponseWriter, r *http.Request) {
	s.performContainerControl(w, r, s.provider.Terminate, "terminated")
}

func (s *Server) startContainer(w http.ResponseWriter, r *http.Request) {
	s.performContainerControl(w, r, s.provider.Start, "started")
}

func (s *Server) stopContainer(w http.ResponseWriter, r *http.Request) {
	s.performContainerControl(w, r, s.provider.Stop, "stopped")
}

func (s *Server) restartContainer(w http.ResponseWriter, r *http.Request) {
	s.performContainerControl(w, r, s.provider.Restart, "restarted")
}
