package test

import (
	dockerclient "github.com/fsouza/go-dockerclient"
)

type MockDockerProxy struct {
	PushImageCalled bool
	PushImageError  error
	PushImageOpts   dockerclient.PushImageOptions
	PushImageAuth   dockerclient.AuthConfiguration
}

func (m *MockDockerProxy) PushImage(opts dockerclient.PushImageOptions, auth dockerclient.AuthConfiguration) error {
	m.PushImageCalled = true
	m.PushImageOpts = opts
	m.PushImageAuth = auth
	return m.PushImageError
}
