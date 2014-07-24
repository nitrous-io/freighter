package docker_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
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
		datadir := os.Getenv("FREIGHTERD_BASE")

		if datadir == "" {
			datadir, err = TempDir()
			Expect(err).To(BeNil())
		}

		logger := &util.Logger{Level: util.LogLevelQuiet}
		prov, err = docker.NewDockerProvider(logger, datadir, namespace)
		Expect(err).To(BeNil())
	})

	cleanUpContainer := func(name string) {
		exec.Command("docker", "stop", name).Run()
		exec.Command("docker", "rm", name).Run()
	}

	fullNamespaceName := func(name string) string {
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
		return doInspect(fullNamespaceName(name), "{{.Id}}")
	}

	getPort := func(id string) string {
		return doInspect(id, `{{(index (index .NetworkSettings.Ports "22/tcp") 0).HostPort}}`)
	}

	getHost := func(id string) string {
		return doInspect(id, "{{.NetworkSettings.IPAddress}}")
	}

	createImages := func(count int, name string, hidden, noFindable, noEntropy bool) []string {
		dockerSource := `
			FROM %s
			MAINTAINER Nitrous.IO, hello@nitrous.io

			RUN echo '%s' >> /tmp/info

			CMD /bin/bash
			`
		var entropy string
		if !noEntropy {
			entropy = name
		} else {
			entropy = "thing"
		}
		interpolated := fmt.Sprintf(dockerSource, image, entropy)
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

	withoutNamespace := func(name string) string {
		return strings.TrimPrefix(name, strings.Join([]string{namespace, "."}, ""))
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
			cleanUpContainer(fullNamespaceName(name))
			os.Remove(sshkeypath)
		})

		It("creates a container successfully", func() {
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(fullNamespaceName(name)))
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
					cleanUpContainer(fullNamespaceName(container2Name))
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
						Expect(names).To(ContainElement(fullNamespaceName(name)))
						Expect(names).To(ContainElement(fullNamespaceName(container2Name)))

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
							Expect(info.Name).To(Equal(fullNamespaceName(name)))
							Expect(info.Image).To(Equal(image))
							Expect(info.Host).To(Equal(""))
							Expect(info.Ports[port]).To(Equal(22))
							Expect(info.State).To(Equal(provider.ContainerStateStopped))

							info, err = prov.GetInfo(container2Info.ID)
							Expect(err).To(BeNil())
							Expect(info.ID).To(Equal(container2Info.ID))
							Expect(info.Name).To(Equal(fullNamespaceName(container2Name)))
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
					Expect(info.Name).To(Equal(fullNamespaceName(name)))
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
					Expect(info.Name).To(Equal(fullNamespaceName(name)))
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
					Expect(info.Name).To(Equal(fullNamespaceName(name)))
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
					Expect(info.Name).To(Equal(fullNamespaceName(name)))
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
						cleanUpContainer(fullNamespaceName(busyboxName))
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
			cleanUpContainer(fullNamespaceName(name))
		})

		It("creates container with options", func() {
			id := getID(name)
			Expect(info.ID).To(Equal(id))
			Expect(info.Name).To(Equal(fullNamespaceName(name)))
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
				publicImages    []string
				nonPublicImages []string
			)

			BeforeEach(func() {
				publicImages = createImages(1, "basic", false, false, false)
				nonPublicImages = createImages(1, "basicprivate", true, true, false)
			})

			AfterEach(func() {
				removeCreated(publicImages)
				removeCreated(nonPublicImages)
			})

			It("should not expose images not managed by the namespace", func() {
				imageList, err := prov.ListImages()
				Expect(err).To(BeNil())

				var found bool
				for _, img := range imageList {
					Expect(img.Name).ToNot(ContainSubstring("basicprivate"))
					if strings.Contains(img.Name, "basic") {
						found = true
					}
				}

				Expect(found).To(BeTrue())
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
				createdImages = createImages(2, "multiple", true, false, true)
			})

			AfterEach(func() {
				removeCreated(createdImages)
			})

			It("should break them out into separate entries", func() {
				imageList, err := prov.ListImages()
				Expect(err).To(BeNil())

				Expect(len(imageList)).To(Equal(2))
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
			outputBuffer := new(bytes.Buffer)
			info, err := prov.BuildImage("myimage", readBuf, outputBuffer)
			Expect(err).To(BeNil())
			Expect(info.ID).ToNot(HaveLen(0))

			Expect(outputBuffer.String()).To(ContainSubstring("Successfully built"))

			exec.Command("docker", "rmi", strings.Join([]string{namespace, "myimage:latest"}, ".")).Run()
		})

		It("correctly stores the additional data", func() {
			readBuf := createImageTarball()
			outputBuffer := new(bytes.Buffer)
			info, err := prov.BuildImage("myimage", readBuf, outputBuffer)
			Expect(err).To(BeNil())

			Expect(info.ID).ToNot(HaveLen(0))
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
			publicImages = createImages(1, "removeable", false, false, false)
			nonPublicImages = createImages(1, "nonremoveable", true, true, false)
		})

		AfterEach(func() {
			removeCreated(publicImages)
			removeCreated(nonPublicImages)
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

					out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", publicImages[0]).CombinedOutput()
					Expect(out).To(ContainSubstring("Error: No such image or container:"))
				})

				Context("with multiple images under ID", func() {
					var (
						multipleImages []string
					)

					BeforeEach(func() {
						multipleImages = createImages(1, "multremoveable", false, false, true)
						multipleImages = append(multipleImages, createImages(1, "multremoveable2", false, false, true)...)
					})

					AfterEach(func() {
						removeCreated(multipleImages)
					})

					It("should not allow for deletion", func() {
						id := doInspect(multipleImages[0], "{{.Id}}")
						Expect(id).ToNot(HaveLen(0))
						err := prov.RemoveImage(id)
						Expect(err).To(Equal(provider.ErrImageShared))

						id = doInspect(multipleImages[0], "{{.Id}}")
						Expect(id).ToNot(HaveLen(0))
					})
				})
			})
		})

		Context("when a Name is provided", func() {
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
					err := prov.RemoveImage(withoutNamespace(publicImages[0]))
					Expect(err).To(BeNil())

					out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", publicImages[0]).CombinedOutput()
					Expect(out).To(ContainSubstring("Error: No such image or container:"))
				})
			})
		})

	})

	Describe(".PullImage()", func() {

		var (
			mockProxy    *MockDockerProxy
			tempAuthFile *os.File
		)

		createBasicImage := func(name string) {
			err := exec.Command("docker", "tag", "-f", image, name).Run()
			Expect(err).To(BeNil())
		}

		checkImageRenamed := func(name string) {
			out, _ := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).CombinedOutput()
			Expect(out).To(ContainSubstring("Error: No such image or container:"))

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

			/* credientials are
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
			err = tempAuthFile.Sync()
			Expect(err).To(BeNil())

			prov.DockerClientProxy = mockProxy
			prov.DockerConfigLocation = tempAuthFile.Name()
		})

		AfterEach(func() {
			tempAuthFile.Close()
		})

		It("should handle if auth credentials could not be located", func() {
			info, err := prov.PullImage("cloudy.io/nodejs")
			Expect(err).To(Equal(provider.ErrImageAuthNotAllowed))
			Expect(info.ID).To(HaveLen(0))
			Expect(mockProxy.PullImageCalled).To(BeFalse())
		})

		It("should allow for importing by default hub", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("nitrousio/nodejs")
			}
			info, err := prov.PullImage("nitrousio/nodejs")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("nitrousio/nodejs"))
			Expect(mockProxy.PullImageOpts.Tag).To(HaveLen(0))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("test"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("1234xxcc"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("docker@callumj.com"))

			Expect(info.Name).To(ContainSubstring("nitrousio/nodejs"))

			checkImageRenamed("nitrousio/nodejs:latest")
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

			checkImageRenamed("ubuntu:10.04")
		})

		It("should allow for importing by simple remote registry", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("quay.io/ubuntu")
			}
			_, err := prov.PullImage("quay.io/ubuntu")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("quay.io/ubuntu"))
			Expect(mockProxy.PullImageOpts.Tag).To(HaveLen(0))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("testo"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("Password1"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("quay@callumj.com"))

			checkImageRenamed("quay.io/ubuntu:latest")
		})

		It("should allow for importing by remote registry", func() {
			mockProxy.PullImageCallBack = func() {
				createBasicImage("quay.io/quay/ubuntu")
			}
			_, err := prov.PullImage("quay.io/quay/ubuntu")
			Expect(err).To(BeNil())

			Expect(mockProxy.PullImageCalled).To(BeTrue())
			Expect(mockProxy.PullImageOpts.Repository).To(Equal("quay.io/quay/ubuntu"))
			Expect(mockProxy.PullImageOpts.Tag).To(HaveLen(0))
			Expect(mockProxy.PullImageAuth.Username).To(Equal("testo"))
			Expect(mockProxy.PullImageAuth.Password).To(ContainSubstring("Password1"))
			Expect(mockProxy.PullImageAuth.Email).To(Equal("quay@callumj.com"))

			checkImageRenamed("quay.io/quay/ubuntu:latest")
		})

		It("should handle a malformed config", func() {
			_, err := prov.PullImage("magic.io/quay/ubuntu")
			Expect(err).To(Equal(provider.ErrImageAuthMisconfigured))

			Expect(mockProxy.PullImageCalled).To(BeFalse())
		})

		It("should surface PushImage errors", func() {
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

		It("should not allow for tagging images outside of the namespace", func() {
			created := createImages(1, "namely", true, true, false)
			err := prov.TagImage(created[0], "namely2", false)
			Expect(err).To(Equal(provider.ErrImageNotFound))

			removeCreated(created)
		})

		It("should not allow for tagging images outside of the namespace with an ID", func() {
			created := createImages(1, "idly", true, true, false)
			id := doInspect(created[0], "{{.Id}}")
			err := prov.TagImage(id, "idly2", false)
			Expect(err).To(Equal(provider.ErrImageNotFound))

			removeCreated(created)
		})

		It("should allow for tagging images without tags", func() {
			created := createImages(1, "withoutly", false, false, false)
			err := prov.TagImage(withoutNamespace(created[0]), "withoutly2", false)
			Expect(err).To(BeNil())

			verifyImageExistence("withoutly2:latest")
			removeCreated(created)
		})

		It("should allow for tagging images with tags", func() {
			created := createImages(1, "tagly", false, false, false)
			err := prov.TagImage(withoutNamespace(created[0]), "tagly2:1907", false)
			Expect(err).To(BeNil())

			verifyImageExistence("tagly2:1907")
			removeCreated(created)
		})

		Context("image tag already exists", func() {

			It("should not override existing tags with no force", func() {
				created := createImages(1, "nooverridely", false, false, false)
				err := prov.TagImage(withoutNamespace(created[0]), "nooverridely:n1", false)
				Expect(err).To(Equal(provider.ErrImageTagExists))

				removeCreated(created)
			})

			It("should override existing tags with force", func() {
				target := createImages(1, "overridely", false, false, false)
				destination := createImages(1, "overridelyreplacey", false, false, false)
				curId := doInspect(destination[0], "{{.Id}}")

				err := prov.TagImage(withoutNamespace(target[0]), "overridelyreplacey:n1", true)
				Expect(err).To(BeNil())

				newId := getID("overridelyreplacey:n1")
				Expect(newId).ToNot(Equal(curId))
				removeCreated(target)
				removeCreated(destination)
			})

		})

	})

	Describe(".PushImage()", func() {

		It("should not push if the image is not accessible", func() {
			created := createImages(1, "namely", true, true, false)
			err := prov.PushImage(created[0], "cloud1")
			Expect(err).To(Equal(provider.ErrImageNotFound))

			removeCreated(created)
		})

		Context("with an dockercfg authentication file", func() {

			var (
				mockProxy    *MockDockerProxy
				tempAuthFile *os.File
			)

			BeforeEach(func() {
				var err error

				mockProxy = &MockDockerProxy{}
				tempAuthFile, err = ioutil.TempFile("", "cfg")
				Expect(err).To(BeNil())

				/* credientials are
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
				err = tempAuthFile.Sync()
				Expect(err).To(BeNil())

				prov.DockerClientProxy = mockProxy
				prov.DockerConfigLocation = tempAuthFile.Name()
			})

			AfterEach(func() {
				tempAuthFile.Close()
			})

			It("should handle if auth credentials could not be located", func() {
				created := createImages(1, "namely", false, false, false)
				err := prov.PushImage(withoutNamespace(created[0]), "thingyquay.io/callumj/test1")
				Expect(err).To(Equal(provider.ErrImageAuthNotAllowed))

				Expect(mockProxy.PushImageCalled).To(BeFalse())

				removeCreated(created)
			})

			It("should support pushing to the standard Docker Registry endpoint", func() {
				created := createImages(1, "namely", false, false, false)
				err := prov.PushImage(withoutNamespace(created[0]), "callumj/test1:n1")
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Name).To(Equal("callumj/test1"))
				Expect(mockProxy.PushImageOpts.Tag).To(Equal("n1"))
				Expect(mockProxy.PushImageAuth.Username).To(Equal("test"))
				Expect(mockProxy.PushImageAuth.Password).To(ContainSubstring("1234xxcc"))
				Expect(mockProxy.PushImageAuth.Email).To(Equal("docker@callumj.com"))

				removeCreated(created)
			})

			It("should not set tag if not explicit", func() {
				created := createImages(1, "namely", false, false, false)
				err := prov.PushImage(withoutNamespace(created[0]), "callumj/test1")
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Tag).To(HaveLen(0))

				removeCreated(created)
			})

			It("should set tag if latest is explicit", func() {
				created := createImages(1, "namely", false, false, false)
				err := prov.PushImage(withoutNamespace(created[0]), "callumj/test1:latest")
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Tag).To(Equal("latest"))

				removeCreated(created)
			})

			It("should support pushing to an alternative endpoint", func() {
				created := createImages(1, "namely", false, false, false)
				err := prov.PushImage(withoutNamespace(created[0]), "quay.io/callumj/test1")
				Expect(err).To(BeNil())

				Expect(mockProxy.PushImageCalled).To(BeTrue())
				Expect(mockProxy.PushImageOpts.Name).To(Equal("quay.io/callumj/test1"))
				Expect(mockProxy.PushImageOpts.Tag).To(HaveLen(0))
				Expect(mockProxy.PushImageAuth.Username).To(Equal("testo"))
				Expect(mockProxy.PushImageAuth.Password).To(ContainSubstring("Password1"))
				Expect(mockProxy.PushImageAuth.Email).To(Equal("quay@callumj.com"))

				removeCreated(created)
			})

			It("should handle a malformed config", func() {
				created := createImages(1, "namely", false, false, false)
				err := prov.PushImage(withoutNamespace(created[0]), "magic.io/callumj/test1")
				Expect(err).To(Equal(provider.ErrImageAuthMisconfigured))

				Expect(mockProxy.PushImageCalled).To(BeFalse())

				removeCreated(created)
			})

			It("should surface PushImage errors", func() {
				created := createImages(1, "namely", false, false, false)
				sharedErr := errors.New("Sorry!")
				mockProxy.PushImageError = sharedErr
				err := prov.PushImage(withoutNamespace(created[0]), "quay.io/callumj/test1")
				Expect(err).To(Equal(sharedErr))

				Expect(mockProxy.PushImageCalled).To(BeTrue())

				removeCreated(created)
			})

		})

	})
})
