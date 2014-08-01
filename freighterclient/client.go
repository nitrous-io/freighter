package freighterclient

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nitrous-io/freighter/pkg/stats"
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

	// ErrPubKeyOpsNotSupported is return when trying to perform a public key op
	// against a container that does not support SSH.
	// This mirrors provider.ErrPubKeyOpsNotSupported
	ErrPubKeyOpsNotSupported = provider.ErrPubKeyOpsNotSupported

	// ErrInvalidParams is returned when received a Unprocessable Entity (HTTP 422) response.
	ErrInvalidParams = errors.New("freighter: invalid params")

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

// Client represents a Freighter API consumer.
type Client interface {
	List() ([]*Container, error)
	Create(name, image string, config *ContainerConfig) (*Container, error)

	// Info returns the Container with the given id.
	// If there is no Container with the given id, Info should return
	// ErrContainerNotFound.
	Info(id string) (*Container, error)

	Start(id string) (*Container, error)
	Stop(id string) (*Container, error)
	Restart(id string) (*Container, error)
	Terminate(id string) (*Container, error)

	AddPublicKeys(id string, keys []string) error
	ClearPublicKeys(id string) error
	RemovePublicKey(id string, fingerprint string) error

	ListVolumes() ([]string, error)
	CreateVolume(slug string) (*Volume, error)
	RemoveVolume(slug string) error
	GetVolumeInfo(slug string) (*Volume, error)

	// Logs get logs of the container's process and copy it to the writer
	//
	// https://docs.docker.com/reference/api/docker_remote_api_v1.13/#21-containers
	// Search for `Get container logs` because docker's api documentation sucks
	Logs(id, follow, timestamps, tail string, w io.Writer) error

	RegistryLogin(params *RegistryAuth) error
	RegistryLogout(registry string) error

	HostInfo() (*HostInfo, error)

	ListImages() ([]*Image, error)
	BuildImage(host *Host, name string, path string) (*Image, error)
	RemoveImage(host *Host, image string) error
	PullImage(host *Host, location string) (*Image, error)
	PushImage(host *Host, image, destination string) error
	TagImage(host *Host, image, newName string, force bool) error
}

type (
	Container       provider.ContainerInfo
	ContainerConfig provider.ContainerConfig
	Image           provider.ImageInfo
	Volume          provider.VolumeInfo
	HostInfo        stats.Stats

	containerResponse struct {
		*Container `json:"container"`

		// TODO "result" seems redundant when Container.State is set and we are
		// getting a HTTP 200 OK response.
		Result string `json:"result,omitempty"`
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

type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("freighter: unexpected HTTP %d", e.Code)
}

type client struct {
	host *Host
	*http.Client
}

// NewClient returns a new Freighter API Client that can be used to communicate
// with a freighterd instance on a host
func NewClient(host *Host) (Client, error) {
	return NewClientTransport(host, nil)
}

// NewClientTransport returns a new Freighter API Client that reuses an existing
// transport but with new TLS config so it can be used to communicate with
// a freighterd instance
func NewClientTransport(host *Host, tp *http.Transport) (Client, error) {
	// generate proper tls config
	crt, err := tls.X509KeyPair(host.Cert, host.Key)
	if err != nil {
		return nil, err
	}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(host.CA)
	tlsConfig := &tls.Config{
		ServerName:   host.ServerName,
		RootCAs:      certPool,
		Certificates: []tls.Certificate{crt},
	}

	if tp == nil {
		tp = &http.Transport{
			Proxy: nil,
			Dial: (&net.Dialer{
				KeepAlive: 10 * time.Second,
			}).Dial,
			TLSHandshakeTimeout: 10 * time.Second,
		}
	}
	tp.TLSClientConfig = tlsConfig
	cl := &http.Client{
		Transport: tp,
	}
	return &client{host: host, Client: cl}, nil
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
		return nil, &StatusError{resp.StatusCode}
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
func (c *client) Create(name, image string, config *ContainerConfig) (*Container, error) {
	u := c.host.BaseURL() + "/containers"
	data := url.Values{}
	data.Set("name", name)
	data.Set("image", image)

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
	default:
		return nil, &StatusError{resp.StatusCode}
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
	}
	return containerResp.Container, nil
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

	switch resp.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case http.StatusNotFound:
		return nil, ErrContainerNotFound
	default:
		return nil, &StatusError{resp.StatusCode}
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
	}
	return containerResp.Container, nil
}

