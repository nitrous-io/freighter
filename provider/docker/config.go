package docker

import (
	"fmt"
	"path/filepath"
	"strings"

	dockerclient "github.com/fsouza/go-dockerclient"

	"github.com/nitrous-io/freighter/provider"
)

var (
	ErrInvalidPortSpec  = fmt.Errorf("port binding invalid")
	ErrInvalidMountSpec = fmt.Errorf("volume specification invalid")
)

// configFromContainer populates a ContainerConfig given a container
func (p *DockerProvider) configFromContainer(cnr *dockerclient.Container) *provider.ContainerConfig {
	config := &provider.ContainerConfig{
		Privileged: cnr.HostConfig.Privileged,
		Cmd:        cnr.Config.Cmd,
		Env:        cnr.Config.Env,
		PublishAll: cnr.HostConfig.PublishAllPorts,
		Mounts:     mountBindsFromDocker(cnr.HostConfig.Binds, p.VolumesDir("")),
	}
	portsSource := cnr.HostConfig.PortBindings
	if cnr.State.Running {
		portsSource = cnr.NetworkSettings.Ports
	}
	config.PortBindings = portBindingsFromDocker(portsSource)

	return config
}

func (p *DockerProvider) mkContainerConfig(cfg *provider.ContainerConfig, isTrustedImage bool) (*dockerclient.Config, error) {
	dcConfig := &dockerclient.Config{
		Hostname:     cfg.Hostname,
		Image:        cfg.Image,
		Volumes:      map[string]struct{}{},
		ExposedPorts: map[dockerclient.Port]struct{}{},
		Env:          []string{},
		OpenStdin:    true,
		Tty:          true,
	}

	dcConfig.Env = cfg.Env
	bs, err := portBindingsToDocker(cfg.PortBindings)
	if err != nil {
		return nil, err
	}
	for port, _ := range bs {
		dcConfig.ExposedPorts[dockerclient.Port(port)] = struct{}{}
	}

	dcConfig.Cmd = make([]string, len(cfg.Cmd))
	copy(dcConfig.Cmd, cfg.Cmd)

	if isTrustedImage {
		dcConfig.ExposedPorts[dockerclient.Port("22/tcp")] = struct{}{}
	}
	dcConfig.Volumes["/nitrous/ssh"] = struct{}{}

	return dcConfig, nil
}

func (p *DockerProvider) mkHostConfig(hostCfg *dockerclient.HostConfig, cfg *provider.ContainerConfig, isTrustedImage bool) (*dockerclient.HostConfig, error) {
	newCfg := dockerclient.HostConfig{}
	if hostCfg != nil {
		newCfg = *hostCfg
	}

	// Set defaults.
	if newCfg.Binds == nil {
		newCfg.Binds = []string{}
	}
	if newCfg.PortBindings == nil {
		newCfg.PortBindings = map[dockerclient.Port][]dockerclient.PortBinding{}
	}

	if cfg == nil {
		return &newCfg, nil
	}

	newCfg.Privileged = cfg.Privileged
	newCfg.PublishAllPorts = cfg.PublishAll
	if cfg.PortBindings != nil {
		bs, err := portBindingsToDocker(cfg.PortBindings)
		if err != nil {
			return nil, err
		}
		newCfg.PortBindings = bs
	}
	if cfg.Mounts != nil {
		ms, err := mountBindsToDocker(cfg.Mounts, p.VolumesDir(""))
		if err != nil {
			return nil, err
		}
		newCfg.Binds = ms
	}
	if isTrustedImage {
		newCfg.PublishAllPorts = true
	}

	hasSSHBind := false
	for _, bind := range newCfg.Binds {
		if strings.HasSuffix(bind, "/nitrous/ssh:ro") {
			hasSSHBind = true
			break
		}
	}
	if !hasSSHBind {
		newCfg.Binds = append(newCfg.Binds,
			p.ContainerSettingsDir(cfg.Hostname)+"/ssh:/nitrous/ssh:ro")
	}

	return &newCfg, nil
}

func mountBindsToDocker(mountSpecs []string, hostVolsPath string) ([]string, error) {
	binds := []string{}
	for _, mspec := range mountSpecs {
		// TODO do we want to validate host volume existence at this point?
		bind, err := parseMountSpec(mspec, hostVolsPath)
		if err != nil {
			return []string{}, err
		}
		binds = append(binds, bind)
	}
	return binds, nil
}

