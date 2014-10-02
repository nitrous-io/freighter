package docker_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker"
	"github.com/nitrous-io/freighter/provider/docker/parsers"
	. "github.com/nitrous-io/freighter/test"
	"github.com/nitrous-io/freighter/util"
	"github.com/nitrous-io/nitrous-common/hmactoken"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func Test(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "provider/docker")
}

const (
	nitrousImage = "nitrousio/ubuntu-bare:trusty"
	busyboxImage = "busybox:latest"
)

var pulledImages = make(map[string]struct{})

func pullImage(name string) {
	if _, ok := pulledImages[name]; ok {
		return
	}
	start := time.Now()
	fmt.Printf("Pulling %s ...", name)
	err := exec.Command("docker", "pull", name).Run()
	fmt.Printf("Pulled after %.1fs\n", time.Since(start).Seconds())
	Expect(err).To(BeNil(), "failed to pull %s image", name)
	pulledImages[name] = struct{}{}
}

var _ = Describe("provider/docker", func() {
	var (
		prov    *docker.DockerProvider
		secret  = "s3cr3t"
		datadir string
	)

	BeforeEach(func() {
		var err error
		datadir = os.Getenv("FREIGHTERD_BASE")

		if datadir == "" {
			datadir, err = TempDir()
			Expect(err).To(BeNil())
		}

		logger := &util.Logger{Level: util.LogLevelQuiet}
		prov, err = docker.NewDockerProvider(logger, datadir)
		Expect(err).To(BeNil())
	})

	cleanUpContainer := func(name string) {
		out, err := exec.Command("docker", "stop", "--time=1", name).CombinedOutput()
		Expect(err).To(BeNil(), "docker stop %s failed with err: %+v", name, string(out))
		out, err = exec.Command("docker", "rm", "-f", name).CombinedOutput()
		Expect(err).To(BeNil(), "docker rm -f %s failed with err: %+v", name, string(out))
	}

	doInspect := func(id, format string) string {
		out, err := exec.Command("docker", "inspect", "-f", format, id).CombinedOutput()
		Expect(err).To(BeNil(), "docker inspect -f %v %v failed with output: %v", format, id, string(out))
		return strings.TrimSpace(string(out))
	}

	getID := func(name string) string {
		return doInspect(name, "{{.Id}}")
	}

	getNetworkSettingsPort := func(id, port string) string {
		return doInspect(id, fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%s") 0).HostPort}}`, port))
	}

	getHost := func(id string) string {
		return doInspect(id, "{{.NetworkSettings.IPAddress}}")
	}

	getGateway := func(id string) string {
		return doInspect(id, "{{.NetworkSettings.Gateway}}")
	}

	getEnv := func(id string) string {
		return doInspect(id, "{{.Config.Env}}")
	}

	expectExposedPort := func(id, port string) {
		out := doInspect(id, fmt.Sprintf(`{{(index .Config.ExposedPorts "%s")}}`, port))
		Expect(out).NotTo(ContainSubstring("<no value>"),
			"Port %s not exposed. docker inspect .Config.ExposedPorts output: %v", port, out)
	}

	type configInfo struct {
		Cmd        []string
		Entrypoint []string
	}

	type stateInfo struct {
		ExitCode   int
		StartedAt  time.Time
		FinishedAt time.Time
	}

	type containerInfo struct {
		Config configInfo
		State  stateInfo
	}

	getContainerInfo := func(id string) *containerInfo {
		out := doInspect(id, `{ "Config":{{json .Config}}, "State":{{json .State}} }`)
		var v containerInfo
		err := json.Unmarshal([]byte(out), &v)
		Expect(err).To(BeNil())
		return &v
	}

	createContainer := func(tag string) string {
		out, err := exec.Command("docker", "run", "-d", tag, "/bin/bash").CombinedOutput()
		Expect(err).To(BeNil(), "Failed to create container, output: %s", string(out))

		return string(out)
	}

	// createImages writes a dockerfile from contents and
	// builds a docker image from it, returning the name of the image.
	// If contents is zero value, it uses a default dockerfile.
	createImages := func(count int, name string, noEntropy bool, contents string) []string {
		pullImage(nitrousImage)
		dockerLoc, err := ioutil.TempDir("", "DockerTest")
		Expect(err).To(BeNil())
		dockerFile, err := os.Create(filepath.Join(dockerLoc, "Dockerfile"))
		Expect(err).To(BeNil())

		entropy := name
		if noEntropy {
			entropy = "thing"
		}

		if contents == "" {
			contents = fmt.Sprintf(`
				FROM %s
				MAINTAINER Nitrous.IO, hello@nitrous.io
				RUN echo '%s' >> /tmp/info
				CMD /bin/bash
			`, nitrousImage, entropy)
		}
		dockerFile.WriteString(contents)
		dockerFile.Close()

		list := []string{}
		for index := 1; index <= count; index++ {
			findableName := fmt.Sprintf("%v:n%v", name, index)
			out, err := exec.Command("docker", "build", "-t", findableName, dockerLoc).CombinedOutput()
			Expect(err).To(BeNil(), "Failed to build image, output: %s", string(out))
			list = append(list, findableName)
		}

		err = os.RemoveAll(dockerLoc)
		Expect(err).To(BeNil())

		return list
	}

	removeImages := func(names []string) {
		args := append([]string{"rmi", "-f"}, names...)
		out, err := exec.Command("docker", args...).CombinedOutput()
		Expect(err).To(BeNil(), "Failed to remove images, output: %v", string(out))
	}

	Describe(".Create()", func() {
		BeforeEach(func() {
			pullImage(nitrousImage)
		})

		It("creates a running container", func() {
			name := RandomName("freightertest")
			info, err := prov.CreateStart(name, nitrousImage, secret, "")
			Expect(err).To(BeNil())
			defer cleanUpContainer(name)
			id := getID(name)
			host := getHost(id)
			gateway := getGateway(id)

			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(name))
			Expect(info.Image).To(Equal(nitrousImage))
			Expect(info.ImageID).To(Equal(getID(nitrousImage)))
			Expect(info.Host).To(Equal(host))
			Expect(info.Gateway).To(Equal(gateway))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			sshPort := getNetworkSettingsPort(id, "22/tcp")
			Expect(info.Ports["22"]).To(ContainElement(sshPort))
			expectExposedPort(info.ID, "22/tcp")
			Expect(getEnv(id)).To(ContainSubstring("__HOST=172.17.42.1"))

			c := getContainerInfo(id)
			Expect(info.Cmd).To(Equal(c.Config.Cmd))
			Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
			Expect(info.ExitCode).To(Equal(c.State.ExitCode))
			Expect(info.StartedAt).To(Equal(c.State.StartedAt))
			Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))

			settingsDir := prov.ContainerSettingsDir(name)
			_, err = os.Stat(settingsDir)
			Expect(err).To(BeNil()) // container settings dir exists

			authKeysFile := path.Join(settingsDir, "ssh", "authorized_keys")
			_, err = os.Stat(authKeysFile)
			Expect(err).To(BeNil()) // authorized_keys file exists

			sharedSecretFile := path.Join(settingsDir, "secret")
			fi, err := os.Stat(sharedSecretFile)
			Expect(err).To(BeNil())                        // shared secret file exists
			Expect(fi.Mode()).To(Equal(os.FileMode(0600))) // shared secret file has perm 0600
			s, err := ioutil.ReadFile(sharedSecretFile)
			Expect(string(s)).To(Equal(secret))
		})

		Context("when image has no CMD or ENTRYPOINT", func() {
			It("creates and starts if CMD is given to container config", func() {
				imgs := createImages(1, "nocmd", false, `
					FROM busybox
					CMD []
				`)
				defer removeImages(imgs)

				imgName := imgs[0]
				name := RandomName("freightertest")
				cnr, err := prov.CreateStart(name, imgName, secret, "")
				if cnr != nil {
					defer cleanUpContainer(name)
				}
				Expect(err).To(MatchError(provider.ErrCmdConfig))

				configJSON := `{ "Cmd": ["yes"] }`
				cnr, err = prov.CreateStart(name, imgName, secret, configJSON)
				Expect(err).To(BeNil())
				defer cleanUpContainer(name)
				Expect(cnr.Cmd).To(Equal([]string{"yes"}))
			})
		})

		It("returns an ErrImageNotFound when creating from an image that does not exist", func() {
			name := RandomName("freightertest")
			_, err := prov.CreateStart(name, "non-existent-image", secret, "")
			Expect(err).To(MatchError(provider.ErrImageNotFound))
		})

		Context("when container is created but cannot be started", func() {
			It("creates a stopped container and returns the container info and a StartFailedError", func() {
				// Create container with config that prevents it from
				// started.
				name := RandomName("freightertest")
				info, err := prov.CreateStart(name, nitrousImage, secret,
					`{"PortBindings": ["127.0.0.1:99999:99999"]}`) // Port out of range.
				defer cleanUpContainer(name)

				_, ok := err.(*provider.StartFailedError)
				Expect(ok).To(BeTrue())
				Expect(info.ID).To(Equal(getID(name)))
				Expect(info.Name).To(Equal(name))
				Expect(info.Image).To(Equal(nitrousImage))
				Expect(info.ImageID).To(Equal(getID(nitrousImage)))
				Expect(info.State).To(Equal(provider.ContainerStateStopped))
				Expect(info.Host).To(Equal(""))
				Expect(info.Gateway).To(Equal(""))
				hostPorts, ok := info.Ports["22"]
				Expect(ok).To(BeTrue())
				Expect(hostPorts).To(HaveLen(0))
			})
		})

		Context("after a container is created", func() {
			var (
				name string
				id   string
				info *provider.ContainerInfo

				// set to false for tests which handle their own termination
				terminateAfterTest = true
			)

			BeforeEach(func() {
				var err error
				name = RandomName("freightertest")
				info, err = prov.CreateStart(name, nitrousImage, secret, "")
				Expect(err).To(BeNil())
				id = getID(name)
			})

			AfterEach(func() {
				if terminateAfterTest {
					cleanUpContainer(name)
				}
			})

			Describe(".List()", func() {
				It("returns a list of all containers", func() {
					// Create another container.
					container2Name := RandomName("freightertest2")
					container2Info, err := prov.CreateStart(container2Name, nitrousImage, secret, "")
					Expect(err).To(BeNil())
					defer cleanUpContainer(container2Name)

					list, err := prov.List()
					Expect(err).To(BeNil())
					Expect(list).To(HaveLen(2))

					var c1, c2 *provider.ContainerInfo
					for _, c := range list {
						if c.ID == id {
							c2 = c
						} else if c.ID == container2Info.ID {
							c1 = c
						}
					}

					imageID := getID(nitrousImage)

					Expect(c1.ID).To(Equal(container2Info.ID))
					Expect(c1.Name).To(Equal(container2Name))
					Expect(c1.Image).To(Equal(nitrousImage))
					Expect(c1.ImageID).To(Equal(imageID))
					Expect(c1.Host).To(Equal(getHost(container2Info.ID)))
					Expect(c1.Gateway).To(Equal(getGateway(container2Info.ID)))
					Expect(c1.State).To(Equal(provider.ContainerStateRunning))
					sshPort := getNetworkSettingsPort(container2Info.ID, "22/tcp")
					Expect(c1.Ports["22"]).To(ContainElement(sshPort))
					expectExposedPort(c1.ID, "22/tcp")

					c := getContainerInfo(container2Info.ID)
					Expect(c1.Cmd).To(Equal(c.Config.Cmd))
					Expect(c1.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(c1.ExitCode).To(Equal(c.State.ExitCode))
					Expect(c1.StartedAt).To(Equal(c.State.StartedAt))
					Expect(c1.FinishedAt).To(Equal(c.State.FinishedAt))

					Expect(c2.ID).To(Equal(id))
					Expect(c2.Name).To(Equal(name))
					Expect(c2.Image).To(Equal(nitrousImage))
					Expect(c2.ImageID).To(Equal(imageID))
					Expect(c2.Host).To(Equal(getHost(id)))
					Expect(c2.Gateway).To(Equal(getGateway(id)))
					Expect(c2.State).To(Equal(provider.ContainerStateRunning))
					sshPort = getNetworkSettingsPort(id, "22/tcp")
					Expect(c2.Ports["22"]).To(ContainElement(sshPort))
					expectExposedPort(id, "22/tcp")

					c = getContainerInfo(id)
					Expect(c2.Cmd).To(Equal(c.Config.Cmd))
					Expect(c2.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(c2.ExitCode).To(Equal(c.State.ExitCode))
					Expect(c2.StartedAt).To(Equal(c.State.StartedAt))
					Expect(c2.FinishedAt).To(Equal(c.State.FinishedAt))
				})
			})

			Describe(".GetInfo()", func() {
				It("returns container info", func() {
					imageID := getID(nitrousImage)

					info, err := prov.GetInfo(id)
					Expect(err).To(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(nitrousImage))
					Expect(info.ImageID).To(Equal(imageID))
					Expect(info.Host).To(Equal(getHost(info.ID)))
					Expect(info.Gateway).To(Equal(getGateway(info.ID)))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))
					Expect(fmt.Sprintf("[%s]", strings.Join(info.Env, " "))).To(Equal(getEnv(id)))
					sshPort := getNetworkSettingsPort(info.ID, "22/tcp")
					Expect(info.Ports["22"]).To(ContainElement(sshPort))
					expectExposedPort(info.ID, "22/tcp")

					c := getContainerInfo(id)
					Expect(info.Cmd).To(Equal(c.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(info.ExitCode).To(Equal(c.State.ExitCode))
					Expect(info.StartedAt).To(Equal(c.State.StartedAt))
					Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))
				})
			})

			Describe(".Start()", func() {
				expectContainerToBeRunning := func() {
					out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
					Expect(err).To(BeNil())
					Expect(out).To(ContainSubstring("true"))
				}

				expectContainerInfoToHaveBeenReturned := func(info *provider.ContainerInfo) {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(nitrousImage))
					Expect(info.ImageID).To(Equal(getID(nitrousImage)))
					Expect(info.Host).To(Equal(getHost(id)))
					Expect(info.Gateway).To(Equal(getGateway(id)))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))
					sshPort := getNetworkSettingsPort(id, "22/tcp")
					Expect(info.Ports["22"]).To(ContainElement(sshPort))
					expectExposedPort(info.ID, "22/tcp")

					c := getContainerInfo(id)
					Expect(info.Cmd).To(Equal(c.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(info.ExitCode).To(Equal(c.State.ExitCode))
					Expect(info.StartedAt).To(Equal(c.State.StartedAt))
					Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))
				}

				Context("when the container is running", func() {
					It("does nothing", func() {
						info, err := prov.Start(id, "")
						Expect(err).To(BeNil())
						expectContainerToBeRunning()
						expectContainerInfoToHaveBeenReturned(info)
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						err := exec.Command("docker", "stop", id).Run()
						Expect(err).To(BeNil())
					})

					It("starts the container", func() {
						info, err := prov.Start(id, "")
						Expect(err).To(BeNil())
						expectContainerToBeRunning()
						expectContainerInfoToHaveBeenReturned(info)
					})
				})
			})

			Describe(".Stop()", func() {
				var info *provider.ContainerInfo

				expectContainerToBeStopped := func() {
					out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
					Expect(err).To(BeNil())
					Expect(out).To(ContainSubstring("false"))
				}

				expectContainerInfoToHaveBeenReturned := func() {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Host).To(Equal(""))
					Expect(info.Gateway).To(Equal(""))
					Expect(info.State).To(Equal(provider.ContainerStateStopped))
					hostPorts, ok := info.Ports["22"]
					Expect(ok).To(BeTrue())
					Expect(hostPorts).To(HaveLen(0))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(nitrousImage))
					Expect(info.ImageID).To(Equal(getID(nitrousImage)))

					c := getContainerInfo(id)
					Expect(info.Cmd).To(Equal(c.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(info.ExitCode).To(Equal(c.State.ExitCode))
					Expect(info.StartedAt).To(Equal(c.State.StartedAt))
					Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))
				}

				JustBeforeEach(func() {
					var err error
					info, err = prov.Stop(id)
					Expect(err).To(BeNil())
				})

				Context("when the container is running", func() {
					It("stops the container", func() {
						expectContainerToBeStopped()
						expectContainerInfoToHaveBeenReturned()
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						_, err := prov.Stop(id)
						Expect(err).To(BeNil())
					})

					It("does nothing", func() {
						expectContainerToBeStopped()
						expectContainerInfoToHaveBeenReturned()
					})
				})
			})

			Describe(".Restart()", func() {
				expectContainerInfoToHaveBeenReturned := func(info *provider.ContainerInfo) {
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(nitrousImage))
					Expect(info.ImageID).To(Equal(getID(nitrousImage)))
					Expect(info.Gateway).To(Equal(getGateway(id)))
					Expect(info.Host).To(Equal(getHost(id)))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))
					sshPort := getNetworkSettingsPort(id, "22/tcp")
					Expect(info.Ports["22"]).To(ContainElement(sshPort))
					expectExposedPort(info.ID, "22/tcp")

					c := getContainerInfo(id)
					Expect(info.Cmd).To(Equal(c.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(info.ExitCode).To(Equal(c.State.ExitCode))
					Expect(info.StartedAt).To(Equal(c.State.StartedAt))
					Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))
				}

				Context("when the container is running", func() {
					It("restarts the container", func() {
						out, err := exec.Command("docker", "inspect", "-f", "{{.State.StartedAt}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						prevStartTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
						Expect(err).To(BeNil())
						info, err := prov.Restart(id)
						Expect(err).To(BeNil())
						expectContainerInfoToHaveBeenReturned(info)

						out, err = exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						Expect(out).To(ContainSubstring("true"))
						out, err = exec.Command("docker", "inspect", "-f", "{{.State.StartedAt}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						newStartTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
						Expect(prevStartTime.Before(newStartTime)).To(BeTrue())
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						err := exec.Command("docker", "stop", id).Run()
						Expect(err).To(BeNil())
					})

					It("starts the container", func() {
						info, err := prov.Restart(id)
						Expect(err).To(BeNil())
						expectContainerInfoToHaveBeenReturned(info)
						out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						Expect(out).To(ContainSubstring("true"))
					})
				})
			})

			Describe(".Terminate()", func() {
				expectContainerToBeTerminatedAndPortReleased := func(info *provider.ContainerInfo) {
					out, err := exec.Command("docker", "inspect", id).CombinedOutput()
					Expect(err).NotTo(BeNil())
					Expect(out).To(ContainSubstring("No such image or container"))
					settingsDir := prov.ContainerSettingsDir(info.Name)
					_, err = os.Stat(settingsDir)
					Expect(os.IsNotExist(err)).To(BeTrue(), "Terminate did not remove settings dir %s", settingsDir)
				}

				expectContainerInfoToHaveBeenReturned := func(info *provider.ContainerInfo, origInfo *containerInfo) {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(nitrousImage))
					Expect(info.ImageID).To(Equal(getID(nitrousImage)))
					Expect(info.Host).To(Equal(""))
					Expect(info.Gateway).To(Equal(""))
					Expect(info.Ports).To(BeEmpty())
					Expect(info.State).To(Equal(""))

					Expect(info.Cmd).To(Equal(origInfo.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(origInfo.Config.Entrypoint))
					Expect(info.StartedAt).To(Equal(origInfo.State.StartedAt))
					Expect(info.FinishedAt.IsZero()).To(BeFalse())
				}

				BeforeEach(func() {
					terminateAfterTest = false
				})

				Context("when the container is running", func() {
					It("terminates the container", func() {
						// Get container info before terminating.
						origInfo := getContainerInfo(id)

						info, err := prov.Terminate(id, true)
						Expect(err).To(BeNil())
						expectContainerToBeTerminatedAndPortReleased(info)
						expectContainerInfoToHaveBeenReturned(info, origInfo)
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						err := exec.Command("docker", "stop", id).Run()
						Expect(err).To(BeNil())
					})

					It("terminates the container", func() {
						// Get container info before terminating.
						origInfo := getContainerInfo(id)

						info, err := prov.Terminate(id, true)
						Expect(err).To(BeNil())
						expectContainerToBeTerminatedAndPortReleased(info)
						expectContainerInfoToHaveBeenReturned(info, origInfo)
					})
				})
			})

			Describe(".Reconfig()", func() {
				BeforeEach(func() {
					terminateAfterTest = false
				})

				It("changes the container id and settings", func() {
					defer func() { // remove image backups
						imageList, err := prov.ListImages()
						Expect(err).To(BeNil(), "error listing images when trying to delete backups")
						toRemove := []string{}
						for _, img := range imageList {
							parts := strings.Split(img.Name, "-")
							if len(parts) >= 3 && parts[len(parts)-2] == "backup" && len(parts[len(parts)-1]) == 19 { // 19 == length of UnixNano
								toRemove = append(toRemove, img.Name)
							}
						}
						if len(toRemove) > 0 {
							removeImages(toRemove)
						}
					}()
					configJSON := `{
						"Env": ["LANG=C","PAGER=more"],
						"PortBindings": ["private:65000:3000", "public:8888:8888"]
					}`

					// In tests the container data will be inside the dind
					// container and not easy to access unless the tests are
					// running inside dind as well.
					// The diff path wouldn't exist on the host where tests are
					// running, so we have to create it.
					rootPath := filepath.Join(docker.BaseAUFSDir, "diff", id, "root")
					filePath := filepath.Join(rootPath, ".vimrc")
					err := os.MkdirAll(rootPath, 0700)
					Expect(err).To(BeNil())
					err = ioutil.WriteFile(filePath, []byte("set nocompatible"), 0600)
					Expect(err).To(BeNil())

					reconfig1, err := prov.Reconfig(id, configJSON)
					if reconfig1 != nil { // id will change, default cleanup won't catch
						// if the second reconfig succeeds, cleanUp of the
						// first container not necessary
						defer func() {
							out, _ := exec.Command("docker", "inspect", reconfig1.ID).CombinedOutput()
							if !strings.Contains(string(out), "No such image or container") {
								cleanUpContainer(reconfig1.ID)
							}
						}()
					}
					Expect(err).To(BeNil())
					Expect(reconfig1.ID).NotTo(Equal(id), "container ID should be changed after recreating")
					Expect(doInspect(reconfig1.ID, "{{.Config.Env}}")).To(ContainSubstring("LANG=C"))
					Expect(doInspect(reconfig1.ID,
						"{{range $p, $stub := .Config.ExposedPorts}}{{$p}} {{end}}")).
						To(Equal("22/tcp 3000 8888"))

					newRootPath := filepath.Join(docker.BaseAUFSDir, "diff", reconfig1.ID, "root")
					bs, err := ioutil.ReadFile(filepath.Join(newRootPath, ".vimrc"))
					Expect(err).To(BeNil(), "old container data not migrated")
					Expect(string(bs)).To(Equal("set nocompatible"))

					newConfigJSON := `{
						"Env": ["PAGER=less"],
						"PortBindings": ["public:4000:4000"]
					}`
					reconfig2, err := prov.Reconfig(reconfig1.ID, newConfigJSON)
					if reconfig2 != nil { // id will change, default cleanup won't catch
						defer cleanUpContainer(reconfig2.ID)
					}
					Expect(err).To(BeNil())
					// reconfig settings are not additive; the given config is
					// the one used, except for any default settings that are
					// added (port 22 for nitrous images, etc)
					Expect(doInspect(reconfig2.ID, "{{.Config.Env}}")).To(ContainSubstring("PAGER=less"))
					Expect(doInspect(reconfig2.ID, "{{.Config.Env}}")).NotTo(ContainSubstring("LANG=C"))
					Expect(doInspect(reconfig2.ID,
						"{{range $p, $stub := .Config.ExposedPorts}}{{$p}} {{end}}")).
						To(Equal("22/tcp 4000"))
				})

				It("returns detailed error on failure to complete", func() {
					configJSON := `{
						"PortBindings": ["public::123456789"]
					}`
					rootPath := filepath.Join(docker.BaseAUFSDir, "diff", id, "root")
					err := os.MkdirAll(rootPath, 0700)
					Expect(err).To(BeNil())

					info, err := prov.Reconfig(id, configJSON)
					if info != nil { // id will change, default cleanup won't catch
						defer cleanUpContainer(info.ID)
					}
					Expect(err).NotTo(BeNil())
					Expect(err.Error()).To(ContainSubstring("reconfig failed on " + id))
					Expect(err.Error()).To(ContainSubstring("during start after termination"))
					Expect(info).NotTo(BeNil(), "created container not returned")
					Expect(info.ID).NotTo(Equal(id))
				})
			})

			Describe(".Commit()", func() {
				It("creates an image", func() {
					image, err := prov.Commit(id, "committed")
					Expect(err).To(BeNil())
					defer removeImages([]string{image.ID})
					Expect(image).NotTo(BeNil())
					Expect(image.Name).To(Equal("committed"))
					Expect(image.Tag).To(Equal("latest"))
					out, err := exec.Command("docker", "images", "--no-trunc").CombinedOutput()
					Expect(err).To(BeNil())
					Expect(out).To(ContainSubstring(image.ID))
				})

				It("creates an image with a custom tag", func() {
					image, err := prov.Commit(id, "archived:deprecated")
					Expect(err).To(BeNil())
					defer removeImages([]string{image.ID})
					Expect(image).NotTo(BeNil())
					Expect(image.Name).To(Equal("archived"))
					Expect(image.Tag).To(Equal("deprecated"))
					out, err := exec.Command("docker", "images", "--no-trunc").CombinedOutput()
					Expect(err).To(BeNil())
					Expect(out).To(ContainSubstring(image.ID))
				})
			})

			Describe("AttachToContainer()", func() {
				It("attaches to container's stdin and stdout streams", func() {
					var stdout bytes.Buffer
					r := strings.NewReader("send value")
					errCh := make(chan error)
					go func() {
						errCh <- prov.AttachToContainer(id, r, &stdout)
					}()

					// FIXME Ideally we don't have to stop a container but a bug
					// in either go-dockerclient or Docker causes
					// AttachToContainer() to not return even after the output
					// stream is closed for writing.
					// This happens to containers that do not write any output.
					err := exec.Command("docker", "stop", id).Run()
					Expect(err).To(BeNil())

					Expect(<-errCh).To(BeNil())
				})
			})

			Describe("Logs()", func() {
				It("writes the container's logs", func() {
					// FIXME Ideally this should test against a container with
					// actual log output and probably exercise the follow,
					// timestamps and tail arguments.
					err := prov.Logs(id, ioutil.Discard, false, false, "")
					Expect(err).To(BeNil())
				})
			})

			Describe("VerifyToken()", func() {
				var correctToken string

				BeforeEach(func() {
					var err error
					correctToken, err = hmactoken.Generate([]string{"attach"}, secret)
					Expect(err).To(BeNil())
				})

				It("doesn't return an error when verifying a correct token", func() {
					err := prov.VerifyToken(id, correctToken, "attach")
					Expect(err).To(BeNil())
				})

				It("returns ErrUnverifiable when the container's shared secret cannot be read", func() {
					secretFile := path.Join(prov.ContainerSettingsDir(name), "secret")
					newpath := path.Join(prov.ContainerSettingsDir(name), "somewhere")
					err := os.Rename(secretFile, newpath)
					Expect(err).To(BeNil())
					defer os.Rename(newpath, secretFile)

					err = prov.VerifyToken(id, correctToken, "attach")
					Expect(err).To(MatchError(provider.ErrUnverifiable))
				})

				It("returns ErrTokenVerificationFailed when token is invalid", func() {
					invalidToken, err := hmactoken.Generate([]string{"attach"}, "wrong secret")
					Expect(err).To(BeNil())

					err = prov.VerifyToken(id, invalidToken, "attach")
					Expect(err).To(MatchError(provider.ErrTokenVerificationFailed))
				})

				It("returns ErrTokenVerificationFailed when token has incorrect permissions", func() {
					invalidToken, err := hmactoken.Generate([]string{"admin"}, secret)
					Expect(err).To(BeNil())

					err = prov.VerifyToken(id, invalidToken, "attach")
					Expect(err).To(MatchError(provider.ErrTokenVerificationFailed))
				})

				It("returns ErrTokenExpired when token has expired", func() {
					hmactoken.DefaultValidityPeriod = 0
					expiredToken, err := hmactoken.Generate([]string{"attach"}, secret)
					Expect(err).To(BeNil())

					err = prov.VerifyToken(id, expiredToken, "attach")
					Expect(err).To(MatchError(provider.ErrTokenExpired))
				})
			})

			Context("with public keys", func() {
				var (
					pubkey1      = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQCxCeR/jJna6xXELDV8HF51QQPbj++JNDE4W/nqLyhzRTEDDnAfyp8gXW/EXXos8w5XXLak6eUDXE/QI6o8blX39RYoT47u45v4b2scN0ZiS5McmESo193KwWH3gXHbnHWywzPy7+GrRF3nHm/uidvp0tqh4ywqIZujaMjST1sIOQ== test@example.com"
					pubkey2      string
					fingerprint1 = "f7:de:4d:6f:54:6b:b9:d5:78:a9:62:15:e2:ad:ec:bb"
					fingerprint2 string
					sshkeypath   string
				)

				BeforeEach(func() {
					var err error
					pubkey2 = strings.TrimSpace(string(TestSSHPubKey))
					fingerprint2, err = util.Md5Fingerprint(pubkey2)
					Expect(err).To(BeNil())

					sshkeypath = TempFile("")
					ioutil.WriteFile(sshkeypath, []byte(TestSSHPrivKey), 0600)
				})

				AfterEach(func() {
					os.Remove(sshkeypath)
				})

				Context("with SSH enabled containers", func() {
					sshTarget := func() string {
						if ip := os.Getenv("DOCKER_IP"); ip != "" {
							return fmt.Sprintf("action@%s", ip)
						} else {
							return "action@localhost"
						}
					}

					expectSSHToWork := func(port, sshkeypath string) {
						time.Sleep(2 * time.Second)
						out, err := exec.Command("ssh", sshTarget(), "-i", sshkeypath, "-p", port, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "echo success").CombinedOutput()
						Expect(out).To(ContainSubstring("success"))
						Expect(err).To(BeNil())
					}

					expectSSHToNotWork := func(port, sshkeypath string) {
						time.Sleep(2 * time.Second)
						out, err := exec.Command("ssh", sshTarget(), "-i", sshkeypath, "-p", port, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "echo success").CombinedOutput()
						Expect(out).To(ContainSubstring("Permission denied"))
						Expect(err).ToNot(BeNil())
					}

					BeforeEach(func() {
						pubkey1 = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQCxCeR/jJna6xXELDV8HF51QQPbj++JNDE4W/nqLyhzRTEDDnAfyp8gXW/EXXos8w5XXLak6eUDXE/QI6o8blX39RYoT47u45v4b2scN0ZiS5McmESo193KwWH3gXHbnHWywzPy7+GrRF3nHm/uidvp0tqh4ywqIZujaMjST1sIOQ== test@example.com"
						fingerprint1 = "f7:de:4d:6f:54:6b:b9:d5:78:a9:62:15:e2:ad:ec:bb"
						fingerprint2 = "9c:b5:99:82:88:b3:6a:85:5f:89:77:6a:86:46:08:8d"
						err := prov.AddPublicKeys(id, []string{pubkey1, pubkey2})
						Expect(err).To(BeNil())
					})

					Describe(".ListPublicKeys()", func() {
						itWorks := func() {
							It("returns list of pub keys and the corresponding fingerprints", func() {
								pubkeys, err := prov.ListPublicKeys(id)
								Expect(err).To(BeNil())
								Expect(pubkeys).To(Equal(map[string]string{
									fingerprint1: pubkey1,
									fingerprint2: pubkey2,
								}))
							})
						}

						Context("when the container is running", func() {
							itWorks()
						})

						Context("when the container is stopped", func() {
							BeforeEach(func() {
								err := exec.Command("docker", "stop", id).Run()
								Expect(err).To(BeNil())
							})

							itWorks()
						})
					})

					Describe(".AddPublicKeys()", func() {
						pubkey3 := "ssh-dss AAAAB3NzaC1kc3MAAACBAKPiMSg6vvWZAK1a+Vj+yPH0kRpCDUqhNCyPJd8yIfvs8c+uGgnXkDG6vW3gamyxkFilxgLvqW89NnvmcBRoAXkrT5e8uiciLJ2CqJuqZPHFNf3T298xFleh/43X5dxgS6CdODzrkreFe8XD1R1DozMgChU8wGVpQNQWGKU/x7xXAAAAFQCa8OP3WB4V2mNA/8cN7fj4gxDbLwAAAIEAhxHz0xu/EAWvq+nZ5CXnWZ2g++t1IEJcJWnTTIQakU+aZV0DxQ4hesrFiXllBX/oUTgr8ZlqYgqTy4+lHpXIhesNy3k9OjjU8/dYm9zZZw3TWIL64dAEp9L0xNZ7MFgOVvDwpjNqjUrbDCIujk7iZ+J/hFFoTqRxSeciX+cbusQAAACBAIcGybaGJ2Z4E21jR0iD05kNhJm97CJHavhH6fgxkeecVdXU2+k/DjLHTz7UfhbAtg6XqE0s5PVUgh0P/stQRGyosTmasYE3hTwwOXYQ2k/HLqM/ZGwEXW2kLSpi2MROowQf25wQrWmJBjTQ8mjG5jm0KOCX2jFf0QN3qTzVovSc vagrant@ubuntu-14"

						Context("with valid public keys", func() {
							var (
								pubkey4      = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBIC/WqXhoJC0FSaocOrx/uyneeqH32CUNbva4L6PdY7mIZ99mTTpdREowMo1T3pvY46JxwL6X3stuhZmIYNJEc0="
								fingerprint3 = "9b:e8:01:40:ff:35:c5:12:05:dc:cd:17:3e:a9:55:53"
								fingerprint4 = "2a:43:73:52:6c:21:b5:5f:cc:cd:73:21:2b:05:50:de"
							)

							itWorks := func() {
								It("adds given pub keys to the authorized_keys file for the container", func() {
									err := prov.AddPublicKeys(id, []string{pubkey3, pubkey4, pubkey1}) // dupe key should be ignored
									Expect(err).To(BeNil())
									pubkeys, err := prov.ListPublicKeys(id)
									Expect(err).To(BeNil())
									Expect(pubkeys).To(Equal(map[string]string{
										fingerprint1: pubkey1,
										fingerprint2: pubkey2,
										fingerprint3: pubkey3,
										fingerprint4: pubkey4,
									}))
								})
							}

							sshWorks := func() {
								It("allows SSH access to a container", func() {
									err := prov.AddPublicKeys(id, []string{pubkey3, pubkey4, pubkey1}) // dupe key should be ignored
									Expect(err).To(BeNil())
									sshPort := getNetworkSettingsPort(id, "22/tcp")
									expectSSHToWork(sshPort, sshkeypath)
								})
							}

							Context("when the container is running", func() {
								itWorks()
								sshWorks()
							})

							Context("when the container is stopped", func() {
								BeforeEach(func() {
									err := exec.Command("docker", "stop", id).Run()
									Expect(err).To(BeNil())
								})

								itWorks()
							})
						})

						Context("with an invalid public key in the mix", func() {
							pubkey4 := "ssh-rsa S4!#AFLJKN*F@NF12f"

							itDoesNotWork := func() {
								It("does not add given pub keys to the authorized_keys file for the container", func() {
									err := prov.AddPublicKeys(id, []string{pubkey3, pubkey4, pubkey1})
									Expect(err).To(MatchError(util.ErrInvalidPublicKey))
									pubkeys, err := prov.ListPublicKeys(id)
									Expect(err).To(BeNil())
									Expect(pubkeys).To(Equal(map[string]string{
										fingerprint1: pubkey1,
										fingerprint2: pubkey2,
									}))
								})
							}

							Context("when the container is running", func() {
								itDoesNotWork()
							})

							Context("when the container is stopped", func() {
								BeforeEach(func() {
									err := exec.Command("docker", "stop", id).Run()
									Expect(err).To(BeNil())
								})

								itDoesNotWork()
							})
						})
					})

					Describe(".ClearPublicKeys()", func() {
						itWorks := func() {
							It("removes all keys from the container", func() {
								err := prov.ClearPublicKeys(id)
								Expect(err).To(BeNil())
								pubkeys, err := prov.ListPublicKeys(id)
								Expect(err).To(BeNil())
								Expect(pubkeys).To(Equal(map[string]string{}))
							})
						}

						sshStopsWorking := func() {
							It("disallows SSH access to the container", func() {
								sshPort := getNetworkSettingsPort(id, "22/tcp")
								expectSSHToWork(sshPort, sshkeypath)
								err := prov.ClearPublicKeys(id)
								Expect(err).To(BeNil())
								expectSSHToNotWork(sshPort, sshkeypath)
							})
						}

						Context("when the container is running", func() {
							itWorks()
							sshStopsWorking()
						})

						Context("when the container is stopped", func() {
							BeforeEach(func() {
								err := exec.Command("docker", "stop", id).Run()
								Expect(err).To(BeNil())
							})

							itWorks()
						})
					})

					Describe(".RemovePublicKeys()", func() {
						Context("when a pub key with a given fingerprint is found", func() {

							itWorks := func() {
								It("removes the matching pub key from the authorized_keys file", func() {
									err := prov.RemovePublicKey(id, fingerprint2)
									Expect(err).To(BeNil())
									pubkeys, err := prov.ListPublicKeys(id)
									Expect(err).To(BeNil())
									Expect(pubkeys).To(Equal(map[string]string{
										fingerprint1: pubkey1,
									}))
								})
							}

							sshStopsWorking := func() {
								It("disallows SSH access to the container", func() {
									sshPort := getNetworkSettingsPort(id, "22/tcp")
									expectSSHToWork(sshPort, sshkeypath)
									err := prov.RemovePublicKey(id, fingerprint2)
									Expect(err).To(BeNil())
									expectSSHToNotWork(sshPort, sshkeypath)
								})
							}

							Context("when the container is running", func() {
								itWorks()
								sshStopsWorking()
							})

							Context("when the container is stopped", func() {
								BeforeEach(func() {
									err := exec.Command("docker", "stop", id).Run()
									Expect(err).To(BeNil())
								})

								itWorks()
							})
						})

						Context("when a pub key with a given fingerprint is not found", func() {
							itDoesNotWork := func() {
								It("does nothing", func() {
									err := prov.RemovePublicKey(id, "11:11:11:11:11:11:11:11:11:11:11:11:11:11:11:11")
									Expect(err).To(BeNil())
									pubkeys, err := prov.ListPublicKeys(id)
									Expect(err).To(BeNil())
									Expect(pubkeys).To(Equal(map[string]string{
										fingerprint1: pubkey1,
										fingerprint2: pubkey2,
									}))
								})
							}

							Context("when the container is running", func() {
								itDoesNotWork()
							})

							Context("when the container is stopped", func() {
								BeforeEach(func() {
									err := exec.Command("docker", "stop", id).Run()
									Expect(err).To(BeNil())
								})

								itDoesNotWork()
							})
						})
					})
				})
			})
		})
	})

	Describe(".Create() with custom options", func() {
		// getPortBindings output looks like "[map[HostIp:127.0.0.1 HostPort:1234]]"
		// or "<no value>" if bindings for given port do not exist
		getPortBindings := func(id, port string) string {
			return doInspect(id, fmt.Sprintf(
				`{{index .NetworkSettings.Ports "%s"}}`, port))
		}

		// given docker inspect output "Volumes": {
		//   "/etc/dotfiles": "/var/lib/freighter/volumes/dotfiles",
		//   "/volumes/backups": "/var/lib/freighter/volumes/backups"
		// }
		// returns
		//   /etc/dotfiles /var/lib/freighter/volumes/dotfiles\n
		//   /volumes/backups /var/lib/freighter/volumes/backups\n
		getVolumes := func(id string) string {
			return doInspect(id,
				"{{range $cnrPath, $hostPath := .Volumes}}{{$cnrPath}} {{$hostPath}}\n{{end}}")
		}

		BeforeEach(func() {
			pullImage(nitrousImage)
		})

		It("creates container with options", func() {
			name := RandomName("freightertest")
			configJSON := `{
			  "Env": ["LANG=C"],
			  "Mounts": ["dotfiles:/etc/dotfiles","backups"],
				"PortBindings": ["private:65000:3000", "public:8888:8888"]
			}`
			info, err := prov.CreateStart(name, nitrousImage, secret, configJSON)
			Expect(err).To(BeNil())
			defer cleanUpContainer(name)

			id := getID(name)
			imageID := getID(nitrousImage)
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(name))
			Expect(info.Image).To(Equal(nitrousImage))
			Expect(info.ImageID).To(Equal(imageID))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			Expect(doInspect(id, "{{.Config.Env}}")).To(ContainSubstring("LANG=C"))
			Expect(doInspect(id,
				"{{range $p, $stub := .Config.ExposedPorts}}{{$p}} {{end}}")).
				To(Equal("22/tcp 3000 8888"))

			port3000Binding := getPortBindings(id, "3000")
			Expect(port3000Binding).To(ContainSubstring("HostIp:172.17.42.1"))
			Expect(port3000Binding).To(ContainSubstring("HostPort:65000"))

			port8888Binding := getPortBindings(id, "8888")
			Expect(port8888Binding).To(ContainSubstring("HostIp:0.0.0.0"))
			Expect(port8888Binding).To(ContainSubstring("HostPort:8888"))

			port22Binding := getPortBindings(id, "22/tcp")
			Expect(port22Binding).To(ContainSubstring("HostIp:0.0.0.0"))
			Expect(port22Binding).To(MatchRegexp(`HostPort:49[0-9][0-9][0-9]`)) // Docker will use 49000-49900

			containerDir := filepath.Join(datadir, "containers")
			volumeDir := filepath.Join(datadir, "volumes")
			vols := getVolumes(id)
			Expect(vols).To(ContainSubstring(
				fmt.Sprintf("/etc/ssh/freighter %s/%s/ssh", containerDir, name)))
			Expect(vols).To(ContainSubstring(
				fmt.Sprintf("/etc/dotfiles %s", filepath.Join(volumeDir, "dotfiles"))))
			Expect(vols).To(ContainSubstring(
				fmt.Sprintf("/volumes/backups %s", filepath.Join(volumeDir, "backups"))))

			// should be split out but kept here due to each container create taking >10s
			// It("persists PortBindings,Binds on next start if no options given")
			_, err = prov.Stop(id)
			Expect(err).To(BeNil())
			_, err = prov.Start(id, "")
			Expect(err).To(BeNil())

			port3000Binding = getPortBindings(id, "3000")
			Expect(port3000Binding).To(ContainSubstring("HostIp:172.17.42.1"))
			Expect(port3000Binding).To(ContainSubstring("HostPort:65000"))

			vols = getVolumes(id)
			Expect(vols).To(ContainSubstring("/etc/ssh/freighter"))
			Expect(vols).To(ContainSubstring("dotfiles"))
			Expect(vols).To(ContainSubstring("backups"))

			// It("overrides existing PortBindings,Binds on next start if options given")
			_, err = prov.Stop(id)
			Expect(err).To(BeNil())
			newConfig := strings.Replace(configJSON, "3000", "4242/tcp", 1)
			newConfig = strings.Replace(newConfig, "backups", "restores", 1)
			_, err = prov.Start(id, newConfig)
			Expect(err).To(BeNil())

			port4242Binding := getPortBindings(id, "4242/tcp")
			Expect(port4242Binding).To(ContainSubstring("HostIp:172.17.42.1"))
			Expect(port4242Binding).To(ContainSubstring("HostPort:65000"))
			Expect(getPortBindings(id, "3000/tcp")).To(Equal("<no value>"))

			vols = getVolumes(id)
			Expect(vols).To(ContainSubstring("/etc/ssh/freighter"))
			Expect(vols).To(ContainSubstring("dotfiles"))
			Expect(vols).To(ContainSubstring("restores"))
			Expect(doInspect(id, fmt.Sprintf(
				`{{.HostConfig.Binds}}`))).NotTo(ContainSubstring("backups"))
		})
	})

	Describe(".ListImages()", func() {
		imageSize := func(id string) int64 {
			sizeInspect := doInspect(id, "{{.Size}}")
			parsedSize, err := strconv.ParseInt(sizeInspect, 10, 64)
			Expect(err).To(BeNil())
			return parsedSize
		}

		Context("with one image", func() {
			var (
				publicImages []string
			)

			BeforeEach(func() {
				publicImages = createImages(1, "basic", false, "")
			})

			AfterEach(func() {
				removeImages(publicImages)
			})

			It("should fetch basic info about an Image", func() {
				imageList, err := prov.ListImages()
				Expect(err).To(BeNil())

				i1 := imageList[0]

				Expect(i1.Name).To(Equal("basic"))
				Expect(i1.Tag).To(Equal("n1"))

				Expect(i1.ActualSize).To(Equal(imageSize(i1.ID)))

				timeStr := doInspect(i1.ID, "{{.Created}}")
				expectingTime, err := time.Parse(time.RFC3339, timeStr)
				Expect(err).To(BeNil())
				expectingUTime := expectingTime.Unix()
				Expect(i1.CreatedAt).To(Equal(expectingUTime))
			})
		})

		Context("when the same image is under multiple repositories", func() {
			var (
				createdImages []string
			)

			BeforeEach(func() {
				createdImages = createImages(2, "multiple", true, "")
			})

			AfterEach(func() {
				removeImages(createdImages)
			})

			It("should break them out into separate entries", func() {
				images, err := prov.ListImages()
				Expect(err).To(BeNil())

				var i1, i2 *provider.ImageInfo
				for _, i := range images {
					if i.Name == "multiple" && i.Tag == "n1" {
						i1 = i
					}
					if i.Name == "multiple" && i.Tag == "n2" {
						i2 = i
					}
				}

				Expect(i1).NotTo(BeNil())
				Expect(i2).NotTo(BeNil())
				Expect(i1.ID).To(Equal(i2.ID))
			})
		})
	})

	Describe(".BuildImage()", func() {
		It("streams output from Docker", func() {
			prov, err := docker.NewDockerProviderWithHost(
				&util.Logger{Level: util.LogLevelQuiet},
				datadir,
				"tcp://127.0.0.1:1234", // TCP address: so that go-dockerclient uses HTTP.
			)
			Expect(err).To(BeNil())

			body := `
				{"stream":"Step 0 : FROM ubuntu:latest\n"}
				{"stream":" ---\u003e 4300eb9d3c8d\n"}
				{"stream":"Step 1 : MAINTAINER docker <eng@docker.com>\n"}
				{"stream":" ---\u003e Using cache\n"}
				{"stream":" ---\u003e 3a3ed758c370\n"}
				{"stream":"Step 2 : CMD /usr/bin/top\n"}
				{"stream":" ---\u003e Running in 36b1479cc2e4\n"}
				{"stream":" ---\u003e 4b6188aebe39\n"}
				{"stream":"Removing intermediate container 36b1479cc2e4\n"}
				{"stream":"Successfully built 4b6188aebe39\n"}
			`
			// Replace dockerclient's HTTP client's RoundTripper with our fake
			// response.
			fakeRT := &FakeRoundTripper{
				message: body,
				status:  http.StatusOK,
				header: map[string]string{
					"Content-Type": "application/json",
				},
			}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: fakeRT}

			in := bytes.NewBufferString(`build context tarball contents`)
			out := bytes.NewBuffer(nil)
			opts := provider.BuildImageOptions{
				Name:          "myimage",
				InputStream:   in,
				OutputStream:  out,
				RawJSONStream: true,
			}
			err = prov.BuildImage(opts)
			Expect(err).To(BeNil())
			Expect(out.String()).To(Equal(body))

			req := fakeRT.requests[0]
			b, _ := ioutil.ReadAll(req.Body)
			Expect(string(b)).To(Equal(`build context tarball contents`))
			assertBuildImageOptions(req, dockerclient.BuildImageOptions{
				Name:                "myimage",
				RmTmpContainer:      true,
				ForceRmTmpContainer: true,
				RawJSONStream:       true,
			})
		})
	})

	Describe(".RemoveImage()", func() {
		Context("when an ID is provided", func() {
			It("should be deleted", func() {
				images := createImages(1, "removable", false, "")

				id := doInspect(images[0], "{{.Id}}")
				Expect(id).ToNot(BeEmpty())
				err := prov.RemoveImage(id)
				Expect(err).To(BeNil())

				out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", images[0]).CombinedOutput()
				Expect(out).To(ContainSubstring("Error: No such image or container:"))
			})

			Context("with running containers using that ID", func() {
				It("should not delete but preserve the tag of the image", func() {
					i1 := createImages(1, "multremovable", true, "")
					defer removeImages(i1)

					id1 := doInspect(i1[0], "{{.Id}}")
					Expect(id1).ToNot(BeEmpty())

					containerId := strings.TrimRight(createContainer(i1[0]), "\n")
					Expect(containerId).ToNot(BeEmpty())

					err := prov.RemoveImage(i1[0])
					Expect(err).To(Equal(provider.ErrImageInUse))

					id1 = doInspect(i1[0], "{{.Id}}")
					Expect(id1).ToNot(BeEmpty())

					cleanUpContainer(containerId)
				})
			})

			Context("with multiple images under ID", func() {
				It("should not allow for deletion", func() {
					i1 := createImages(1, "multremovable", true, "")
					defer removeImages(i1)
					i2 := createImages(1, "multremovable2", true, "")
					defer removeImages(i2)

					id1 := doInspect(i1[0], "{{.Id}}")
					id2 := doInspect(i2[0], "{{.Id}}")
					Expect(id1).ToNot(BeEmpty())
					Expect(id1).To(Equal(id2))

					err := prov.RemoveImage(id1)
					Expect(err).To(Equal(provider.ErrImageShared))

					id1 = doInspect(i1[0], "{{.Id}}")
					Expect(id1).ToNot(BeEmpty())
				})
			})
		})

		Context("when a Name is provided", func() {
			It("should be deleted", func() {
				images := createImages(1, "removable", false, "")

				err := prov.RemoveImage(images[0])
				Expect(err).To(BeNil())

				out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", images[0]).CombinedOutput()
				Expect(out).To(ContainSubstring("Error: No such image or container:"))
			})
		})
	})

	Describe(".PullImage()", func() {
		var (
			tempAuthFile *os.File
		)

		BeforeEach(func() {
			var err error
			tempAuthFile, err = ioutil.TempFile("", "cfg")
			Expect(err).To(BeNil())

			/* credentials are
			test:1234xxcc
			testo:Password1
			magic:abc34x1
			*/
			authFileSrc := `
			{
				"https://index.docker.io/v1/": { "auth": "dGVzdDoxMjM0eHhjYwo=", "email": "docker@callumj.com" },
				"quay.io": { "auth": "dGVzdG86UGFzc3dvcmQxCg==", "email": "quay@callumj.com" },
				"cloud.inc": { "auth": "bWFnaWM6YWJjMzR4MQo=", "email": "cloud@callumj.com" },
				"magic.io": {}
			}
			`

			_, err = tempAuthFile.WriteString(authFileSrc)
			Expect(err).To(BeNil())
			err = tempAuthFile.Close()
			Expect(err).To(BeNil())

			// the tests uses FakeRoundTripper so we inject a random tcp address here
			// instead of using the unix socket so go-dockerclient can inherit
			// the FakeRoundTripper
			logger := &util.Logger{Level: util.LogLevelQuiet}
			prov, err = docker.NewDockerProviderWithHost(logger, datadir, "tcp://127.0.0.1:1234")
			Expect(err).To(BeNil())
			// FIXME: remove this stuff
			prov.DockerConfigLocation = tempAuthFile.Name()
		})

		AfterEach(func() {
			os.Remove(tempAuthFile.Name())
		})

		It("streams the docker output", func() {
			body := `
			{"status":"Pulling..."}
			{"status":"Pulling", "progress":"1 B/ 100 B", "progressDetail":{"current":1, "total":100}}
			`
			rt := &FakeRoundTripper{
				message: body,
				status:  http.StatusOK,
				header: map[string]string{
					"Content-Type": "application/json",
				},
			}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}
			stdout := bytes.NewBuffer(nil)

			err := prov.PullImage("nitrousio/nodejs", stdout, true)
			Expect(err).To(BeNil())
			Expect(stdout.String()).To(Equal(body), "unexpected stream output")
		})

		It("should allow for importing by default hub", func() {
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			err := prov.PullImage("nitrousio/nodejs", ioutil.Discard, true)
			Expect(err).To(BeNil())

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "nitrousio/nodejs",
			})
			assertRegistryAuth(r, dockerclient.AuthConfiguration{
				Username: "test",
				Password: "1234xxcc\n",
				Email:    "docker@callumj.com",
			})
		})

		It("should allow for importing by a tagged image", func() {
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			err := prov.PullImage("ubuntu:10.04", ioutil.Discard, true)
			Expect(err).To(BeNil())

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "ubuntu",
				Tag:        "10.04",
			})
		})

		It("should allow for importing by simple remote registry", func() {
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			err := prov.PullImage("quay.io/ubuntu", ioutil.Discard, true)
			Expect(err).To(BeNil())

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "quay.io/ubuntu",
			})
			assertRegistryAuth(r, dockerclient.AuthConfiguration{
				Username: "testo",
				Password: "Password1\n",
				Email:    "quay@callumj.com",
			})
		})

		It("should allow for importing by remote registry", func() {
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			err := prov.PullImage("quay.io/quay/ubuntu", ioutil.Discard, true)
			Expect(err).To(BeNil())

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "quay.io/quay/ubuntu",
			})
			assertRegistryAuth(r, dockerclient.AuthConfiguration{
				Username: "testo",
				Password: "Password1\n",
				Email:    "quay@callumj.com",
			})
		})

		It("should allow even when auth credentials could not be located", func() {
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			prov.PullImage("cloudy.io/nodejs", ioutil.Discard, true)

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "cloudy.io/nodejs",
			})
			assertRegistryAuth(r, dockerclient.AuthConfiguration{})
		})

		It("should allow even a malformed config", func() {
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			prov.PullImage("magic.io/quay/ubuntu", ioutil.Discard, true)

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "magic.io/quay/ubuntu",
			})
			assertRegistryAuth(r, dockerclient.AuthConfiguration{})
		})

		It("should allow to pull without credentials when we dont have a auth configuration", func() {
			os.Remove(tempAuthFile.Name())
			rt := &FakeRoundTripper{message: "", status: http.StatusOK}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			prov.PullImage("cloudy.io/nodejs", ioutil.Discard, true)

			r := rt.requests[0]
			assertPullOptions(r, dockerclient.PullImageOptions{
				Repository: "cloudy.io/nodejs",
			})
			assertRegistryAuth(r, dockerclient.AuthConfiguration{})
		})

		It("should surface PullImage errors", func() {
			rt := &FakeRoundTripper{message: `{"error":"Sorry!"}`, status: http.StatusInternalServerError}
			cl := prov.Client()
			cl.HTTPClient = &http.Client{Transport: rt}

			stdout := bytes.NewBuffer(nil)
			err := prov.PullImage("quay/ubuntu", stdout, false)

			dockerErr, ok := err.(*dockerclient.Error)
			Expect(ok).To(BeTrue(), "invalid error type %q", err)
			Expect(dockerErr.Status).To(Equal(http.StatusInternalServerError), "invalid http status")
			Expect(dockerErr.Message).To(Equal(`{"error":"Sorry!"}`), "invalid error message")
		})
	})

	Describe(".TagImage()", func() {

		verifyImageExistence := func(name string) {
			parts := strings.Split(name, ":")
			Expect(parts).To(HaveLen(2))
			repo, tag := parts[0], parts[1]

			knownImages, err := prov.ListImages()
			Expect(err).To(BeNil())
			var targetImage *provider.ImageInfo
			for _, image := range knownImages {

				if image.Name == repo && image.Tag == tag {
					targetImage = image
					break
				}
			}

			Expect(targetImage).ToNot(BeNil())
			err = prov.RemoveImage(name)
			Expect(err).To(BeNil())
		}

		It("should allow for tagging images without tags", func() {
			created := createImages(1, "withoutly", false, "")
			err := prov.TagImage(created[0], "withoutly2", false)
			Expect(err).To(BeNil())

			verifyImageExistence("withoutly2:latest")
			removeImages(created)
		})

		It("should allow for tagging images with tags", func() {
			created := createImages(1, "tagly", false, "")
			err := prov.TagImage(created[0], "tagly2:1907", false)
			Expect(err).To(BeNil())

			verifyImageExistence("tagly2:1907")
			removeImages(created)
		})

		Context("image tag already exists", func() {

			It("should not override existing tags with no force", func() {
				created := createImages(1, "nooverridely", false, "")
				err := prov.TagImage(created[0], "nooverridely:n1", false)
				Expect(err).To(Equal(provider.ErrImageExists))
				removeImages(created)
			})

			It("should override existing tags with force", func() {
				target := createImages(1, "overridely", false, "")
				destination := createImages(1, "overridelyreplacey", false, "")
				curId := doInspect(destination[0], "{{.Id}}")

				err := prov.TagImage(target[0], "overridelyreplacey:n1", true)
				Expect(err).To(BeNil())

				newId := getID("overridelyreplacey:n1")
				Expect(newId).ToNot(Equal(curId))
				removeImages(target)
				removeImages(destination)
			})

		})

	})

	Describe(".PushImage()", func() {
		It("should not push if the image is not accessible", func() {
			err := prov.PushImage("invalid-image-name", "dont-care")
			Expect(err).To(Equal(provider.ErrImageNotFound))
		})

		Context("with an dockercfg authentication file", func() {
			var (
				mockProxy    *MockDockerProxy
				tempAuthFile *os.File
				srcImage     string
			)

			BeforeEach(func() {
				var err error

				images := createImages(1, "namely", false, "")
				srcImage = images[0]

				mockProxy = &MockDockerProxy{}
				tempAuthFile, err = ioutil.TempFile("", "cfg")
				Expect(err).To(BeNil())

				/* credentials are
				test:1234xxcc
				testo:Password1
				magic:abc34x1
				*/
				authFileSrc := `
				{
				  "https://index.docker.io/v1/": {
				    "auth": "dGVzdDoxMjM0eHhjYwo=",
				    "email": "docker@callumj.com"
				  },
				  "quay.io": {
				    "auth": "dGVzdG86UGFzc3dvcmQxCg==",
				    "email": "quay@callumj.com"
				  },
				  "cloud.inc": {
				    "auth": "bWFnaWM6YWJjMzR4MQo=",
				    "email": "cloud@callumj.com"
				  },
				  "magic.io": {}
				}
				`

				_, err = tempAuthFile.WriteString(authFileSrc)
				Expect(err).To(BeNil())
				err = tempAuthFile.Close()
				Expect(err).To(BeNil())

				prov.DockerClientProxy = mockProxy
				prov.DockerConfigLocation = tempAuthFile.Name()
			})

			AfterEach(func() {
				removeImages([]string{srcImage})
				os.Remove(tempAuthFile.Name())
			})

			It("should support pushing to the default Docker registry endpoint", func() {
				err := prov.PushImage(srcImage, "callumj/test1:n1")
				defer removeImages([]string{"callumj/test1:n1"})
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Name).To(Equal("callumj/test1"))
				Expect(mockProxy.PushImageOpts.Tag).To(Equal("n1"))
				Expect(mockProxy.PushImageAuth.Username).To(Equal("test"))
				Expect(mockProxy.PushImageAuth.Password).To(ContainSubstring("1234xxcc"))
				Expect(mockProxy.PushImageAuth.Email).To(Equal("docker@callumj.com"))
			})

			It("does not set tag if not specified", func() {
				err := prov.PushImage(srcImage, "callumj/test1")
				defer removeImages([]string{"callumj/test1"})
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Tag).To(Equal(""))
			})

			It("sets tag if specified", func() {
				err := prov.PushImage(srcImage, "callumj/test1:latest")
				defer removeImages([]string{"callumj/test1:latest"})
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Tag).To(Equal("latest"))
			})

			It("should support pushing to an alternative registry endpoint", func() {
				err := prov.PushImage(srcImage, "quay.io/callumj/test1:tag")
				defer removeImages([]string{"quay.io/callumj/test1:tag"})
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Name).To(Equal("quay.io/callumj/test1"))
				Expect(mockProxy.PushImageOpts.Tag).To(Equal("tag"))
				Expect(mockProxy.PushImageAuth.Username).To(Equal("testo"))
				Expect(mockProxy.PushImageAuth.Password).To(ContainSubstring("Password1"))
				Expect(mockProxy.PushImageAuth.Email).To(Equal("quay@callumj.com"))
			})

			It("pushes the image if destination repository name is not specified", func() {
				name := srcImage
				repo, tag := parsers.ParseRepoAndTag(name)

				err := prov.PushImage(name, "")
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Name).To(Equal(repo))
				Expect(mockProxy.PushImageOpts.Tag).To(Equal(tag))
			})

			It("tags the image with the destination repository name", func() {
				err := prov.PushImage(srcImage, "callumj/test1:mytag")
				defer removeImages([]string{"callumj/test1:mytag"})
				Expect(err).To(BeNil())

				srcID := doInspect(srcImage, "{{.Id}}")
				destID := doInspect("callumj/test1:mytag", "{{.Id}}")
				Expect(destID).NotTo(BeEmpty())
				Expect(destID).To(Equal(srcID))
			})

			Context("when destination repository already exists locally", func() {
				var (
					existingImageName = "test/existing"
					existingImageID   string
				)

				BeforeEach(func() {
					err := exec.Command("docker", "tag", "-f", nitrousImage, existingImageName).Run()
					Expect(err).To(BeNil())
					o, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", existingImageName).CombinedOutput()
					existingImageID = strings.TrimSpace(string(o))
					Expect(err).To(BeNil())
					Expect(existingImageID).NotTo(BeEmpty())
				})

				AfterEach(func() {
					removeImages([]string{existingImageName})
				})

				It("overrides the local image by tagging it", func() {
					err := prov.PushImage(srcImage, existingImageName)
					Expect(err).To(BeNil())

					Expect(mockProxy.PushImageCalled).To(BeTrue())
					Expect(mockProxy.PushImageOpts.Name).To(Equal(existingImageName))
					Expect(mockProxy.PushImageOpts.Tag).To(Equal(""))

					newID := doInspect(existingImageName, "{{.Id}}")
					Expect(newID).NotTo(Equal(existingImageID))
				})
			})

			It("returns an error when auth credentials could not be located", func() {
				err := prov.PushImage(srcImage, "thingyquay.io/callumj/test1")
				Expect(err).To(Equal(provider.ErrImagePushAuthRequired))
				Expect(mockProxy.PushImageCalled).To(BeFalse())
			})

			It("returns an error when auth config is malformed", func() {
				err := prov.PushImage(srcImage, "magic.io/callumj/test1")
				Expect(err).To(Equal(provider.ErrImagePushAuthRequired))
				Expect(mockProxy.PushImageCalled).To(BeFalse())
			})

			It("should surface PushImage errors", func() {
				e := errors.New("Sorry!")
				mockProxy.PushImageError = e
				err := prov.PushImage(srcImage, "callumj/test1")
				defer removeImages([]string{"callumj/test1"})
				Expect(err).To(Equal(e))
				Expect(mockProxy.PushImageCalled).To(BeTrue())
			})
		})
	})

	Describe("ContainerSettingsDir()", func() {
		It("is in subdirectory 'containers' in base freighter dir", func() {
			d := prov.ContainerSettingsDir("containername")
			Expect(d).To(Equal(fmt.Sprintf("%s/containers/containername", datadir)))
		})
	})

	Describe("IsNitrousImage()", func() {
		cases := []struct {
			imageName string
			expected  bool
		}{
			{"nitrousio/ubuntu-bare:trusty", true},
			{"nitrousio/ubuntu:trusty", true},
			{"nitrousio/ruby:latest", true},
			{"nitrousio/golang", true},
			{"nitrousio/python:2.7.0", true},
			{"nitrousio/php:hiphop", true},
			{"nitrousio/ubuntu:gutsy", false},       // wrong tag
			{"actionio/ruby:latest", false},         // wrong registry
			{"nitrousio/visualbasic:latest", false}, // visualbasic not in whitelist
		}

		for _, c2 := range cases {
			c := c2
			It(fmt.Sprintf("is %v for image '%s'", c.expected, c.imageName), func() {
				Expect(docker.IsNitrousImage(c.imageName)).To(Equal(c.expected))
			})
		}
	})
})

