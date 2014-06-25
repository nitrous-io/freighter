package server

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/provider"
)

var (
	jsonStatusOK       = []byte(`{"status":"ok"}`)
	jsonStatusAccepted = []byte(`{"status":"accepted"}`)
	jsonErrorNotFound  = []byte(`{"error":"no such container"}`)
)

type httpFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) wrapError(handler httpFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := handler(w, r); err != nil {
			s.handleError(w, err)
			return
		}
	}
}

func (s *Server) handleError(w http.ResponseWriter, err error) {
	s.logger.Error(err)
	if err != nil {
		if s.provider.IsNotFoundError(err) {
			s.respond(w, http.StatusNotFound, jsonErrorNotFound)
		} else {
			s.respondWith500(w, err.Error())
		}
	}
}

func (s *Server) respond(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(data)
	s.logger.Infof("Response: %v %v", status, string(data))
}

func (s *Server) respondEncodeJSON(w http.ResponseWriter, status int, data map[string]interface{}) {
	j, err := json.Marshal(data)
	if err != nil {
		s.respondWith500(w, "json marshaling failed")
		return
	}
	s.respond(w, status, j)
}

func (s *Server) respondWith500(w http.ResponseWriter, errMsg string) {
	e := map[string]string{"error": errMsg}
	j, err := json.Marshal(e)
	if err != nil {
		j = []byte("json marshaling failed")
	}
	s.respond(w, http.StatusInternalServerError, []byte(j))
}

func (s *Server) performContainerControl(w http.ResponseWriter, r *http.Request, controlFunc func(string) (*provider.ContainerInfo, error), successStatus string) {
	id := mux.Vars(r)["id"]
	callbackURL := r.FormValue("callback_url")
	state := r.FormValue("state")

	_, err := s.provider.GetInfo(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	go func() {
		info, err := controlFunc(id)

		s.invokeCallback(callbackURL, state, map[string]interface{}{
			"result":    successStatus,
			"container": info,
		}, err)
	}()

	s.respond(w, http.StatusAccepted, jsonStatusAccepted)
}

func (s *Server) invokeCallback(callbackURL string, state string, data map[string]interface{}, err error) {
	if callbackURL == "" {
		return
	}
	var res map[string]interface{}

	if err != nil {
		s.logger.Error(err)
		res = map[string]interface{}{"error": err.Error()}
	} else {
		res = data
	}
	if state != "" {
		res["state"] = state
	}
	j, err := json.Marshal(res)
	if err != nil {
		s.logger.Error(err)
		return
	}
	sj := string(j)
	s.logger.Infof("Callback: %v %v", callbackURL, sj)
	s.callback.Invoke(callbackURL, sj)
}
