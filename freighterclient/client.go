package freighterclient

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nitrous-io/freighter/provider"
)

const (
	StateRunning = "running"
	StateStopped = "stopped"

	StatusUnprocessableEntity = 422
)

var (
	// ErrUnexpectedResponse is returned when any of the API requests receives
	// an unexpected response body.
	ErrUnexpectedResponse = errors.New("freighter: unexpected response")

	// ErrInvalidParams is returned when we receive a Unprocessable Entity (HTTP 422) response.
	ErrInvalidParams = errors.New("freighter: invalid params")

	// ErrPubKeyOpsNotSupported is return when trying to perform a public key op
	// against a container that does not support SSH.
	// This mirrors provider.ErrPubKeyOpsNotSupported
	ErrPubKeyOpsNotSupported = provider.ErrPubKeyOpsNotSupported

	ErrTokenVerificationFailed = provider.ErrTokenVerificationFailed
	ErrTokenExpired            = provider.ErrTokenExpired
	ErrUnverifiable            = provider.ErrUnverifiable
	ErrImageNotFound           = provider.ErrImageNotFound
	ErrImageExists             = provider.ErrImageExists
	ErrImageInUse              = provider.ErrImageInUse
	ErrImagePushAuthRequired   = provider.ErrImagePushAuthRequired
	ErrImageAuthNotAllowed     = provider.ErrImageAuthNotAllowed

	// ErrInvalidSecret is returned when freighterclient caller passed in an
	// empty/invalid secret
	ErrInvalidSecret = errors.New("freighter: invalid secret")

	ErrEndpointNotFound  = errors.New("freighter: endpoint not found")
	ErrContainerNotFound = errors.New("freighter: container not found")
	ErrVolumeNotFound    = errors.New("freighter: volume not found")
	ErrHostNotFound      = errors.New("freighter: host not found")
)

type RegistryAuth struct {
	Username string
	Password string
	Email    string
	Registry string
}

type imageResponse struct {
	Image *Image `json:"image"`
}

// Client represents a Freighter API consumer.
type Client interface {
	List() ([]*Container, error)
	Create(name, image, secret string, config *ContainerConfig) (*Container, error)
	Commit(id, image string) (*Image, error)

	// Info returns the Container with the given id.
	// If there is no Container with the given id, Info should return
	// ErrContainerNotFound.
	Info(id string) (*Container, error)

	Start(id string, config *ContainerConfig) (*Container, error)
	Stop(id string) (*Container, error)
	Restart(id string) (*Container, error)
	Terminate(id string) (*Container, error)
	Config(id string) (cnrConfig *DockerConfig, cnrHostConfig *DockerHostConfig, imgConfig *DockerConfig, err error)
	Reconfig(id string, config *ContainerConfig) (*Container, error)

	AddPublicKeys(id string, keys []string) error
	ClearPublicKeys(id string) error
	RemovePublicKey(id string, fingerprint string) error

	ListVolumes() ([]string, error)
	CreateVolume(slug string) (*Volume, error)
	RemoveVolume(slug string) error
	GetVolumeInfo(slug string) (*Volume, error)

	RegistryLogin(params *RegistryAuth) error
	RegistryLogout(registry string) error

	HostInfo(period time.Duration) (*HostInfo, error)

	ListImages() ([]*Image, error)
	BuildImage(name string, in io.Reader, out io.Writer, remote string) error
	RemoveImage(image string) error
	PullImage(location string, update bool, output io.Writer) error
	PushImage(image, destination string, out io.Writer) error
	TagImage(image, newName string, force bool) error
	InspectImage(image string) (*Image, error)
}