func mountBindsFromDocker(dockerSpecs []string, hostVolsPath string) []string {
	binds := []string{}
	for _, spec := range dockerSpecs {
		parts := strings.Split(spec, ":")
		if len(parts) == 3 { // hostPath:containerPath:ro style spec
			binds = append(binds, spec)
			continue
		}
		if len(parts) != 2 {
			continue
		}

		if !strings.HasPrefix(parts[0], hostVolsPath) { // non-nitrous style spec
			binds = append(binds, spec)
			continue
		}

		// nitrous volume handling
		slug := filepath.Base(parts[1])
		if parts[0] == filepath.Join(hostVolsPath, slug) && parts[1] == filepath.Join("/volumes", slug) {
			binds = append(binds, slug) // volumeSlug spec
			continue
		}
		binds = append(binds, fmt.Sprintf("%s:%s", slug, parts[1])) // volumeSlug:mountPoint spec
	}
	return binds
}

// parseMountSpec supports any of:
//   volumeSlug:mountPoint
//   volumeSlug
//   hostPath:containerPath:ro
// It appends the appropriate host path to the volume slug.  No validations on
// mountPoint are performed -- let docker handle.  If mountPoint is missing,
// mount to /volumes/<volumeSlug>
func parseMountSpec(mspec string, hostVolsPath string) (string, error) {
	var mountPoint string
	parts := strings.Split(mspec, ":")
	switch len(parts) {
	case 1:
		mountPoint = fmt.Sprintf("/volumes/%s", parts[0])
	case 2:
		mountPoint = parts[1]
	case 3:
		return mspec, nil
	default:
		return "", ErrInvalidMountSpec
	}
	return fmt.Sprintf("%s:%s", filepath.Join(hostVolsPath, parts[0]), mountPoint), nil
}

func portBindingsToDocker(portSpecs []string) (map[dockerclient.Port][]dockerclient.PortBinding, error) {
	bindings := map[dockerclient.Port][]dockerclient.PortBinding{}
	for _, portSpec := range portSpecs {
		p, pb, err := parsePortSpec(portSpec)
		if err != nil {
			return nil, err
		}
		if _, ok := bindings[p]; ok {
			bindings[p] = append(bindings[p], pb)
		} else {
			bindings[p] = []dockerclient.PortBinding{pb}
		}
	}
	return bindings, nil
}

// parsePortSpec supports any of:
//   public:hostPort:containerPort
//   private:hostPort:containerPort
//   public::containerPort
//   private::containerPort
//   hostPort:containerPort ... : (which defaults to private)
//
//   private means it will be bound to the defaultDockerGateway
//   public means that will be accessible on the internet
func parsePortSpec(portSpec string) (dockerclient.Port, dockerclient.PortBinding, error) {
	parts := strings.Split(portSpec, ":")
	var hostIP, hostPort, containerPort string
	switch { // TODO more stringent checking
	case len(parts) == 3 && parts[1] == "": // ip::containerPort
		hostIP, hostPort, containerPort = parts[0], "", parts[2]
	case len(parts) == 3 && parts[1] != "": // ip:hostPort:containerPort
		hostIP, hostPort, containerPort = parts[0], parts[1], parts[2]
	case len(parts) == 2: // hostPort:containerPort
		hostIP, hostPort, containerPort = defaultDockerGateway, parts[0], parts[1]
	default:
		return "", dockerclient.PortBinding{}, ErrInvalidPortSpec
	}

	if hostIP == "public" {
		hostIP = "0.0.0.0"
	} else {
		hostIP = defaultDockerGateway
	}
	return dockerclient.Port(containerPort),
		dockerclient.PortBinding{HostIp: hostIP, HostPort: hostPort}, nil
}

// portBindingsFromDocker converts docker portbindings back to
// provider.ContainerConfig's format. It is almost the inverse of
// portBindingsToDocker (except that unassigned host ports will be filled in).
// Which means port specs will only have one of:
//   public:hostPort:containerPort
//   private:hostPort:containerPort
func portBindingsFromDocker(dockerBindings map[dockerclient.Port][]dockerclient.PortBinding) []string {
	bindings := []string{}
	for containerPort, portBindings := range dockerBindings {
		if portBindings == nil {
			// TODO investigate how else to resolve
			continue
		}
		for _, pb := range portBindings {
			access := "public"
			if pb.HostIp == defaultDockerGateway {
				access = "private"
			}
			bindings = append(bindings, fmt.Sprintf("%s:%s:%s", access, pb.HostPort, containerPort))
		}
	}

	return bindings
}
