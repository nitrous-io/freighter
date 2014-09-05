package provider

import (
	"errors"
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
	ErrPubKeyOpsNotSupported   = errors.New("Public key operations are not supported on this container")
	ErrImageNotFound           = errors.New("Image does not exist")
	ErrImageShared             = errors.New("Image is shared across multiple tags")
	ErrImageExists             = errors.New("Image with same name and tag already exists")
	ErrImageAuthNotAllowed     = errors.New("Do not have permission to push/pull image")
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
	Create(name, image, secret, config string) (info *ContainerInfo, err error)
	Commit(id, imageName string) (info *ImageInfo, err error)
	GetInfo(id string) (info *ContainerInfo, err error)
	Start(id, config string) (info *ContainerInfo, err error)
	Stop(id string) (info *ContainerInfo, err error)
	Restart(id string) (info *ContainerInfo, err error)
	Terminate(id string) (info *ContainerInfo, err error)
	ListPublicKeys(id string) (publicKeys map[string]string, err error)
	AddPublicKeys(id string, publicKeys []string) (err error)
	ClearPublicKeys(id string) (err error)
	RemovePublicKey(id string, keyFingerprint string) (err error)
	//ForwardPort(id string, hostPort uint16, containerPort uint16) (err error)
	//TakeSnapshot(id string, tag name, description string) (info *SnapshotInfo, err error)
	//RestoreSnapshot(id string, snapshotID string) (err error)
	IsNotFoundError(err error) bool
	Export(id string, output io.Writer) error
	Logs(id string, w io.Writer, follow, timestamps bool, tail string) error

	Login(username, password, email, registry string) error
	Logout(registry string) error

	ListImages() (infoList []*ImageInfo, err error)
	BuildImage(name string, incoming io.Reader, result io.Writer) (*ImageInfo, error)
	RemoveImage(name string) error
	PullImage(repository string) (*ImageInfo, error)
	PushImage(name, destination string) error
	TagImage(name, tag string, force bool) error
	InspectImage(name string) (*ImageInfo, error)

	CreateVolume(slug string) (*VolumeInfo, error)
	ListVolumes() ([]string, error)
	RemoveVolume(slug string) error
	GetVolumeInfo(slug string) (*VolumeInfo, error)

	AttachToContainer(id string, in io.Reader, out io.Writer) error
}

type ContainerInfo struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Image      string         `json:"image"`
	ImageID    string         `json:"image_id"`
	Host       string         `json:"host"`
	State      string         `json:"state"`
	Ports      map[string]int `json:"ports"`
	Cmd        []string       `json:"cmd,omitempty"`
	Entrypoint []string       `json:"entrypoint,omitempty"`
	ExitCode   int            `json:"exit_code"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
}

type ContainerConfig struct {
	Hostname     string   `json:"-"`
	Image        string   `json:"-"`
	Env          []string `json:"Env"`
	Mounts       []string `json:"Mounts"`
	PortBindings []string `json:"PortBindings"`
}

type ImageInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Tag         string `json:"tag"`
	CreatedAt   int64  `json:"created_at"`
	ActualSize  int64  `json:"actual_size"`
	VirtualSize int64  `json:"virtual_size"`
}

type VolumeInfo struct {
	Slug string `json:"slug"`
	Size uint64 `json:"size"`
}

//type SnapshotInfo struct {
//	ID          string `json:"id"`
//	Tag         string `json:"tag"`
//	Description string `json:"description"`
//}

func IsValidName(name string) bool {
	return containerNameRe.Match([]byte(name))
}