// Start makes an API request to a given Freighter host to start the Container
// with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Start(id string) (*Container, error) {
	return c.containerControl(id, "start")
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

	switch resp.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case http.StatusNotFound:
		return nil, ErrContainerNotFound
	default:
		return nil, &StatusError{resp.StatusCode}
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
	}
	return containerResp.Container, nil
}

// Establish a streaming HTTP connection with Freighter host to pull the
// specified container's logs. The response will be write into a provided
// `io.Writer`
func (c *client) Logs(
	id string,
	follow string,
	timestamps string,
	tail string,
	w io.Writer,
) error {
	params := url.Values{}
	params.Set("follow", follow)
	params.Set("timestamps", timestamps)
	params.Set("tail", tail)
	u := fmt.Sprintf("%s/containers/%s/logs?%s", c.host.BaseURL(), id, params.Encode())
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Failed to fetch container %s logs, status : %d", id, resp.StatusCode)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("Unexpected error when fetching logs: %v", err)
	}
	return nil
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
	switch resp.StatusCode {
	case http.StatusCreated:
		// Success, do nothing.
	case http.StatusNotFound:
		return ErrEndpointNotFound
	case StatusUnprocessableEntity:
		return ErrInvalidParams
	default:
		return &StatusError{resp.StatusCode}
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
	switch resp.StatusCode {
	case http.StatusNoContent:
		// Deleted, do nothing.
	case http.StatusNotFound:
		return ErrEndpointNotFound
	case StatusUnprocessableEntity:
		return ErrInvalidParams
	default:
		return &StatusError{resp.StatusCode}
	}

	return nil
}

func (c *client) doPublicKeyOp(req *http.Request) error {
	resp, err := performRequest(c, req)
	if err != nil {
		return err
	}

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
		return &StatusError{resp.StatusCode}
	default:
		return &StatusError{resp.StatusCode}
	}
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

func (c *client) HostInfo() (*HostInfo, error) {
	u := fmt.Sprintf("%s/info", c.host.BaseURL())
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
		return nil, &StatusError{resp.StatusCode}
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
		return nil, &StatusError{resp.StatusCode}
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

func (c *client) BuildImage(h *Host, name string, path string) (*Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	u := fmt.Sprintf("%s/images", h.BaseURL())
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, err
	}
	_ = writer.WriteField("name", name)
	err = writer.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", u, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{resp.StatusCode}
	}

	var imageInfo struct {
		Image  *Image `json:"image"`
		output string `json:"output"`
	}
	err = json.NewDecoder(resp.Body).Decode(&imageInfo)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}

	return imageInfo.Image, nil
}

func (c *client) RemoveImage(h *Host, image string) error {
	data := url.Values{"image": []string{image}}
	u := fmt.Sprintf("%s/images?%s", h.BaseURL(), data.Encode())
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

	if resp.StatusCode != http.StatusOK {
		return &StatusError{resp.StatusCode}
	}

	return nil
}

func (c *client) PullImage(h *Host, repository string) (*Image, error) {
	u := fmt.Sprintf("%s/images/pull", h.BaseURL())
	data := &url.Values{"repository": []string{repository}}
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
		return nil, &StatusError{resp.StatusCode}
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

func (c *client) PushImage(h *Host, image, destination string) error {
	u := fmt.Sprintf("%s/images/push", h.BaseURL())
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

	if resp.StatusCode != http.StatusOK {
		return &StatusError{resp.StatusCode}
	}

	return nil
}

func (c *client) TagImage(h *Host, image, tag string, force bool) error {
	u := fmt.Sprintf("%s/images/tag", h.BaseURL())
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
		return &StatusError{resp.StatusCode}
	}

	return nil
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
		return nil, &StatusError{resp.StatusCode}
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
		return nil, &StatusError{resp.StatusCode}
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
		return &StatusError{resp.StatusCode}
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
		return nil, &StatusError{resp.StatusCode}
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

	switch resp.StatusCode {
	case http.StatusOK:
		// Success, do nothing.
	case http.StatusNotFound:
		return nil, ErrContainerNotFound
	default:
		return nil, &StatusError{resp.StatusCode}
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
	}
	return containerResp.Container, nil
}

func performRequest(c *client, req *http.Request) (*http.Response, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, &RequestFailedError{Req: req, Err: err}
	}
	return resp, nil
}

func unmarshalContainerResponse(resp *http.Response) (*containerResponse, error) {
	var cntnr containerResponse
	err := json.NewDecoder(resp.Body).Decode(&cntnr)
	if err != nil {
		return nil, ErrUnexpectedResponse
	}
	return &cntnr, nil
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
