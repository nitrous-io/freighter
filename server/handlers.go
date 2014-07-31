package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/pkg/flusher"
	"github.com/nitrous-io/freighter/pkg/shell"
	"github.com/nitrous-io/freighter/pkg/stats"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker"
)

const StatusUnprocessableEntity = 422

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

	if name == "" || !provider.IsValidName(name) {
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"name is not valid"}`))
		return
	} else if image == "" {
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"image is not valid"}`))
		return
	}

	list, err := s.provider.List()
	if err != nil {
		s.handleError(w, err)
		return
	}
	for _, c := range list {
		if c.Name == name {
			s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"name is already taken"}`))
			return
		}
	}

	info, err := s.provider.Create(name, image, r.FormValue("config"))

	if err != nil {
		s.logger.Error(err)
		s.respondEncodeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
	} else {
		s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"container": info})
	}
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
	config := r.FormValue("config")
	curryStart := func(id string) (*provider.ContainerInfo, error) {
		return s.provider.Start(id, config)
	}

	s.performContainerControl(w, r, curryStart, "started")
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
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"public_key[] is missing"}`))
		return
	}

	err = s.provider.AddPublicKeys(id, publicKeys)
	if err != nil {
		if err == provider.ErrPubKeyOpsNotSupported {
			s.respondEncodeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		} else {
			s.handleError(w, err)
		}

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

func (s *Server) clearPublicKeys(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	err := s.provider.ClearPublicKeys(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respond(w, http.StatusOK, []byte{})
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) error {
	slug := r.FormValue("slug")
	vi, err := s.provider.CreateVolume(slug)
	if err != nil {
		// temporary workaround until we start using httperror
		if strings.HasPrefix(err.Error(), "volume") {
			s.respond(w, 422, []byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
			return nil
		}
		return err
	}
	s.respondEncodeJSON(w, http.StatusCreated, map[string]interface{}{"volume": vi})
	return nil
}

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) error {
	volumes, err := s.provider.ListVolumes()
	if err != nil {
		return err
	}
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"volumes": volumes})
	return nil
}

func (s *Server) getVolumeInfo(w http.ResponseWriter, r *http.Request) error {
	slug := mux.Vars(r)["slug"]
	vi, err := s.provider.GetVolumeInfo(slug)
	if err != nil {
		// temporary workaround until we start using httperror
		if strings.HasPrefix(err.Error(), "volume") {
			s.respond(w, 422, []byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
			return nil
		}
		return err
	}
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"volume": vi})
	return nil
}

func (s *Server) removeVolume(w http.ResponseWriter, r *http.Request) error {
	slug := mux.Vars(r)["slug"]
	err := s.provider.RemoveVolume(slug)
	if err != nil {
		// temporary workaround until we start using httperror
		if strings.HasPrefix(err.Error(), "volume") {
			s.respond(w, 422, []byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
			return nil
		}
		return err
	}
	s.respond(w, http.StatusOK, jsonStatusOK)
	return nil
}

// export content of the container as a .tar stream
func (s *Server) exportContainer(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	w.Header().Set("Content-Type", "application/octet-stream")
	err := s.provider.Export(id, w)
	if err != nil {
		s.handleError(w, err)
	}
}

func (s *Server) getContainerLogs(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	// convert to bool, ignore the errors as the value will be false when error
	// happens
	follow, _ := strconv.ParseBool(r.FormValue("follow"))
	timestamps, _ := strconv.ParseBool(r.FormValue("timestamps"))
	tail := r.FormValue("tail")

	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")

	// write the logs to response writer
	outstream := flusher.NewWriteFlusher(w)
	err := s.provider.Logs(id, outstream, follow, timestamps, tail)
	if err != nil {
		s.handleError(w, err)
	}
}

func (s *Server) createShell(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	info, err := s.provider.GetInfo(id)
	if err != nil {
		return err
	}
	containerID := info.ID
	if info.State != provider.ContainerStateRunning {
		return fmt.Errorf("Cannot attach to stopped container %v", containerID)
	}

	command := r.FormValue("command")
	width := getFormInt(r, "width")
	height := getFormInt(r, "height")

	shell, err := shell.OpenShell(containerID, command, width, height)
	if err != nil {
		fmt.Println(err)
		return err
	}
	shellID := randomString()
	s.shells[shellID] = shell
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"id": shellID})
	return nil
}

