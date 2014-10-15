package docker_test

import (
	"fmt"
	"path/filepath"

	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker"
	. "github.com/nitrous-io/freighter/test"
	"github.com/nitrous-io/freighter/util"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("provider/docker config", func() {
	var (
		prov             *docker.DockerProvider
		datadir, sshBind string
		cnrName          = "php-box"
	)

	BeforeEach(func() {
		var err error
		datadir, err = TempDir()
		Expect(err).To(BeNil())
		sshBind = filepath.Join(datadir, "containers", cnrName, "ssh") + ":/nitrous/ssh:ro"
		logger := &util.Logger{Level: util.LogLevelQuiet}
		prov, err = docker.NewDockerProvider(logger, datadir)
		Expect(err).To(BeNil())
	})

	Describe("configFromContainer", func() {
		var cnr *dockerclient.Container

		addBinding := func(ports map[dockerclient.Port][]dockerclient.PortBinding, hostIP, hostPort, containerPort string) {
			cport := dockerclient.Port(containerPort)
			ports[cport] = append(ports[cport], dockerclient.PortBinding{
				HostIp:   hostIP,
				HostPort: hostPort,
			})
		}

		BeforeEach(func() {
			cnr = &dockerclient.Container{
				State: dockerclient.State{},
				Config: &dockerclient.Config{
					Env: []string{},
				},
				HostConfig: &dockerclient.HostConfig{
					PortBindings: map[dockerclient.Port][]dockerclient.PortBinding{},
				},
				NetworkSettings: &dockerclient.NetworkSettings{
					Ports: map[dockerclient.Port][]dockerclient.PortBinding{},
				},
			}
		})

		It("returns privileged from container", func() {
			cnr.HostConfig.Privileged = true
			Expect(prov.ConfigFromContainer(cnr).Privileged).To(BeTrue())
			cnr.HostConfig.Privileged = false
			Expect(prov.ConfigFromContainer(cnr).Privileged).NotTo(BeTrue())
		})

		It("returns cmd from container", func() {
			cnr.Config.Cmd = []string{"/sbin/init"}
			Expect(prov.ConfigFromContainer(cnr).Cmd).To(Equal([]string{"/sbin/init"}))
		})

		It("returns envs from container", func() {
			cnr.Config.Env = []string{"container=docker"}
			Expect(prov.ConfigFromContainer(cnr).Env).To(Equal([]string{"container=docker"}))
		})

		It("returns publish all from container", func() {
			cnr.HostConfig.PublishAllPorts = true
			Expect(prov.ConfigFromContainer(cnr).PublishAll).To(BeTrue())
			cnr.HostConfig.PublishAllPorts = false
			Expect(prov.ConfigFromContainer(cnr).PublishAll).NotTo(BeTrue())
		})

		It("returns port bindings from network settings if running", func() {
			cnr.State.Running = true
			cnr.NetworkSettings.Ports[dockerclient.Port("9191")] = nil
			addBinding(cnr.NetworkSettings.Ports, "0.0.0.0", "8080", "3000")
			addBinding(cnr.NetworkSettings.Ports, docker.DefaultDockerGateway, "8000", "3000")
			addBinding(cnr.NetworkSettings.Ports, docker.DefaultDockerGateway, "49123", "4000")
			config := prov.ConfigFromContainer(cnr)
			Expect(config.PortBindings).To(ConsistOf([]string{"public:8080:3000", "private:8000:3000", "private:49123:4000"}))
		})

		It("returns port bindings from hostconfig if not running", func() {
			cnr.State.Running = false
			cnr.NetworkSettings.Ports[dockerclient.Port("9191")] = nil
			addBinding(cnr.HostConfig.PortBindings, "0.0.0.0", "8080", "3000")
			addBinding(cnr.HostConfig.PortBindings, docker.DefaultDockerGateway, "49123", "4000")
			config := prov.ConfigFromContainer(cnr)
			Expect(config.PortBindings).To(ConsistOf([]string{"public:8080:3000", "private:49123:4000"}))
		})

		It("returns mounts from container", func() {
			cnr.HostConfig.Binds = []string{
				"/var/lib/freighter/containers/testcon/ssh:/nitrous/ssh:ro",
				"/var/lib/docker/vfs/dir/12345:/data",
				fmt.Sprintf("%s:%s", filepath.Join(datadir, "volumes", "music"), "/volumes/music"),
				fmt.Sprintf("%s:%s", filepath.Join(datadir, "volumes", "misc"), "/data/misc"),
			}
			config := prov.ConfigFromContainer(cnr)
			Expect(config.Mounts).To(ConsistOf([]string{
				"/var/lib/freighter/containers/testcon/ssh:/nitrous/ssh:ro",
				"/var/lib/docker/vfs/dir/12345:/data",
				"music",
				"misc:/data/misc",
			}))
		})
	})

	Describe("mkContainerConfig", func() {
		var cfg *dockerclient.Config

		Context("nitrous image", func() {
			BeforeEach(func() {
				var err error
				cfg, err = prov.MkContainerConfig(&provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "nitrousio/php", // Nitrous image.
				}, true)
				Expect(err).To(BeNil())
			})

			It("has hostname and image", func() {
				Expect(cfg.Hostname).To(Equal(cnrName))
				Expect(cfg.Image).To(Equal("nitrousio/php"))
			})

			It("has ssh volume and bind", func() {
				Expect(cfg.Volumes).To(HaveLen(1))
				_, ok := cfg.Volumes["/nitrous/ssh"]
				Expect(ok).To(BeTrue())
			})

			It("has no env", func() {
				Expect(cfg.Env).To(BeEmpty())
			})

			It("exposes 22/tcp", func() {
				_, ok := cfg.ExposedPorts["22/tcp"]
				Expect(ok).To(BeTrue())
			})
		})

		Context("non-nitrous image", func() {
			BeforeEach(func() {
				var err error
				cfg, err = prov.MkContainerConfig(&provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "custom-image",
				}, false)
				Expect(err).To(BeNil())
			})

			It("does not have Nitrous-related port settings", func() {
				Expect(cfg.ExposedPorts).To(HaveLen(0))
			})

			It("has Nitrous-related ssh volume settings", func() {
				Expect(cfg.Volumes).To(HaveLen(1))
				_, ok := cfg.Volumes["/nitrous/ssh"]
				Expect(ok).To(BeTrue())
			})
		})

		Context("with PortBindings config", func() {
			BeforeEach(func() {
				var err error
				cfg, err = prov.MkContainerConfig(&provider.ContainerConfig{
					Hostname:     cnrName,
					Image:        "nitrousio/php",
					PortBindings: []string{"public::4000", "3000:8080", "private:5000:5001", "127.0.0.1:5002:5002"},
				}, true)
				Expect(err).To(BeNil())
			})

			It("parses portspecs", func() {
				Expect(cfg.ExposedPorts).To(HaveLen(5))
				for _, p := range []dockerclient.Port{"4000", "8080", "5001", "5002", "22/tcp"} {
					Expect(cfg.ExposedPorts[p]).To(Equal(struct{}{}))
				}
			})
		})

		Context("with Cmd config", func() {
			BeforeEach(func() {
				var err error
				cfg, err = prov.MkContainerConfig(&provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "nitrousio/php",
					Cmd:      []string{"/bin/bash", "-c", "laravel"},
				}, true)
				Expect(err).To(BeNil())
			})

			It("populates Cmd", func() {
				Expect(cfg.Cmd).To(Equal([]string{"/bin/bash", "-c", "laravel"}))
			})
		})
	})

	Describe("mkHostConfig", func() {
		var (
			oldHostCfg *dockerclient.HostConfig
			hostCfg    *dockerclient.HostConfig
		)

		BeforeEach(func() {
			oldHostCfg = &dockerclient.HostConfig{
				PortBindings: map[dockerclient.Port][]dockerclient.PortBinding{
					dockerclient.Port("22"): []dockerclient.PortBinding{},
				},
				Binds: []string{sshBind},
			}
		})

		Context("nitrous image", func() {
			BeforeEach(func() {
				var err error
				hostCfg, err = prov.MkHostConfig(oldHostCfg, &provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "nitrousio/php", // Nitrous image.
				}, true)
				Expect(err).To(BeNil())
			})

			It("preserves existing port bindings", func() {
				Expect(hostCfg.PortBindings).To(HaveLen(1))
				Expect(hostCfg.PortBindings[dockerclient.Port("22")]).To(Equal([]dockerclient.PortBinding{}))
			})

			It("sets PublishAllPorts", func() {
				Expect(hostCfg.PublishAllPorts).To(BeTrue())
			})
		})

		Context("non-nitrous image", func() {
			BeforeEach(func() {
				oldHostCfg.PortBindings = map[dockerclient.Port][]dockerclient.PortBinding{}
				oldHostCfg.Binds = []string{}
				var err error
				hostCfg, err = prov.MkHostConfig(oldHostCfg, &provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "not/nitrous",
				}, false)
				Expect(err).To(BeNil())
			})

			It("does not have Nitrous-related port settings", func() {
				Expect(hostCfg.PortBindings).To(HaveLen(0))
			})

			It("has Nitrous-related ssh volume settings", func() {
				Expect(hostCfg.Binds).To(Equal([]string{sshBind}))
			})
		})

		Context("Mounts config handling (always keeps ssh bind)", func() {
			getCfg := func(cfg *provider.ContainerConfig) *dockerclient.HostConfig {
				var err error
				hostCfg, err = prov.MkHostConfig(oldHostCfg, cfg, true)
				Expect(err).To(BeNil())
				return hostCfg
			}

			It("replaces existing with mount specs", func() {
				oldHostCfg.Binds = []string{sshBind, "REMOVEME"}
				hostCfg = getCfg(&provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "nitrousio/php", // Nitrous image.
					Mounts:   []string{"data", "music:/mpd/albums"},
				})
				Expect(hostCfg.Binds).To(HaveLen(3))
				Expect(hostCfg.Binds).To(ContainElement(filepath.Join(
					datadir, "volumes/data") + ":/volumes/data"))
				Expect(hostCfg.Binds).To(ContainElement(filepath.Join(
					datadir, "volumes/music") + ":/mpd/albums"))
				Expect(hostCfg.Binds).To(ContainElement(sshBind))
				Expect(hostCfg.Binds).NotTo(ContainElement("REMOVEME"))
			})

			It("does not remove existing when Mounts is nil", func() {
				oldHostCfg.Binds = []string{sshBind, "KEEPME"}
				hostCfg = getCfg(&provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "nitrousio/php",
					Mounts:   nil,
				})
				Expect(hostCfg.Binds).To(HaveLen(2))
				Expect(hostCfg.Binds).To(ContainElement(sshBind))
				Expect(hostCfg.Binds).To(ContainElement("KEEPME"))
			})

			It("removes existing when Mounts is present but empty", func() {
				oldHostCfg.Binds = []string{sshBind, "REMOVEME"}
				hostCfg = getCfg(&provider.ContainerConfig{
					Hostname: cnrName,
					Image:    "nitrousio/php", // Nitrous image.
					Mounts:   []string{},
				})
				Expect(hostCfg.Binds).To(HaveLen(1))
				Expect(hostCfg.Binds).To(ContainElement(sshBind))
			})
		})

		Context("with PortBindings config", func() {
			BeforeEach(func() {
				var err error
				hostCfg, err = prov.MkHostConfig(oldHostCfg,
					&provider.ContainerConfig{
						Hostname:     cnrName,
						Image:        "nitrousio/php", // Nitrous image.
						PortBindings: []string{"public::4000", "3000:8080", "private:5000:5001"},
					}, true)
				Expect(err).To(BeNil())
			})

			It("parses portspecs", func() {
				Expect(hostCfg.PortBindings).To(HaveLen(3))
				portBinds := []struct {
					conPort  string
					hostIP   string
					hostPort string
				}{
					{"4000", "0.0.0.0", ""},
					{"8080", "172.17.42.1", "3000"},
					{"5001", "172.17.42.1", "5000"},
				}
				for _, pb := range portBinds {
					pbs := hostCfg.PortBindings[dockerclient.Port(pb.conPort)]
					Expect(pbs).To(HaveLen(1))
					Expect(pbs[0]).To(Equal(
						dockerclient.PortBinding{
							HostIp: pb.hostIP, HostPort: pb.hostPort}))
				}
			})
		})

		It("sets Privileged to the value of ContainerConfig.Privileged", func() {
			cnCfg := &provider.ContainerConfig{
				Hostname:   cnrName,
				Image:      "not/nitrous",
				Privileged: false,
			}

			hostCfg, err := prov.MkHostConfig(oldHostCfg, cnCfg, true)
			Expect(err).To(BeNil())
			Expect(hostCfg.Privileged).To(BeFalse())

			cnCfg.Privileged = true
			hostCfg, err = prov.MkHostConfig(oldHostCfg, cnCfg, true)
			Expect(err).To(BeNil())
			Expect(hostCfg.Privileged).To(BeTrue())
		})

		Context("with PublishAll config", func() {
			BeforeEach(func() {
				var err error
				hostCfg, err = prov.MkHostConfig(oldHostCfg,
					&provider.ContainerConfig{
						Hostname:   cnrName,
						Image:      "not/nitrous",
						PublishAll: true,
					}, true)
				Expect(err).To(BeNil())
			})

			It("sets PublishAllPorts to true", func() {
				Expect(hostCfg.PublishAllPorts).To(BeTrue())
			})
		})
	})
})
