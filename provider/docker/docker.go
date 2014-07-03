package docker

import (
	"archive/tar"
	"bytes"
	"errors"
	"strings"
	"text/template"
	"time"

	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/util"
)

var dockerfileUbuntu = template.Must(template.New("ubuntu").Parse(dockerfileUbuntuTmpl))

type TemplateData struct {
	Name  string
	Image string
}

type DockerProvider struct {
	client         *dockerclient.Client
	dataPath       string
	imageNamespace string
	logger         *util.Logger
}

func NewDockerProvider(logger *util.Logger, dataPath string, imageNamespace string) (*DockerProvider, error) {
	if logger == nil || dataPath == "" || imageNamespace == "" {
		return nil, errors.New("docker.NewDockerProvider: invalid argument")
	}
	client, err := dockerclient.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		return nil, err
	}
	return &DockerProvider{client: client, dataPath: dataPath, imageNamespace: imageNamespace + ".", logger: logger}, nil
}

func (p *DockerProvider) List() ([]*provider.ContainerInfo, error) {
	apicontainers, err := p.client.ListContainers(dockerclient.ListContainersOptions{
		All: true,
	})
	if err != nil {
		return nil, err
	}

	list := []*provider.ContainerInfo{}

	for _, apicontainer := range apicontainers {
		if strings.HasPrefix(apicontainer.Image, p.imageNamespace) {
			info, err := p.GetInfo(apicontainer.ID)
			if err != nil {
				return nil, err
			}
			list = append(list, info)
		}
	}

	return list, nil
}

func (p *DockerProvider) Create(name string, image string) (*provider.ContainerInfo, error) {
	imageName, err := p.buildImage(name, image)
	if err != nil {
		return nil, err
	}

	container, err := p.createContainer(imageName, name)
	if err != nil {
		return nil, err
	}

	return p.GetInfo(container.ID)
}

func (p *DockerProvider) GetInfo(id string) (*provider.ContainerInfo, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	return &provider.ContainerInfo{
		ID:    container.ID,
		Name:  nameFromContainer(container),
		Image: container.Config.Image,
		Host:  container.NetworkSettings.IPAddress,
		State: stateFromContainer(container),
	}, nil
}

func (p *DockerProvider) Start(id string) (*provider.ContainerInfo, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	if !container.State.Running {
		p.logger.Info("DockerProvider: starting container", id)
		start := time.Now()

		err = p.client.StartContainer(id, container.HostConfig)
		if err != nil {
			return nil, err
		}

		p.logger.TimeInfo(start, "DockerProvider: started container", id, "in")
	}
	return p.GetInfo(id)
}

func (p *DockerProvider) Stop(id string) (*provider.ContainerInfo, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	if container.State.Running {
		p.logger.Info("DockerProvider: stopping container", id)
		start := time.Now()

		err := p.client.StopContainer(id, 15)
		if err != nil {
			return nil, err
		}

		p.logger.TimeInfo(start, "DockerProvider: stopped container", id, "in")
	}
	return p.GetInfo(id)
}

func (p *DockerProvider) Restart(id string) (*provider.ContainerInfo, error) {
	_, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	p.logger.Info("DockerProvider: restarting container", id)
	start := time.Now()

	err = p.client.RestartContainer(id, 15)
	if err != nil {
		return nil, err
	}

	p.logger.TimeInfo(start, "DockerProvider: restarted container", id, "in")
	return p.GetInfo(id)
}

func (p *DockerProvider) Terminate(id string) (*provider.ContainerInfo, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	p.logger.Info("DockerProvider: terminating container", id)
	terminationStart := time.Now()

	info, err := p.Stop(id)
	if err != nil {
		return nil, err
	}

	p.logger.Info("DockerProvider: removing container", id)
	start := time.Now()

	err = p.client.RemoveContainer(dockerclient.RemoveContainerOptions{ID: id})
	if err != nil {
		return nil, err
	}

	p.logger.TimeInfo(start, "DockerProvider: removed container", id, "in")

	containerImage := container.Config.Image
	p.logger.Info("DockerProvider: removing container image", containerImage)
	start = time.Now()

	err = p.client.RemoveImage(containerImage)
	if err != nil {
		return nil, err
	}

	p.logger.TimeInfo(start, "DockerProvider: removed container image", containerImage, "in")

	info.Host = ""
	info.State = ""

	p.logger.TimeInfo(terminationStart, "DockerProvider: terminated container", id, "in")
	return info, nil
}

func (p *DockerProvider) IsNotFoundError(err error) bool {
	_, ok := err.(*dockerclient.NoSuchContainer)
	return ok
}

func (p *DockerProvider) buildImage(name string, image string) (string, error) {
	imageName := p.imageNamespace + name
	p.logger.Info("DockerProvider: building container image", imageName, "with base image", image)
	start := time.Now()

	buf := &bytes.Buffer{}
	err := dockerfileUbuntu.Execute(buf, &TemplateData{Name: name, Image: image})
	if err != nil {
		return "", err
	}

	inputbuf, outputbuf := &bytes.Buffer{}, &bytes.Buffer{}
	tr := tar.NewWriter(inputbuf)
	tr.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0644, Size: int64(buf.Len()), ModTime: start, AccessTime: start, ChangeTime: start})
	tr.Write(buf.Bytes())
	tr.Close()

	opts := dockerclient.BuildImageOptions{
		Name:           imageName,
		InputStream:    inputbuf,
		OutputStream:   outputbuf,
		RmTmpContainer: true,
	}

	err = p.client.BuildImage(opts)
	if err != nil {
		return "", err
	}

	p.logger.TimeInfo(start, "DockerProvider: built container image", imageName, "in")
	return imageName, err
}

func (p *DockerProvider) createContainer(imageName string, containerName string) (*dockerclient.Container, error) {
	p.logger.Info("DockerProvider: creating container", containerName, "with image", imageName)
	start := time.Now()

	container, err := p.client.CreateContainer(dockerclient.CreateContainerOptions{
		Name: containerName,
		Config: &dockerclient.Config{
			Hostname: containerName,
			Image:    imageName,
			Volumes: map[string]struct{}{
				"/etc/ssh/freighter": {},
			},
		},
	})
	if err != nil {
		return container, err
	}

	id := container.ID
	p.logger.TimeInfo(start, "DockerProvider: created container", containerName, id, "in")
	p.logger.Info("DockerProvider: starting container", containerName)
	start = time.Now()

	err = p.client.StartContainer(id, &dockerclient.HostConfig{
		PortBindings:    map[dockerclient.Port][]dockerclient.PortBinding{},
		PublishAllPorts: true,
	})

	p.logger.TimeInfo(start, "DockerProvider: started container", containerName, id, "in")
	return container, err
}

func (p *DockerProvider) inspectContainer(id string) (*dockerclient.Container, error) {
	// returns container only if container is created by freighter
	container, err := p.client.InspectContainer(id)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(container.Config.Image, p.imageNamespace) {
		return nil, &dockerclient.NoSuchContainer{ID: id}
	}
	return container, nil
}

func nameFromContainer(container *dockerclient.Container) string {
	return container.Name[1:]
}

func stateFromContainer(container *dockerclient.Container) string {
	if container.State.Running {
		return provider.ContainerStateRunning
	}
	return provider.ContainerStateStopped
}
