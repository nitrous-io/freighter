package test

import (
	dockerclient "github.com/fsouza/go-dockerclient"
)

type MockDockerProxy struct {
	PushImageCalled bool
	PushImageError  error
	PushImageOpts   dockerclient.PushImageOptions
	PushImageAuth   dockerclient.AuthConfiguration

	PullImageCalled   bool
	PullImageError    error
	PullImageOpts     dockerclient.PullImageOptions
	PullImageAuth     dockerclient.AuthConfiguration
	PullImageCallBack func()
}

func (m *MockDockerProxy) PushImage(opts dockerclient.PushImageOptions, auth dockerclient.AuthConfiguration) error {
	m.PushImageCalled = true
	m.PushImageOpts = opts
	m.PushImageAuth = auth
	return m.PushImageError
}

func (m *MockDockerProxy) PullImage(opts dockerclient.PullImageOptions, auth dockerclient.AuthConfiguration) error {
	m.PullImageCalled = true
	m.PullImageOpts = opts
	m.PullImageAuth = auth

	if m.PullImageCallBack != nil {
		m.PullImageCallBack()
	}
	return m.PullImageError
}
