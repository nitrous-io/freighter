package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/pkg/shell"
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
	publicKeys := r.PostForm["public_key[]"]

	if publicKeys == nil {
		publicKeys = []string{}
	}

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
		info, err := s.provider.Create(name, image, publicKeys)
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

func (s *Server) listPublicKeys(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	allPubKeys, err := s.provider.ListPublicKeys(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"public_keys": allPubKeys})
}

func (s *Server) addPublicKeys(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	err := r.ParseForm()
	if err != nil {
		s.handleError(w, err)
		return
	}

	publicKeys := r.PostForm["public_key[]"]

	if publicKeys == nil {
		s.respond(w, 422, []byte(`{"error":"public_key[] is missing"}`))
		return
	}

	err = s.provider.AddPublicKeys(id, publicKeys)
	if err != nil {
		s.handleError(w, err)
		return
	}

	allPubKeys, err := s.provider.ListPublicKeys(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"public_keys": allPubKeys})
}

func (s *Server) removePublicKey(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	keyFingerprint := mux.Vars(r)["key_fingerprint"]

	err := s.provider.RemovePublicKey(id, keyFingerprint)
	if err != nil {
		s.handleError(w, err)
		return
	}

	allPubKeys, err := s.provider.ListPublicKeys(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"public_keys": allPubKeys})
}

func (s *Server) createShell(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	info, err := s.provider.GetInfo(id)
	if err != nil {
		s.handleError(w, err)
		return
	}
	if info.State != provider.ContainerStateRunning {
		s.handleError(w, fmt.Errorf("Cannot attach to stopped container %v", id))
		return
	}

	username := r.FormValue("username")
	term := r.FormValue("term")
	width, err := strconv.Atoi(r.FormValue("width"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	height, err := strconv.Atoi(r.FormValue("height"))
	if err != nil {
		s.handleError(w, err)
		return
	}

	shell, err := shell.NewShell(id, username, term, width, height)
	if err != nil {
		s.handleError(w, err)
		return
	}
	shellId := randomString()
	s.shells[shellId] = shell
	s.respondEncodeJson(w, http.StatusOK, map[string]interface{}{"id": shellId})
}

func (s *Server) attachShell(w http.ResponseWriter, r *http.Request) {
	shellId := mux.Vars(r)["id"]
	shell, ok := s.shells[shellId]
	if !ok {
		s.handleError(w, fmt.Errorf("Invalid shell %v", shellId))
		return
	}

	stdin, stdout, err := hijackServer(w)
	if err != nil {
		shell.Close()
		s.handleError(w, err)
		return
	}

	defer func() {
		if tcpc, ok := stdin.(*net.TCPConn); ok {
			tcpc.CloseWrite()
		} else {
			stdin.Close()
		}

		if tcpc, ok := stdout.(*net.TCPConn); ok {
			tcpc.CloseWrite()
		} else if closer, ok := stdout.(io.Closer); ok {
			closer.Close()
		}
	}()

	<-shell.Attach(stdin, stdout)
}

func (s *Server) resizeShell(w http.ResponseWriter, r *http.Request) {
	shellId := mux.Vars(r)["id"]
	shell, ok := s.shells[shellId]
	if !ok {
		s.handleError(w, fmt.Errorf("Invalid shell %v", shellId))
		return
	}

	width, err := strconv.Atoi(r.FormValue("width"))
	if err != nil {
		s.handleError(w, fmt.Errorf("Invalid width %v", err))
		return
	}

	height, err := strconv.Atoi(r.FormValue("height"))
	if err != nil {
		s.handleError(w, fmt.Errorf("Invalid height %v", err))
		return
	}

	if err := shell.Resize(width, height); err != nil {
		s.handleError(w, fmt.Errorf("Resize failed %v", err))
		return
	}

	s.respond(w, http.StatusAccepted, jsonStatusAccepted)
}

func hijackServer(w http.ResponseWriter) (io.ReadCloser, io.Writer, error) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	// Flush the options to make sure the client sets the raw mode
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/vnd.containerd.raw-stream\r\n\r\n")
	return conn, conn, nil
}

func randomString() string {
	id := make([]byte, 32)
	_, err := io.ReadFull(rand.Reader, id)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(id)
}
