package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/gorilla/mux"
	"github.com/nitrous-io/freighter/pkg/shell"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/util"
)

// default server's configuration when we dont have a valid configuration
var DefaultConfig *Config

func init() {
	DefaultConfig = &Config{
		DataPath:  "/var/lib/freighter",
		TLSVerify: true,
		CA:        "/etc/freighterd/ca.crt",
		Cert:      "/etc/freighterd/cert.crt",
		Key:       "/etc/freighterd/key.crt",
		Secret:    "/etc/freighterd/secret",

		Listen: []HostAddress{
			{Proto: "unix", Address: "/var/run/freighterd.sock"},
			{Proto: "tcp", Address: "0.0.0.0:65333"},
		},
	}
}

// address that the server will listen at. For example
// { "proto": "unix", "address": "/var/run/docker.sock" }
// { "proto": "tcp", "address": "0.0.0.0:65333" }
type HostAddress struct {
	Proto   string `json:"proto"`
	Address string `json:"address"`
}

// server config
type Config struct {
	DataPath string `json:"data_path"`

	TLSVerify bool   `json:"tls_verify"` // whether we want to use TLS mode for all HTTP hosts
	CA        string `json:"tls_ca"`     // path to CA
	Cert      string `json:"tls_cert"`   // path to cert
	Key       string `json:"tls_key"`    // path to key
	Secret    string `json:"secret"`     // path to secret

	Listen []HostAddress `json:"listen"`
}

type Server struct {
	logger   *util.Logger
	provider provider.Provider
	router   *mux.Router
	conf     *Config
	secret   string
	// collection of current attached shells
	shells map[string]*shell.Shell
}

func NewServer(logger *util.Logger, provider provider.Provider, conf *Config) *Server {
	if logger == nil || provider == nil || conf == nil {
		panic("server.NewServer: invalid argument")
	}
	svr := &Server{
		logger:   logger,
		provider: provider,
		conf:     conf,
		shells:   map[string]*shell.Shell{},
	}

	svr.router = mux.NewRouter()
	svr.drawRoutes()
	return svr
}

func (s *Server) Start() {
	chErrors := make(chan error, len(s.conf.Listen))
	for _, h := range s.conf.Listen {
		proto, addr := h.Proto, h.Address
		switch h.Proto {
		case "unix":
			go func() {
				s.logger.Infof("Listening on %s %s", proto, addr)
				chErrors <- s.serveUnix(proto, addr)
			}()
		case "tcp":
			go func() {
				s.logger.Infof("Listening on %s %s", proto, addr)
				chErrors <- s.serveTCP(proto, addr)
			}()
		}
	}

	for i := 0; i < len(s.conf.Listen); i++ {
		if err := <-chErrors; err != nil {
			s.logger.FatalExit(err)
		}
	}
}

func (s *Server) serveUnix(proto, addr string) error {
	if err := syscall.Unlink(addr); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("server: cannot remove %s, %v", addr, err)
	}
	l, err := net.Listen(proto, addr)
	if err != nil {
		return fmt.Errorf("server: cannot listen to unix %v, %v", addr, err)
	}
	svr := &http.Server{Addr: addr, Handler: s.router}
	return svr.Serve(l)
}

func (s *Server) serveTCP(proto, addr string) error {
	var l net.Listener
	l, err := net.Listen(proto, addr)
	if err != nil {
		return fmt.Errorf("server: cannot listen to tcp %v, %v", addr, err)
	}
	if s.conf.TLSVerify {
		tlsConfig, err := getTLSConfig(s.conf.CA, s.conf.Cert, s.conf.Key)
		if err != nil {
			return fmt.Errorf("server: invalid tls configs %v", err)
		}
		l = tls.NewListener(l, tlsConfig)
	}
	if s.conf.Secret != "" {
		secret, err := ioutil.ReadFile(s.conf.Secret)
		if err != nil {
			return fmt.Errorf("Error reading secret at %s: %s", s.conf.Secret, err)
		}
		s.secret = strings.TrimSpace(string(secret))
	}
	svr := &http.Server{Addr: addr, Handler: s.router}
	return svr.Serve(l)
}

func (s *Server) Router() *mux.Router {
	return s.router
}

func (s *Server) secretRoute(route string, handler http.HandlerFunc, methods ...string) {
	h := func(w http.ResponseWriter, r *http.Request) {

		if secret := r.Header.Get("X-Freighter-Secret"); secret != s.secret {
			errMsg := "invalid_secret"
			if secret == "" {
				errMsg = "missing_secret"
			}
			s.respondEncodeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": errMsg})
			return
		}

		// authentication succeed, process the middleware as normal now
		handler(w, r)
	}
	s.router.HandleFunc(route, h).Methods(methods...)
}

