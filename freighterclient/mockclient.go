package freighterclient

import (
	"io"
)

// MockClient is a mock satisfying the freighter.Client interface.
type MockClient struct {
	Containers []*Container

	ListCalled int
	ListError  error

	CreateCalled     int
	CreateError      error
	CreatedContainer *Container

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
	HostInfoError  error
	HostInfoReturn *HostInfo

	ReceivedID     string
	ReceivedName   string
	ReceivedImage  string
	ReceivedTag    string
	ReceivedConfig *ContainerConfig

	ReceivedUsername string
	ReceivedPassword string
	ReceivedEmail    string
	ReceivedRegistry string

	ListImagesCalled int
	ListImagesArray  []*Image
	ListImagesError  error

	BuildImageCalled int
	BuildImageReturn *Image
	BuildImageError  error
	BuildImageName   string
	BuildImagePath   string

	RemoveImageCalled int
	RemoveImageError  error

	PullImageCalled int
	PullImageReturn *Image
	PullImageError  error

	PushImageCalled int
	PushImageError  error

	TagImageCalled int
	TagImageForce  bool
	TagImageError  error
}

func (c *MockClient) List() ([]*Container, error) {
	c.ListCalled = c.ListCalled + 1
	if c.ListError != nil {
		return nil, c.ListError
	}
	if c.Containers == nil {
		return []*Container{}, nil
	}
	return c.Containers, nil
}

func (c *MockClient) Create(name, image string, config *ContainerConfig) (*Container, error) {
	c.CreateCalled = c.CreateCalled + 1
	c.ReceivedName = name
	c.ReceivedImage = image
	c.ReceivedConfig = config
	return c.CreatedContainer, c.CreateError
}

func (c *MockClient) Info(id string) (*Container, error) {
	c.InfoCalled = c.InfoCalled + 1
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

func (c *MockClient) Start(id string) (*Container, error) {
	c.StartCalled = c.StartCalled + 1
	c.ReceivedID = id
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
	c.StopCalled = c.StopCalled + 1
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
	c.RestartCalled = c.RestartCalled + 1
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
	c.TerminateCalled = c.TerminateCalled + 1
	c.ReceivedID = id
	cntnr, _ := c.Info(id)
	if cntnr != nil {
		cntnr.Host = ""
		cntnr.State = ""
	}
	return cntnr, c.TerminateError
}

func (c *MockClient) AddPublicKeys(id string, pubkeys []string) error {
	c.AddPublicKeysCalled = c.AddPublicKeysCalled + 1
	c.ReceivedID = id

	return c.AddPublicKeysError
}

func (c *MockClient) ClearPublicKeys(id string) error {
	c.ClearPublicKeysCalled = c.ClearPublicKeysCalled + 1
	c.ReceivedID = id

	return c.ClearPublicKeysError
}

func (c *MockClient) RemovePublicKey(id string, fingerprint string) error {
	c.RemovePublicKeyCalled = c.RemovePublicKeyCalled + 1
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

func (c *MockClient) Logs(id string, follow, timestamps, tail string, w io.Writer) error {
	return nil
}

func (c *MockClient) RegistryLogin(p *RegistryAuth) error {
	c.RegistryLoginCalled = c.RegistryLoginCalled + 1
	c.ReceivedUsername = p.Username
	c.ReceivedPassword = p.Password
	c.ReceivedEmail = p.Email
	c.ReceivedRegistry = p.Registry

	return c.RegistryLoginError
}

func (c *MockClient) RegistryLogout(registry string) error {
	c.RegistryLogoutCalled = c.RegistryLogoutCalled + 1
	c.ReceivedRegistry = registry

	return c.RegistryLogoutError
}

func (c *MockClient) HostInfo() (*HostInfo, error) {
	c.HostInfoCalled = c.HostInfoCalled + 1
	return c.HostInfoReturn, c.HostInfoError
}

func (c *MockClient) ListImages() ([]*Image, error) {
	c.ListImagesCalled = c.ListImagesCalled + 1

	return c.ListImagesArray, c.ListImagesError
}

func (c *MockClient) BuildImage(h *Host, name, path string) (*Image, error) {
	c.BuildImageCalled = c.BuildImageCalled + 1
	c.BuildImageName = name
	c.BuildImagePath = path

	return c.BuildImageReturn, c.BuildImageError
}

func (c *MockClient) RemoveImage(h *Host, slug string) error {
	c.RemoveImageCalled = c.RemoveImageCalled + 1

	return c.RemoveImageError
}

func (c *MockClient) PullImage(h *Host, location string) (*Image, error) {
	c.PullImageCalled = c.PullImageCalled + 1

	return c.PullImageReturn, c.PullImageError
}

func (c *MockClient) PushImage(h *Host, slug, location string) error {
	c.PushImageCalled = c.PushImageCalled + 1

	return c.PushImageError
}

func (c *MockClient) TagImage(h *Host, slug, newName string, force bool) error {
	c.TagImageCalled = c.TagImageCalled + 1
	c.ReceivedImage = slug
	c.ReceivedTag = newName
	c.TagImageForce = force

	return c.TagImageError
}
