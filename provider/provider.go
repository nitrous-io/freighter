package provider

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

const (
	ContainerStateStopped = "stopped"
	ContainerStateRunning = "running"
)

var containerNameRe = regexp.MustCompile(`\A[a-z][a-z0-9\-]*[a-z0-9]\z`)
var (
	ErrTimeout                = errors.New("Operation timed out")
	ErrPubKeyOpsNotSupported  = errors.New("Public key operations are not supported on this container")
	ErrImageNotFound          = errors.New("Image does not exist")
	ErrImageShared            = errors.New("Image is shared across multiple tags")
	ErrImageTagExists         = errors.New("Image with same tag already exists")
	ErrImageAuthNotAllowed    = errors.New("Do not have permission to push/pull image")
	ErrImageAuthMisconfigured = errors.New("Image registry configuration invalid")
	ErrImagePushAuthRequired  = errors.New("Log in to the registry before pushing an image to it")
)

type Provider interface {
	List() (infoList []*ContainerInfo, err error)
	Create(name, image, config string) (info *ContainerInfo, err error)
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
	RemoveImage(imageIDOrName string) error
	PullImage(location string) (*ImageInfo, error)
	PushImage(imageIDOrName, location string) error
	TagImage(imageIDOrName, newName string, force bool) error

	CreateVolume(slug string) (*VolumeInfo, error)
	ListVolumes() ([]string, error)
	RemoveVolume(slug string) error
	GetVolumeInfo(slug string) (*VolumeInfo, error)
}

type ContainerInfo struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Image string         `json:"image"`
	Host  string         `json:"host"`
	State string         `json:"state"`
	Ports map[string]int `json:"ports"`
}

type ContainerConfig interface {
	json.Marshaler
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