// unauthedRoute is for routes that have no secret-based authentication (either
// no authentication at all, or handled within the route itself)
func (s *Server) route(route string, handler http.HandlerFunc, methods ...string) {
	s.router.HandleFunc(route, handler).Methods(methods...)
}

func (s *Server) drawRoutes() {
	s.route("/", s.home, "GET")
	s.secretRoute("/containers", s.listContainers, "GET")
	s.secretRoute("/containers", s.createContainer, "POST")
	s.secretRoute("/containers/{id}", s.getContainerInfo, "GET")
	s.secretRoute("/containers/{id}", s.terminateContainer, "DELETE")
	s.secretRoute("/containers/{id}/commit", s.commitContainer, "POST")
	s.secretRoute("/containers/{id}/start", s.startContainer, "POST")
	s.secretRoute("/containers/{id}/stop", s.stopContainer, "POST")
	s.secretRoute("/containers/{id}/restart", s.restartContainer, "POST")
	s.secretRoute("/containers/{id}/export", s.exportContainer, "GET")
	s.secretRoute("/containers/{id}/public_keys", s.listPublicKeys, "GET")
	s.secretRoute("/containers/{id}/public_keys", s.addPublicKeys, "POST", "PUT")
	s.secretRoute("/containers/{id}/public_keys", s.clearPublicKeys, "DELETE")
	s.secretRoute("/containers/{id}/public_keys/{key_fingerprint}", s.removePublicKey, "DELETE")
	s.secretRoute("/containers/{id}/reconfig", s.reconfigureContainer, "POST")
	s.route("/containers/{id}/attach/ws", s.wrapError(s.withContainerToken(s.wsAttachToContainer, "attach")), "GET")

	// handle websocket custom protocol
	s.route("/containers/{id}/shell", s.handleCORSPreflight, "OPTIONS")
	s.route("/containers/{id}/shell", s.wrapError(s.cors(s.withContainerToken(s.wsNewShell, "shell"))), "GET")
	s.route("/containers/{id}/logs", s.handleCORSPreflight, "OPTIONS")
	s.route("/containers/{id}/logs", s.wrapError(s.cors(s.withContainerToken(s.wsContainerLogs, "logs"))), "GET")

	s.secretRoute("/registry/auth", s.loginRegistry, "POST")
	s.secretRoute("/registry/auth", s.logoutRegistry, "DELETE")

	s.secretRoute("/images", s.wrapError(s.listImages), "GET")
	s.secretRoute("/images", s.wrapError(s.buildImage), "POST")
	s.secretRoute("/images", s.wrapError(s.removeImage), "DELETE")
	s.secretRoute("/images/pull", s.wrapError(s.pullImage), "POST")
	s.secretRoute("/images/push", s.wrapError(s.pushImage), "POST")
	s.secretRoute("/images/tag", s.wrapError(s.tagImage), "POST")
	s.secretRoute("/images/inspect", s.wrapError(s.inspectImage), "GET")

	s.secretRoute("/volumes", s.wrapError(s.listVolumes), "GET")
	s.secretRoute("/volumes", s.wrapError(s.createVolume), "POST")
	s.secretRoute("/volumes/{slug}", s.wrapError(s.removeVolume), "DELETE")
	s.secretRoute("/volumes/{slug}", s.wrapError(s.getVolumeInfo), "GET")

	s.secretRoute("/info", s.wrapError(s.serverInfo), "GET")
}

// given:
// - a certificate authority
// - a certificate signed by the CA
// - a private key for the certificate
// return a proper tls config that can be used with tls.NewListener to secure a
// net.Listener
func getTLSConfig(caPath, certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("Couldn't load X509 key pair (%s, %s): %s. Key encrypted?",
			certPath, keyPath, err)
	}
	file, err := ioutil.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("Couldn't read CA certificate %s: %s", caPath, err)
	}
	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(file); !ok {
		return nil, fmt.Errorf("Couldn't append CA certificate %s", caPath)
	}

	// by default, we dont require any client cert i.e. no authentication
	// as long as the client supplies the correct CA, it's allowed to talk to
	// the server's endpoints
	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: []tls.Certificate{cert},
		ClientCAs:    certPool,
	}

	return tlsConfig, nil
}