// FakeRoundTripper implements a fake Transport layer that records the executed
// requests and provide a fake response
//
// This is more lightweight than implementing a full HTTP server and should be
// approriate for some tests
type FakeRoundTripper struct {
	message  string
	status   int
	header   map[string]string
	requests []*http.Request
}

func (rt *FakeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	body := strings.NewReader(rt.message)
	rt.requests = append(rt.requests, r)
	res := &http.Response{
		StatusCode: rt.status,
		Body:       ioutil.NopCloser(body),
		Header:     make(http.Header),
	}
	for k, v := range rt.header {
		res.Header.Set(k, v)
	}
	return res, nil
}

func (rt *FakeRoundTripper) Reset() {
	rt.requests = nil
}

// parses base64-encoded auth information string
func parseRegistryAuth(s string) (dockerclient.AuthConfiguration, error) {
	a := dockerclient.AuthConfiguration{}
	b, err := base64.URLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return a, err
	}
	return a, nil
}

// asserts that docker API receives the correct auth information
// refers to https://docs.docker.com/reference/api/docker_remote_api_v1.13/#22-images
// for more documentation
func assertRegistryAuth(r *http.Request, expected dockerclient.AuthConfiguration) {
	auth, err := parseRegistryAuth(r.Header.Get("X-Registry-Auth"))
	Expect(err).To(BeNil(), "failed to decode auth data in header")
	Expect(auth).To(Equal(expected), "invalid auth configuration")
}

