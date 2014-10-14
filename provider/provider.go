package provider

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

const (
	ContainerStateStopped = "stopped"
	ContainerStateRunning = "running"
)

var containerNameRe = regexp.MustCompile(`\A[a-z][a-z0-9\-]*[a-z0-9]\z`)
var (
	ErrTimeout                 = errors.New("Operation timed out")
	ErrCmdConfig               = errors.New("Container command not specified")
	ErrPubKeyOpsNotSupported   = errors.New("Public key operations are not supported on this container")
	ErrImageNotFound           = errors.New("Image does not exist")
	ErrImageShared             = errors.New("Image is shared across multiple tags")
	ErrImageInUse              = errors.New("Image is being used by one or more containers")
	ErrImageExists             = errors.New("Image with same name and tag already exists")
	ErrImageAuthNotAllowed     = errors.New("Do not have permission to access this image")
	ErrImageAuthMisconfigured  = errors.New("Image registry configuration invalid")
	ErrImagePushAuthRequired   = errors.New("Log in to the registry before pushing an image to it")
	ErrTokenVerificationFailed = errors.New("Permission denied")
	ErrTokenExpired            = errors.New("Verification expired, please re-try your request")
	ErrUnverifiable            = errors.New("Container not configured for verification")
)

type Provider interface {
	// VerifyToken verifies that the given token has the required permissions
	// on the container with the given ID.
	// It is the caller's responsibility to verify a token before subsequently
	// calling other functions, wherever necessary.
	VerifyToken(id, token string, permissions ...string) error

	List() (infoList []*ContainerInfo, err error)
	CreateStart(name, image, secret, config string) (info *ContainerInfo, err error)
	Commit(id, imageName string) (info *ImageInfo, err error)
	GetInfo(id string) (info *ContainerInfo, err error)
	GetConfig(id string) (config *ContainerConfig, err error)
	Start(id, config string) (info *ContainerInfo, err error)
	Stop(id string) (info *ContainerInfo, err error)
	Restart(id string) (info *ContainerInfo, err error)
	Terminate(id string, removeSettings bool) (info *ContainerInfo, err error)
	ListPublicKeys(id string) (publicKeys map[string]string, err error)
	AddPublicKeys(id string, publicKeys []string) (err error)
	ClearPublicKeys(id string) (err error)
	RemovePublicKey(id string, keyFingerprint string) (err error)
	Reconfig(id, config string) (info *ContainerInfo, err error)

	//ForwardPort(id string, hostPort uint16, containerPort uint16) (err error)
	//TakeSnapshot(id string, tag name, description string) (info *SnapshotInfo, err error)
	//RestoreSnapshot(id string, snapshotID string) (err error)
	IsNotFoundError(err error) bool
	Export(id string, output io.Writer) error
	Logs(id string, w io.Writer, follow, timestamps bool, tail string) error

	Login(username, password, email, registry string) error
	Logout(registry string) error

	ListImages() (infoList []*ImageInfo, err error)
	BuildImage(opts BuildImageOptions) error
	RemoveImage(name string) error
	PullImage(repository string, output io.Writer, rawJSONStream bool) error
	PushImage(name, destination string, output io.Writer, rawJSONStream bool) error
	TagImage(name, tag string, force bool) error
	InspectImage(name string, remoteFallback bool) (*ImageInfo, error)

	CreateVolume(slug string) (*VolumeInfo, error)
	ListVolumes() ([]string, error)
	RemoveVolume(slug string) error
	GetVolumeInfo(slug string) (*VolumeInfo, error)

	AttachToContainer(id string, in io.Reader, out io.Writer, noStdin bool) error
	ResizeContainerTTY(id string, height, width int) error
}

// StartFailedError is returned when a container is created successfully but
// could not be started.
type StartFailedError struct {
	Err error
}

func (e *StartFailedError) Error() string {
	return fmt.Sprintf("provider: container created but could not be started: %s", e.Err.Error())
}

type ContainerInfo struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Image      string              `json:"image"`
	ImageID    string              `json:"image_id"`
	Host       string              `json:"host"`
	Gateway    string              `json:"gateway"`
	State      string              `json:"state"`
	Ports      map[string][]string `json:"ports"`
	Cmd        []string            `json:"cmd,omitempty"`
	Entrypoint []string            `json:"entrypoint,omitempty"`
	ExitCode   int                 `json:"exit_code"`
	StartedAt  time.Time           `json:"started_at"`
	FinishedAt time.Time           `json:"finished_at"`
	Env        []string            `json:"-"`
}

// ContainerConfig is our internal mechanism for configuring containers.  It
// combines elements of docker's Config and HostConfig, and functions exist to
// generate Config and HostConfig from a given ContainerConfig
type ContainerConfig struct {
	Hostname     string   `json:"-"`
	Image        string   `json:"-"`
	Privileged   bool     `json:"Privileged,omitempty"`
	Cmd          []string `json:"Cmd,omitempty"`
	Env          []string `json:"Env,omitempty"`
	Mounts       []string `json:"Mounts,omitempty"`
	PortBindings []string `json:"PortBindings,omitempty"`
	PublishAll   bool     `json:"PublishAll,omitempty"`
}

// DockerConfig is fields we care about from the container's Config on Docker;
// it is a subset of dockerclient.Config .  Note: json keys are lowercased
// instead of camel-cased.
type DockerConfig struct {
	PortSpecs    []string            `json:"port_specs,omitempty"`
	ExposedPorts map[string]struct{} `json:"exposed_ports,omitempty"`
	Env          []string            `json:"env,omitempty"`
	Cmd          []string            `json:"cmd,omitempty"`
	Image        string              `json:"image,omitempty"`
	Volumes      map[string]struct{} `json:"volumes,omitempty"`
}

type PortBinding struct {
	HostIp   string `json:"host_ip,omitempty"`
	HostPort string `json:"host_port,omitempty"`
}

// DockerConfig is fields we care about from the container's HostConfig on
// Docker; it is a subset of dockerclient.HostConfig .  Note: json keys are
// lowercased instead of camel-cased.
type DockerHostConfig struct {
	Binds           []string                 `json:"binds,omitempty"`
	PortBindings    map[string][]PortBinding `json:"port_bindings,omitempty"`
	PublishAllPorts bool                     `json:"publish_all_ports,omitempty"`
}

type ImageInfo struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Tag         string        `json:"tag"`
	CreatedAt   int64         `json:"created_at"`
	ActualSize  int64         `json:"actual_size"`
	VirtualSize int64         `json:"virtual_size"`
	Config      *DockerConfig `json:"config"`
}

type VolumeInfo struct {
	Slug string `json:"slug"`
	Size uint64 `json:"size"`
}

// duplicates dockerclient.BuildImageOptions
type BuildImageOptions struct {
	Name          string
	InputStream   io.Reader
	OutputStream  io.Writer
	RawJSONStream bool
	Remote        string
}

//type SnapshotInfo struct {
//	ID          string `json:"id"`
//	Tag         string `json:"tag"`
//	Description string `json:"description"`
//}

func IsValidName(name string) bool {
	return containerNameRe.Match([]byte(name))
}
