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

	RemovePublicKeyCalled bool
	RemovePublicKeyError  error

	ExportCalled     bool
	ExportError      error
	ReceivedIOWriter io.Writer

	ReceivedID             string
	ReceivedName           string
	ReceivedImage          string
	ReceivedPublicKeys     []string
	ReceivedKeyFingerprint string

	Containers []*provider.ContainerInfo
	PublicKeys map[string]string

	ExportCalled     bool
	ReceivedIOWriter io.Writer
}

func (p *MockProvider) List() ([]*provider.ContainerInfo, error) {
	if p.Containers == nil {
		return []*provider.ContainerInfo{}, nil
	}
	return p.Containers, nil
}

func (p *MockProvider) Create(name string, image string) (*provider.ContainerInfo, error) {
	p.CreateCalled = true
	p.ReceivedName = name
	p.ReceivedImage = image
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

func (p *MockProvider) Start(id string) (*provider.ContainerInfo, error) {
	info, _ := p.GetInfo(id)
	if info != nil {
		info.State = provider.ContainerStateRunning
	}
	p.StartCalled = true
	p.ReceivedID = id
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

func (p *MockProvider) IsNotFoundError(err error) bool {
	return err == ErrMockProviderContainerNotFound
}

func (p *MockProvider) Export(id string, output io.Writer) error {
	p.ExportCalled = true
	p.ReceivedID = id
	p.ReceivedIOWriter = output

	return nil
}
