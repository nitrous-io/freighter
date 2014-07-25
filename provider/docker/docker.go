package docker

import (
	"errors"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"text/template"
	"time"

	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/util"
)

var (
	dockerfileUbuntu = template.Must(template.New("ubuntu").Parse(dockerfileUbuntuTmpl))
	dockerHost       = "unix:///var/run/docker.sock"
	registryName     = "nitrousio"
	images           = []string{"ubuntu-bare:trusty", "ubuntu:trusty", "ruby", "python", "nodejs", "golang", "php"}
)

func init() {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		dockerHost = host
	}
}

type TemplateData struct {
	Name  string
	Image string
}

type DockerProvider struct {
	client    *dockerclient.Client
	dataPath  string
	namespace string
	logger    *util.Logger
}

func NewDockerProvider(logger *util.Logger, dataPath string, namespace string) (*DockerProvider, error) {
	if logger == nil || dataPath == "" || namespace == "" {
		return nil, errors.New("docker.NewDockerProvider: invalid argument")
	}
	client, err := dockerclient.NewClient(dockerHost)
	if err != nil {
		return nil, err
	}
	return &DockerProvider{client: client, dataPath: dataPath, namespace: namespace + ".", logger: logger}, nil
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
			if strings.HasPrefix(name, "/"+p.namespace) {
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

func (p *DockerProvider) Create(name, imageName, configJSON string) (*provider.ContainerInfo, error) {
	err := p.createAuthorizedKeys(p.namespace+name, []string{})
	if err != nil {
		return nil, err
	}

	config, hostConfig, err := parseConfigParam(configJSON)
	if err != nil {
		return nil, err
	}

	container, err := p.createContainer(imageName, name, config, hostConfig)
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
		ID:         container.ID,
		Name:       nameFromContainer(container),
		Image:      container.Config.Image,
		Host:       container.NetworkSettings.IPAddress,
		Ports:      portsFromContainer(container),
		State:      stateFromContainer(container),
		ExitCode:   container.State.ExitCode,
		FinishedAt: finishedAtFromContainer(container),
	}, nil
}

func (p *DockerProvider) Start(id, config string) (*provider.ContainerInfo, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	startConfig := container.HostConfig
	_, hostConfig, err := parseConfigParam(config)
	if err != nil {
		return nil, err
	}
	overrideHostConfig(startConfig, hostConfig)

	if !container.State.Running {
		p.logger.Info("DockerProvider: starting container", id)
		start := time.Now()

		err = p.client.StartContainer(id, startConfig)
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

		ch := make(chan error, 1)

		go func() {
			timeout := time.After(15 * time.Second)
			for {
				select {
				case <-timeout:
					ch <- provider.ErrTimeout
					return
				default:
					info, err := p.GetInfo(id)
					if err != nil {
						ch <- err
						return
					}
					if info.State == provider.ContainerStateStopped && info.Host == "" {
						p.logger.TimeInfo(start, "DockerProvider: stopped container", id)
						ch <- nil
						return
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
		}()

		if err := <-ch; err != nil {
			return nil, err
		}
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

	info.Host = ""
	info.State = ""
	info.Ports = map[string]int{}

	p.logger.TimeInfo(terminationStart, "DockerProvider: terminated container", id, "in")
	return info, nil
}

func (p *DockerProvider) ListPublicKeys(id string) (map[string]string, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	if !usesNitrousImage(container) {
		return nil, provider.ErrPubKeyOpsNotSupported
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

	if !usesNitrousImage(container) {
		return provider.ErrPubKeyOpsNotSupported
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

func (p *DockerProvider) ClearPublicKeys(id string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}

	if !usesNitrousImage(container) {
		return provider.ErrPubKeyOpsNotSupported
	}

	name := nameFromContainer(container)
	return p.createAuthorizedKeys(name, []string{})
}

func (p *DockerProvider) RemovePublicKey(id string, keyFingerprint string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}

	if !usesNitrousImage(container) {
		return provider.ErrPubKeyOpsNotSupported
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

// Read the logs from the container. If success, caller of this methods can then read the
// logs from the `io.Reader` result
func (p *DockerProvider) Logs(id string, w io.Writer, follow, timestamps bool, tail string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}
	return p.client.Logs(dockerclient.LogsOptions{
		Container:    container.ID,
		Follow:       follow,
		Stdout:       true,
		Stderr:       true,
		Timestamps:   timestamps,
		Tail:         tail,
		OutputStream: w,
		ErrorStream:  w,
	})
}

func (p *DockerProvider) Login(username, password, email, registry string) error {
	args := []string{
		"login",
		"--username", username,
		"--password", password,
		"--email", email,
	}
	if registry != "" {
		args = append(args, registry)
	}
	return exec.Command("docker", args...).Run()
}

// Added to Docker with PR https://github.com/dotcloud/docker/pull/6719
func (p *DockerProvider) Logout(registry string) error {
	args := []string{
		"logout",
	}
	if registry != "" {
		args = append(args, registry)
	}
	return exec.Command("docker", args...).Run()
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

func (p *DockerProvider) ListImages() ([]*provider.ImageInfo, error) {
	dockerImages, err := p.client.ListImages(false)
	if err != nil {
		return nil, err
	}

	list := []*provider.ImageInfo{}
	for _, dockerImage := range dockerImages {
		for _, name := range dockerImage.RepoTags {
			if strings.HasPrefix(name, p.namespace) {
				cleansedName := strings.TrimPrefix(name, p.namespace)
				repoSplit := strings.SplitN(cleansedName, ":", 2)
				info := &provider.ImageInfo{
					ID:          dockerImage.ID,
					Name:        repoSplit[0],
					CreatedAt:   dockerImage.Created,
					ActualSize:  dockerImage.Size,
					VirtualSize: dockerImage.VirtualSize,
				}
				if len(repoSplit) > 1 {
					info.Tag = repoSplit[1]
				}
				list = append(list, info)
			}
		}
	}

	return list, nil
}

func (p *DockerProvider) createContainer(imageName, containerName string, config *allowedConfig, hostConfig *allowedHostConfig) (*dockerclient.Container, error) {
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
		Tag:        tag,
	}, dockerclient.AuthConfiguration{})

	if err != nil {
		return nil, err
	}

	containerName = p.namespace + containerName

	createConfig := &dockerclient.Config{
		Hostname: containerName,
		Image:    imageName,
		Volumes: map[string]struct{}{
			"/etc/ssh/freighter": {},
		},
		ExposedPorts: map[dockerclient.Port]struct{}{},
	}
	overrideConfig(createConfig, config)

	container, err := p.client.CreateContainer(dockerclient.CreateContainerOptions{
		Name:   containerName,
		Config: createConfig,
	})

	if err != nil {
		return nil, err
	}

	id := container.ID
	p.logger.TimeInfo(start, "DockerProvider: created container", containerName, id, "in")
	p.logger.Info("DockerProvider: starting container", containerName)
	start = time.Now()

	startConfig := &dockerclient.HostConfig{
		Binds: []string{
			(p.containerSettingsDir(containerName) + "/ssh:/etc/ssh/freighter:ro"),
		},
		PortBindings:    map[dockerclient.Port][]dockerclient.PortBinding{},
		PublishAllPorts: true,
	}
	overrideHostConfig(startConfig, hostConfig)

	err = p.client.StartContainer(id, startConfig)

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
	if !strings.HasPrefix(container.Name, "/"+p.namespace) {
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

func finishedAtFromContainer(container *dockerclient.Container) time.Time {
	if container.State.Running {
		// If a container has previously finished but is now running again,
		// FinishedAt retains its previous value, but we want a zero-value
		// FinishedAt in this case.
		return time.Time{}
	}
	return container.State.FinishedAt
}

func portsFromContainer(container *dockerclient.Container) (ports map[string]int) {
	ports = make(map[string]int)
	for port, bindings := range container.HostConfig.PortBindings {
		containerPort, _ := strconv.Atoi(port.Port())
		for _, binding := range bindings {
			ports[string(binding.HostPort)] = containerPort
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

func usesNitrousImage(container *dockerclient.Container) bool {
	image := container.Config.Image
	for _, nitrousImage := range images {
		if registryName+"/"+nitrousImage == image {
			return true
		}
	}

	return false
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
