package provider

import (
	"io"
	"regexp"
)

var containerNameRe = regexp.MustCompile(`\A[a-z][a-z0-9\-]*[a-z0-9]\z`)

type Provider interface {
	List() (infoList []*ContainerInfo, err error)
	Create(name string, image string) (info *ContainerInfo, err error)
	GetInfo(id string) (info *ContainerInfo, err error)
	Start(id string) (info *ContainerInfo, err error)
	Stop(id string) (info *ContainerInfo, err error)
	Restart(id string) (info *ContainerInfo, err error)
	Terminate(id string) (info *ContainerInfo, err error)
	ListPublicKeys(id string) (publicKeys map[string]string, err error)
	AddPublicKeys(id string, publicKeys []string) (err error)
	RemovePublicKey(id string, keyFingerprint string) (err error)
	//ForwardPort(id string, hostPort uint16, containerPort uint16) (err error)
	//TakeSnapshot(id string, tag name, description string) (info *SnapshotInfo, err error)
	//RestoreSnapshot(id string, snapshotID string) (err error)
	IsNotFoundError(err error) bool
	Export(id string, output io.Writer) error
}

type ContainerInfo struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Image string      `json:"image"`
	Host  string      `json:"host"`
	State string      `json:"state"`
	Ports map[int]int `json:"ports"`
}

const (
	ContainerStateStopped = "stopped"
	ContainerStateRunning = "running"
)

//type SnapshotInfo struct {
//	ID          string `json:"id"`
//	Tag         string `json:"tag"`
//	Description string `json:"description"`
//}

func IsValidName(name string) bool {
	return containerNameRe.Match([]byte(name))
}
