package freighterclient

import (
	"io"
	"time"
)

// MockClient is a mock satisfying the freighter.Client interface.
type MockClient struct {
	Containers []*Container

	ListCalled int
	ListError  error

	CreateCalled     int
	CreateError      error
	CreatedContainer *Container

	CommitCalled int
	CommitImage  *Image
	CommitError  error

	InfoCalled int
	InfoError  error

	StartCalled int
	StartError  error

	StopCalled int
	StopError  error

	RestartCalled int
	RestartError  error

	TerminateCalled int
	TerminateError  error

	ConfigCalled              int
	ConfigContainerConfig     *DockerConfig
	ConfigContainerHostConfig *DockerHostConfig
	ConfigImageConfig         *DockerConfig
	ConfigError               error

	ReconfigCalled    int
	ReconfigContainer *Container
	ReconfigError     error

	AddPublicKeysCalled int
	AddPublicKeysError  error

	ClearPublicKeysCalled int
	ClearPublicKeysError  error

	RemovePublicKeyCalled int
	RemovePublicKeyError  error

	RegistryLoginCalled int
	RegistryLoginError  error

	RegistryLogoutCalled int
	RegistryLogoutError  error

	ListVolumesCalled  int
	ListVolumesReturn  []string
	ListVolumesError   error
	CreateVolumeCalled int
	CreateVolumeReturn *Volume
	CreateVolumeSlug   string
	CreateVolumeError  error
	RemoveVolumeCalled int
	RemoveVolumeError  error
	VolumeInfoCalled   int
	VolumeInfoReturn   *Volume
	VolumeInfoError    error

	HostInfoCalled int
	HostInfoPeriod time.Duration
	HostInfoError  error
	HostInfoReturn *HostInfo

	ReceivedID     string
	ReceivedName   string
	ReceivedImage  string
	ReceivedSecret string
	ReceivedConfig *ContainerConfig
	ReceivedTag    string

	ReceivedUsername string
	ReceivedPassword string
	ReceivedEmail    string
	ReceivedRegistry string

	ListImagesCalled int
	ListImagesArray  []*Image
	ListImagesError  error

	BuildImageCalled int
	BuildImageError  error
	BuildImageName   string
	BuildImageReader io.Reader
	BuildImageRemote string

	RemoveImageCalled int
	RemoveImageError  error

	PullImageCalled int
	PullImageError  error

	PushImageCalled int
	PushImageError  error

	TagImageCalled int
	TagImageForce  bool
	TagImageError  error

	InspectImageCalled        int
	InspectImageReceivedImage string
	InspectImageReturn        *Image
	InspectImageError         error

	OutputStream io.Reader
}

func (c *MockClient) List() ([]*Container, error) {
	c.ListCalled++
	if c.ListError != nil {
		return nil, c.ListError
	}
	if c.Containers == nil {
		return []*Container{}, nil
	}
	return c.Containers, nil
}

func (c *MockClient) Create(name, image string, secret string, config *ContainerConfig) (*Container, error) {
	c.CreateCalled++
	c.ReceivedName = name
	c.ReceivedImage = image
	c.ReceivedSecret = secret
	c.ReceivedConfig = config
	return c.CreatedContainer, c.CreateError
}

func (c *MockClient) Commit(id, imageName string) (*Image, error) {
	c.CommitCalled += 1
	c.ReceivedID = id
	c.ReceivedName = imageName
	return c.CommitImage, c.CommitError
}

