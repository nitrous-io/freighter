package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
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

	Listen []HostAddress `json:"listen"`
}

type Server struct {
	logger   *util.Logger
	provider provider.Provider
	router   *mux.Router
	conf     *Config
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
	svr := &http.Server{Addr: addr, Handler: s.router}
	return svr.Serve(l)
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
	s.route("/images/{slug}", s.wrapError(s.removeImage), "DELETE")
	s.route("/images/pull", s.wrapError(s.pullImage), "POST")
	s.route("/images/{slug}/push", s.wrapError(s.pushImage), "POST")
	s.route("/images/{slug}/tag", s.wrapError(s.tagImage), "POST")

	// volumes
	s.route("/volumes", s.wrapError(s.listVolumes), "GET")
	s.route("/volumes", s.wrapError(s.createVolume), "POST")
	s.route("/volumes/{slug}", s.wrapError(s.removeVolume), "DELETE")
	s.route("/volumes/{slug}", s.wrapError(s.getVolumeInfo), "GET")

	s.route("/info", s.wrapError(s.serverInfo), "GET")
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
	certPool.AppendCertsFromPEM(file)

	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
	}

	return tlsConfig, nil
}