func (s *Server) attachShell(w http.ResponseWriter, r *http.Request) error {
	shellID := mux.Vars(r)["id"]
	shell, ok := s.shells[shellID]
	if !ok {
		return fmt.Errorf("Invalid shell %v", shellID)
	}

	stdin, stdout, err := hijackServer(w)
	if err != nil {
		shell.Close()
		return err
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
	return nil
}

func (s *Server) resizeShell(w http.ResponseWriter, r *http.Request) error {
	shellID := mux.Vars(r)["id"]
	shell, ok := s.shells[shellID]
	if !ok {
		return fmt.Errorf("Invalid shell %v", shellID)
	}

	width := getFormInt(r, "width")
	height := getFormInt(r, "height")
	if err := shell.Resize(width, height); err != nil {
		return fmt.Errorf("Resize failed %v", err)
	}
	s.respond(w, http.StatusAccepted, jsonStatusAccepted)
	return nil
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	listing, err := s.provider.ListImages()
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"images": listing})
}

func (s *Server) loginRegistry(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	email := r.FormValue("email")
	registry := r.FormValue("registry")

	status := http.StatusCreated
	err := s.provider.Login(username, password, email, registry)
	if err != nil {
		status = StatusUnprocessableEntity
	}
	s.respond(w, status, nil)
}

func (s *Server) logoutRegistry(w http.ResponseWriter, r *http.Request) {
	registry := r.FormValue("registry")

	status := http.StatusNoContent
	err := s.provider.Logout(registry)
	if err != nil {
		status = StatusUnprocessableEntity
	}
	s.respond(w, status, nil)
}

func (s *Server) serverInfo(w http.ResponseWriter, r *http.Request) error {
	// FIXME Hardcoded sample period for now - maybe make it a query param.
	stats, err := stats.Sample(100 * time.Millisecond)
	if err != nil {
		return err
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"info": stats})
	return nil
}

func (s *Server) buildImage(w http.ResponseWriter, r *http.Request) {
	fn, _, err := r.FormFile("file")
	if err != nil {
		s.handleError(w, err)
		return
	}
	defer fn.Close()

	name := r.FormValue("name")
	var buildBuffer bytes.Buffer
	info, err := s.provider.BuildImage(name, fn, &buildBuffer)
	if err != nil {
		output := buildBuffer.String()
		if output == "" {
			s.handleError(w, err)
		} else {
			s.respondEncodeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error(), "output": output})
			return
		}
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"image": info, "output": buildBuffer.String()})
}

func (s *Server) removeImage(w http.ResponseWriter, r *http.Request) error {
	slug := mux.Vars(r)["slug"]
	err := s.provider.RemoveImage(slug)
	if err != nil {
		return err
	}
	s.respond(w, http.StatusOK, jsonStatusOK)
	return nil
}

func (s *Server) pullImage(w http.ResponseWriter, r *http.Request) error {
	repo := r.FormValue("repository")
	info, err := s.provider.PullImage(repo)
	if err != nil {
		return err
	}
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"image": info})
	return nil
}

func (s *Server) pushImage(w http.ResponseWriter, r *http.Request) error {
	slug := mux.Vars(r)["slug"]
	location := mux.Vars(r)["location"]
	err := s.provider.PushImage(slug, location)
	if err != nil {
		return err
	}
	s.respond(w, http.StatusOK, jsonStatusOK)
	return nil
}

func (s *Server) tagImage(w http.ResponseWriter, r *http.Request) error {
	slug := mux.Vars(r)["slug"]
	tag := mux.Vars(r)["tag"]
	force := false
	if mux.Vars(r)["force"] == "true" {
		force = true
	}
	err := s.provider.TagImage(slug, tag, force)
	if err != nil {
		return err
	}
	s.respond(w, http.StatusOK, jsonStatusOK)
	return nil
}

func (s *Server) exportImage(w http.ResponseWriter, r *http.Request) error {
	prov, ok := s.provider.(*docker.DockerProvider)
	if !ok {
		return errors.New("Provider does not support export image.")
	}

	slug := mux.Vars(r)["slug"]
	w.Header().Set("Content-Type", "application/octet-stream")
	err := prov.ExportImage(slug, w)
	if err != nil {
		return err
	}
	return nil
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

func getFormInt(r *http.Request, k string) int {
	v, err := strconv.Atoi(r.FormValue(k))
	if err != nil {
		return 0
	}
	return v
}
