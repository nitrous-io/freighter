package docker

import (
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

	// list of images 'blessed' by Nitrous. If tag is included, the specific
	// tag is checked for, otherwise any tag is allowed
	nitrousImages = []string{"ubuntu-bare:trusty", "ubuntu:trusty", "ruby", "python", "nodejs", "golang", "php"}

	errNoCommand = errors.New("no command specified")
)

const defaultDockerGateway = "172.17.42.1"
const nitrousImageEnv = "NITROUS_IMAGE_VERSION"

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

// Create creates and starts a container.
// If the container cannot be started, the container info *and* a
// provider.StartFailedError is returned.
func (p *DockerProvider) CreateStart(name, imageName, secret, configJSON string) (*provider.ContainerInfo, error) {
	createdCnr, err := p.create(name, imageName, secret, configJSON)
	if err != nil {
		if derr, ok := err.(*dockerclient.Error); ok {
			if strings.Contains(derr.Message, "No command specified") {
				return nil, provider.ErrCmdConfig
			}
		}
		return nil, err
	}

	startedCnr, err := p.Start(createdCnr.ID, configJSON)
	if err != nil {
		// Return container info as well if container was created but failed
		// to start.
		p.logger.Errorf("DockerProvider: failed to start container %s: %v", name, err)
		info, _ := p.GetInfo(createdCnr.ID)
		return info, &provider.StartFailedError{Err: err}
	}

	return startedCnr, nil
}

func (p *DockerProvider) create(name, imageName, secret, configJSON string) (*provider.ContainerInfo, error) {
	if err := p.storeSharedSecret(name, secret); err != nil {
		return nil, err
	}

	if err := p.createAuthorizedKeys(name, []string{}); err != nil {
		return nil, err
	}

	cfg, err := containerConfigFromJSON(configJSON)
	if err != nil {
		return nil, err
	}
	setDefaultContainerConfig(cfg)
	cfg.Hostname = name
	cfg.Image = imageName

	createConfig, err := p.mkContainerConfig(cfg, p.isNitrousImage(cfg.Image))
	if err != nil {
		return nil, err
	}

	container, err := p.client.CreateContainer(dockerclient.CreateContainerOptions{
		Name:   name,
		Config: createConfig,
	})
	if err != nil {
		if err == dockerclient.ErrNoSuchImage {
			return nil, provider.ErrImageNotFound
		}
		return nil, err
	}
	return p.GetInfo(container.ID)
}

func (p *DockerProvider) Commit(id, imageName string) (*provider.ImageInfo, error) {
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

	startConfig, err := p.mkHostConfig(container.HostConfig, cfg, p.isNitrousImage(cfg.Image))
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

func (p *DockerProvider) Terminate(id string, removeSettings bool) (*provider.ContainerInfo, error) {
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

	if removeSettings {
		if err := os.RemoveAll(p.ContainerSettingsDir(info.Name)); err != nil {
			return nil, err
		}
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

func (p *DockerProvider) ClearPublicKeys(id string) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}

	name := nameFromContainer(container)
	return p.createAuthorizedKeys(name, []string{})
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

// Reconfig recreates a container from its original image with new config,
// since at this time docker container configs are frozen once set.
func (p *DockerProvider) Reconfig(id, configJSON string) (*provider.ContainerInfo, error) {
	fromCnr, err := p.Stop(id)
	if err != nil {
		return nil, err
	}
	name := fromCnr.Name
	secret, err := p.readSharedSecret(name)
	if err != nil {
		return nil, err
	}

	imageName := fmt.Sprintf("%s-backup-%d", name, time.Now().UnixNano())
	if _, err := p.Commit(id, imageName); err != nil {
		return nil, err
	}

	if err := p.backup(id, name); err != nil {
		return nil, err
	}
	if _, err := p.Terminate(id, false); err != nil {
		return nil, err
	}

	errf := "reconfig failed on %s during %s after termination: %s" // (id, step, error)
	toCnr, err := p.create(name, fromCnr.Image, secret, configJSON)
	if err != nil {
		return nil, fmt.Errorf(errf, id, "create", err)
	}

	if err := p.restore(name, fromCnr.ID, toCnr.ID); err != nil {
		return toCnr, fmt.Errorf(errf, id, "restore", err)
	}

	startedCnr, err := p.Start(toCnr.ID, configJSON)
	if err != nil {
		return toCnr, fmt.Errorf(errf, id, "start", err)
	}

	return startedCnr, nil
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

func (p *DockerProvider) BuildImage(o provider.BuildImageOptions) error {
	opts := dockerclient.BuildImageOptions{
		Name:                o.Name,
		InputStream:         o.InputStream,
		OutputStream:        o.OutputStream,
		RawJSONStream:       o.RawJSONStream,
		Remote:              o.Remote,
		RmTmpContainer:      true,
		ForceRmTmpContainer: true,
	}

	return p.client.BuildImage(opts)
}

func (p *DockerProvider) RemoveImage(imageIDOrName string) error {
	lookupResult, err := p.lookupImage(imageIDOrName)
	if err != nil {
		return err
	}

	image, useID := lookupResult.image, lookupResult.byID
	var removeID string
	if useID {
		removeID = image.ID
	} else {
		removeID = strings.Join([]string{image.Name, image.Tag}, ":")
	}
	err = p.client.RemoveImage(removeID)
	if err == nil {
		return nil
	}

	// TODO: This can be removed once this https://github.com/docker/docker/pull/7963/files
	// has been released
	if !useID {
		_, lookupErr := p.lookupImage(imageIDOrName)
		if lookupErr == provider.ErrImageNotFound {
			tagErr := p.client.TagImage(image.ID, dockerclient.TagImageOptions{Repo: image.Name, Tag: image.Tag})
			if tagErr != nil {
				return tagErr
			}
		}
	}

	if strings.Contains(err.Error(), "tagged in multiple repositories") {
		return provider.ErrImageShared
	}

	if strings.Contains(err.Error(), "Conflict, cannot delete") {
		// TODO: this is retarded, we should patch go-dockerclient to report a proper error
		// if status code == 409
		return provider.ErrImageInUse
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
func (p *DockerProvider) PushImage(imageIDOrName, dest string, output io.Writer, rawJSONStream bool) error {
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

	if err := p.dockerTag(image.ID, dest, true); err != nil {
		return err
	}

	repository, tag := parsers.ParseRepoAndTag(dest)
	// while PushImageOptions contains a Registry attribute, it does not work
	// docker push XXXXXX calls the API with the full name (registry and repository) as the image name
	pushOpts := dockerclient.PushImageOptions{
		Name:          repository,
		Tag:           tag,
		OutputStream:  output,
		RawJSONStream: rawJSONStream,
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

func (p *DockerProvider) ResizeContainerTTY(id string, height, width int) error {
	container, err := p.inspectContainer(id)
	if err != nil {
		return err
	}

	return p.client.ResizeContainerTTY(container.ID, height, width)
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
			return lookupImageResult{image: image}, nil
		} else {
			if repository == image.Name && tag == image.Tag {
				return lookupImageResult{image: image}, nil
			}
		}
	}

	return lookupImageResult{}, provider.ErrImageNotFound
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

func (p *DockerProvider) isNitrousImage(fullName string) bool {
	imageInfo, err := p.client.InspectImage(fullName)
	if err != nil {
		return false
	}

	if imageInfo.Config == nil {
		return false
	}

	for _, val := range imageInfo.Config.Env {
		split := strings.SplitN(val, "=", 2)
		if len(split) == 2 && split[0] == nitrousImageEnv {
			return true
		}
	}
	return false
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
