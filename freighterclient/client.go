package freighterclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	ErrImageExists             = provider.ErrImageExists
	ErrImagePushAuthRequired   = provider.ErrImagePushAuthRequired

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
	Reconfig(id, secret string, config *ContainerConfig) (*Container, error)

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

	// Logs get logs of the container's process and copy it to the writer.
	// http://docs.docker.com/reference/api/docker_remote_api_v1.13/#get-container-logs
	Logs(id, token, follow, timestamps, tail string, w io.Writer) error

	RegistryLogin(params *RegistryAuth) error
	RegistryLogout(registry string) error

	HostInfo(period time.Duration) (*HostInfo, error)

	ListImages() ([]*Image, error)
	BuildImage(name string, in io.Reader, out io.Writer) error
	RemoveImage(image string) error
	PullImage(location string, output io.Writer) error
	PushImage(image, destination string) error
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
	Image           provider.ImageInfo
	Volume          provider.VolumeInfo
	HostInfo        Stats

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
// with a freighterd instance on a host.
func NewClient(host *Host) Client {
	return &client{host: host, Client: &http.Client{}}
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
	default:
		return nil, &StatusError{resp.StatusCode}
	}

	containerResp, err := unmarshalContainerResponse(resp)
	if err != nil {
		return nil, err
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

func (c *client) Reconfig(id, secret string, config *ContainerConfig) (*Container, error) {
	imageName := fmt.Sprintf("%s%v-%v", provider.ReconfigPrefix, id, time.Now().Unix())
	image, err := c.Commit(id, imageName)
	if err != nil {
		return nil, err
	}

	container, err := c.Terminate(id)
	if err != nil {
		return nil, err
	}

	return c.Create(container.Name, image.Name, secret, config)
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
	token string,
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
	if token != "" {
		req.Header.Set("Authorization", "Token "+token)
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorResp, err := unmarshalErrorResponse(resp)
		if err != nil {
			return err
		}
		switch errorResp.Error {
		case ErrTokenVerificationFailed.Error():
			return ErrTokenVerificationFailed
		case ErrTokenExpired.Error():
			return ErrTokenExpired
		case ErrUnverifiable.Error():
			return ErrUnverifiable
		default:
			return &StatusError{resp.StatusCode}
		}
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

// BuildImage builds an image. The input stream should be a tar archive (which
// can be either uncompressed or compressed with gzip/bzip2/xz). The archive
// must include a file named Dockerfile at its root. Any other files are made
// available as the build context.
func (c *client) BuildImage(name string, in io.Reader, out io.Writer) error {
	u := fmt.Sprintf("%s/images", c.host.BaseURL())
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "buildcontext.tar")
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, in); err != nil {
		return err
	}
	_ = writer.WriteField("name", name)
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
		return &StatusError{resp.StatusCode}
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

	if resp.StatusCode != http.StatusOK {
		return &StatusError{resp.StatusCode}
	}

	return nil
}

func (c *client) PullImage(repository string, output io.Writer) error {
	u := fmt.Sprintf("%s/images/pull", c.host.BaseURL())
	data := &url.Values{"repository": []string{repository}}
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

func (c *client) PushImage(image, destination string) error {
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
		return nil
	case http.StatusUnauthorized:
		return ErrImagePushAuthRequired
	default:
		return &StatusError{resp.StatusCode}
	}
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
		return &StatusError{resp.StatusCode}
	}

	return nil
}

func (c *client) InspectImage(image string) (*Image, error) {
	data := &url.Values{
		"image": []string{image},
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
		return nil, &StatusError{resp.StatusCode}
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
