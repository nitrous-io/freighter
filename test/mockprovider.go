package test

import (
	"errors"
	"io"

	"github.com/nitrous-io/freighter/provider"
)

var ErrMockProviderContainerNotFound = errors.New("no such container")

type MockProvider struct {
	CreateCalled        bool
	CreateContainerInfo *provider.ContainerInfo
	CreateError         error

	TerminateCalled bool
	TerminateError  error

	StartCalled bool
	StartError  error

	StopCalled bool
	StopError  error

	RestartCalled bool
	RestartError  error

	ListPublicKeysCalled bool
	ListPublicKeysError  error

	AddPublicKeysCalled bool
	AddPublicKeysError  error

	ClearPublicKeysCalled bool
	ClearPublicKeysError  error

	RemovePublicKeyCalled bool
	RemovePublicKeyError  error

	ExportCalled     bool
	ExportError      error
	ReceivedIOWriter io.Writer

	ReceivedID             string
	ReceivedName           string
	ReceivedConfig         string
	ReceivedImage          string
	ReceivedPublicKeys     []string
	ReceivedKeyFingerprint string

	CreateVolumeCalled bool
	CreateVolumeReturn *provider.VolumeInfo
	CreateVolumeError  error
	CreateVolumeSlug   string

	ListVolumesCalled bool
	ListVolumesReturn []string
	ListVolumesError  error

	RemoveVolumeCalled bool
	RemoveVolumeError  error

	VolumeInfoCalled bool
	VolumeInfoReturn *provider.VolumeInfo
	VolumeInfoError  error

	LogsCalled         bool
	LogsError          error
	ReceivedFollow     bool
	ReceivedTimestamps bool
	ReceivedTail       string

	LoginCalled      bool
	LoginError       error
	ReceivedUsername string
	ReceivedPassword string
	ReceivedEmail    string
	ReceivedRegistry string

	LogoutCalled bool
	LogoutError  error

	ListImagesCalled   bool
	ListImagesContents []*provider.ImageInfo
	ListImagesError    error

	BuildImageCalled bool
	BuildImageOutput string
	BuildImageReturn *provider.ImageInfo
	BuildImageError  error

	RemoveImageCalled bool
	RemoveImageError  error

	PullImageCalled    bool
	PullImageReturn    *provider.ImageInfo
	PullImageError     error
	ReceivedRepository string

	PushImageCalled bool
	PushImageError  error

	TagImageCalled bool
	TagImageError  error

	Containers []*provider.ContainerInfo
	PublicKeys map[string]string
}

func (p *MockProvider) List() ([]*provider.ContainerInfo, error) {
	if p.Containers == nil {
		return []*provider.ContainerInfo{}, nil
	}
	return p.Containers, nil
}

func (p *MockProvider) Create(name, image, config string) (*provider.ContainerInfo, error) {
	p.CreateCalled = true
	p.ReceivedName = name
	p.ReceivedImage = image
	p.ReceivedConfig = config
	return p.CreateContainerInfo, p.CreateError
}

func (p *MockProvider) GetInfo(id string) (*provider.ContainerInfo, error) {
	for _, info := range p.Containers {
		if info.ID == id {
			return info, nil
		}
	}
	return nil, ErrMockProviderContainerNotFound
}

func (p *MockProvider) Start(id, config string) (*provider.ContainerInfo, error) {
	info, _ := p.GetInfo(id)
	if info != nil {
		info.State = provider.ContainerStateRunning
	}
	p.StartCalled = true
	p.ReceivedID = id
	p.ReceivedConfig = config
	return info, p.StartError
}

func (p *MockProvider) Stop(id string) (*provider.ContainerInfo, error) {
	info, _ := p.GetInfo(id)
	if info != nil {
		info.Host = ""
		info.State = provider.ContainerStateStopped
	}
	p.StopCalled = true
	p.ReceivedID = id
	return info, p.StopError
}

func (p *MockProvider) Restart(id string) (*provider.ContainerInfo, error) {
	info, _ := p.GetInfo(id)
	if info != nil {
		info.State = provider.ContainerStateRunning
	}
	p.RestartCalled = true
	p.ReceivedID = id
	return info, p.RestartError
}

