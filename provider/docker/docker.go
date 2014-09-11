package docker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"
	"time"

	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker/parsers"
	"github.com/nitrous-io/freighter/util"
	"github.com/nitrous-io/nitrous-common/hmactoken"
)

var (
	dockerfileUbuntu = template.Must(template.New("ubuntu").Parse(dockerfileUbuntuTmpl))
	dockerHost       = "unix:///var/run/docker.sock"
	registryName     = "nitrousio"
	images           = []string{"ubuntu-bare:trusty", "ubuntu:trusty", "ruby", "python", "nodejs", "golang", "php"}
)

const defaultDockerGateway = "172.17.42.1"

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
	client   *dockerclient.Client
	dataPath string
	logger   *util.Logger

	DockerClientProxy interface {
		PushImage(opts dockerclient.PushImageOptions, auth dockerclient.AuthConfiguration) error
		PullImage(opts dockerclient.PullImageOptions, auth dockerclient.AuthConfiguration) error
	}
	DockerConfigLocation string
}

type lookupImageResult struct {
	image *provider.ImageInfo
	byID  bool
}

func NewDockerProvider(logger *util.Logger, dataPath string) (*DockerProvider, error) {
	if logger == nil || dataPath == "" {
		return nil, errors.New("docker.NewDockerProvider: invalid argument")
	}
	client, err := dockerclient.NewClient(dockerHost)
	if err != nil {
		return nil, err
	}
	return &DockerProvider{
		client:            client,
		dataPath:          dataPath,
		logger:            logger,
		DockerClientProxy: client,
	}, nil
}

func (p *DockerProvider) VerifyToken(id, token string, permissions ...string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}

	secret, err := p.readSharedSecret(nameFromContainer(container))
	if err != nil {
		return provider.ErrUnverifiable
	}
	err = hmactoken.Verify(permissions, token, secret)
	switch err {
	case hmactoken.ErrInvalidToken, hmactoken.ErrPermissionDenied:
		return provider.ErrTokenVerificationFailed
	case hmactoken.ErrTokenExpired:
		return provider.ErrTokenExpired
	default:
		return err
	}
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
		info, err := p.GetInfo(apicontainer.ID)
		if err != nil {
			return nil, err
		}
		list = append(list, info)
	}

	return list, nil
}

func (p *DockerProvider) Create(name, imageName, secret, configJSON string) (*provider.ContainerInfo, error) {
	err := p.storeSharedSecret(name, secret)
	if err != nil {
		return nil, err
	}

	err = p.createAuthorizedKeys(name, []string{})
	if err != nil {
		return nil, err
	}

	cfg, err := containerConfigFromJSON(configJSON)
	if err != nil {
		return nil, err
	}
	setDefaultContainerConfig(cfg)
	cfg.Hostname = name
	cfg.Image = imageName

	container, err := p.createContainer(imageName, name, cfg)
	if err != nil {
		return nil, err
	}

	return p.GetInfo(container.ID)
}

func (p *DockerProvider) Commit(id, imageName string) (*provider.ImageInfo, error) {
	p.logger.Infof("DockerProvider: committing container %s to image %s", id, imageName)

	repo, tag := parsers.ParseRepoAndTag(imageName)
	if tag == "" {
		tag = "latest"
	}

	opts := dockerclient.CommitContainerOptions{
		Container:  id,
		Repository: repo,
		Tag:        tag,
	}
	dockerImage, err := p.client.CommitContainer(opts)
	if err != nil {
		return nil, err
	}

	info, err := p.lookupImage(dockerImage.ID)
	if err != nil {
		return nil, err
	}
	return info.image, nil
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
		ImageID:    container.Image,
		Host:       container.NetworkSettings.IPAddress,
		Gateway:    container.NetworkSettings.Gateway,
		Ports:      portsFromContainer(container),
		State:      stateFromContainer(container),
		Cmd:        container.Config.Cmd,
		Entrypoint: container.Config.Entrypoint,
		ExitCode:   container.State.ExitCode,
		StartedAt:  container.State.StartedAt,
		FinishedAt: container.State.FinishedAt,
		Env:        container.Config.Env,
	}, nil
}

func (p *DockerProvider) Start(id, configJSON string) (*provider.ContainerInfo, error) {
	container, err := p.inspectContainer(id)
	if err != nil {
		return nil, err
	}

	cfg, err := containerConfigFromJSON(configJSON)
	if err != nil {
		return nil, err
	}
	setDefaultContainerConfig(cfg)
	cfg.Hostname = container.Config.Hostname
	cfg.Image = container.Config.Image

	startConfig, err := p.mkHostConfig(container.HostConfig, cfg)
	if err != nil {
		return nil, err
	}

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
	p.logger.Info("DockerProvider: restarting container", id)
	start := time.Now()
	// Stop and then start until https://github.com/docker/docker/issues/7693
	// is fixed.
	_, err := p.Stop(id)
	if err != nil {
		return nil, err
	}
	info, err := p.Start(id, "")
	if err != nil {
		return nil, err
	}
	p.logger.TimeInfo(start, "DockerProvider: restarted container", id, "in")
	return info, nil
}

