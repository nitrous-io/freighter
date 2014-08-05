package dockertest

import (
	"net/http"

	"github.com/gorilla/mux"
)

var (
	ErrorResponse    = &response{501, `internal server error`}
	NotfoundResponse = &response{404, `not found`}
	OkResponse       = &response{200, ``}

	urlMapping = map[string]map[string]string{
		"GET": {
			"/containers/json":         "containers",
			"/containers/{id:.*}/json": "containers/id",
			"/images/json":             "images",
			"/images/{name:.*}/json":   "images/id",
			"/events":                  "events",
			"/ping":                    "ping",
		},
		"PUT": {},
		"POST": {
			"/commit":                     "commit",
			"/containers/create":          "containers/create",
			"/containers/{id:.*}/start":   "containers/start",
			"/containers/{id:.*}/stop":    "containers/stop",
			"/containers/{id:.*}/pause":   "containers/pause",
			"/containers/{id:.*}/unpause": "containers/unpause",
			"/containers/{id:.*}/wait":    "containers/wait",
			"/containers/{id:.*}/attach":  "containers/attach",
			"/containers/{id:.*}/restart": "containers/restart",
			"/images/create":              "images/create",
			"/images/{name:.*}/push":      "images/push",
			"/build":                      "build",
		},
		"DELETE": {
			"/containers/{id:.*}": "containers/id",
			"/images/{id:.*}":     "images/id",
		},
	}
)

// return the json string of the response
type responder func(w http.ResponseWriter, r *http.Request) *response

type response struct {
	code int
	data string
}

type Server struct {
	Router *mux.Router
	// collection of response and request per methods
	Responses map[string]map[string][]*response
	Requests  map[string]map[string][]*http.Request
	rCounter  map[string]map[string]int
}

func (s *Server) makeResponse(method, endpoint string) func(w http.ResponseWriter, r *http.Request) {
	cachedEndpoint := endpoint
	return func(w http.ResponseWriter, r *http.Request) {
		s.addRequests(method, cachedEndpoint, r)
		res := s.nextResponse(method, cachedEndpoint)
		if res.code == 0 {
			res.code = 200
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.code)
		w.Write([]byte(res.data))
	}
}

func (s *Server) nextResponse(method, endpoint string) *response {
	responses, ok := s.Responses[method][endpoint]
	if !ok {
		return NotfoundResponse
	}
	c, ok := s.rCounter[method][endpoint]
	if !ok {
		s.rCounter[method][endpoint] = 0
	}
	if len(responses) <= int(c) {
		return NotfoundResponse
	}
	r := responses[c]
	s.rCounter[method][endpoint] += 1
	return r
}

func (s *Server) addRequests(method, endpoint string, r *http.Request) {
	_, ok := s.Requests[method][endpoint]
	if ok {
		s.Requests[method][endpoint] = append(s.Requests[method][endpoint], r)
	} else {
		s.Requests[method][endpoint] = []*http.Request{r}
	}
}

func (s *Server) AddResponse(method, endpoint string, r *response) {
	_, ok := s.Responses[method][endpoint]
	if ok {
		s.Responses[method][endpoint] = append(s.Responses[method][endpoint], r)
	} else {
		s.Responses[method][endpoint] = []*response{r}
	}
	_, ok = s.rCounter[method][endpoint]
	if !ok {
		s.rCounter[method][endpoint] = 0
	}
}

func NewServer() *Server {
	s := &Server{
		Responses: map[string]map[string][]*response{
			"GET":    {},
			"POST":   {},
			"PUT":    {},
			"DELETE": {},
		},
		Requests: map[string]map[string][]*http.Request{
			"GET":    {},
			"POST":   {},
			"PUT":    {},
			"DELETE": {},
		},
		rCounter: map[string]map[string]int{
			"GET":    {},
			"POST":   {},
			"PUT":    {},
			"DELETE": {},
		},
	}
	r := mux.NewRouter()
	for method, mapping := range urlMapping {
		for route, name := range mapping {
			r.HandleFunc(route, s.makeResponse(method, name)).Methods(method)
		}
	}
	s.Router = r
	return s

}

func NewResponse(code int, message string) *response {
	return &response{code, message}
}
