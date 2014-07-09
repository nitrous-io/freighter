package docker

import (
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strconv"
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
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	client, err := dockerclient.NewClient(host)
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
		for _, name := range apicontainer.Names {
			if strings.HasPrefix(name, "/" + p.imageNamespace) {
				info, err := p.GetInfo(apicontainer.ID)
				if err != nil {
					return nil, err
				}
				list = append(list, info)
				break
			}
		}
	}

	return list, nil
}

func (p *DockerProvider) Create(name string, imageName string) (*provider.ContainerInfo, error) {
	err := p.createAuthorizedKeys(p.imageNamespace + name, []string{})

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
		ID:      container.ID,
		Name:    nameFromContainer(container),
		Image:   container.Config.Image,
		Host:    container.NetworkSettings.IPAddress,
		Ports:   portsFromContainer(container),
		State:   stateFromContainer(container),
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

	info, err = p.GetInfo(id)

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

func (p *DockerProvider) ListPublicKeys(id string) (map[string]string, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	keys, err := p.readAuthorizedKeys(nameFromContainer(container))
	if err != nil {
		return nil, err
	}

	return keysToKeyMap(keys)
}

func (p *DockerProvider) AddPublicKeys(id string, publicKeys []string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}
	name := nameFromContainer(container)

	existingKeys, err := p.readAuthorizedKeys(name)
	if err != nil {
		return err
	}

	// validate pubkeys and remove duplicates
	keymap, err := keysToKeyMap(append(publicKeys, existingKeys...))
	if err != nil {
		return err
	}
	publicKeys = keyMapToKeys(keymap)

	return p.createAuthorizedKeys(name, publicKeys)
}

func (p *DockerProvider) RemovePublicKey(id string, keyFingerprint string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}
	name := nameFromContainer(container)

	keys, err := p.readAuthorizedKeys(name)
	if err != nil {
		return err
	}

	keymap, err := keysToKeyMap(keys)
	if err != nil {
		return err
	}
	delete(keymap, keyFingerprint)
	keys = keyMapToKeys(keymap)

	return p.createAuthorizedKeys(name, keys)
}

func (p *DockerProvider) IsNotFoundError(err error) bool {
	_, ok := err.(*dockerclient.NoSuchContainer)
	return ok
}

func (p *DockerProvider) Export(id string, output io.Writer) error {
	defer func(start time.Time) {
		p.logger.Infof("Export container %s took %v", id, time.Now().Sub(start))
	}(time.Now())
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}
	opts := dockerclient.ExportContainerOptions{
		ID:           container.ID,
		OutputStream: output,
	}
	return p.client.ExportContainer(opts)
}

func (p *DockerProvider) createContainer(imageName string, containerName string) (*dockerclient.Container, error) {
	p.logger.Info("DockerProvider: creating container", containerName, "with image", imageName)
	start := time.Now()

	parts := strings.Split(imageName, ":")
	var repository, tag string
	repository = parts[0]

	if len(parts) == 1 {
		tag = "latest"
	} else {
		tag = parts[1]
	}

	err := p.client.PullImage(dockerclient.PullImageOptions{
		Repository: repository,
		Tag: tag,
	}, dockerclient.AuthConfiguration{
	})

	if err != nil {
		return nil, err
	}

	container, err := p.client.CreateContainer(dockerclient.CreateContainerOptions{
		Name: p.imageNamespace + containerName,
		Config: &dockerclient.Config{
			Hostname: containerName,
			Image:    imageName,
			Volumes: map[string]struct{}{
				"/etc/ssh/freighter": {},
			},
		},
	})

	if err != nil {
		return nil, err
	}

	id := container.ID
	p.logger.TimeInfo(start, "DockerProvider: created container", containerName, id, "in")
	p.logger.Info("DockerProvider: starting container", containerName)
	start = time.Now()

	err = p.client.StartContainer(id, &dockerclient.HostConfig{
		Binds: []string{
			(p.containerSettingsDir(containerName) + "/ssh:/etc/ssh/freighter:ro"),
		},
		PortBindings:    map[dockerclient.Port][]dockerclient.PortBinding{},
		PublishAllPorts: true,
	})

	p.logger.TimeInfo(start, "DockerProvider: started container", containerName, id, "in")
	return container, err
}

func (p *DockerProvider) containerSettingsDir(containerName string) string {
	return path.Join(p.dataPath, "containers", containerName)
}

func (p *DockerProvider) createAuthorizedKeys(containerName string, publicKeys []string) error {
	p.logger.Info("DockerProvider: creating SSH authorized keys file for container", containerName, "with", len(publicKeys), "entries")
	dir := path.Join(p.containerSettingsDir(containerName), "ssh")
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}
	filename := path.Join(dir, "authorized_keys")
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, key := range publicKeys {
		key = strings.TrimSpace(strings.Replace(key, "\n", "", -1)) + "\n"
		_, err = file.Write([]byte(key))
		if err != nil {
			return err
		}
	}

	p.logger.Info("DockerProvider: created SSH authorized keys file for container", containerName)
	return nil
}

func (p *DockerProvider) readAuthorizedKeys(containerName string) ([]string, error) {
	keysFile := path.Join(p.containerSettingsDir(containerName), "ssh", "authorized_keys")
	bkeys, err := ioutil.ReadFile(keysFile)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, key := range strings.Split(strings.TrimSpace(string(bkeys)), "\n") {
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (p *DockerProvider) inspectContainer(id string) (*dockerclient.Container, error) {
	// returns container only if container is created by freighter
	container, err := p.client.InspectContainer(id)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(container.Name, "/" + p.imageNamespace) {
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

func portsFromContainer(container *dockerclient.Container) (ports map[int]int) {
	ports = make(map[int]int)
	for port, bindings := range container.HostConfig.PortBindings {
		containerPort, _ := strconv.Atoi(port.Port())
		for _, binding := range bindings {
			hostPort, _ := strconv.Atoi(binding.HostPort)
			ports[hostPort] = containerPort
		}
	}
	return
}

// converts []pubkey to map[keyFingerprint]pubkey
func keysToKeyMap(keys []string) (map[string]string, error) {
	keymap := map[string]string{}
	for _, key := range keys {
		fingerprint, err := util.Md5Fingerprint(key)
		if err != nil {
			return nil, err
		}
		keymap[fingerprint] = key
	}
	return keymap, nil
}

// converts map[keyFingerprint]pubkey back to []pubkey
func keyMapToKeys(keymap map[string]string) []string {
	keys := make([]string, len(keymap))
	i := 0
	for _, key := range keymap {
		keys[i] = key
		i++
	}
	return keys
}
