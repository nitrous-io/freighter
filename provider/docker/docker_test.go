package docker_test

import (
	"os/exec"
	"strings"
	"time"

	dockerclient "github.com/fsouza/go-dockerclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker"
	. "github.com/nitrous-io/freighter/test"
	"github.com/nitrous-io/freighter/util"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func Test(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "provider/docker")
}

var _ = Describe("provider/docker", func() {
	var (
		dbFile    string
		prov      *docker.DockerProvider
		namespace = "freightertest"
	)

	BeforeEach(func() {
		var err error
		dbFile = TempFile("sqlite3")
		Expect(err).To(BeNil())

		datadir, err := TempDir()
		Expect(err).To(BeNil())
		logger := &util.Logger{Level: util.LogLevelQuiet}
		prov, err = docker.NewDockerProvider(logger, datadir, namespace)
		Expect(err).To(BeNil())
	})

	cleanUpContainer := func(name string) {
		exec.Command("docker", "stop", name).Run()
		exec.Command("docker", "rm", name).Run()
		exec.Command("docker", "rmi", namespace+"."+name).Run()
	}

	Describe(".Create()", func() {
		var (
			name string
			id   string
			host string
			info *provider.ContainerInfo
		)

		getID := func(name string) string {
			id, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).CombinedOutput()
			Expect(err).To(BeNil())
			return strings.TrimSpace(string(id))
		}

		getHost := func(id string) string {
			host, err := exec.Command("docker", "inspect", "-f", "{{.NetworkSettings.IPAddress}}", id).CombinedOutput()
			Expect(err).To(BeNil())
			return strings.TrimSpace(string(host))
		}

		BeforeEach(func() {
			var err error
			name = RandomName("freightertest")

			info, err = prov.Create(name, "ubuntu-upstart:trusty")
			Expect(err).To(BeNil())
			id = getID(name)
			host = getHost(id)
		})

		AfterEach(func() {
			cleanUpContainer(name)
		})

		It("creates a container successfully", func() {
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(name))
			Expect(info.Image).To(Equal(namespace + "." + name))
			Expect(info.Host).To(Equal(host))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
		})

		Context("after container is created", func() {
			Describe("Inspecting", func() {
				var (
					container2Name string
					container2Info *provider.ContainerInfo

					foreignContainerName string
					foreignContainerID   string
				)

				BeforeEach(func() {
					container2Name = RandomName("freightertest2")
					foreignContainerName = RandomName("freightertest-foreign")

					// shut down first container
					err := exec.Command("docker", "stop", id).Run()
					Expect(err).To(BeNil())

					// create another container
					container2Info, err = prov.Create(container2Name, "ubuntu-upstart:trusty")
					Expect(err).To(BeNil())

					// create a container not managed by freighter
					out, err := exec.Command("docker", "run", "-d", "-i", "-t", "--name", foreignContainerName, "ubuntu-upstart:trusty", "/bin/bash").CombinedOutput()
					Expect(err).To(BeNil())
					foreignContainerID = strings.TrimSpace(string(out))
				})

				AfterEach(func() {
					cleanUpContainer(container2Name)
					cleanUpContainer(foreignContainerName)
				})

				Describe(".List()", func() {
					It("returns a list of all containers managed by freighter, running or stopped", func() {
						list, err := prov.List()
						Expect(err).To(BeNil())

						Expect(list).To(HaveLen(2))
						Expect(list).To(ContainElement(&provider.ContainerInfo{
							ID:    id,
							Name:  name,
							Image: namespace + "." + name,
							Host:  "",
							State: provider.ContainerStateStopped,
						}))

						Expect(list).To(ContainElement(&provider.ContainerInfo{
							ID:    container2Info.ID,
							Name:  container2Name,
							Host:  container2Info.Host,
							Image: namespace + "." + container2Name,
							State: provider.ContainerStateRunning,
						}))
					})
				})

				Describe(".GetInfo()", func() {
					Context("if a container with a given id is managed by freighter", func() {
						It("returns container info", func() {
							info, err := prov.GetInfo(id)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(id))
							Expect(info.Name).To(Equal(name))
							Expect(info.Image).To(Equal(namespace + "." + name))
							Expect(info.Host).To(Equal(""))
							Expect(info.State).To(Equal(provider.ContainerStateStopped))

							info, err = prov.GetInfo(container2Info.ID)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(container2Info.ID))
							Expect(info.Name).To(Equal(container2Name))
							Expect(info.Image).To(Equal(namespace + "." + container2Name))
							Expect(info.Host).To(Equal(container2Info.Host))
							Expect(info.State).To(Equal(provider.ContainerStateRunning))
						})
					})

					Context("if a container with a given id is not managed by freighter", func() {
						It("returns no such container error", func() {
							info, err := prov.GetInfo(foreignContainerID)
							Expect(err).To(MatchError(&dockerclient.NoSuchContainer{ID: foreignContainerID}))
							Expect(info).To(BeNil())
						})
					})
				})
			})

			Describe(".Start()", func() {
				var info *provider.ContainerInfo

				expectContainerToBeRunning := func() {
					out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
					Expect(err).To(BeNil())
					Expect(out).To(ContainSubstring("true"))
				}

				expectContainerInfoToHaveBeenReturned := func() {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(namespace + "." + name))
					Expect(info.Host).To(Equal(host))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))
				}

				JustBeforeEach(func() {
					var err error
					info, err = prov.Start(id)
					Expect(err).To(BeNil())
					host = getHost(id)
				})

				Context("when the container is running", func() {
					It("does nothing", func() {
						expectContainerToBeRunning()
						expectContainerInfoToHaveBeenReturned()
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						err := exec.Command("docker", "stop", id).Run()
						Expect(err).To(BeNil())
					})

					It("starts the container", func() {
						expectContainerToBeRunning()
						expectContainerInfoToHaveBeenReturned()
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
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(namespace + "." + name))
					Expect(info.Host).To(Equal(""))
					Expect(info.State).To(Equal(provider.ContainerStateStopped))
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
						err := exec.Command("docker", "stop", id).Run()
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
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(namespace + "." + name))
					Expect(info.Host).To(Equal(host))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))
				}

				Context("when the container is running", func() {
					It("restarts the container", func() {
						out, err := exec.Command("docker", "inspect", "-f", "{{.State.StartedAt}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						prevStartTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
						Expect(err).To(BeNil())
						info, err = prov.Restart(id)
						Expect(err).To(BeNil())
						host = getHost(id)
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
						host = getHost(id)
						expectContainerInfoToHaveBeenReturned(info)
						out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						Expect(out).To(ContainSubstring("true"))
					})
				})
			})

			Describe(".Terminate()", func() {
				var info *provider.ContainerInfo

				expectContainerToBeTerminated := func() {
					out, err := exec.Command("docker", "inspect", id).CombinedOutput()
					Expect(err).NotTo(BeNil())
					Expect(out).To(ContainSubstring("No such image or container"))
				}

				expectContainerInfoToHaveBeenReturned := func() {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(namespace + "." + name))
					Expect(info.Host).To(Equal(""))
					Expect(info.State).To(Equal(""))
				}

				JustBeforeEach(func() {
					var err error
					info, err = prov.Terminate(id)
					Expect(err).To(BeNil())
				})

				Context("when the container is running", func() {
					It("terminates the container", func() {
						expectContainerToBeTerminated()
						expectContainerInfoToHaveBeenReturned()
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						err := exec.Command("docker", "stop", id).Run()
						Expect(err).To(BeNil())
					})

					It("terminates the container", func() {
						expectContainerToBeTerminated()
						expectContainerInfoToHaveBeenReturned()
					})
				})
			})
		})
	})
})
