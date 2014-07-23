package freighterclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker"
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

	// ErrEndpointNotFound is returned when trying to send a request
	// to a non-existing endpoint.
	ErrEndpointNotFound = errors.New("freighter: endpoint not found")

	// ErrContainerNotFound is returned when trying to perform an action against a
	// container that does not exist.
	ErrContainerNotFound = errors.New("freighter: container not found")

	// ErrPubKeyOpsNotSupported is return when trying to perform a public key op
	// against a container that does not support SSH.
	// This mirrors provider.ErrPubKeyOpsNotSupported
	ErrPubKeyOpsNotSupported = provider.ErrPubKeyOpsNotSupported

	// ErrInvalidParams is returned when received a Unprocessable Entity (HTTP 422) response.
	ErrInvalidParams = errors.New("freighter: invalid params")
)

// SharedHTTPClient is the shared default http.Client used by Clients created
// with NewClient() without a given http.Client.
var SharedHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
		Dial: (&net.Dialer{
			KeepAlive: 10 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

type RegistryAuth struct {
	Username string
	Password string
	Email    string
	Registry string
}

// Client represents a Freighter API consumer.
type Client interface {
	List(host *Host) ([]*Container, error)
	Create(host *Host, name, image string, config ContainerConfig) (*Container, error)

	// Info returns the Container with the given id.
	// If there is no Container with the given id, Info should return
	// ErrContainerNotFound.
	Info(host *Host, id string) (*Container, error)

	Start(host *Host, id string) (*Container, error)
	Stop(host *Host, id string) (*Container, error)
	Restart(host *Host, id string) (*Container, error)
	Terminate(host *Host, id string) (*Container, error)

	AddPublicKeys(host *Host, id string, keys []string) error
	ClearPublicKeys(host *Host, id string) error
	RemovePublicKey(host *Host, id string, fingerprint string) error

	// Logs get logs of the container's process and copy it to the writer
	//
	// https://docs.docker.com/reference/api/docker_remote_api_v1.13/#21-containers
	// Search for `Get container logs` because docker's api documentation sucks
	Logs(host *Host, id, follow, timestamps, tail string, w io.Writer) error

	RegistryLogin(host *Host, params *RegistryAuth) error
	RegistryLogout(host *Host, registry string) error

	ListImages(host *Host, showAll bool) ([]*Image, error)
}

type (
	Container       provider.ContainerInfo
	ContainerConfig provider.ContainerConfig

	Image provider.ImageInfo

	containerResponse struct {
		*Container `json:"container"`

		// TODO "result" seems redundant when Container.State is set and we are
		// getting a HTTP 200 OK response.
		Result string `json:"result,omitempty"`
	}

	containersResponse struct {
		Containers []*Container `json:"containers"`
	}

	errorResponse struct {
		Error string `json:error`
	}
)

func NewDockerContainerConfig() *docker.ContainerConfig {
	return docker.NewContainerConfig()
}

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
	*http.Client
}

// NewClient returns a new Freighter API Client.
// httpclient is optional and defaults to SharedHTTPClient.
//
// Clients wrap around the underlying HTTP request/response business and are
// safe for concurrent use in multiple goroutines.
func NewClient(httpclient *http.Client) Client {
	if httpclient == nil {
		httpclient = SharedHTTPClient
	}
	return &client{Client: httpclient}
}

// List makes an API request to a given Freighter Host and returns a list of
// Containers.
func (c *client) List(h *Host) ([]*Container, error) {
	u := h.BaseURL() + "/containers"
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
func (c *client) Create(h *Host, name, image string, config ContainerConfig) (*Container, error) {
	u := h.BaseURL() + "/containers"
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
func (c *client) Info(h *Host, id string) (*Container, error) {
	u := h.BaseURL() + "/containers/" + id
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}

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
func (c *client) Start(h *Host, id string) (*Container, error) {
	return c.containerControl(h, id, "start")
}

// Stop makes an API request to a given Freighter host to stop the Container
// with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Stop(h *Host, id string) (*Container, error) {
	return c.containerControl(h, id, "stop")
}

// Restart makes an API request to a given Freighter host to restart the
// Container with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Restart(h *Host, id string) (*Container, error) {
	return c.containerControl(h, id, "restart")
}

// Terminate makes an API request to a given Freighter host to terminate the
// Container with the given id. The Container is returned.
// If the Container does not exist, ErrContainerNotFound is returned.
func (c *client) Terminate(h *Host, id string) (*Container, error) {
	u := fmt.Sprintf("%s/containers/%s", h.BaseURL(), id)
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}

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
	h *Host,
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
	u := fmt.Sprintf("%s/containers/%s/logs?%s", h.BaseURL(), id, params.Encode())
	resp, err := http.Get(u)
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

func (c *client) RegistryLogin(h *Host, p *RegistryAuth) error {
	params := url.Values{}
	params.Set("username", p.Username)
	params.Set("password", p.Password)
	params.Set("email", p.Email)
	if p.Registry != "" {
		params.Set("registry", p.Registry)
	}

	u := fmt.Sprintf("%s/registry/auth", h.BaseURL())
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

func (c *client) RegistryLogout(h *Host, registry string) error {
	params := url.Values{}
	params.Set("registry", registry)

	u := fmt.Sprintf("%s/registry/auth", h.BaseURL())
	req, err := http.NewRequest("DELETE", u, strings.NewReader(params.Encode()))
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
		} else {
			return &StatusError{resp.StatusCode}
		}
	default:
		return &StatusError{resp.StatusCode}
	}

	return nil
}

func (c *client) AddPublicKeys(h *Host, id string, keys []string) error {
	u := fmt.Sprintf("%s/containers/%s/public_keys", h.BaseURL(), id)

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

func (c *client) ClearPublicKeys(h *Host, id string) error {
	u := fmt.Sprintf("%s/containers/%s/public_keys", h.BaseURL(), id)

	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doPublicKeyOp(req)
}

func (c *client) RemovePublicKey(h *Host, id string, fingerprint string) error {
	u := fmt.Sprintf("%s/containers/%s/public_keys/%s", h.BaseURL(), id, fingerprint)

	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doPublicKeyOp(req)
}

// ListImages makes an API request to a given Freighter Host and returns a list of
// Images.
func (c *client) ListImages(h *Host, showAll bool) ([]*Image, error) {
	formattedURL := fmt.Sprintf("%s/images?show_all=%t", h.BaseURL(), showAll)
	req, err := http.NewRequest("GET", formattedURL, nil)
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

func (c *client) containerControl(h *Host, id string, action string) (*Container, error) {
	u := fmt.Sprintf("%s/containers/%s/%s", h.BaseURL(), id, action)
	req, err := http.NewRequest("POST", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := performRequest(c, req)
	if err != nil {
		return nil, err
	}

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
	resp.Body.Close()
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