func (c *MockClient) Info(id string) (*Container, error) {
	c.InfoCalled++
	c.ReceivedID = id
	if c.InfoError != nil {
		return nil, c.InfoError
	}
	for _, c := range c.Containers {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, ErrContainerNotFound
}

func (c *MockClient) Start(id string, config *ContainerConfig) (*Container, error) {
	c.StartCalled++
	c.ReceivedID = id
	c.ReceivedConfig = config
	if c.StartError != nil {
		return nil, c.StartError
	}
	cntnr, _ := c.Info(id)
	if cntnr != nil {
		cntnr.State = StateRunning
	}
	return cntnr, nil
}

func (c *MockClient) Stop(id string) (*Container, error) {
	c.StopCalled++
	c.ReceivedID = id
	if c.StopError != nil {
		return nil, c.StopError
	}
	cntnr, _ := c.Info(id)
	if cntnr != nil {
		cntnr.State = StateStopped
	}
	return cntnr, nil
}

func (c *MockClient) Restart(id string) (*Container, error) {
	c.RestartCalled++
	c.ReceivedID = id
	if c.RestartError != nil {
		return nil, c.RestartError
	}
	cntnr, _ := c.Info(id)
	if cntnr != nil {
		cntnr.State = StateRunning
	}
	return cntnr, nil
}

func (c *MockClient) Terminate(id string) (*Container, error) {
	c.TerminateCalled++
	c.ReceivedID = id
	cntnr, _ := c.Info(id)
	if cntnr != nil {
		cntnr.Host = ""
		cntnr.State = ""
	}
	return cntnr, c.TerminateError
}

func (c *MockClient) Config(id string) (cnrConfig *DockerConfig, cnrHostConfig *DockerHostConfig, imgConfig *DockerConfig, err error) {
	c.ConfigCalled++
	return c.ConfigContainerConfig, c.ConfigContainerHostConfig, c.ConfigImageConfig, c.ConfigError
}

func (c *MockClient) Reconfig(id string, config *ContainerConfig) (*Container, error) {
	c.ReconfigCalled++
	c.ReceivedID = id
	c.ReceivedConfig = config
	return c.ReconfigContainer, c.ReconfigError
}

func (c *MockClient) AddPublicKeys(id string, pubkeys []string) error {
	c.AddPublicKeysCalled++
	c.ReceivedID = id
	return c.AddPublicKeysError
}

func (c *MockClient) ClearPublicKeys(id string) error {
	c.ClearPublicKeysCalled++
	c.ReceivedID = id
	return c.ClearPublicKeysError
}

func (c *MockClient) RemovePublicKey(id string, fingerprint string) error {
	c.RemovePublicKeyCalled++
	c.ReceivedID = id
	return c.RemovePublicKeyError
}

func (c *MockClient) ListVolumes() ([]string, error) {
	c.ListVolumesCalled += 1
	return c.ListVolumesReturn, c.ListVolumesError
}

func (c *MockClient) CreateVolume(slug string) (*Volume, error) {
	c.CreateVolumeCalled += 1
	c.CreateVolumeSlug = slug
	return c.CreateVolumeReturn, c.CreateVolumeError
}

func (c *MockClient) RemoveVolume(slug string) error {
	c.RemoveVolumeCalled += 1
	return c.RemoveVolumeError
}

func (c *MockClient) GetVolumeInfo(slug string) (*Volume, error) {
	c.VolumeInfoCalled += 1
	return c.VolumeInfoReturn, c.VolumeInfoError
}

func (c *MockClient) RegistryLogin(p *RegistryAuth) error {
	c.RegistryLoginCalled++
	c.ReceivedUsername = p.Username
	c.ReceivedPassword = p.Password
	c.ReceivedEmail = p.Email
	c.ReceivedRegistry = p.Registry
	return c.RegistryLoginError
}

func (c *MockClient) RegistryLogout(registry string) error {
	c.RegistryLogoutCalled++
	c.ReceivedRegistry = registry
	return c.RegistryLogoutError
}

func (c *MockClient) HostInfo(period time.Duration) (*HostInfo, error) {
	c.HostInfoCalled++
	c.HostInfoPeriod = period
	return c.HostInfoReturn, c.HostInfoError
}

func (c *MockClient) ListImages() ([]*Image, error) {
	c.ListImagesCalled++
	return c.ListImagesArray, c.ListImagesError
}

func (c *MockClient) BuildImage(name string, in io.Reader, out io.Writer, remote string) error {
	c.BuildImageCalled++
	c.BuildImageName = name
	c.BuildImageReader = in
	c.BuildImageRemote = remote
	if c.OutputStream != nil {
		io.Copy(out, c.OutputStream)
	}
	return c.BuildImageError
}

func (c *MockClient) RemoveImage(slug string) error {
	c.RemoveImageCalled++
	return c.RemoveImageError
}

func (c *MockClient) PullImage(location string, update bool, stdout io.Writer) error {
	c.PullImageCalled++
	return c.PullImageError
}

func (c *MockClient) PushImage(slug, location string, out io.Writer) error {
	c.PushImageCalled++
	return c.PushImageError
}

func (c *MockClient) TagImage(slug, newName string, force bool) error {
	c.TagImageCalled++
	c.ReceivedImage = slug
	c.ReceivedTag = newName
	c.TagImageForce = force
	return c.TagImageError
}

func (c *MockClient) InspectImage(name string) (*Image, error) {
	c.InspectImageCalled++
	c.InspectImageReceivedImage = name
	return c.InspectImageReturn, c.InspectImageError
}