type Stats struct {
	// Uptime in seconds.
	Uptime uint64 `json:"uptime"`

	// MemoryUsage in bytes.
	MemoryUsage uint64 `json:"memory_usage"`
	// MemoryTotal in bytes.
	MemoryTotal uint64 `json:"memory_total"`

	// StorageUsage in bytes.
	StorageUsage uint64 `json:"storage_usage"`
	// StorageTotal in bytes.
	StorageTotal uint64 `json:"storage_total"`

	NumCPU int `json:"num_cpu"`
	// Percent usage per CPU.
	PerCPUUsage []float64 `json:"per_cpu_usage"`
}

type (
	Container       provider.ContainerInfo
	ContainerConfig provider.ContainerConfig
	DockerConfig    provider.DockerConfig
	PortBinding     provider.PortBinding
	// duplication but needed to get freighterclient.PortBinding instead of provider.PortBinding
	DockerHostConfig struct {
		Binds           []string                 `json:"binds,omitempty"`
		PortBindings    map[string][]PortBinding `json:"port_bindings,omitempty"`
		PublishAllPorts bool                     `json:"publish_all_ports,omitempty"`
	}
	// same duplication
	Image struct {
		ID          string        `json:"id"`
		Name        string        `json:"name"`
		Tag         string        `json:"tag"`
		CreatedAt   int64         `json:"created_at"`
		ActualSize  int64         `json:"actual_size"`
		VirtualSize int64         `json:"virtual_size"`
		Config      *DockerConfig `json:"config"`
	}
	Volume   provider.VolumeInfo
	HostInfo Stats

	containerResponse struct {
		*Container   `json:"container"`
		Status       string `json:"status,omitempty"`
		ErrorMessage string `json:"error,omitempty"`
	}

	containersResponse struct {
		Containers []*Container `json:"containers"`
	}

	listImagesResponse struct {
		Images []*Image `json:"images"`
	}

	errorResponse struct {
		Error string `json:"error"`
	}
)

type RequestFailedError struct {
	Req *http.Request
	Err error
}

func (e *RequestFailedError) Error() string {
	return fmt.Sprintf("freighter: request %s %s failed: %s",
		e.Req.Method, e.Req.URL.String(), e.Err.Error())
}

type InvalidParamError struct {
	Msg string
}

func (e *InvalidParamError) Error() string {
	return fmt.Sprintf("freighter: invalid param: %s", e.Msg)
}

// StartFailedError is returned when a container is created successfully but
// could not be started.
type StartFailedError struct {
	Msg string
}

func (e *StartFailedError) Error() string {
	return fmt.Sprintf("freighter: container created but could not be started: %s", e.Msg)
}

type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("freighter: unexpected HTTP %d, body: %s", e.Code, e.Body)
}

type client struct {
	secret string
	host   *Host
	*http.Client
}

// NewClient returns a new Freighter API Client that can be used to communicate
// with a freighterd instance on a host
func NewClient(host *Host, secret string) (Client, error) {
	return NewClientTransport(host, secret, nil)
}

// NewClientTransport returns a new Freighter API Client that reuses an existing
// transport but with new TLS config so it can be used to communicate with
// a freighterd instance
//
// Note: if we use this method to customize the transport, make sure that we
// are sending the correct authentication token because we customize the token
// using proxy
func NewClientTransport(host *Host, secret string, tp *http.Transport) (Client, error) {
	if secret == "" {
		return nil, ErrInvalidSecret
	}
	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(host.CA); !ok {
		return nil, fmt.Errorf("Error appending cert")
	}
	tlsConfig := &tls.Config{
		ServerName: host.ServerName,
		RootCAs:    certPool,
	}

	if tp == nil {
		tp = &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				req.Header.Set("X-Freighter-Secret", secret)
				return nil, nil
			},
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 10 * time.Second,
		}
	}
	tp.TLSClientConfig = tlsConfig
	cl := &http.Client{
		Transport: tp,
	}
	return &client{
		secret: secret,
		host:   host,
		Client: cl,
	}, nil
}

// List makes an API request to a given Freighter Host and returns a list of
// Containers.
func (c *client) List() ([]*Container, error) {
	u := c.host.BaseURL() + "/containers"
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var containers containersResponse
	err = json.NewDecoder(resp.Body).Decode(&containers)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}

	return containers.Containers, nil
}

