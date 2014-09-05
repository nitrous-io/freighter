package docker_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
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

var _ = Describe("provider/docker", func() {
	var (
		prov    *docker.DockerProvider
		image   = "nitrousio/ubuntu-bare:trusty"
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
		exec.Command("docker", "stop", "--time=1", name).Run()
		exec.Command("docker", "rm", name).Run()
	}

	// Stolen from @ddollar's script here:
	// https://github.com/nitrous-io/freighter/commit/c5ebce6afd9e5bdf0727c9f2c24dc818a39368be#diff-b57cb1684dfddcbd75f478574803cd36R10
	doInspect := func(id, format string) string {
		out, err := exec.Command("docker", "inspect", "-f", format, id).CombinedOutput()
		Expect(err).To(BeNil())
		return strings.TrimSpace(string(out))
	}

	getID := func(name string) string {
		return doInspect(name, "{{.Id}}")
	}

	getPort := func(id string) string {
		return doInspect(id, `{{(index (index .NetworkSettings.Ports "22/tcp") 0).HostPort}}`)
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

	createImages := func(count int, name string, noEntropy bool) []string {
		dockerLoc, err := ioutil.TempDir("", "DockerTest")
		Expect(err).To(BeNil())
		dockerFile, err := os.Create(filepath.Join(dockerLoc, "Dockerfile"))
		Expect(err).To(BeNil())

		entropy := name
		if noEntropy {
			entropy = "thing"
		}
		dockerFile.WriteString(fmt.Sprintf(`
			FROM %s
			MAINTAINER Nitrous.IO, hello@nitrous.io

			RUN echo '%s' >> /tmp/info

			CMD /bin/bash
		`, image, entropy))
		dockerFile.Close()

		list := []string{}
		for index := 1; index <= count; index++ {
			findableName := fmt.Sprintf("%v:n%v", name, index)
			exec.Command("docker", "build", "-t", findableName, dockerLoc).Run()
			list = append(list, findableName)
		}

		err = os.RemoveAll(dockerLoc)
		Expect(err).To(BeNil())

		return list
	}

	removeImages := func(names []string) {
		args := append([]string{"rmi"}, names...)
		err := exec.Command("docker", args...).Run()
		Expect(err).To(BeNil())
	}

	Describe(".Create()", func() {
		var (
			name         string
			id           string
			host         string
			gateway      string
			port         string
			info         *provider.ContainerInfo
			pubkey1      = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQCxCeR/jJna6xXELDV8HF51QQPbj++JNDE4W/nqLyhzRTEDDnAfyp8gXW/EXXos8w5XXLak6eUDXE/QI6o8blX39RYoT47u45v4b2scN0ZiS5McmESo193KwWH3gXHbnHWywzPy7+GrRF3nHm/uidvp0tqh4ywqIZujaMjST1sIOQ== test@example.com"
			pubkey2      string
			fingerprint1 = "f7:de:4d:6f:54:6b:b9:d5:78:a9:62:15:e2:ad:ec:bb"
			fingerprint2 string
			sshkeypath   string
		)

		BeforeEach(func() {
			var err error
			name = RandomName("freightertest")

			info, err = prov.Create(name, image, secret, "")
			Expect(err).To(BeNil())
			id = getID(name)
			host = getHost(id)
			gateway = getGateway(id)
			port = getPort(id)

			pubkey2 = TestSSHPubKey
			fingerprint2, err = util.Md5Fingerprint(pubkey2)
			Expect(err).To(BeNil())

			sshkeypath = TempFile("")
			ioutil.WriteFile(sshkeypath, []byte(TestSSHPrivKey), 0600)
		})

		AfterEach(func() {
			cleanUpContainer(name)
			os.Remove(sshkeypath)
		})

		It("creates and starts container successfully", func() {
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(name))
			Expect(info.Image).To(Equal(image))
			Expect(info.ImageID).To(Equal(getID(image)))
			Expect(info.Host).To(Equal(host))
			Expect(info.Gateway).To(Equal(gateway))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			Expect(info.Ports[port]).To(Equal(22))
			Expect(getEnv(id)).To(ContainSubstring("__HOST=172.17.42.1"))

			c := getContainerInfo(id)
			Expect(info.Cmd).To(Equal(c.Config.Cmd))
			Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
			Expect(info.ExitCode).To(Equal(c.State.ExitCode))
			Expect(info.StartedAt).To(Equal(c.State.StartedAt))
			Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))

			settingsDir := prov.ContainerSettingsDir(name)
			_, err := os.Stat(settingsDir)
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

		Context("when container cannot be started", func() {
			It("creates a stopped container successfully (i.e. no error returned)", func() {
				// Create container with config that prevents it from
				// started.
				name := RandomName("freightertest")
				info, err := prov.Create(name, image, secret,
					`{"PortBindings": ["127.0.0.1:99999:99999"]}`) // Port out of range.
				Expect(err).To(BeNil())
				defer cleanUpContainer(name)
				Expect(info.Name).To(Equal(name))
				Expect(info.State).To(Equal(provider.ContainerStateStopped))
			})
		})

		Context("after container is created", func() {
			Describe("Inspecting", func() {
				var (
					container2Name string
					container2Info *provider.ContainerInfo
					container2Port string
				)

				BeforeEach(func() {
					container2Name = RandomName("freightertest2")

					// shut down first container
					err := exec.Command("docker", "stop", id).Run()
					Expect(err).To(BeNil())

					// create another container
					container2Info, err = prov.Create(container2Name, image, secret, "")
					Expect(err).To(BeNil())

					container2Port = getPort(container2Info.ID)
				})

				AfterEach(func() {
					cleanUpContainer(container2Name)
				})

				Describe(".List()", func() {
					It("returns a list of all containers running or stopped", func() {
						list, err := prov.List()
						Expect(err).To(BeNil())

						Expect(list).To(HaveLen(2))

						var stopped, running *provider.ContainerInfo
						for _, c := range list {
							if c.ID == id {
								stopped = c
							} else {
								Expect(c.ID).To(Equal(container2Info.ID))
								running = c
							}
						}

						imageID := getID(image)
						Expect(stopped.ID).To(Equal(id))
						Expect(stopped.Name).To(Equal(name))
						Expect(stopped.Image).To(Equal(image))
						Expect(stopped.ImageID).To(Equal(imageID))
						Expect(stopped.Host).To(Equal(""))
						Expect(stopped.Gateway).To(Equal(""))
						Expect(stopped.State).To(Equal(provider.ContainerStateStopped))
						Expect(stopped.Ports[port]).To(Equal(22))

						c := getContainerInfo(id)
						Expect(stopped.Cmd).To(Equal(c.Config.Cmd))
						Expect(stopped.Entrypoint).To(Equal(c.Config.Entrypoint))
						Expect(stopped.ExitCode).To(Equal(c.State.ExitCode))
						Expect(stopped.StartedAt).To(Equal(c.State.StartedAt))
						Expect(stopped.FinishedAt).To(Equal(c.State.FinishedAt))

						Expect(running.ID).To(Equal(container2Info.ID))
						Expect(running.Name).To(Equal(container2Name))
						Expect(running.Image).To(Equal(image))
						Expect(running.ImageID).To(Equal(imageID))
						Expect(running.Host).To(Equal(container2Info.Host))
						Expect(running.State).To(Equal(provider.ContainerStateRunning))
						Expect(running.Ports[container2Port]).To(Equal(22))

						c = getContainerInfo(container2Info.ID)
						Expect(running.Cmd).To(Equal(c.Config.Cmd))
						Expect(running.Entrypoint).To(Equal(c.Config.Entrypoint))
						Expect(running.ExitCode).To(Equal(c.State.ExitCode))
						Expect(running.StartedAt).To(Equal(c.State.StartedAt))
						Expect(running.FinishedAt).To(Equal(c.State.FinishedAt))
					})
				})

				Describe(".GetInfo()", func() {
					Context("if a container with a given id is managed by freighter", func() {
						It("returns container info", func() {
							imageID := getID(image)

							info, err := prov.GetInfo(id)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(id))
							Expect(info.Name).To(Equal(name))
							Expect(info.Image).To(Equal(image))
							Expect(info.ImageID).To(Equal(imageID))
							Expect(info.Host).To(Equal(""))
							Expect(info.Gateway).To(Equal(""))
							Expect(info.Ports[port]).To(Equal(22))
							Expect(info.State).To(Equal(provider.ContainerStateStopped))

							c := getContainerInfo(id)
							Expect(info.Cmd).To(Equal(c.Config.Cmd))
							Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
							Expect(info.ExitCode).To(Equal(c.State.ExitCode))
							Expect(info.StartedAt).To(Equal(c.State.StartedAt))
							Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))

							info, err = prov.GetInfo(container2Info.ID)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(container2Info.ID))
							Expect(info.Name).To(Equal(container2Name))
							Expect(info.Image).To(Equal(image))
							Expect(info.ImageID).To(Equal(imageID))
							Expect(info.Host).To(Equal(container2Info.Host))
							Expect(info.Gateway).To(Equal(container2Info.Gateway))
							Expect(info.Ports[container2Port]).To(Equal(22))
							Expect(info.State).To(Equal(provider.ContainerStateRunning))

							c = getContainerInfo(container2Info.ID)
							Expect(info.Cmd).To(Equal(c.Config.Cmd))
							Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
							Expect(info.ExitCode).To(Equal(c.State.ExitCode))
							Expect(info.StartedAt).To(Equal(c.State.StartedAt))
							Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))
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
					Expect(info.Image).To(Equal(image))
					Expect(info.ImageID).To(Equal(getID(image)))
					Expect(info.Host).To(Equal(host))
					Expect(info.Gateway).To(Equal(gateway))
					Expect(info.Ports[port]).To(Equal(22))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))

					c := getContainerInfo(id)
					Expect(info.Cmd).To(Equal(c.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(c.Config.Entrypoint))
					Expect(info.ExitCode).To(Equal(c.State.ExitCode))
					Expect(info.StartedAt).To(Equal(c.State.StartedAt))
					Expect(info.FinishedAt).To(Equal(c.State.FinishedAt))
				}

				JustBeforeEach(func() {
					var err error
					info, err = prov.Start(id, "")
					Expect(err).To(BeNil())
					host = getHost(id)
					gateway = getGateway(id)
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
					Expect(info.Host).To(Equal(""))
					Expect(info.Gateway).To(Equal(""))
					Expect(info.Ports[port]).To(Equal(22))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(image))
					Expect(info.ImageID).To(Equal(getID(image)))
					Expect(info.State).To(Equal(provider.ContainerStateStopped))

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
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(image))
					Expect(info.ImageID).To(Equal(getID(image)))
					Expect(info.Host).To(Equal(host))
					Expect(info.Gateway).To(Equal(gateway))
					Expect(info.Ports[port]).To(Equal(22))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))

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
						info, err = prov.Restart(id)
						Expect(err).To(BeNil())
						host = getHost(id)
						gateway = getGateway(id)
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
						gateway = getGateway(id)
						expectContainerInfoToHaveBeenReturned(info)
						out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
						Expect(err).To(BeNil())
						Expect(out).To(ContainSubstring("true"))
					})
				})
			})

			Describe(".Terminate()", func() {
				var info *provider.ContainerInfo
				var cntnrInfo *containerInfo

				// fetch the container info before terminating the container
				BeforeEach(func() {
					cntnrInfo = getContainerInfo(id)
				})

				expectContainerToBeTerminatedAndPortReleased := func() {
					out, err := exec.Command("docker", "inspect", id).CombinedOutput()
					Expect(err).NotTo(BeNil())
					Expect(out).To(ContainSubstring("No such image or container"))
					settingsDir := prov.ContainerSettingsDir(info.Name)
					_, err = os.Stat(settingsDir)
					Expect(os.IsNotExist(err)).To(BeTrue(), "Terminate did not remove settings dir %s", settingsDir)
				}

				expectContainerInfoToHaveBeenReturned := func() {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(name))
					Expect(info.Image).To(Equal(image))
					Expect(info.ImageID).To(Equal(getID(image)))
					Expect(info.Host).To(Equal(""))
					Expect(info.Gateway).To(Equal(""))
					Expect(info.Ports).To(Equal(map[string]int{}))
					Expect(info.State).To(Equal(""))

					Expect(info.Cmd).To(Equal(cntnrInfo.Config.Cmd))
					Expect(info.Entrypoint).To(Equal(cntnrInfo.Config.Entrypoint))
					Expect(info.StartedAt).To(Equal(cntnrInfo.State.StartedAt))
					Expect(info.FinishedAt.IsZero()).NotTo(BeTrue())
				}

				JustBeforeEach(func() {
					var err error
					info, err = prov.Terminate(id)
					Expect(err).To(BeNil())
				})

				Context("when the container is running", func() {
					It("terminates the container", func() {
						expectContainerToBeTerminatedAndPortReleased()
						expectContainerInfoToHaveBeenReturned()
					})
				})

				Context("when the container is stopped", func() {
					BeforeEach(func() {
						err := exec.Command("docker", "stop", id).Run()
						Expect(err).To(BeNil())
					})

					It("terminates the container", func() {
						expectContainerToBeTerminatedAndPortReleased()
						expectContainerInfoToHaveBeenReturned()
					})
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
				Context("with generic containers", func() {
					var (
						busyboxName string
						busyboxID   string
					)

					BeforeEach(func() {
						image := "jpetazzo/busybox"
						busyboxName = RandomName("freightertest")
						_, err := prov.Create(busyboxName, image, secret, "")
						Expect(err).To(BeNil())
						busyboxID = getID(busyboxName)
					})

					AfterEach(func() {
						cleanUpContainer(busyboxName)
					})

					Describe(".ListPublicKeys()", func() {
						It("returns an error", func() {
							pubkeys, err := prov.ListPublicKeys(busyboxID)
							Expect(pubkeys).To(BeNil())
							Expect(err).To(Equal(provider.ErrPubKeyOpsNotSupported))
						})
					})

					Describe(".AddPublicKeys()", func() {
						It("returns an error", func() {
							err := prov.AddPublicKeys(busyboxID, []string{pubkey1})
							Expect(err).To(Equal(provider.ErrPubKeyOpsNotSupported))
						})
					})

					Describe(".RemovePublicKey()", func() {
						It("returns an error", func() {
							err := prov.RemovePublicKey(busyboxID, fingerprint1)
							Expect(err).To(Equal(provider.ErrPubKeyOpsNotSupported))
						})
					})

					Describe(".ClearPublicKeys()", func() {
						It("returns an error", func() {
							err := prov.ClearPublicKeys(busyboxID)
							Expect(err).To(Equal(provider.ErrPubKeyOpsNotSupported))
						})
					})
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
									expectSSHToWork(port, sshkeypath)
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
								expectSSHToWork(port, sshkeypath)
								err := prov.ClearPublicKeys(id)
								Expect(err).To(BeNil())
								expectSSHToNotWork(port, sshkeypath)
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
									expectSSHToWork(port, sshkeypath)
									err := prov.RemovePublicKey(id, fingerprint2)
									Expect(err).To(BeNil())
									expectSSHToNotWork(port, sshkeypath)
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
		var (
			name       string
			info       *provider.ContainerInfo
			configJSON string
		)

		// getPortBindings output looks like "[map[HostIp:127.0.0.1 HostPort:1234]]"
		// or "<no value>" if bindings for given port do not exist
		getPortBindings := func(id, port string) string {
			return doInspect(id, fmt.Sprintf(
				`{{index .HostConfig.PortBindings "%s"}}`, port))
		}

		// given docker inspect output "Volumes": {
		//   "/etc/dotfiles": "/var/lib/freighter/volumes/dotfiles",
		//   "/voluems/backups": "/var/lib/freighter/volumes/backups"
		// }
		// returns
		//   /etc/dotfiles /var/lib/freighter/volumes/dotfiles\n
		//   /volumes/backups /var/lib/freighter/volumes/backups\n
		getVolumes := func(id string) string {
			return doInspect(id,
				"{{range $cnrPath, $hostPath := .Volumes}}{{$cnrPath}} {{$hostPath}}\n{{end}}")
		}

		BeforeEach(func() {
			var err error
			configJSON = `{
  "Env": ["LANG=C"],
  "Mounts": ["dotfiles:/etc/dotfiles","backups"],
  "PortBindings": ["private:1234:5000"]
}`
			name = RandomName("freightertest")
			info, err = prov.Create(name, image, secret, configJSON)
			Expect(err).To(BeNil())
		})

		AfterEach(func() {
			cleanUpContainer(name)
		})

		It("creates container with options", func() {
			id := getID(name)
			imageID := getID(image)
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(name))
			Expect(info.Image).To(Equal(image))
			Expect(info.ImageID).To(Equal(imageID))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			Expect(doInspect(id, "{{.Config.Env}}")).To(ContainSubstring("LANG=C"))
			Expect(doInspect(id,
				"{{range $p, $stub := .Config.ExposedPorts}}{{$p}} {{end}}")).
				To(Equal("22/tcp 5000"))
			Expect(getPortBindings(id, "5000")).To(
				Equal("[map[HostIp:172.17.42.1 HostPort:1234]]"))
			Expect(getPortBindings(id, "22/tcp")).To(
				ContainSubstring("[map[HostIp:0.0.0.0 HostPort:49")) // Docker will use 49000-49900
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
			_, err := prov.Stop(id)
			Expect(err).To(BeNil())
			_, err = prov.Start(id, "")
			Expect(err).To(BeNil())
			Expect(getPortBindings(id, "5000")).To(
				Equal("[map[HostIp:172.17.42.1 HostPort:1234]]"))
			vols = getVolumes(id)
			Expect(vols).To(ContainSubstring("/etc/ssh/freighter"))
			Expect(vols).To(ContainSubstring("dotfiles"))
			Expect(vols).To(ContainSubstring("backups"))

			// It("overrides existing PortBindings,Binds on next start if options given")
			_, err = prov.Stop(id)
			Expect(err).To(BeNil())
			newConfig := strings.Replace(configJSON, "5000", "4242/tcp", 1)
			newConfig = strings.Replace(newConfig, "backups", "restores", 1)
			_, err = prov.Start(id, newConfig)
			Expect(err).To(BeNil())
			Expect(getPortBindings(id, "4242/tcp")).To(
				Equal("[map[HostIp:172.17.42.1 HostPort:1234]]"))
			Expect(getPortBindings(id, "5000/tcp")).To(Equal("<no value>"))
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
				publicImages = createImages(1, "basic", false)
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
				createdImages = createImages(2, "multiple", true)
			})

			AfterEach(func() {
				removeImages(createdImages)
			})

			It("should break them out into separate entries", func() {
				imageList, err := prov.ListImages()
				Expect(err).To(BeNil())

				i1 := imageList[0]
				i2 := imageList[1]
				Expect(i1.Name).To(Equal("multiple"))
				Expect(i2.Name).To(Equal("multiple"))
				Expect(i1.Tag).To(Equal("n1"))
				Expect(i2.Tag).To(Equal("n2"))
				Expect(i1.ID).To(Equal(i2.ID))
			})
		})
	})

	Describe(".BuildImage()", func() {
		createImageTarball := func() io.Reader {
			buf := new(bytes.Buffer)
			gzipWriter := gzip.NewWriter(buf)
			tarWriter := tar.NewWriter(gzipWriter)

			dockerSource := fmt.Sprintf(`
			FROM %s
			MAINTAINER Nitrous.IO, hello@nitrous.io
			ADD some_file.txt /some/file.txt
			CMD /bin/bash -c 'cat /some/file.txt'`, image)

			err := tarWriter.WriteHeader(&tar.Header{
				Name: "Dockerfile",
				Size: int64(len(dockerSource)),
			})
			Expect(err).To(BeNil())
			_, err = tarWriter.Write([]byte(dockerSource))
			Expect(err).To(BeNil())

			someFile := "Hello from Docker!"
			someFileHeader := &tar.Header{
				Name: "some_file.txt",
				Size: int64(len(someFile)),
			}
			err = tarWriter.WriteHeader(someFileHeader)
			Expect(err).To(BeNil())
			_, err = tarWriter.Write([]byte(someFile))
			Expect(err).To(BeNil())

			err = tarWriter.Close()
			Expect(err).To(BeNil())
			err = gzipWriter.Close()
			Expect(err).To(BeNil())

			return buf
		}

		It("creates an Image successfully", func() {
			readBuf := createImageTarball()
			outputBuffer := new(bytes.Buffer)
			info, err := prov.BuildImage("myimage", readBuf, outputBuffer)
			defer exec.Command("docker", "rmi", "myimage:latest").Run()
			Expect(err).To(BeNil())
			Expect(info).ToNot(BeNil())

			Expect(outputBuffer.String()).To(ContainSubstring("Successfully built"))
		})

		It("correctly stores the additional data", func() {
			readBuf := createImageTarball()
			outputBuffer := new(bytes.Buffer)
			info, err := prov.BuildImage("myimage", readBuf, outputBuffer)
			defer exec.Command("docker", "rmi", "myimage:latest").Run()
			Expect(err).To(BeNil())
			Expect(info).ToNot(BeNil())

			allImages, err := prov.ListImages()
			Expect(err).To(BeNil())

			var matchingImage *provider.ImageInfo
			for _, image := range allImages {
				if image.Name == "myimage" && image.Tag == "latest" {
					matchingImage = image
				}
			}

			Expect(matchingImage).ToNot(BeNil())

			res, err := exec.Command("docker", "run", "--rm=true", matchingImage.ID).Output()
			Expect(err).To(BeNil())
			Expect(string(res)).To(Equal("Hello from Docker!"))
		})
	})

	Describe(".RemoveImage()", func() {
		Context("when an ID is provided", func() {
			It("should be deleted", func() {
				images := createImages(1, "removeable", false)

				id := doInspect(images[0], "{{.Id}}")
				Expect(id).ToNot(BeEmpty())
				err := prov.RemoveImage(id)
				Expect(err).To(BeNil())

				out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", images[0]).CombinedOutput()
				Expect(out).To(ContainSubstring("Error: No such image or container:"))
			})

			Context("with multiple images under ID", func() {
				It("should not allow for deletion", func() {
					i1 := createImages(1, "multremoveable", true)
					defer removeImages(i1)
					i2 := createImages(1, "multremoveable2", true)
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
				images := createImages(1, "removeable", false)

				err := prov.RemoveImage(images[0])
				Expect(err).To(BeNil())

				out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", images[0]).CombinedOutput()
				Expect(out).To(ContainSubstring("Error: No such image or container:"))
			})
		})
	})

	Describe(".PullImage()", func() {
		var (
			mockProxy    *MockDockerProxy
			tempAuthFile *os.File
		)

		createBasicImage := func(name string) {
			// FIXME: image originally comes from the other test, if we want to
			// run this test independently, or if the order is wrong, we pull it
			// here first
			var err error
			err = exec.Command("docker", "pull", image).Run()
			Expect(err).To(BeNil())
			err = exec.Command("docker", "tag", "-f", image, name).Run()
			Expect(err).To(BeNil())
		}

		checkImage := func(name string) {
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

		BeforeEach(func() {
			var err error

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
			os.Remove(tempAuthFile.Name())
		})

		It("should allow for importing by default hub", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("nitrousio/nodejs")
			}
			info, err := prov.PullImage("nitrousio/nodejs")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("nitrousio/nodejs"))
			Expect(mockProxy.PullImageOpts.Tag).To(Equal("latest"))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("test"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("1234xxcc"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("docker@callumj.com"))

			Expect(info.Name).To(ContainSubstring("nitrousio/nodejs"))

			checkImage("nitrousio/nodejs:latest")
		})

		It("should allow for importing by a tagged image", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("ubuntu:10.04")
			}
			_, err := prov.PullImage("ubuntu:10.04")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("ubuntu"))
			Expect(mockProxy.PullImageOpts.Tag).To(Equal("10.04"))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("test"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("1234xxcc"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("docker@callumj.com"))

			checkImage("ubuntu:10.04")
		})

		It("should allow for importing by simple remote registry", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("quay.io/ubuntu")
			}
			_, err := prov.PullImage("quay.io/ubuntu")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("quay.io/ubuntu"))
			Expect(mockProxy.PullImageOpts.Tag).To(Equal("latest"))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("testo"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("Password1"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("quay@callumj.com"))

			checkImage("quay.io/ubuntu:latest")
		})

		It("should allow for importing by remote registry", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("quay.io/quay/ubuntu")
			}
			_, err := prov.PullImage("quay.io/quay/ubuntu")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("quay.io/quay/ubuntu"))
			Expect(mockProxy.PullImageOpts.Tag).To(Equal("latest"))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("testo"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("Password1"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("quay@callumj.com"))

			checkImage("quay.io/quay/ubuntu:latest")
		})

		It("should allow even when auth credentials could not be located", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("cloudy.io/nodejs")
			}
			info, err := prov.PullImage("cloudy.io/nodejs")
			Expect(err).To(BeNil())
			Expect(info).NotTo(BeNil())
			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageAuth).To(Equal(dockerclient.AuthConfiguration{}))

			checkImage("cloudy.io/nodejs:latest")
		})

		It("should allow even a malformed config", func() {
			mockProxy.PullImageError = errors.New("Forbidden")
			info, err := prov.PullImage("magic.io/quay/ubuntu")
			Expect(err).To(Equal(errors.New("Forbidden")))
			Expect(info).To(BeNil())
			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageAuth).To(Equal(dockerclient.AuthConfiguration{}))
		})

		It("should allow to pull without credentials when we dont have a auth configuration", func() {
			os.Remove(tempAuthFile.Name())
			mockProxy.PullImageCallBack = func() {
				createBasicImage("cloudy.io/nodejs")
			}
			_, err := prov.PullImage("cloudy.io/nodejs")
			Expect(err).To(BeNil())
			Expect(mockProxy.PullImageAuth).To(Equal(dockerclient.AuthConfiguration{}))
		})

		It("should surface PullImage errors", func() {
			sharedErr := errors.New("Sorry!")
			mockProxy.PullImageError = sharedErr
			_, err := prov.PullImage("quay/ubuntu")
			Expect(err).To(Equal(sharedErr))

			Expect(mockProxy.PullImageCalled).To(BeTrue())
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
			created := createImages(1, "withoutly", false)
			err := prov.TagImage(created[0], "withoutly2", false)
			Expect(err).To(BeNil())

			verifyImageExistence("withoutly2:latest")
			removeImages(created)
		})

		It("should allow for tagging images with tags", func() {
			created := createImages(1, "tagly", false)
			err := prov.TagImage(created[0], "tagly2:1907", false)
			Expect(err).To(BeNil())

			verifyImageExistence("tagly2:1907")
			removeImages(created)
		})

		Context("image tag already exists", func() {

			It("should not override existing tags with no force", func() {
				created := createImages(1, "nooverridely", false)
				err := prov.TagImage(created[0], "nooverridely:n1", false)
				Expect(err).To(Equal(provider.ErrImageExists))
				removeImages(created)
			})

			It("should override existing tags with force", func() {
				target := createImages(1, "overridely", false)
				destination := createImages(1, "overridelyreplacey", false)
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

				images := createImages(1, "namely", false)
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
					err := exec.Command("docker", "tag", "-f", image, existingImageName).Run()
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
})