func (p *DockerProvider) Terminate(id string) (*provider.ContainerInfo, error) {
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

	err = os.RemoveAll(p.ContainerSettingsDir(info.Name))
	if err != nil {
		return nil, err
	}

	p.logger.TimeInfo(start, "DockerProvider: removed container", id, "in")

	info.Host = ""
	info.Gateway = ""
	info.State = ""
	info.Ports = nil

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

// Logs gets stdout and stderr logs from a container. If successful, the logs
// are written to the given io.Writer.
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
			repo, tag := parsers.ParseRepoAndTag(name)
			info := &provider.ImageInfo{
				ID:          dockerImage.ID,
				Name:        repo,
				Tag:         tag,
				CreatedAt:   dockerImage.Created,
				ActualSize:  dockerImage.Size,
				VirtualSize: dockerImage.VirtualSize,
			}
			list = append(list, info)
		}
	}

	return list, nil
}

func (p *DockerProvider) BuildImage(name string, input io.Reader, output io.Writer, rawJSONStream bool) error {
	opts := dockerclient.BuildImageOptions{
		Name:                name,
		InputStream:         input,
		OutputStream:        output,
		RmTmpContainer:      true,
		ForceRmTmpContainer: true,
		RawJSONStream:       rawJSONStream,
	}
	return p.client.BuildImage(opts)
}

func (p *DockerProvider) RemoveImage(imageIDOrName string) error {
	lookupResult, err := p.lookupImage(imageIDOrName)
	if err != nil {
		return err
	}

	image, useID := lookupResult.image, lookupResult.byID

	if useID {
		err = p.client.RemoveImage(image.ID)
	} else {
		merged := strings.Join([]string{image.Name, image.Tag}, ":")
		err = p.client.RemoveImage(merged)
	}
	if err != nil && strings.Contains(err.Error(), "tagged in multiple repositories") {
		return provider.ErrImageShared
	}

	return err
}

// PullImage pulls the image specified with `location` and writes the pull's
// output into the provided writer
func (p *DockerProvider) PullImage(location string, output io.Writer, rawJSONStream bool) error {
	registry := parsers.ParseRegistry(location)
	auth, err := p.fetchAuthenticationInfoBestMatch(registry)
	if err != nil {
		return err
	}

	repository, tag := parsers.ParseRepoAndTag(location)
	if tag == "" {
		tag = "latest"
	}

	pullOpts := dockerclient.PullImageOptions{
		Repository:    repository,
		Tag:           tag,
		OutputStream:  output,
		RawJSONStream: rawJSONStream,
	}
	return p.client.PullImage(pullOpts, auth)
}

// PushImage pushes an image identified by its ID or name to a destination
// repository.
//
// If destination repository is "", the image is simply pushed.
//
// As a side effect, the image will be tagged with the destination repository.
//
// Examples:
//   push rails to chuyeow/rails
//   push rails to quay.io/chuyeow/railsbox
//   push quay.io/chuyeow/rails
//   push quay.io/chuyeow/rails to tsuru.io/nitrous/railsbox
func (p *DockerProvider) PushImage(imageIDOrName, dest string) error {
	lookupResult, err := p.lookupImage(imageIDOrName)
	if err != nil {
		return err
	}

	image := lookupResult.image

	// If destination repository is blank, we just want to push the image.
	if dest == "" {
		dest = strings.Join([]string{image.Name, image.Tag}, ":")
	}

	registry := parsers.ParseRegistry(dest)
	auth, err := p.fetchAuthenticationInfoBestMatch(registry)
	if err != nil {
		return err
	}
	if auth == (dockerclient.AuthConfiguration{}) {
		// docker push requires logging in to the registry first.
		return provider.ErrImagePushAuthRequired
	}

	err = p.dockerTag(image.ID, dest, true)
	if err != nil {
		return err
	}

	repository, tag := parsers.ParseRepoAndTag(dest)
	var buf bytes.Buffer
	// while PushImageOptions contains a Registry attribute, it does not work
	// docker push XXXXXX calls the API with the full name (registry and repository) as the image name
	pushOpts := dockerclient.PushImageOptions{
		Name:         repository,
		Tag:          tag,
		OutputStream: &buf,
	}

	return p.DockerClientProxy.PushImage(pushOpts, auth)
}