// asserts that docker API receives the correct pull information
// refers to https://docs.docker.com/reference/api/docker_remote_api_v1.13/#22-images
// for more documentation
func assertPullOptions(r *http.Request, expected dockerclient.PullImageOptions) {
	// if we dont expect any tag, by default it only pulls the latest tag
	// https://github.com/nitrous-io/freighter/commit/f3af226068ddd6eb697a58be38b2acb521e8e0b4
	if expected.Tag == "" {
		expected.Tag = "latest"
	}
	repo := r.FormValue("fromImage")
	registry := r.FormValue("registry")
	tag := r.FormValue("tag")
	Expect(repo).To(Equal(expected.Repository), "invalid PullImage repository")
	Expect(registry).To(Equal(expected.Registry), "invalid PullImage registry")
	Expect(tag).To(Equal(expected.Tag), "invalid PullImage tag")
}

func assertBuildImageOptions(r *http.Request, expected dockerclient.BuildImageOptions) {
	name := r.FormValue("t")
	rm := r.FormValue("rm")
	forcerm := r.FormValue("forcerm")
	Expect(name).To(Equal(expected.Name))
	if expected.RmTmpContainer {
		Expect(rm).To(Equal("1"))
	}
	if expected.ForceRmTmpContainer {
		Expect(forcerm).To(Equal("1"))
	}
}
