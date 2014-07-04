package provider

import (
	"io"
	"regexp"
)

var containerNameRe = regexp.MustCompile(`\A[a-z][a-z0-9\-]*[a-z0-9]\z`)

type Provider interface {
	List() (infoList []*ContainerInfo, err error)
	Create(name string, image string, publicKeys []string) (info *ContainerInfo, err error)
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

	// run a command inside a container, stream back the result to an writer
	Run(id string, w io.Writer, cmd string) error
}

type ContainerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Host    string `json:"host"`
	SSHPort uint16 `json:"ssh_port"`
	State   string `json:"state"`
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