func (p *MockProvider) Terminate(id string) (*provider.ContainerInfo, error) {
	info, _ := p.GetInfo(id)
	if info != nil {
		info.Host = ""
		info.State = ""
	}
	p.TerminateCalled = true
	p.ReceivedID = id
	return info, p.TerminateError
}

func (p *MockProvider) ListPublicKeys(id string) (map[string]string, error) {
	p.ListPublicKeysCalled = true
	p.ReceivedID = id
	return p.PublicKeys, p.ListPublicKeysError
}

func (p *MockProvider) AddPublicKeys(id string, publicKeys []string) error {
	p.AddPublicKeysCalled = true
	p.ReceivedID = id
	p.ReceivedPublicKeys = publicKeys
	return p.AddPublicKeysError
}

func (p *MockProvider) ClearPublicKeys(id string) error {
	p.ClearPublicKeysCalled = true
	p.ReceivedID = id
	return p.ClearPublicKeysError
}

func (p *MockProvider) RemovePublicKey(id string, keyFingerprint string) error {
	p.RemovePublicKeyCalled = true
	p.ReceivedID = id
	p.ReceivedKeyFingerprint = keyFingerprint
	return p.RemovePublicKeyError
}

func (p *MockProvider) Export(id string, output io.Writer) error {
	p.ExportCalled = true
	p.ReceivedID = id
	p.ReceivedIOWriter = output
	return p.ExportError
}

func (p *MockProvider) Logs(id string, w io.Writer, follow, timestamps bool, tail string) error {
	p.LogsCalled = true
	p.ReceivedID = id
	p.ReceivedFollow = follow
	p.ReceivedTimestamps = timestamps
	p.ReceivedTail = tail
	return p.LogsError
}

func (p *MockProvider) Login(username, password, email, registry string) error {
	p.LoginCalled = true
	p.ReceivedUsername = username
	p.ReceivedPassword = password
	p.ReceivedEmail = email
	p.ReceivedRegistry = registry
	return p.LoginError
}

func (p *MockProvider) Logout(registry string) error {
	p.LogoutCalled = true
	p.ReceivedRegistry = registry
	return p.LogoutError
}

func (p *MockProvider) ListImages() ([]*provider.ImageInfo, error) {
	p.ListImagesCalled = true

	if p.ListImagesError != nil {
		return nil, p.ListImagesError
	}

	if p.ListImagesContents == nil {
		return []*provider.ImageInfo{}, nil
	}

	return p.ListImagesContents, nil
}

func (p *MockProvider) BuildImage(name string, incoming io.Reader, result io.Writer) (*provider.ImageInfo, error) {
	p.BuildImageCalled = true

	if p.BuildImageError != nil {
		return nil, p.BuildImageError
	}

	result.Write([]byte(p.BuildImageOutput))

	return p.BuildImageReturn, nil
}

func (p *MockProvider) RemoveImage(imageIDOrName string) error {
	p.RemoveImageCalled = true
	return p.RemoveImageError
}

func (p *MockProvider) PullImage(location string) (*provider.ImageInfo, error) {
	p.PullImageCalled = true
	p.ReceivedRepository = location
	return p.PullImageReturn, p.PullImageError
}

func (p *MockProvider) PushImage(imageIDOrName, location string) error {
	p.PushImageCalled = true
	return p.PushImageError
}

func (p *MockProvider) TagImage(imageIDOrName, newName string, force bool) error {
	p.TagImageCalled = true
	return p.TagImageError
}

func (p *MockProvider) CreateVolume(slug string) (*provider.VolumeInfo, error) {
	p.CreateVolumeCalled = true
	p.CreateVolumeSlug = slug
	return p.CreateVolumeReturn, p.CreateVolumeError
}

func (p *MockProvider) ListVolumes() ([]string, error) {
	p.ListVolumesCalled = true
	return p.ListVolumesReturn, p.ListVolumesError
}

func (p *MockProvider) RemoveVolume(slug string) error {
	p.RemoveVolumeCalled = true
	return p.RemoveVolumeError
}

func (p *MockProvider) GetVolumeInfo(slug string) (*provider.VolumeInfo, error) {
	p.VolumeInfoCalled = true
	return p.VolumeInfoReturn, p.VolumeInfoError
}

func (p *MockProvider) IsNotFoundError(err error) bool {
	return err == ErrMockProviderContainerNotFound
}
