package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/nitrous-io/freighter/pkg/flusher"
	"github.com/nitrous-io/freighter/pkg/shell"
	"github.com/nitrous-io/freighter/pkg/stats"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/nitrous-common/wsconn"
)

const StatusUnprocessableEntity = 422

// default size of the shell's message queue
const messageQueueSize = 10

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// allow all origins here
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.respond(w, http.StatusOK, jsonStatusOK)
}

func (s *Server) handleCORSPreflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if h := r.Header["Access-Control-Request-Headers"]; h != nil {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(h, ", "))
	}
	w.Header().Set("Access-Control-Allow-Credentials", "true")
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

func (s *Server) reconfigureContainer(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	info, err := s.provider.Reconfig(id, r.FormValue("config"))
	if err != nil {
		s.logger.Error(err)
		code := http.StatusInternalServerError
		if err == provider.ErrImageNotFound {
			code = http.StatusNotFound
		}
		s.respondEncodeJSON(w, code, map[string]interface{}{"error": err.Error()})
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"container": info})
}

func (s *Server) createContainer(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	image := r.FormValue("image")
	secret := r.FormValue("secret")

	if name == "" || !provider.IsValidName(name) {
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"name is not valid"}`))
		return
	} else if image == "" {
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"image is not valid"}`))
		return
	} else if secret == "" {
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"secret is not valid"}`))
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

	info, err := s.provider.CreateStart(name, image, secret, r.FormValue("config"))
	_, startFailed := err.(*provider.StartFailedError)
	switch {
	case err == nil:
		s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"container": info})
	case startFailed:
		s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{
			"container": info,
			"status":    "start_failed",
			"error":     err.Error(),
		})
	case err == provider.ErrImageNotFound:
		s.respondEncodeJSON(w, http.StatusNotFound, map[string]interface{}{"error": err.Error()})
	case err == provider.ErrCmdConfig:
		s.respondEncodeJSON(w, StatusUnprocessableEntity, map[string]interface{}{"error": err.Error()})
	default:
		s.respondEncodeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
	}
}

func (s *Server) commitContainer(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	name := r.FormValue("name")
	if name == "" {
		s.respond(w, StatusUnprocessableEntity, []byte(`{"error":"name is missing"}`))
		return
	}

	image, err := s.provider.Commit(id, name)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"image": image})
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

func (s *Server) getContainerConfig(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	config, err := s.provider.GetConfig(id)
	if err != nil {
		s.handleError(w, err)
		return
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{
		"config": config,
	})
}

func (s *Server) terminateContainer(w http.ResponseWriter, r *http.Request) {
	s.performContainerControl(w, r,
		func(id string) (*provider.ContainerInfo, error) {
			return s.provider.Terminate(id, true)
		},
		"terminated")
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

func (s *Server) wsContainerLogs(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]

	// convert to bool, ignore the errors as the value will be false when error
	// happens
	follow, _ := strconv.ParseBool(r.FormValue("follow"))
	timestamps, _ := strconv.ParseBool(r.FormValue("timestamps"))
	tail := r.FormValue("tail")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("failed to upgrade websocket: %v", err)
	}
	stream := wsconn.NewConnectionHeartbeat(conn, true)
	defer stream.Close()

	return s.provider.Logs(id, shell.NewShellWriter(stream), follow, timestamps, tail)
}

// wsNewShell creates a new shell to a container using protocol specified in
// pkg/shell/protocol.go
func (s *Server) wsNewShell(w http.ResponseWriter, r *http.Request) error {
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
	// check if the client request to use BinaryMessage instead of TextMessage
	binary, _ := strconv.ParseBool(r.FormValue("binary"))
	// check if the client wants to have a TTY. Usually the client who wants a
	// stdin pipe will not need a TTY
	noTTY, _ := strconv.ParseBool(r.FormValue("notty"))

	log.Printf("[INFO]: starting a new shell on %s with command `%s`", containerID, command)
	sh, err := shell.OpenShell(containerID, command, width, height, noTTY, info.Env)
	if err != nil {
		fmt.Println("wsNewShell: failed to open a shell for container %v", containerID, err)
		return err
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("failed to upgrade websocket: %v", err)
	}
	stream := wsconn.NewConnectionHeartbeat(conn, true)
	if binary {
		stream.MessageType = websocket.BinaryMessage
	}
	defer stream.Close()

	// given the shell an ID so we can multiplex the shell IO later given its
	// id i.e. gomux style
	shellID := randomString()
	s.shells[shellID] = sh

	// create stdin/stdout stream with the shell protocol
	msgCh := make(chan shell.Message, messageQueueSize)
	stdin := shell.NewShellReader(conn, msgCh, stream.MessageType)
	stdout := shell.NewShellWriter(stream)

	ch := sh.Attach(stdin, stdout)
	for {
		select {
		case <-ch:
			stdout.Close() // Inform client that server has closed connection.
			return nil
		case msg := <-msgCh:
			switch msg := msg.(type) {
			case *shell.ResizeMsg:
				sh.Resize(msg.Width, msg.Height)
			default:
				log.Println("shell: got unknown msg %v", msg)
			}
		}
	}
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

var defaultPeriod = 100 * time.Millisecond

func (s *Server) serverInfo(w http.ResponseWriter, r *http.Request) error {
	period, err := time.ParseDuration(r.FormValue("period"))
	if err != nil || period == 0 {
		period = defaultPeriod
	}
	stats, err := stats.Sample(period)
	if err != nil {
		return err
	}
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"info": stats})
	return nil
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) error {
	images, err := s.provider.ListImages()
	if err != nil {
		return err
	}

	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"images": images})
	return nil
}

func (s *Server) buildImage(w http.ResponseWriter, r *http.Request) error {
	outstream := flusher.NewWriteFlusher(w)
	opts := provider.BuildImageOptions{
		Name:          r.FormValue("name"),
		OutputStream:  outstream,
		RawJSONStream: true,
	}

	if remote := r.FormValue("remote"); remote != "" {
		opts.Remote = remote
	} else {
		_, fh, err := r.FormFile("file")
		if err != nil {
			return err
		}

		f, err := fh.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		opts.InputStream = f
	}

	if err := s.provider.BuildImage(opts); err != nil {
		return err
	}
	return nil
}

func (s *Server) removeImage(w http.ResponseWriter, r *http.Request) error {
	image := r.FormValue("image")
	err := s.provider.RemoveImage(image)
	if err != nil {
		if err == provider.ErrImageInUse {
			s.respondEncodeJSON(w, http.StatusConflict, map[string]interface{}{"error": err.Error()})
			return nil
		}
		return err
	}
	s.respond(w, http.StatusOK, jsonStatusOK)
	return nil
}

// pullImage outputs raw docker's raw json stream to the client so it can
// format the download progress by itself
//
// Refers to // https://docs.docker.com/reference/api/docker_remote_api_v1.13/#22-images
// for more information on the output format
func (s *Server) pullImage(w http.ResponseWriter, r *http.Request) error {
	update, err := strconv.ParseBool(r.FormValue("update"))
	if err != nil {
		update = true
	}
	repo := r.FormValue("repository")

	if !update {
		// Don't update if image already exists.
		if _, err := s.provider.InspectImage(repo, false); err == nil {
			s.respondEncodeJSON(w, http.StatusNotModified, nil)
			return nil
		}
	}

	outstream := flusher.NewWriteFlusher(w)
	if err := s.provider.PullImage(repo, outstream, true); err != nil {
		return err
	}
	return nil
}

func (s *Server) pushImage(w http.ResponseWriter, r *http.Request) error {
	image := r.FormValue("image")
	destination := r.FormValue("destination")

	outstream := flusher.NewWriteFlusher(w)
	if err := s.provider.PushImage(image, destination, outstream, true); err != nil {
		if err == provider.ErrImagePushAuthRequired {
			s.respondEncodeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) tagImage(w http.ResponseWriter, r *http.Request) error {
	image := r.FormValue("image")
	tag := r.FormValue("tag")
	force, _ := strconv.ParseBool(r.FormValue("force"))
	err := s.provider.TagImage(image, tag, force)
	if err != nil {
		return err
	}
	s.respond(w, http.StatusOK, jsonStatusOK)
	return nil
}

func (s *Server) inspectImage(w http.ResponseWriter, r *http.Request) error {
	image := r.FormValue("image")
	if image == "" {
		s.respondEncodeJSON(w, StatusUnprocessableEntity, map[string]interface{}{
			"error": "image is not valid",
		})
		return nil
	}
	remote, err := strconv.ParseBool(r.FormValue("remote"))
	if err != nil {
		remote = true
	}
	info, err := s.provider.InspectImage(image, remote)
	if err != nil {
		return err
	}
	s.respondEncodeJSON(w, http.StatusOK, map[string]interface{}{"image": info})
	return nil
}

func (s *Server) wsAttachToContainer(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	info, err := s.provider.GetInfo(id)
	if err != nil {
		return err
	}
	if info.State != provider.ContainerStateRunning {
		return fmt.Errorf("Cannot attach to stopped container %v", info.ID)
	}

	// check if the client request to use BinaryMessage instead of TextMessage
	binary, _ := strconv.ParseBool(r.FormValue("binary"))
	// if no-stdin in param is set, STDIN should not be attached
	noStdin, _ := strconv.ParseBool(r.FormValue("nostdin"))

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("failed to upgrade websocket: %v", err)
	}
	stream := wsconn.NewConnectionHeartbeat(conn, true)
	if binary {
		stream.MessageType = websocket.BinaryMessage
	}
	defer stream.Close()

	// create stdin/stdout stream with the shell protocol
	msgCh := make(chan shell.Message, messageQueueSize)
	stdin := shell.NewShellReader(conn, msgCh, stream.MessageType)
	stdout := shell.NewShellWriter(stream)
	exit := make(chan struct{})

	go func() {
		defer close(exit)
		if err := s.provider.AttachToContainer(id, stdin, stdout, noStdin); err != nil {
			// Connection is already hijacked,
			// log the error and close the connection
			s.logger.Errorf("attach: %s", err)
			stream.Write([]byte("c|an error occurred"))
		}
	}()

	for {
		select {
		case <-exit:
			return nil
		case msg := <-msgCh:
			switch msg := msg.(type) {
			case *shell.ResizeMsg:
				err = s.provider.ResizeContainerTTY(id, msg.Height, msg.Width)
			default:
				s.logger.Info("attach: got unknown msg %v", msg)
			}
		}
	}
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