// Create makes an API request to a given Freighter Host to create a container
// with the given name, image, and configuration.
func (c *client) Create(name, image, secret string, config *ContainerConfig) (*Container, error) {
	u := c.host.BaseURL() + "/containers"
	data := url.Values{}
	data.Set("name", name)
	data.Set("image", image)
	data.Set("secret", secret)

	if config != nil {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		data.Set("config", string(configJSON))
	}

	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case StatusUnprocessableEntity:
		errResp, err := unmarshalErrorResponse(resp)
		if err != nil {
			return nil, ErrUnexpectedResponse
		}
		return nil, &InvalidParamError{errResp.Error}
	case http.StatusNotFound:
		return nil, ErrImageNotFound
	default:
		return nil, statusError(resp)
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
	}
	// Check whether there is an error starting the container.
	if containerResp.Status == "start_failed" {
		return containerResp.Container, &StartFailedError{Msg: containerResp.ErrorMessage}
	}

	return containerResp.Container, nil
}

func (c *client) Commit(id, imageName string) (*Image, error) {
	u := fmt.Sprintf("%s/containers/%s/commit", c.host.BaseURL(), id)
	data := &url.Values{"name": []string{imageName}}
	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var imageInfo struct {
		Image *Image `json:"image"`
	}
	err = json.NewDecoder(resp.Body).Decode(&imageInfo)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}

	return imageInfo.Image, nil
}

// Info makes an API request to a given Freighter Host and returns the
// Container with the given id.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Info(id string) (*Container, error) {
	u := c.host.BaseURL() + "/containers/" + id
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return processContainerResponse(resp)
}

