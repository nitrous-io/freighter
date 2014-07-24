package docker_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		prov      *docker.DockerProvider
		namespace = "freightertest"
		image     = "nitrousio/ubuntu-bare:trusty"
	)

	BeforeEach(func() {
		var err error

		datadir, err := TempDir()
		Expect(err).To(BeNil())
		logger := &util.Logger{Level: util.LogLevelQuiet}
		prov, err = docker.NewDockerProvider(logger, datadir, namespace)
		Expect(err).To(BeNil())
	})

	cleanUpContainer := func(name string) {
		exec.Command("docker", "stop", name).Run()
		exec.Command("docker", "rm", name).Run()
	}

	fullContainerName := func(name string) string {
		return fmt.Sprintf("%s.%s", namespace, name)
	}

	// Stolen from @ddollar's script here:
	// https://github.com/nitrous-io/freighter/commit/c5ebce6afd9e5bdf0727c9f2c24dc818a39368be#diff-b57cb1684dfddcbd75f478574803cd36R10
	doInspect := func(id, format string) string {
		out, err := exec.Command("docker", "inspect", "-f", format, id).CombinedOutput()
		Expect(err).To(BeNil())
		return strings.TrimSpace(string(out))
	}

	getID := func(name string) string {
		return doInspect(fullContainerName(name), "{{.Id}}")
	}

	getPort := func(id string) string {
		return doInspect(id, `{{(index (index .NetworkSettings.Ports "22/tcp") 0).HostPort}}`)
	}

	getHost := func(id string) string {
		return doInspect(id, "{{.NetworkSettings.IPAddress}}")
	}

	createImages := func(count int, name string, hidden, noFindable bool) []string {
		dockerSource := `
			FROM %s
			MAINTAINER Nitrous.IO, hello@nitrous.io

			RUN echo '%s' >> /tmp/info

			CMD /bin/bash
			`
		interpolated := fmt.Sprintf(dockerSource, image, name)
		dockerLoc, err := ioutil.TempDir("", "DockerTest")
		Expect(err).To(BeNil())
		fileLoc := filepath.Join(dockerLoc, "Dockerfile")
		dockerFile, err := os.Create(fileLoc)
		Expect(err).To(BeNil())
		dockerFile.WriteString(interpolated)
		dockerFile.Close()

		list := []string{}
		for index := 1; index <= count; index++ {
			if !noFindable {
				findableName := fmt.Sprintf("%v.%v:n%v", namespace, name, index)
				exec.Command("docker", "build", "-t", findableName, dockerLoc).Run()
				list = append(list, findableName)
			}

			if hidden {
				hiddenName := fmt.Sprintf("%vprivate.bad%v:n%v", namespace, name, index)
				exec.Command("docker", "build", "-t", hiddenName, dockerLoc).Run()
				list = append(list, hiddenName)
			}
		}

		err = os.Remove(fileLoc)
		Expect(err).To(BeNil())
		err = os.Remove(dockerLoc)
		Expect(err).To(BeNil())

		return list
	}

	removeCreated := func(names []string) {
		for _, name := range names {
			exec.Command("docker", "rmi", name).Run()
		}
	}

	Describe(".Create()", func() {
		var (
			name         string
			id           string
			host         string
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

			info, err = prov.Create(name, image, "")
			Expect(err).To(BeNil())
			id = getID(name)
			host = getHost(id)
			port = getPort(id)

			userPubkey := TestSSHPubKey
			pubkey2 = strings.TrimSpace(string(userPubkey))
			fingerprint2, err = util.Md5Fingerprint(pubkey2)
			Expect(err).To(BeNil())

			sshkeypath = TempFile("")
			ioutil.WriteFile(sshkeypath, []byte(TestSSHPrivKey), 0600)
		})

		AfterEach(func() {
			cleanUpContainer(fullContainerName(name))
			os.Remove(sshkeypath)
		})

		It("creates a container successfully", func() {
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(fullContainerName(name)))
			Expect(info.Image).To(Equal(image))
			Expect(info.Host).To(Equal(host))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			Expect(info.Ports[port]).To(Equal(22))
		})

		Context("after container is created", func() {
			Describe("Inspecting", func() {
				var (
					container2Name       string
					container2Info       *provider.ContainerInfo
					container2Port       string
					foreignContainerName string
					foreignContainerID   string
				)

				BeforeEach(func() {
					container2Name = RandomName("freightertest2")
					foreignContainerName = "notfreighter." + RandomName("freightertest-foreign")

					// shut down first container
					err := exec.Command("docker", "stop", id).Run()
					Expect(err).To(BeNil())

					// create another container
					container2Info, err = prov.Create(container2Name, image, "")
					Expect(err).To(BeNil())

					container2Port = getPort(container2Info.ID)

					// create a container not managed by freighter
					out, err := exec.Command("docker", "run", "-d", "-i", "-t", "--name", foreignContainerName, image, "/bin/sh").CombinedOutput()
					Expect(err).To(BeNil())
					foreignContainerID = strings.TrimSpace(string(out))
				})

				AfterEach(func() {
					cleanUpContainer(fullContainerName(container2Name))
					cleanUpContainer(foreignContainerName)
				})

				Describe(".List()", func() {
					getAttributes := func(attr string, list []*provider.ContainerInfo) []string {
						attributes := []string{}
						for _, info := range list {
							switch attr {
							case "Name":
								attributes = append(attributes, info.Name)
							case "Host":
								attributes = append(attributes, info.Host)
							case "Image":
								attributes = append(attributes, info.Image)
							case "State":
								attributes = append(attributes, info.State)
							case "ID":
								attributes = append(attributes, info.ID)
							case "Ports":
								for port := range info.Ports {
									attributes = append(attributes, port)
								}
							}
						}

						return attributes
					}

					It("returns a list of all containers managed by freighter, running or stopped", func() {
						list, err := prov.List()
						Expect(err).To(BeNil())

						Expect(list).To(HaveLen(2))

						names := getAttributes("Name", list)
						Expect(names).To(ContainElement(fullContainerName(name)))
						Expect(names).To(ContainElement(fullContainerName(container2Name)))

						images := getAttributes("Image", list)
						Expect(images).To(ContainElement(image))

						hosts := getAttributes("Host", list)
						Expect(hosts).To(ContainElement(""))
						Expect(hosts).To(ContainElement(container2Info.Host))

						states := getAttributes("State", list)
						Expect(states).To(ContainElement(provider.ContainerStateStopped))
						Expect(states).To(ContainElement(provider.ContainerStateRunning))

						ids := getAttributes("ID", list)
						Expect(ids).To(ContainElement(id))
						Expect(ids).To(ContainElement(container2Info.ID))

						ports := getAttributes("Ports", list)
						Expect(ports).To(ContainElement(port))
						Expect(ports).To(ContainElement(container2Port))
					})
				})

				Describe(".GetInfo()", func() {
					Context("if a container with a given id is managed by freighter", func() {
						It("returns container info", func() {
							info, err := prov.GetInfo(id)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(id))
							Expect(info.Name).To(Equal(fullContainerName(name)))
							Expect(info.Image).To(Equal(image))
							Expect(info.Host).To(Equal(""))
							Expect(info.Ports[port]).To(Equal(22))
							Expect(info.State).To(Equal(provider.ContainerStateStopped))

							info, err = prov.GetInfo(container2Info.ID)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(container2Info.ID))
							Expect(info.Name).To(Equal(fullContainerName(container2Name)))
							Expect(info.Image).To(Equal(image))
							Expect(info.Host).To(Equal(container2Info.Host))
							Expect(info.Ports[container2Port]).To(Equal(22))
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
					Expect(info.Name).To(Equal(fullContainerName(name)))
					Expect(info.Image).To(Equal(image))
					Expect(info.Host).To(Equal(host))
					Expect(info.Ports[port]).To(Equal(22))
					Expect(info.State).To(Equal(provider.ContainerStateRunning))
				}

				JustBeforeEach(func() {
					var err error
					info, err = prov.Start(id, "")
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
					Expect(info.Host).To(Equal(""))
					Expect(info.Ports[port]).To(Equal(22))
					Expect(info.Name).To(Equal(fullContainerName(name)))
					Expect(info.Image).To(Equal(image))
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
					Expect(info.Name).To(Equal(fullContainerName(name)))
					Expect(info.Image).To(Equal(image))
					Expect(info.Host).To(Equal(host))
					Expect(info.Ports[port]).To(Equal(22))
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

				expectContainerToBeTerminatedAndPortReleased := func() {
					out, err := exec.Command("docker", "inspect", id).CombinedOutput()
					Expect(err).NotTo(BeNil())
					Expect(out).To(ContainSubstring("No such image or container"))
				}

				expectContainerInfoToHaveBeenReturned := func() {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(fullContainerName(name)))
					Expect(info.Image).To(Equal(image))
					Expect(info.Host).To(Equal(""))
					Expect(info.Ports).To(Equal(map[string]int{}))
					Expect(info.State).To(Equal(""))
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

			Context("with public keys", func() {
				Context("with generic containers", func() {
					var (
						busyboxName string
						busyboxID   string
					)

					BeforeEach(func() {
						image := "jpetazzo/busybox"
						busyboxName = RandomName("freightertest")
						_, err := prov.Create(busyboxName, image, "")
						Expect(err).To(BeNil())
						busyboxID = getID(busyboxName)
					})

					AfterEach(func() {
						cleanUpContainer(fullContainerName(busyboxName))
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
					expectSSHToWork := func(port, sshkeypath string) {
						time.Sleep(2 * time.Second)
						out, err := exec.Command("ssh", "action@localhost", "-i", sshkeypath, "-p", port, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "echo success").CombinedOutput()
						Expect(out).To(ContainSubstring("success"))
						Expect(err).To(BeNil())
					}

					expectSSHToNotWork := func(port, sshkeypath string) {
						time.Sleep(2 * time.Second)
						out, err := exec.Command("ssh", "action@localhost", "-i", sshkeypath, "-p", port, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "echo success").CombinedOutput()
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

		BeforeEach(func() {
			var err error
			configJSON = `{
  "Config": {
    "Env": ["LANG=C"],
    "ExposedPorts": {"8080/tcp": {}},
    "Volumes": {"/data": {}, "/music": {}}
  },
  "HostConfig": {
    "PortBindings": {"3000/tcp": [{"HostPort":"1234", "HostIp": "127.0.0.1"}]}
  }
}`
			name = RandomName("freightertest")
			info, err = prov.Create(name, image, configJSON)
			Expect(err).To(BeNil())
		})

		AfterEach(func() {
			cleanUpContainer(fullContainerName(name))
		})

		It("creates container with options", func() {
			id := getID(name)
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(fullContainerName(name)))
			Expect(info.Image).To(Equal(image))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			Expect(doInspect(id, "{{.Config.Env}}")).To(ContainSubstring("LANG=C"))
			Expect(doInspect(id,
				"{{range $p, $stub := .Config.ExposedPorts}}{{$p}} {{end}}")).
				To(Equal("22/tcp 8080/tcp"))
			Expect(getPortBindings(id, "3000/tcp")).To(
				Equal("[map[HostIp:127.0.0.1 HostPort:1234]]"))
			Expect(getPortBindings(id, "22/tcp")).To(
				ContainSubstring("[map[HostIp:0.0.0.0 HostPort:49")) // Docker will use 49000-49900
			Expect(doInspect(id,
				"{{range $v, $stub := .Config.Volumes}}{{$v}} {{end}}")).
				To(Equal("/data /etc/ssh/freighter /music"))

			// should be split out but kept here due to each container create taking >10s
			// It("persists PortBindings on next start if no options given")
			_, err := prov.Stop(id)
			Expect(err).To(BeNil())
			_, err = prov.Start(id, "")
			Expect(err).To(BeNil())
			Expect(getPortBindings(id, "3000/tcp")).To(
				Equal("[map[HostIp:127.0.0.1 HostPort:1234]]"))

			// It("overrides existing PortBindings on next start if options given")
			_, err = prov.Stop(id)
			Expect(err).To(BeNil())
			_, err = prov.Start(id, strings.Replace(configJSON, "3000/tcp", "4242/tcp", 1))
			Expect(err).To(BeNil())
			Expect(getPortBindings(id, "4242/tcp")).To(
				Equal("[map[HostIp:127.0.0.1 HostPort:1234]]"))
			Expect(getPortBindings(id, "3000/tcp")).To(Equal("<no value>"))

			// It("supports VolumesFrom option")
			// VolumesFrom will error out if the container id given does not exist
			volsName := RandomName("freightertest")
			_, err = prov.Create(volsName, image,
				fmt.Sprintf(`{"HostConfig": {"VolumesFrom":["%s"]}}`, id))
			Expect(err).To(BeNil())
			defer cleanUpContainer(fullContainerName(volsName))
			volsID := getID(volsName)
			Expect(doInspect(volsID, `{{.HostConfig.VolumesFrom}}`)).To(
				Equal(fmt.Sprintf("[%s]", id)))
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
				createdImages []string
			)

			BeforeEach(func() {
				createdImages = createImages(1, "basic", true, false)
			})

			AfterEach(func() {
				removeCreated(createdImages)
			})

			It("should not expose images not managed by the namespace", func() {
				imageList, err := prov.ListImages()
				Expect(err).To(BeNil())

				Expect(imageList).To(HaveLen(1))

				Expect(imageList[0].Name).To(Equal("basic"))
			})

			It("should fetch the basic info about an Image", func() {
				imageList, err := prov.ListImages()
				Expect(err).To(BeNil())

				splitSet := strings.Split(image, ":")
				Expect(splitSet).To(HaveLen(2))

				var target *provider.ImageInfo
				for _, imageInf := range imageList {
					if imageInf.Name == "basic" && imageInf.Tag == "n1" {
						target = imageInf
						break
					}
				}

				Expect(target).ToNot(BeNil())
				Expect(target.ActualSize).To(Equal(imageSize(target.ID)))

				timeStr := doInspect(target.ID, "{{.Created}}")
				expectingTime, err := time.Parse(time.RFC3339, timeStr)
				Expect(err).To(BeNil())
				expectingUTime := expectingTime.Unix()
				Expect(target.CreatedAt).To(Equal(expectingUTime))
			})
		})

		Context("when the same image is under multiple repositories", func() {
			var (
				createdImages []string
			)

			BeforeEach(func() {
				createdImages = createImages(2, "multiple", true, false)
			})

			AfterEach(func() {
				removeCreated(createdImages)
			})

			It("should break them out into separate entries", func() {
				imageList, _ := prov.ListImages()

				splitSet := strings.Split(image, ":")
				Expect(splitSet).To(HaveLen(2))

				var target1 *provider.ImageInfo
				var target2 *provider.ImageInfo
				for _, imageInf := range imageList {
					if imageInf.Name == "multiple" {
						switch imageInf.Tag {
						case "n1":
							target1 = imageInf
						case "n2":
							target2 = imageInf
						}
					}

					if target1 != nil && target2 != nil {
						break
					}
				}

				Expect(target1).ToNot(BeNil())
				Expect(target2).ToNot(BeNil())
				Expect(target2.ID).To(Equal(target1.ID))
			})
		})

	})

	Describe(".BuildImage()", func() {

		createImageTarball := func() io.Reader {
			buf := new(bytes.Buffer)
			gzipWriter := gzip.NewWriter(buf)
			tarWriter := tar.NewWriter(gzipWriter)

			dockerSource := `
			FROM %s
			MAINTAINER Nitrous.IO, hello@nitrous.io

			ADD some_file.txt /some/file.txt

			CMD /bin/bash -c 'cat /some/file.txt'
			`
			formattedDockerSource := fmt.Sprintf(dockerSource, image)

			dockerSourceHeader := &tar.Header{
				Name: "Dockerfile",
				Size: int64(len(formattedDockerSource)),
			}
			err := tarWriter.WriteHeader(dockerSourceHeader)
			Expect(err).To(BeNil())
			written, err := tarWriter.Write([]byte(formattedDockerSource))
			Expect(err).To(BeNil())
			Expect(written).ToNot(Equal(0))

			someFile := "Hello from Docker!"
			someFileHeader := &tar.Header{
				Name: "some_file.txt",
				Size: int64(len(someFile)),
			}
			err = tarWriter.WriteHeader(someFileHeader)
			Expect(err).To(BeNil())
			Expect(written).ToNot(Equal(0))
			written, err = tarWriter.Write([]byte(someFile))
			Expect(err).To(BeNil())
			Expect(written).ToNot(Equal(0))

			err = tarWriter.Close()
			Expect(err).To(BeNil())
			err = gzipWriter.Close()
			Expect(err).To(BeNil())

			return buf
		}

		It("creates an Image successfully", func() {
			readBuf := createImageTarball()
			err := prov.BuildImage("myimage", readBuf)
			Expect(err).To(BeNil())

			exec.Command("docker", "rmi", strings.Join([]string{namespace, "myimage:latest"}, ".")).Run()
		})

		It("correctly stores the additional data", func() {
			readBuf := createImageTarball()
			err := prov.BuildImage("myimage", readBuf)
			Expect(err).To(BeNil())

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

			exec.Command("docker", "rmi", strings.Join([]string{namespace, "myimage:latest"}, ".")).Run()
		})

	})

	Describe(".RemoveImage()", func() {

		var (
			publicImages    []string
			nonPublicImages []string
		)

		BeforeEach(func() {
			publicImages = createImages(1, "removeable", false, false)
			nonPublicImages = createImages(1, "nonremoveable", true, true)
		})

		AfterEach(func() {
			/*removeCreated(publicImages)
			removeCreated(nonPublicImages)*/
		})

		Context("when an ID is provided", func() {
			Context("when the image is not owned the namespace", func() {
				It("should return an error not locating image", func() {
					id := doInspect(nonPublicImages[0], "{{.Id}}")
					Expect(id).ToNot(HaveLen(0))
					err := prov.RemoveImage(id)
					Expect(err).To(Equal(provider.ErrImageNotFound))

					id = doInspect(publicImages[0], "{{.Id}}")
					Expect(id).ToNot(HaveLen(0))
				})
			})

			Context("when the image is owned by the namespace", func() {
				It("should be deleted", func() {
					id := doInspect(publicImages[0], "{{.Id}}")
					Expect(id).ToNot(HaveLen(0))
					err := prov.RemoveImage(id)
					Expect(err).To(BeNil())

					id = doInspect(publicImages[0], "{{.Id}}")
					Expect(id).To(HaveLen(0))
				})
			})
		})

		FContext("when a Name is provided", func() {
			Context("when the image is not owned the namespace", func() {
				It("should return an error not locating image", func() {
					err := prov.RemoveImage(nonPublicImages[0])
					Expect(err).To(Equal(provider.ErrImageNotFound))

					id := doInspect(nonPublicImages[0], "{{.Id}}")
					Expect(id).ToNot(HaveLen(0))
				})
			})

			Context("when the image is owned by the namespace", func() {
				It("should be deleted", func() {
					err := prov.RemoveImage(publicImages[0])
					Expect(err).To(BeNil())

					id := doInspect(publicImages[0], "{{.Id}}")
					Expect(id).To(HaveLen(0))
				})
			})
		})

	})
})