func (p *DockerProvider) TagImage(imageIDOrName, newName string, force bool) error {
	lookupResult, err := p.lookupImage(imageIDOrName)
	if err != nil {
		return err
	}

	image := lookupResult.image

	repository, tag := parsers.ParseRepoAndTag(newName)
	if tag == "" {
		tag = "latest"
	}
	finalName := strings.Join([]string{repository, tag}, ":")

	if !force {
		// check for collision
		_, err := p.client.InspectImage(finalName)
		if err != dockerclient.ErrNoSuchImage {
			return provider.ErrImageExists
		}
	}

	return p.dockerTag(image.ID, finalName, true)
}

// InspectImage returns information about a single image ( which can be
// identified by either its ID or its tag )
func (p *DockerProvider) InspectImage(name string) (*provider.ImageInfo, error) {
	lookupResult, err := p.lookupImage(name)
	if err != nil {
		return nil, err
	}
	return lookupResult.image, nil
}

func (p *DockerProvider) AttachToContainer(id string, in io.Reader, out io.Writer) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}

	return p.client.AttachToContainer(dockerclient.AttachToContainerOptions{
		Container:    container.ID,
		InputStream:  in,
		OutputStream: out,
		ErrorStream:  out,
		Stream:       true,
		Stdin:        true,
		Stdout:       true,
		Stderr:       true,
	})
}

type authconfig struct {
	Auth  string `json:"auth"`
	Email string `json:"email"`
}

func (p *DockerProvider) fetchAuthenticationInfoBestMatch(registry string) (dockerclient.AuthConfiguration, error) {
	if registry == "" {
		registry = "https://index.docker.io/v1/"
	}

	// if we dont have the auth configuration, use unauthenticated request
	authPath, err := p.dockerConfigLocation()
	if err != nil {
		p.logger.Warnf("failed to find authentication location, err: %v", err)
		return dockerclient.AuthConfiguration{}, nil
	}

	fileContents, err := ioutil.ReadFile(authPath)
	if err != nil {
		p.logger.Warnf("failed to find authentication details, err: %v", err)
		return dockerclient.AuthConfiguration{}, nil
	}

	// got the auth contents here, parse it and get the correct configuration
	// out
	var endpointMap map[string]authconfig
	err = json.Unmarshal(fileContents, &endpointMap)
	if err != nil {
		return dockerclient.AuthConfiguration{}, err
	}

	for endpoint, authconfig := range endpointMap {
		if strings.Contains(endpoint, registry) {
			if authconfig.Auth == "" {
				return dockerclient.AuthConfiguration{}, nil
			}
			decoded, err := base64.StdEncoding.DecodeString(authconfig.Auth)
			if err != nil {
				return dockerclient.AuthConfiguration{}, err
			}
			authParams := strings.Split(string(decoded), ":")
			if len(authParams) != 2 {
				return dockerclient.AuthConfiguration{}, provider.ErrImageAuthMisconfigured
			}
			user, password := authParams[0], authParams[1]
			return dockerclient.AuthConfiguration{
				Username: user,
				Password: password,
				Email:    authconfig.Email,
			}, nil
		}
	}

	return dockerclient.AuthConfiguration{}, nil
}

func (p *DockerProvider) dockerConfigLocation() (string, error) {
	if len(p.DockerConfigLocation) != 0 {
		return p.DockerConfigLocation, nil
	}

	// Use $HOME as the root path when fetching .dockercfg file.
	// As suggested in - https://github.com/nitrous-io/nitrous/issues/134
	configPath := path.Join(os.Getenv("HOME"), ".dockercfg")

	_, err := os.Stat(configPath)
	if err != nil {
		return "", err
	}
	return configPath, nil
}

func (p *DockerProvider) lookupImage(imageIDOrName string) (lookupImageResult, error) {
	// look up the image to test its validity
	images, err := p.ListImages()
	if err != nil {
		return lookupImageResult{}, err
	}

	repository, tag := parsers.ParseRepoAndTag(imageIDOrName)
	if tag == "" {
		tag = "latest"
	}
	for _, image := range images {
		if image.ID == imageIDOrName {
			return lookupImageResult{image: image, byID: true}, nil
		} else {
			if repository == image.Name && tag == image.Tag {
				return lookupImageResult{image: image}, nil
			}
		}
	}

	return lookupImageResult{}, provider.ErrImageNotFound
}