// Config makes an API Request to a given Freigher Host and returns the Config,
// HostConfig, and image config for a container with the given id.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Config(id string) (*DockerConfig, *DockerHostConfig, *DockerConfig, error) {
	u := fmt.Sprintf("%s/containers/%s/config", c.host.BaseURL(), id)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case http.StatusNotFound:
		return nil, nil, nil, ErrContainerNotFound
	default:
		return nil, nil, nil, statusError(resp)
	}

	confs := struct {
		Config      *DockerConfig     `json:"config"`
		HostConfig  *DockerHostConfig `json:"host_config"`
		ImageConfig *DockerConfig     `json:"image_config"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&confs); err != nil {
		return nil, nil, nil, err
	}

	return confs.Config, confs.HostConfig, confs.ImageConfig, nil
}

// Start makes an API request to a given Freighter host to start the Container
// with the given id with the given configuration. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Start(id string, config *ContainerConfig) (*Container, error) {
	params := url.Values{}
	if config != nil {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		params.Set("config", string(configJSON))
	}

	u := fmt.Sprintf("%s/containers/%s/start", c.host.BaseURL(), id)
	req, err := http.NewRequest("POST", u, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return processContainerResponse(resp)
}

// Stop makes an API request to a given Freighter host to stop the Container
// with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Stop(id string) (*Container, error) {
	return c.containerControl(id, "stop")
}

// Restart makes an API request to a given Freighter host to restart the
// Container with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Restart(id string) (*Container, error) {
	return c.containerControl(id, "restart")
}

// Terminate makes an API request to a given Freighter host to terminate the
// Container with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Terminate(id string) (*Container, error) {
	u := fmt.Sprintf("%s/containers/%s", c.host.BaseURL(), id)
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return processContainerResponse(resp)
}

func (c *client) RegistryLogin(p *RegistryAuth) error {
	params := url.Values{}
	params.Set("username", p.Username)
	params.Set("password", p.Password)
	params.Set("email", p.Email)
	if p.Registry != "" {
		params.Set("registry", p.Registry)
	}

	u := fmt.Sprintf("%s/registry/auth", c.host.BaseURL())
	req, err := http.NewRequest("POST", u, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.Do(req)
	if err != nil {
		return &RequestFailedError{Req: req, Err: err}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		// Success, do nothing.
	case http.StatusNotFound:
		return ErrEndpointNotFound
	case StatusUnprocessableEntity:
		return ErrInvalidParams
	default:
		return statusError(resp)
	}

	return nil
}

func (c *client) RegistryLogout(registry string) error {
	params := url.Values{}
	params.Set("registry", registry)
	u := fmt.Sprintf("%s/registry/auth?%s", c.host.BaseURL(), params.Encode())
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.Do(req)
	if err != nil {
		return &RequestFailedError{Req: req, Err: err}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		// Deleted, do nothing.
	case http.StatusNotFound:
		return ErrEndpointNotFound
	case StatusUnprocessableEntity:
		return ErrInvalidParams
	default:
		return statusError(resp)
	}

	return nil
}

func (c *client) doPublicKeyOp(req *http.Request) error {
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrContainerNotFound
	case http.StatusBadRequest:
		errorResp, err := unmarshalErrorResponse(resp)
		if err != nil {
			return err
		}
		if errorResp.Error == provider.ErrPubKeyOpsNotSupported.Error() {
			return ErrPubKeyOpsNotSupported
		}
	}

	return statusError(resp)
}

func (c *client) AddPublicKeys(id string, keys []string) error {
	u := fmt.Sprintf("%s/containers/%s/public_keys", c.host.BaseURL(), id)

	data := url.Values{}
	for _, key := range keys {
		data.Add("public_key[]", key)
	}

	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))

	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doPublicKeyOp(req)
}

func (c *client) ClearPublicKeys(id string) error {
	u := fmt.Sprintf("%s/containers/%s/public_keys", c.host.BaseURL(), id)

	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doPublicKeyOp(req)
}

func (c *client) RemovePublicKey(id string, fingerprint string) error {
	u := fmt.Sprintf("%s/containers/%s/public_keys/%s", c.host.BaseURL(), id, fingerprint)

	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doPublicKeyOp(req)
}

func (c *client) Reconfig(id string, config *ContainerConfig) (*Container, error) {
	data := url.Values{}
	if config != nil {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		data.Set("config", string(configJSON))
	}

	u := fmt.Sprintf("%s/containers/%s/reconfig", c.host.BaseURL(), id)
	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case StatusUnprocessableEntity:
		errResp, err := unmarshalErrorResponse(resp)
		if err != nil {
			return nil, ErrUnexpectedResponse
		}
		return nil, &InvalidParamError{errResp.Error}
	default:
		return nil, statusError(resp)
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
	}
	return containerResp.Container, nil
}

func (c *client) HostInfo(period time.Duration) (*HostInfo, error) {
	data := url.Values{"period": []string{period.String()}}
	u := fmt.Sprintf("%s/info?%s", c.host.BaseURL(), data.Encode())
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var info struct {
		Info *HostInfo `json:"info"`
	}
	err = json.NewDecoder(resp.Body).Decode(&info)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}

	return info.Info, nil
}

// ListImages makes an API request to a given Freighter Host and returns a list of
// Images.
func (c *client) ListImages() ([]*Image, error) {
	u := fmt.Sprintf("%s/images", c.host.BaseURL())
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var images struct {
		Images []*Image `json:"images"`
	}
	err = json.NewDecoder(resp.Body).Decode(&images)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}

	return images.Images, nil
}

// Structs for unmarshalling JSON stream responses of the form:
// {"error":"Error...", "errorDetail":{"code": 123, "message": "Error..."}}
type (
	jsonError struct {
		Code    int    `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	}
	jsonMessage struct {
		ErrorMessage string     `json:"error"`
		ErrorDetail  *jsonError `json:"errorDetail,omitempty"`
	}
)

func (e *jsonMessage) isError() bool {
	if e.ErrorDetail != nil || e.ErrorMessage != "" {
		return true
	}
	return false
}

