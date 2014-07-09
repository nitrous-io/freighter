package docker_test

import (
	"fmt"
	"os"
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
		image     = "ddollar/busybox-fakessh"
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
	}

	Describe(".Create()", func() {
		var (
			name         string
			id           string
			host         string
			info         *provider.ContainerInfo
			pubkey1      = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQCxCeR/jJna6xXELDV8HF51QQPbj++JNDE4W/nqLyhzRTEDDnAfyp8gXW/EXXos8w5XXLak6eUDXE/QI6o8blX39RYoT47u45v4b2scN0ZiS5McmESo193KwWH3gXHbnHWywzPy7+GrRF3nHm/uidvp0tqh4ywqIZujaMjST1sIOQ== test@example.com"
			pubkey2      string
			fingerprint1 = "f7:de:4d:6f:54:6b:b9:d5:78:a9:62:15:e2:ad:ec:bb"
			fingerprint2 string
			sshkeypath   string
		)

		getID := func(name string) string {
			id, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", fmt.Sprintf("%s.%s", namespace, name)).CombinedOutput()
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

			info, err = prov.Create(name, image)
			Expect(err).To(BeNil())
			id = getID(name)
			host = getHost(id)
		})

		AfterEach(func() {
			cleanUpContainer(namespace + "." + name)
			os.Remove(sshkeypath)
		})

		/* expectSSHToWork := func() {*/
		/*   time.Sleep(1 * time.Second)*/
		/*   out, err := exec.Command("ssh", "action@localhost", "-i", sshkeypath, "-p", strconv.Itoa(int(port)), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "echo success").CombinedOutput()*/
		/*   Expect(out).To(ContainSubstring("success"))*/
		/*   Expect(err).To(BeNil())*/
		/* }*/

		It("creates a container successfully", func() {
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(namespace + "." + name))
			Expect(info.Image).To(Equal(image))
			Expect(info.Host).To(Equal(host))
			Expect(info.State).To(Equal(provider.ContainerStateRunning))
			/* expectSSHToWork()*/
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
					foreignContainerName = "notfreighter." + RandomName("freightertest-foreign")

					// shut down first container
					err := exec.Command("docker", "stop", id).Run()
					Expect(err).To(BeNil())

					// create another container
					container2Info, err = prov.Create(container2Name, image)
					Expect(err).To(BeNil())

					// create a container not managed by freighter
					out, err := exec.Command("docker", "run", "-d", "-i", "-t", "--name", foreignContainerName, image, "/bin/bash").CombinedOutput()
					Expect(err).To(BeNil())
					foreignContainerID = strings.TrimSpace(string(out))
				})

				AfterEach(func() {
					cleanUpContainer(namespace + "." + container2Name)
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
							}
						}

						return attributes
					}

					It("returns a list of all containers managed by freighter, running or stopped", func() {
						list, err := prov.List()
						Expect(err).To(BeNil())

						Expect(list).To(HaveLen(2))

						names := getAttributes("Name", list)
						Expect(names).To(ContainElement(namespace + "." + name))
						Expect(names).To(ContainElement(namespace + "." + container2Name))

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
					})
				})

				Describe(".GetInfo()", func() {
					Context("if a container with a given id is managed by freighter", func() {
						It("returns container info", func() {
							info, err := prov.GetInfo(id)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(id))
							Expect(info.Name).To(Equal(namespace + "." + name))
							Expect(info.Image).To(Equal(image))
							Expect(info.Host).To(Equal(""))
							/* Expect(info.SSHPort).To(Equal(port))*/
							Expect(info.State).To(Equal(provider.ContainerStateStopped))

							info, err = prov.GetInfo(container2Info.ID)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(container2Info.ID))
							Expect(info.Name).To(Equal(namespace + "." + container2Name))
							Expect(info.Image).To(Equal(image))
							Expect(info.Host).To(Equal(container2Info.Host))
							/* Expect(info.SSHPort).To(Equal(container2Info.SSHPort))*/
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
					/* expectSSHToWork()*/
				}

				expectContainerInfoToHaveBeenReturned := func() {
					Expect(info).NotTo(BeNil())
					Expect(info.ID).To(Equal(id))
					Expect(info.Name).To(Equal(namespace + "." + name))
					Expect(info.Image).To(Equal(image))
					Expect(info.Host).To(Equal(host))
					/* Expect(info.SSHPort).To(Equal(port))*/
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
					Expect(info.Host).To(Equal(""))
					/* Expect(info.SSHPort).To(Equal(port))*/
					Expect(info.Name).To(Equal(namespace + "." + name))
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
					Expect(info.Name).To(Equal(namespace + "." + name))
					Expect(info.Image).To(Equal(image))
					Expect(info.Host).To(Equal(host))
					/* Expect(info.SSHPort).To(Equal(port))*/
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
						/* expectSSHToWork()*/
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
						/* expectSSHToWork()*/
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
					Expect(info.Name).To(Equal(namespace + "." + name))
					Expect(info.Image).To(Equal(image))
					Expect(info.Host).To(Equal(""))
					/* Expect(info.SSHPort).To(Equal(uint16(0)))*/
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
				BeforeEach(func() {
					pubkey1 = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQCxCeR/jJna6xXELDV8HF51QQPbj++JNDE4W/nqLyhzRTEDDnAfyp8gXW/EXXos8w5XXLak6eUDXE/QI6o8blX39RYoT47u45v4b2scN0ZiS5McmESo193KwWH3gXHbnHWywzPy7+GrRF3nHm/uidvp0tqh4ywqIZujaMjST1sIOQ== test@example.com"
					pubkey2 = strings.TrimSpace(string(TestSSHPubKey))
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

				Describe(".RemovePublicKeys()", func() {
					Context("when a pub key with a given fingerprint is found", func() {
						itWorks := func() {
							It("removes the matching pub key from the authorized_keys file", func() {
								err := prov.RemovePublicKey(id, fingerprint1)
								Expect(err).To(BeNil())
								pubkeys, err := prov.ListPublicKeys(id)
								Expect(err).To(BeNil())
								Expect(pubkeys).To(Equal(map[string]string{
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