func (p *DockerProvider) createContainer(imageName, containerName string, config *provider.ContainerConfig) (*dockerclient.Container, error) {
	p.logger.Info("DockerProvider: creating container", containerName, "with image", imageName)
	start := time.Now()

	// if image exists locally, use it without pulling from the registry
	_, err := p.lookupImage(imageName)
	if err == provider.ErrImageNotFound {
		// get authentication details first in case we want to pull a private
		// image that requires authentication
		registry := parsers.ParseRegistry(imageName)
		auth, err := p.fetchAuthenticationInfoBestMatch(registry)
		if err != nil {
			return nil, err
		}

		repository, tag := parsers.ParseRepoAndTag(imageName)
		if tag == "" {
			tag = "latest"
		}
		err = p.client.PullImage(dockerclient.PullImageOptions{
			Repository: repository,
			Tag:        tag,
		}, auth)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	createConfig, err := p.mkContainerConfig(config)
	if err != nil {
		return nil, err
	}

	container, err := p.client.CreateContainer(dockerclient.CreateContainerOptions{
		Name:   containerName,
		Config: createConfig,
	})
	if err != nil {
		return nil, err
	}
	p.logger.TimeInfo(start, "DockerProvider: created container", containerName, container.ID, "in")

	// Get container info so we get an up-to-date HostConfig since
	// CreateContainer() *does not* return a full container struct.
	container, err = p.inspectContainer(container.ID)
	if err != nil {
		return nil, err
	}

	startConfig, err := p.mkHostConfig(container.HostConfig, config)
	if err != nil {
		return nil, err
	}

	p.logger.Info("DockerProvider: starting container", containerName)
	start = time.Now()
	err = p.client.StartContainer(container.ID, startConfig)
	p.logger.TimeInfo(start, "DockerProvider: started container", containerName, container.ID, "in")

	return container, err
}

func (p *DockerProvider) ContainerSettingsDir(containerName string) string {
	return path.Join(p.dataPath, "containers", containerName)
}

func (p *DockerProvider) storeSharedSecret(containerName string, secret string) error {
	p.logger.Info("DockerProvider: storing shared secret for", containerName)
	dir := p.ContainerSettingsDir(containerName)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}

	filename := path.Join(dir, "secret")
	err = ioutil.WriteFile(filename, []byte(secret), 0600)
	if err != nil {
		return err
	}
	return nil
}

func (p *DockerProvider) readSharedSecret(containerName string) (string, error) {
	filename := path.Join(p.ContainerSettingsDir(containerName), "secret")
	secret, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret)), nil
}

func (p *DockerProvider) createAuthorizedKeys(containerName string, publicKeys []string) error {
	p.logger.Info("DockerProvider: creating SSH authorized keys file for container", containerName, "with", len(publicKeys), "entries")
	dir := path.Join(p.ContainerSettingsDir(containerName), "ssh")
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
	keysFile := path.Join(p.ContainerSettingsDir(containerName), "ssh", "authorized_keys")
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
	return container, nil
}

func (p *DockerProvider) dockerTag(image string, newName string, force bool) error {
	repo, tag := parsers.ParseRepoAndTag(newName)
	return p.client.TagImage(image, dockerclient.TagImageOptions{
		Repo:  repo,
		Tag:   tag,
		Force: force,
	})
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

// portsFromContainer returns a mapping from container port to a slice of host
// ports. The result means different things depending on container state.
// For running containers, the actual "live" port bindings are returned.
// For stopped containers, the *configured* port bindings are returned and
// will look like this: "22" -> [] to indicate that there is a port binding for
// port 22.
func portsFromContainer(container *dockerclient.Container) map[string][]string {
	ports := make(map[string][]string)

	// If container is running, the actual "live" port bindings are set in
	// NetworkSettings.Ports by Docker.
	if container.State.Running {
		for port, bindings := range container.NetworkSettings.Ports {
			containerPort := port.Port()
			for _, binding := range bindings {
				ports[containerPort] = append(ports[containerPort], binding.HostPort)
			}
		}
		return ports
	}

	// Otherwise, container is stopped so we have to merge HostConfig and
	// ExposedPorts to determine the configured port bindings.
	for port, bindings := range container.HostConfig.PortBindings {
		containerPort := port.Port()
		for _, binding := range bindings {
			ports[containerPort] = append(ports[containerPort], binding.HostPort)
		}
	}
	for port := range container.Config.ExposedPorts {
		containerPort := port.Port()
		if _, ok := ports[containerPort]; !ok {
			ports[containerPort] = []string{}
		}
	}
	return ports
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
	return isNitrousImage(container.Config.Image)
}

func isNitrousImage(image string) bool {
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

func containerConfigFromJSON(jsonStr string) (*provider.ContainerConfig, error) {
	cfg := &provider.ContainerConfig{}
	if jsonStr == "" {
		return cfg, nil
	}
	err := json.NewDecoder(strings.NewReader(jsonStr)).Decode(&cfg)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func setDefaultContainerConfig(c *provider.ContainerConfig) {
	c.Env = append(c.Env, fmt.Sprintf("__HOST=%s", defaultDockerGateway))
}