func (e *jsonMessage) Error() string {
	// Prefer "errorDetail" because "error" field is deprecated in Docker.
	if e.ErrorDetail != nil {
		return e.ErrorDetail.Error()
	}
	return e.ErrorMessage
}

func (e *jsonError) Error() string {
	return e.Message
}

// BuildImage builds an image. The input stream can be a tar archive (which
// can be either uncompressed or compressed with gzip/bzip2/xz). The archive
// must include a file named Dockerfile at its root. Any other files are made
// available as the build context.
// BuildImage alternatively accepts a 'remote' argument, which can be a URL
// pointing to a Dockerfile or a git repository which contains a Dockerfile
// and build context.
func (c *client) BuildImage(name string, in io.Reader, out io.Writer, remote string) error {
	u := fmt.Sprintf("%s/images", c.host.BaseURL())
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if in != nil {
		part, err := writer.CreateFormFile("file", "buildcontext.tar")
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, in); err != nil {
			return err
		}
	}
	_ = writer.WriteField("name", name)
	_ = writer.WriteField("remote", remote)
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}

	pr, pw := io.Pipe()
	combined := io.MultiWriter(out, pw) // Tee response to output stream and pipe.

	// Stream Freighter progress to output streams.
	writeErrCh := make(chan error, 1)
	go func() {
		_, err = io.Copy(combined, resp.Body)
		writeErrCh <- err
		pw.Close()
	}()

	// Check if JSON stream contains an error and return it as an error.
	decoder := json.NewDecoder(pr)
	for {
		var msg jsonMessage
		err := decoder.Decode(&msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("freighter: failed to decode stream: %v", err)
		}
		if msg.isError() {
			return &msg
		}
	}

	return <-writeErrCh
}

func (c *client) RemoveImage(image string) error {
	data := url.Values{"image": []string{image}}
	u := fmt.Sprintf("%s/images?%s", c.host.BaseURL(), data.Encode())
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		return ErrImageInUse
	default:
		return statusError(resp)
	}

	return nil
}

func (c *client) PullImage(repository string, update bool, output io.Writer) error {
	u := fmt.Sprintf("%s/images/pull", c.host.BaseURL())
	data := &url.Values{}
	data.Set("repository", repository)
	data.Set("update", strconv.FormatBool(update))
	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Pass.
	case http.StatusNotModified:
		return nil
	default:
		return statusError(resp)
	}

	pr, pw := io.Pipe()
	writer := io.MultiWriter(output, pw)

	// stream docker progress data to output
	writeErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(writer, resp.Body)
		writeErrCh <- err
		pw.Close()
	}()

	// check if the data contains any error. If there is, we should return an
	// error indicates that .PullImage failed
	decoder := json.NewDecoder(pr)
	for {
		var msg jsonMessage
		err := decoder.Decode(&msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("freighter: failed to decode stream: %v", err)
		}
		if msg.isError() {
			return &msg
		}
	}

	return <-writeErrCh
}

func (c *client) PushImage(image, destination string, out io.Writer) error {
	u := fmt.Sprintf("%s/images/push", c.host.BaseURL())
	data := &url.Values{
		"image":       []string{image},
		"destination": []string{destination},
	}
	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// no-op
	case http.StatusUnauthorized:
		return ErrImagePushAuthRequired
	default:
		return statusError(resp)
	}

	pr, pw := io.Pipe()
	writer := io.MultiWriter(out, pw)

	// stream docker progress data to output
	writeErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(writer, resp.Body)
		writeErrCh <- err
		pw.Close()
	}()

	decoder := json.NewDecoder(pr)
	for {
		var msg jsonMessage
		err := decoder.Decode(&msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("freighter: failed to decode stream: %v", err)
		}
		if msg.isError() {
			return &msg
		}
	}

	return <-writeErrCh
}

