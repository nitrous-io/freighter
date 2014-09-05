package docker

import (
	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
)

func (p *DockerProvider) MkContainerConfig(cfg *provider.ContainerConfig) (*dockerclient.Config, error) {
	return p.mkContainerConfig(cfg)
}

func (p *DockerProvider) MkHostConfig(hostCfg *dockerclient.HostConfig, cfg *provider.ContainerConfig) (*dockerclient.HostConfig, error) {
	return p.mkHostConfig(hostCfg, cfg)
}