func (c *client) TagImage(image, tag string, force bool) error {
	u := fmt.Sprintf("%s/images/tag", c.host.BaseURL())
	data := &url.Values{
		"image": []string{image},
		"tag":   []string{tag},
		"force": []string{strconv.FormatBool(force)},
	}
	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}

	return nil
}

func (c *client) InspectImage(image string) (*Image, error) {
	data := &url.Values{
		"image":  []string{image},
		"remote": []string{"true"},
	}
	u := fmt.Sprintf("%s/images/inspect?%s", c.host.BaseURL(), data.Encode())
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var imageInfo imageResponse
	err = json.NewDecoder(resp.Body).Decode(&imageInfo)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}
	return imageInfo.Image, nil
}

func (c *client) ListVolumes() ([]string, error) {
	u := fmt.Sprintf("%s/volumes", c.host.BaseURL())
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var volumes struct {
		Volumes []string `json:"volumes"`
	}
	err = json.NewDecoder(resp.Body).Decode(&volumes)

	if err != nil {
		return nil, ErrUnexpectedResponse
	}

	return volumes.Volumes, nil
}

func (c *client) CreateVolume(slug string) (*Volume, error) {
	u := fmt.Sprintf("%s/volumes", c.host.BaseURL())
	data := &url.Values{"slug": []string{slug}}
	req, err := http.NewRequest("POST", u, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		var vol struct {
			Vol *Volume `json:"volume"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&vol); err != nil {
			return nil, ErrUnexpectedResponse
		}
		return vol.Vol, nil
	}

	switch resp.StatusCode {
	case StatusUnprocessableEntity:
		return nil, ErrInvalidParams
	case http.StatusNotFound:
		return nil, ErrVolumeNotFound
	default:
		return nil, statusError(resp)
	}
}

func (c *client) RemoveVolume(slug string) error {
	u := fmt.Sprintf("%s/volumes/%s", c.host.BaseURL(), slug)
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case StatusUnprocessableEntity:
		return ErrInvalidParams
	case http.StatusNotFound:
		return ErrVolumeNotFound
	default:
		return statusError(resp)
	}
}

func (c *client) GetVolumeInfo(slug string) (*Volume, error) {
	u := fmt.Sprintf("%s/volumes/%s", c.host.BaseURL(), slug)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var vol struct {
			Vol *Volume `json:"volume"`
		}
		err = json.NewDecoder(resp.Body).Decode(&vol)
		if err != nil {
			return nil, ErrUnexpectedResponse
		}
		return vol.Vol, nil
	}

	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, ErrVolumeNotFound
	default:
		return nil, statusError(resp)
	}
}

func (c *client) containerControl(id string, action string) (*Container, error) {
	u := fmt.Sprintf("%s/containers/%s/%s", c.host.BaseURL(), id, action)
	req, err := http.NewRequest("POST", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return processContainerResponse(resp)
}

func performRequest(c *client, req *http.Request) (*http.Response, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, &RequestFailedError{Req: req, Err: err}
	}
	return resp, nil
}

func processContainerResponse(res *http.Response) (*Container, error) {
	switch res.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case http.StatusNotFound:
		return nil, ErrContainerNotFound
	default:
		return nil, statusError(res)
	}

	c, err := unmarshalContainerResponse(res)
	if err != nil {
		return nil, err
	}
	return c.Container, nil
}

func unmarshalContainerResponse(resp *http.Response) (*containerResponse, error) {
	var c containerResponse
	err := json.NewDecoder(resp.Body).Decode(&c)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}
	return &c, nil
}

func unmarshalErrorResponse(resp *http.Response) (*errorResponse, error) {
	var errResp errorResponse
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	resp.Body.Close()
	if err != nil {
		return nil, ErrUnexpectedResponse
	}
	return &errResp, nil
}

func statusError(res *http.Response) *StatusError {
	if b, err := ioutil.ReadAll(res.Body); err == nil {
		return &StatusError{Code: res.StatusCode, Body: string(b)}
	}
	return &StatusError{Code: res.StatusCode}
}
