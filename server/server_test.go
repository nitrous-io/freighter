package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/nitrous-io/freighter/pkg/stats"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/server"
	"github.com/nitrous-io/freighter/util"

	. "github.com/nitrous-io/freighter/test"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func Test(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "server")
}

var hostSecret = "sekrit"

var _ = Describe("server", func() {
	var (
		handler *mux.Router
		res     *httptest.ResponseRecorder
		req     *http.Request
		err     error
		svr     *server.Server
		prov    *MockProvider
	)

	configJSON := `{"HostConfig": {"PortBindings": {"3000/tcp": [{"HostPort":"1234", "HostIp": "127.0.0.1"}]}}}`

	BeforeEach(func() {
		prov = &MockProvider{}
		logger := &util.Logger{Level: util.LogLevelQuiet}
		svr = server.NewServer(logger, prov, &server.Config{})
		svr.SetSecret(hostSecret)
		handler = svr.Router()
		res = httptest.NewRecorder()
	})

	Describe("GET /", func() {
		BeforeEach(func() {
			req, err = NewRequestWithSecret(hostSecret, "GET", "/", nil)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		})

		It("responds with 200 ok", func() {
			Expect(res.Code).To(Equal(http.StatusOK))
			Expect(res.Body.String()).To(MatchJSON(`{"status":"ok"}`))
		})
	})

	Describe("GET /containers", func() {
		doRequest := func() {
			req, err = NewRequestWithSecret(hostSecret, "GET", "/containers", nil)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		Context("when there isn't any container", func() {
			BeforeEach(func() {
				doRequest()
			})

			It("responds with 200 ok with empty container info array", func() {
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(MatchJSON(`{"containers":[]}`))
			})
		})

		Context("when there exists containers", func() {
			BeforeEach(func() {
				prov.Containers = []*provider.ContainerInfo{
					{ID: "aaa", Name: "foo", Image: "ubuntu", ImageID: "abc123", Host: "1.2.3.4", Gateway: "1.2.3.0", State: provider.ContainerStateRunning},
					{ID: "bbb", Name: "bar", Image: "centos", ImageID: "def456", Host: "1.2.3.5", Gateway: "1.2.3.0", State: provider.ContainerStateStopped},
				}
				doRequest()
			})

			It("responds with 200 ok with container info array", func() {
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(MatchJSON(`{"containers":[
				{"id":"aaa","name":"foo","image":"ubuntu","image_id":"abc123","host":"1.2.3.4","gateway":"1.2.3.0","state":"running","ports":null,"exit_code":0,"started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"},
				{"id":"bbb","name":"bar","image":"centos","image_id":"def456","host":"1.2.3.5","gateway":"1.2.3.0","state":"stopped","ports":null,"exit_code":0,"started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"}
				]}`))
			})
		})
	})

	Describe("POST /containers", func() {
		var (
			params     *url.Values
			name       string
			image      string
			secret     string
			publicKey  string
			publicKey2 string
			state      string
		)

		BeforeEach(func() {
			name = "fragrant-wind-69"
			image = "ubuntu"
			secret = "s3cr3t"
			publicKey = "ssh-rsa Foo"
			publicKey2 = "ssh-rsa Bar"
			params = &url.Values{}
			params.Set("name", name)
			params.Set("image", image)
			params.Set("secret", secret)
			params.Set("public_key[]", publicKey)
			params.Add("public_key[]", publicKey2)
			params.Set("config", configJSON)
			state = RandomName("state")
			params.Set("state", state)
		})

		doRequest := func() {
			req, err = NewFormRequest(hostSecret, "POST", "/containers", params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		Context("when params are valid", func() {
			It("calls provider.Create()", func() {
				doRequest()
				Expect(prov.CreateCalled).To(BeTrue())
				Expect(prov.ReceivedName).To(Equal(name))
				Expect(prov.ReceivedImage).To(Equal(image))
				Expect(prov.ReceivedSecret).To(Equal(secret))
				Expect(prov.ReceivedConfig).To(Equal(configJSON))
			})

			Context("when provider.Create()", func() {
				Context("returns an error", func() {
					BeforeEach(func() {
						prov.CreateError = errors.New("pc load letter")
					})

					It("responds with 500 Internal Server Error", func() {
						doRequest()
						Expect(res.Code).To(Equal(http.StatusInternalServerError))
						Expect(res.Body.String()).To(MatchJSON(`{"error":"pc load letter"}`))
					})
				})

				Context("returns a provider.StartFailedError", func() {
					BeforeEach(func() {
						prov.CreateContainerInfo = &provider.ContainerInfo{ID: "aaa", Name: name, Image: "ubuntu", ImageID: "abc123", Host: "", Gateway: "", State: provider.ContainerStateStopped}
						prov.CreateError = &provider.StartFailedError{Err: errors.New("start failed")}
					})

					It("responds with 200 OK with container info and the error", func() {
						doRequest()
						Expect(res.Code).To(Equal(http.StatusOK))
						Expect(res.Body.String()).To(MatchJSON(fmt.Sprintf(`{
							"container":{
								"id":"aaa","name":"fragrant-wind-69","image":"ubuntu","image_id":"abc123","host":"","gateway":"","state":"stopped","ports": null,"exit_code":0,"started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"
							},
							"status":"start_failed",
							"error":"%s"
						}`, prov.CreateError)))
					})
				})

				Context("when image does not exist", func() {
					BeforeEach(func() {
						prov.CreateError = provider.ErrImageNotFound
					})

					It("responds with 404 Not Found", func() {
						doRequest()
						Expect(res.Code).To(Equal(http.StatusNotFound))
						Expect(res.Body.String()).To(MatchJSON(fmt.Sprintf(`{"error":"%s"}`, provider.ErrImageNotFound.Error())))
					})
				})

				Context("when Cmd config has an error", func() {
					BeforeEach(func() {
						prov.CreateError = provider.ErrCmdConfig
					})

					It("responds with 422 unprocessable entity", func() {
						doRequest()
						Expect(res.Code).To(Equal(422))
						Expect(res.Body.String()).To(MatchJSON(fmt.Sprintf(`{"error":"%s"}`, provider.ErrCmdConfig.Error())))
					})
				})

				Context("returns a container info with no error", func() {
					BeforeEach(func() {
						prov.CreateContainerInfo = &provider.ContainerInfo{ID: "aaa", Name: name, Image: "ubuntu", ImageID: "abc123", Host: "1.2.3.4", Gateway: "1.2.3.0", State: provider.ContainerStateRunning}
					})

					It("responds with 200 OK and container info", func() {
						doRequest()
						Expect(res.Code).To(Equal(http.StatusOK))
						Expect(res.Body.String()).To(MatchJSON(`{"container":{
							"id":"aaa","name":"fragrant-wind-69","image":"ubuntu","image_id":"abc123","host":"1.2.3.4","gateway":"1.2.3.0","state":"running","ports": null,"exit_code":0,"started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"
						}}`))
					})
				})
			})
		})

		for _, param := range []string{"name", "image"} {
			func(param string) {
				Context(fmt.Sprintf("when param '%v' is missing", param), func() {
					BeforeEach(func() {
						params.Del(param)
						doRequest()
					})

					It("responds with 422 unprocessable entity", func() {
						Expect(res.Code).To(Equal(422))
						Expect(res.Body.String()).To(MatchJSON(fmt.Sprintf(`{"error":"%v is not valid"}`, param)))
					})

					It("does not call provider.Create()", func() {
						Expect(prov.CreateCalled).To(BeFalse())
					})
				})
			}(param)
		}

		Context("when an invalid name is given", func() {
			BeforeEach(func() {
				params.Set("name", "-foobar")
				doRequest()
			})

			It("responds with 422 unprocessable entity", func() {
				Expect(res.Code).To(Equal(422))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"name is not valid"}`))
			})

			It("does not call provider.Create()", func() {
				Expect(prov.CreateCalled).To(BeFalse())
			})
		})

		Context("when a given name is already taken", func() {
			BeforeEach(func() {
				prov.Containers = []*provider.ContainerInfo{
					{ID: "aaa", Name: name, Image: "ubuntu", Host: "1.2.3.4", State: provider.ContainerStateRunning},
				}
				doRequest()
			})

			It("responds with 422 unprocessable entity", func() {
				Expect(res.Code).To(Equal(422))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"name is already taken"}`))
			})

			It("does not call provider.Create()", func() {
				Expect(prov.CreateCalled).To(BeFalse())
			})
		})
	})

	Describe("GET /containers/:container_id", func() {
		BeforeEach(func() {
			prov.Containers = []*provider.ContainerInfo{
				{ID: "aaa", Name: "foo", Image: "ubuntu", ImageID: "abc123", Host: "1.2.3.4", Gateway: "1.2.3.0", State: provider.ContainerStateRunning},
			}
		})

		doRequest := func(id string) {
			req, err = NewRequestWithSecret(hostSecret, "GET", fmt.Sprintf("/containers/%v", id), nil)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		Context("when container by the given id exists", func() {
			BeforeEach(func() {
				doRequest("aaa")
			})

			It("responds with 200 ok with container information", func() {
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(MatchJSON(`{"container":{
					"id":"aaa","name":"foo","image":"ubuntu","image_id":"abc123","host":"1.2.3.4","gateway":"1.2.3.0","state":"running","ports":null,"exit_code":0,"started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"
				}}`))
			})
		})

		Context("when container by the given id does not exist", func() {
			BeforeEach(func() {
				doRequest("bbb")
			})

			It("responds with 404 not found", func() {
				Expect(res.Code).To(Equal(http.StatusNotFound))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
			})
		})
	})

	Describe("POST /containers/:container_id/commit", func() {
		doRequest := func(id, name string) {
			params := &url.Values{}
			if name != "" {
				params.Set("name", name)
			}
			req, err = NewRequestWithSecret(hostSecret, "POST", fmt.Sprintf("/containers/%v/commit", id), params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		BeforeEach(func() {
			prov.Containers = []*provider.ContainerInfo{
				{ID: "python", Name: "foo", Image: "ubuntu", Host: "1.2.3.4", State: provider.ContainerStateRunning},
			}
		})

		It("responds with 200 OK when successful", func() {
			prov.CommitInfo = &provider.ImageInfo{
				ID:          "ID1",
				Name:        "pipped",
				Tag:         "latest",
				CreatedAt:   1975,
				ActualSize:  12,
				VirtualSize: 19,
			}
			doRequest("python", "pipped")
			Expect(res.Code).To(Equal(http.StatusOK))
			Expect(prov.CommitCalled).To(BeTrue())
			Expect(res.Body.String()).To(MatchJSON(`{"image":{
				"id":"ID1","name":"pipped","tag":"latest","created_at":1975,"actual_size":12,"virtual_size":19}}`))
		})

		Context("when container commit fails", func() {
			BeforeEach(func() {
				prov.CommitError = fmt.Errorf("commit failed")
				doRequest("python", "virtualenved")
			})

			It("responds with 500", func() {
				Expect(prov.CommitCalled).To(BeTrue())
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"commit failed"}`))
			})
		})
	})

	Describe("Container Control", func() {
		var (
			provFuncCalled   *bool
			provFuncError    *error
			expectedResponse string
			doRequest        func(string)
			id               = "aaa"
			name             = "foobar"
			image            = "ubuntu"
			imageID          = "abc123"
			host             = "1.2.3.4"
			gateway          = "1.2.3.0"
			params           *url.Values
		)

		BeforeEach(func() {
			prov.Containers = []*provider.ContainerInfo{
				{ID: id, Name: name, Image: image, ImageID: imageID, Host: host, Gateway: gateway},
			}
			params = &url.Values{}
		})

		itWorks := func() {
			Context("when container by the given id exists", func() {
				It("calls provider func", func() {
					doRequest(id)
					Expect(*provFuncCalled).To(BeTrue())
					Expect(prov.ReceivedID).To(Equal(id))
				})

				Context("when provider func", func() {
					Context("returns an error", func() {
						BeforeEach(func() {
							*provFuncError = errors.New("pc load letter")
							doRequest(id)
						})

						It("responds with 500 Internal Server Error", func() {
							Expect(res.Code).To(Equal(http.StatusInternalServerError))
							Expect(res.Body.String()).To(MatchJSON(`{"error":"pc load letter"}`))
						})
					})

					Context("returns a container info with no error", func() {
						BeforeEach(func() {
							doRequest(id)
						})

						It("responds with 200 OK", func() {
							Expect(res.Code).To(Equal(http.StatusOK))
							Expect(res.Body.String()).To(MatchJSON(expectedResponse))
						})
					})
				})
			})

			Context("when container by the given id does not exist", func() {
				BeforeEach(func() {
					doRequest("bbb")
				})

				It("responds with 404 not found", func() {
					Expect(res.Code).To(Equal(http.StatusNotFound))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
				})
			})
		}

		Describe("DELETE /containers/:container_id", func() {
			BeforeEach(func() {
				provFuncCalled = &prov.TerminateCalled
				provFuncError = &prov.TerminateError
				expectedResponse = `{
					"result": "terminated",
					"container": {
						"id": "` + id + `",
						"name": "` + name + `",
						"image": "` + image + `",
						"image_id": "` + imageID + `",
						"host": "",
						"gateway": "",
						"state": "",
						"ports": null,
						"exit_code":0,
						"started_at":"0001-01-01T00:00:00Z",
						"finished_at":"0001-01-01T00:00:00Z"
					}
				}`

				doRequest = func(id string) {
					req, err = NewRequestWithSecret(hostSecret, "DELETE", fmt.Sprintf("/containers/%v", id), params)
					Expect(err).To(BeNil())
					handler.ServeHTTP(res, req)
				}
			})

			itWorks()
		})

		Describe("POST /containers/:container_id/start", func() {
			BeforeEach(func() {
				provFuncCalled = &prov.StartCalled
				provFuncError = &prov.StartError
				expectedResponse = `{
					"result": "started",
					"container": {
						"id": "` + id + `",
						"name": "` + name + `",
						"image": "` + image + `",
						"image_id": "` + imageID + `",
						"host": "` + host + `",
						"gateway": "` + gateway + `",
						"state": "running",
						"ports": null,
						"exit_code":0,
						"started_at":"0001-01-01T00:00:00Z",
						"finished_at":"0001-01-01T00:00:00Z"
					}
				}`

				doRequest = func(id string) {
					req, err = NewFormRequest(hostSecret, "POST", fmt.Sprintf("/containers/%v/start", id), params)
					Expect(err).To(BeNil())
					handler.ServeHTTP(res, req)
				}
			})

			itWorks()

			It("receives config", func() {
				params.Set("config", configJSON)
				doRequest(id)
				Expect(prov.ReceivedConfig).To(Equal(configJSON))
			})
		})

		Describe("POST /containers/:container_id/stop", func() {
			BeforeEach(func() {
				provFuncCalled = &prov.StopCalled
				provFuncError = &prov.StopError
				expectedResponse = `{
					"result": "stopped",
					"container": {
						"id": "` + id + `",
						"name": "` + name + `",
						"image": "` + image + `",
						"image_id": "` + imageID + `",
						"host": "",
						"gateway": "",
						"state": "stopped",
						"ports": null,
						"exit_code":0,
						"started_at":"0001-01-01T00:00:00Z",
						"finished_at":"0001-01-01T00:00:00Z"
					}
				}`

				doRequest = func(id string) {
					req, err = NewFormRequest(hostSecret, "POST", fmt.Sprintf("/containers/%v/stop", id), params)
					Expect(err).To(BeNil())
					handler.ServeHTTP(res, req)
				}
			})

			itWorks()
		})

		Describe("POST /containers/:container_id/restart", func() {
			BeforeEach(func() {
				provFuncCalled = &prov.RestartCalled
				provFuncError = &prov.RestartError
				expectedResponse = `{
					"result": "restarted",
					"container": {
						"id": "` + id + `",
						"name": "` + name + `",
						"image": "` + image + `",
						"image_id": "` + imageID + `",
						"host": "` + host + `",
						"gateway": "` + gateway + `",
						"state": "running",
						"ports": null,
						"exit_code":0,
						"started_at":"0001-01-01T00:00:00Z",
						"finished_at":"0001-01-01T00:00:00Z"
					}
				}`

				doRequest = func(id string) {
					req, err = NewFormRequest(hostSecret, "POST", fmt.Sprintf("/containers/%v/restart", id), params)
					Expect(err).To(BeNil())
					handler.ServeHTTP(res, req)
				}
			})

			itWorks()
		})
	})

	Describe("GET /containers/:container_id/public_keys", func() {
		var (
			id           = "cafebabe"
			publicKey1   = "ssh-rsa Foo"
			publicKey2   = "ssh-rsa Bar"
			publicKey3   = "ssh-dsa Baz"
			fingerprint1 = "11:11:11:11:11:11:11:11:11:11:11:11:11:11:11:11"
			fingerprint2 = "22:22:22:22:22:22:22:22:22:22:22:22:22:22:22:22"
			fingerprint3 = "33:33:33:33:33:33:33:33:33:33:33:33:33:33:33:33"
		)

		doRequest := func() {
			req, err = NewRequestWithSecret(hostSecret, "GET", fmt.Sprintf("/containers/%v/public_keys", id), nil)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.ListPublicKeys()", func() {
			Expect(prov.ListPublicKeysCalled).To(BeFalse())
			doRequest()
			Expect(prov.ListPublicKeysCalled).To(BeTrue())
			Expect(prov.ReceivedID).To(Equal(id))
		})

		Context("when provider.ListPublicKeys() fails with error", func() {
			Context("because no such container exists", func() {
				BeforeEach(func() {
					prov.ListPublicKeysError = ErrMockProviderContainerNotFound
					doRequest()
				})

				It("responds with 404 not found", func() {
					Expect(res.Code).To(Equal(http.StatusNotFound))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
				})
			})

			Context("because of some unknown error", func() {
				BeforeEach(func() {
					prov.ListPublicKeysError = errors.New("unknown error")
					doRequest()
				})

				It("responds with 500 internal server error with error json", func() {
					Expect(res.Code).To(Equal(http.StatusInternalServerError))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"unknown error"}`))
				})
			})
		})

		Context("when provider.ListPublicKeys() succeeds", func() {
			BeforeEach(func() {
				prov.PublicKeys = map[string]string{fingerprint1: publicKey1, fingerprint2: publicKey2, fingerprint3: publicKey3}
				doRequest()
			})

			It("responds with 200 ok with public keys json", func() {
				Expect(res.Code).To(Equal(http.StatusOK))
				j, _ := json.Marshal(map[string]interface{}{
					"public_keys": prov.PublicKeys,
				})

				Expect(res.Body.String()).To(MatchJSON(string(j)))
			})
		})
	})

	Describe("POST /containers/:container_id/public_keys", func() {
		var (
			params     *url.Values
			id         = "cafebabe"
			publicKey  = "ssh-rsa Foo"
			publicKey2 = "ssh-rsa Bar"
		)

		BeforeEach(func() {
			params = &url.Values{}
			params.Set("public_key[]", publicKey)
			params.Add("public_key[]", publicKey2)
		})

		doRequest := func() {
			req, err = NewFormRequest(hostSecret, "POST", fmt.Sprintf("/containers/%v/public_keys", id), params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		Context("when params are valid", func() {
			It("calls provider.AddPublicKeys()", func() {
				Expect(prov.AddPublicKeysCalled).To(BeFalse())
				doRequest()
				Expect(prov.AddPublicKeysCalled).To(BeTrue())
				Expect(prov.ReceivedID).To(Equal(id))
				Expect(prov.ReceivedPublicKeys).To(HaveLen(2))
				Expect(prov.ReceivedPublicKeys).To(ContainElement(publicKey))
				Expect(prov.ReceivedPublicKeys).To(ContainElement(publicKey2))
			})

			Context("when provider.AddPublicKeys() fails with error", func() {
				Context("because no such container exists", func() {
					BeforeEach(func() {
						prov.AddPublicKeysError = ErrMockProviderContainerNotFound
						doRequest()
					})

					It("responds with 404 not found", func() {
						Expect(res.Code).To(Equal(http.StatusNotFound))
						Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
					})
				})

				Context("because the container does not support public key operations", func() {
					BeforeEach(func() {
						prov.AddPublicKeysError = provider.ErrPubKeyOpsNotSupported
						doRequest()
					})

					It("responds with 400 bad request", func() {
						Expect(res.Code).To(Equal(http.StatusBadRequest))
						Expect(res.Body.String()).To(MatchJSON(fmt.Sprintf(`{"error":"%s"}`, provider.ErrPubKeyOpsNotSupported.Error())))
					})
				})

				Context("because of some unknown error", func() {
					BeforeEach(func() {
						prov.AddPublicKeysError = errors.New("unknown error")
						doRequest()
					})

					It("responds with 500 internal server error with error json", func() {
						Expect(res.Code).To(Equal(http.StatusInternalServerError))
						Expect(res.Body.String()).To(MatchJSON(`{"error":"unknown error"}`))
					})
				})
			})

			Context("when provider.AddPublicKeys() succeeds", func() {
				Context("when provider.ListPublicKeys() fails with error", func() {
					Context("because no such container exists", func() {
						BeforeEach(func() {
							prov.ListPublicKeysError = ErrMockProviderContainerNotFound
							doRequest()
						})

						It("responds with 404 not found", func() {
							Expect(res.Code).To(Equal(http.StatusNotFound))
							Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
						})
					})

					Context("because of some unknown error", func() {
						BeforeEach(func() {
							prov.ListPublicKeysError = errors.New("unknown error")
							doRequest()
						})

						It("responds with 500 internal server error with error json", func() {
							Expect(res.Code).To(Equal(http.StatusInternalServerError))
							Expect(res.Body.String()).To(MatchJSON(`{"error":"unknown error"}`))
						})
					})
				})

				Context("when provider.ListPublicKeys() succeeds", func() {
					fingerprint1 := "11:11:11:11:11:11:11:11:11:11:11:11:11:11:11:11"
					fingerprint2 := "22:22:22:22:22:22:22:22:22:22:22:22:22:22:22:22"
					fingerprint3 := "33:33:33:33:33:33:33:33:33:33:33:33:33:33:33:33"
					publicKey3 := "ssh-dsa Baz"

					BeforeEach(func() {
						prov.PublicKeys = map[string]string{fingerprint1: publicKey, fingerprint2: publicKey2, fingerprint3: publicKey3}
						doRequest()
					})

					It("responds with 200 ok with public keys json", func() {
						Expect(prov.ListPublicKeysCalled).To(BeTrue())
						Expect(prov.ReceivedID).To(Equal(id))
						Expect(res.Code).To(Equal(http.StatusOK))
						j, _ := json.Marshal(map[string]interface{}{
							"public_keys": prov.PublicKeys,
						})

						Expect(res.Body.String()).To(MatchJSON(string(j)))
					})
				})
			})
		})

		Context("when public_key[] param is missing", func() {
			BeforeEach(func() {
				params.Del("public_key[]")
				doRequest()
			})

			It("responds with 422 unprocessable entity", func() {
				Expect(res.Code).To(Equal(422))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"public_key[] is missing"}`))
			})

			It("does not call provider.AddPublicKeys()", func() {
				Expect(prov.AddPublicKeysCalled).To(BeFalse())
			})
		})
	})

	Describe("DELETE /containers/:container_id/public_keys", func() {
		var (
			params *url.Values
			id     = "cafebabe"
		)

		doRequest := func() {
			req, err = NewRequestWithSecret(hostSecret, "DELETE", fmt.Sprintf("/containers/%v/public_keys", id), params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.ClearPublicKeys()", func() {
			Expect(prov.ClearPublicKeysCalled).To(BeFalse())
			doRequest()
			Expect(prov.ClearPublicKeysCalled).To(BeTrue())
			Expect(prov.ReceivedID).To(Equal(id))
		})

		Context("when provider.ClearPublicKeys() fails with error", func() {
			Context("because no such container exists", func() {
				BeforeEach(func() {
					prov.ClearPublicKeysError = ErrMockProviderContainerNotFound
					doRequest()
				})

				It("responds with 404 not found", func() {
					Expect(res.Code).To(Equal(http.StatusNotFound))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
				})
			})

			Context("because of some unknown error", func() {
				BeforeEach(func() {
					prov.ClearPublicKeysError = errors.New("unknown error")
					doRequest()
				})

				It("responds with 500 internal server error with error json", func() {
					Expect(res.Code).To(Equal(http.StatusInternalServerError))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"unknown error"}`))
				})
			})
		})
	})

	Describe("DELETE /containers/:container_id/public_keys/:key_fingerprint", func() {
		var (
			params      *url.Values
			id          = "cafebabe"
			fingerprint = "11:11:11:11:11:11:11:11:11:11:11:11:11:11:11:11"
		)

		doRequest := func() {
			req, err = NewRequestWithSecret(hostSecret, "DELETE", fmt.Sprintf("/containers/%v/public_keys/%v", id, fingerprint), params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.RemovePublicKey()", func() {
			Expect(prov.RemovePublicKeyCalled).To(BeFalse())
			doRequest()
			Expect(prov.RemovePublicKeyCalled).To(BeTrue())
			Expect(prov.ReceivedID).To(Equal(id))
			Expect(prov.ReceivedKeyFingerprint).To(Equal(fingerprint))
		})

		Context("when provider.RemovePublicKey() fails with error", func() {
			Context("because no such container exists", func() {
				BeforeEach(func() {
					prov.RemovePublicKeyError = ErrMockProviderContainerNotFound
					doRequest()
				})

				It("responds with 404 not found", func() {
					Expect(res.Code).To(Equal(http.StatusNotFound))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
				})
			})

			Context("because of some unknown error", func() {
				BeforeEach(func() {
					prov.RemovePublicKeyError = errors.New("unknown error")
					doRequest()
				})

				It("responds with 500 internal server error with error json", func() {
					Expect(res.Code).To(Equal(http.StatusInternalServerError))
					Expect(res.Body.String()).To(MatchJSON(`{"error":"unknown error"}`))
				})
			})
		})

		Context("when provider.RemovePublicKey() succeeds", func() {
			Context("when provider.ListPublicKeys() fails with error", func() {
				Context("because no such container exists", func() {
					BeforeEach(func() {
						prov.ListPublicKeysError = ErrMockProviderContainerNotFound
						doRequest()
					})

					It("responds with 404 not found", func() {
						Expect(res.Code).To(Equal(http.StatusNotFound))
						Expect(res.Body.String()).To(MatchJSON(`{"error":"no such container"}`))
					})
				})

				Context("because of some unknown error", func() {
					BeforeEach(func() {
						prov.ListPublicKeysError = errors.New("unknown error")
						doRequest()
					})

					It("responds with 500 internal server error with error json", func() {
						Expect(res.Code).To(Equal(http.StatusInternalServerError))
						Expect(res.Body.String()).To(MatchJSON(`{"error":"unknown error"}`))
					})
				})
			})

			Context("when provider.ListPublicKeys() succeeds", func() {
				publicKey := "ssh-dsa Baz"

				BeforeEach(func() {
					prov.PublicKeys = map[string]string{fingerprint: publicKey}
					doRequest()
				})

				It("responds with 200 ok with public keys json", func() {
					Expect(prov.ListPublicKeysCalled).To(BeTrue())
					Expect(prov.ReceivedID).To(Equal(id))
					Expect(res.Code).To(Equal(http.StatusOK))
					j, _ := json.Marshal(map[string]interface{}{
						"public_keys": prov.PublicKeys,
					})

					Expect(res.Body.String()).To(MatchJSON(string(j)))
				})
			})
		})
	})

	Describe("POST /volumes", func() {
		doRequest := func(slug string) {
			req, _ := NewRequestWithSecret(hostSecret, "POST", "/volumes", &url.Values{"slug": []string{slug}})
			handler.ServeHTTP(res, req)
		}

		It("is Created when provider.CreateVolumes() succeeds", func() {
			prov.CreateVolumeReturn = &provider.VolumeInfo{
				Slug: "good-slug",
				Size: 0,
			}
			doRequest("good-slug")
			Expect(prov.CreateVolumeSlug).To(Equal("good-slug"))
			Expect(res.Code).To(Equal(http.StatusCreated))
			Expect(res.Body.String()).To(MatchJSON(`{"volume": {"slug":"good-slug","size":0}}`))
		})

		Context("when provider.CreateVolumes() fails", func() {
			It("with a known error", func() {
				prov.CreateVolumeError = fmt.Errorf("volume is pi r cubed")
				doRequest("fails")
				Expect(res.Code).To(Equal(422))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"volume is pi r cubed"}`))
			})

			It("with an unexpected error", func() {
				prov.CreateVolumeError = fmt.Errorf("mkdir failed")
				doRequest("fails")
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"mkdir failed"}`))
			})
		})
	})

	Describe("GET /volumes", func() {
		doRequest := func() {
			req, _ := NewRequestWithSecret(hostSecret, "GET", "/volumes", nil)
			handler.ServeHTTP(res, req)
		}

		It("lists volumes on success", func() {
			prov.ListVolumesReturn = []string{"data", "music", "downloads"}
			doRequest()
			Expect(res.Code).To(Equal(http.StatusOK))
			Expect(res.Body.String()).To(MatchJSON(`{"volumes":["data","music","downloads"]}`))
		})

		It("returns err on failure", func() {
			prov.ListVolumesError = fmt.Errorf("glob failed")
			doRequest()
			Expect(res.Code).To(Equal(http.StatusInternalServerError))
			Expect(res.Body.String()).To(MatchJSON(`{"error":"glob failed"}`))
		})
	})

	Describe("DELETE /volumes/:slug", func() {
		doRequest := func() {
			req, _ := NewRequestWithSecret(hostSecret, "DELETE", "/volumes/some-volume", nil)
			handler.ServeHTTP(res, req)
		}

		It("returns ok on success", func() {
			doRequest()
			Expect(prov.RemoveVolumeCalled).To(BeTrue())
			Expect(res.Code).To(Equal(http.StatusOK))
		})

		It("returns error on failure", func() {
			prov.RemoveVolumeError = fmt.Errorf("remove failed")
			doRequest()
			Expect(res.Code).To(Equal(http.StatusInternalServerError))
			Expect(res.Body.String()).To(MatchJSON(`{"error":"remove failed"}`))
		})
	})

	Describe("GET /volumes/:slug", func() {
		doRequest := func() {
			req, _ := NewRequestWithSecret(hostSecret, "GET", "/volumes/eleven", nil)
			handler.ServeHTTP(res, req)
		}

		It("returns ok on success", func() {
			prov.VolumeInfoReturn = &provider.VolumeInfo{
				Slug: "eleven",
				Size: 1234567,
			}
			doRequest()
			Expect(prov.VolumeInfoCalled).To(BeTrue())
			Expect(res.Code).To(Equal(http.StatusOK))
			Expect(res.Body.String()).To(MatchJSON(`{"volume":{"slug":"eleven","size":1234567}}`))
		})

		It("returns error on failure", func() {
			prov.VolumeInfoError = fmt.Errorf("info failed")
			doRequest()
			Expect(res.Code).To(Equal(http.StatusInternalServerError))
			Expect(res.Body.String()).To(MatchJSON(`{"error":"info failed"}`))
		})
	})

	Describe("GET /containers/:id/logs", func() {
		doRequest := func(id, token, follow, timestamps, tail string) {
			params := &url.Values{}
			params.Set("follow", follow)
			params.Set("timestamps", timestamps)
			params.Set("tail", tail)
			req, err := NewRequestWithSecret(hostSecret, "GET", fmt.Sprintf("/containers/%v/logs", id), params)
			Expect(err).To(BeNil())
			if token != "" {
				req.Header.Set("Authorization", "Token "+token)
			}
			handler.ServeHTTP(res, req)
		}

		It("requires a container token", func() {
			token := ""
			doRequest("test-container", token, "false", "false", "")
			Expect(res.Code).To(Equal(http.StatusUnauthorized))
		})

		It("requires a valid container token", func() {
			prov.VerifyTokenError = errors.New("invalid token")
			token := "some token"
			doRequest("test-container", token, "false", "false", "")
			Expect(res.Code).To(Equal(http.StatusUnauthorized))
			Expect(prov.ReceivedID).To(Equal("test-container"))
			Expect(prov.ReceivedToken).To(Equal(token))
			Expect(prov.ReceivedPermissions).To(Equal([]string{"logs"}))
		})

		It("ignore invalid parameters", func() {
			testCases := []struct {
				follow     string
				timestamps string
				status     int
			}{
				{"", "", 200},
				{"invalid", "bool", 200},
			}
			for _, tc := range testCases {
				doRequest("test-container", "container-token", tc.follow, tc.timestamps, "tail-value")
				Expect(res.Code).To(Equal(tc.status), "Unexpected status %+v for %+v", res.Code, tc)
				Expect(prov.ReceivedID).To(Equal("test-container"), "Unexpected id for %+v", tc)
				Expect(prov.ReceivedToken).To(Equal("container-token"), "unexpected token for %+v", tc)
				Expect(prov.ReceivedFollow).To(BeFalse(), "Unexpected follow for %+v", tc)
				Expect(prov.ReceivedTimestamps).To(BeFalse(), "Unexpected timestamps for %+v", tc)
			}
		})

		It("accepts any value for tail", func() {
			doRequest("test-container", "container-token", "true", "false", "tail-value")
			Expect(res.Code).To(Equal(200), "Unexpected status %v", res.Code)
			Expect(prov.ReceivedTail).To(Equal("tail-value"))
		})

		It("accepts valid parameters for follow and timestamps", func() {
			testCases := []struct {
				follow             string
				timestamps         string
				expectedFollow     bool
				expectedTimestamps bool
			}{
				{"1", "1", true, true},
				{"true", "false", true, false},
			}
			for _, tc := range testCases {
				doRequest("test-container", "container-token", tc.follow, tc.timestamps, "tail-value")
				Expect(prov.ReceivedFollow).To(Equal(tc.expectedFollow), "Unexpected follow for %v", tc)
				Expect(prov.ReceivedTimestamps).To(Equal(tc.expectedTimestamps), "Unexpected timestamps for %v", tc)
			}
		})
	})

	Describe("GET /images", func() {
		doRequest := func() {
			req, err = NewRequestWithSecret(hostSecret, "GET", "/images", nil)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.ListImages()", func() {
			Expect(prov.ListImagesCalled).To(BeFalse())
			doRequest()
			Expect(prov.ListImagesCalled).To(BeTrue())
		})

		It("responds with 200 OK and images", func() {
			i1 := &provider.ImageInfo{
				ID:          "ID1",
				Name:        "Source1",
				Tag:         "SourceTag1",
				CreatedAt:   1975,
				ActualSize:  12,
				VirtualSize: 19,
			}
			i2 := &provider.ImageInfo{
				ID:          "ID2",
				Name:        "Source2",
				Tag:         "SourceTag2",
				CreatedAt:   1901,
				ActualSize:  40,
				VirtualSize: 28,
			}
			prov.ListImagesContents = []*provider.ImageInfo{i1, i2}
			doRequest()
			Expect(res.Code).To(Equal(http.StatusOK))

			j, err := json.Marshal(map[string]interface{}{
				"images": prov.ListImagesContents,
			})
			Expect(err).To(BeNil())
			Expect(res.Body.String()).To(MatchJSON(string(j)))
		})

		Context("ListImages() returns an error", func() {
			BeforeEach(func() {
				prov.ListImagesError = errors.New("Could not contact cloud.")
			})

			It("should pass through the failure", func() {
				doRequest()
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Could not contact cloud."}`))
			})
		})
	})

	Describe("GET /info", func() {
		doRequest := func(period string) {
			var params *url.Values
			if period != "" {
				params = &url.Values{"period": []string{period}}
			}
			req, err = NewRequestWithSecret(hostSecret, "GET", "/info", params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		var origFn = stats.Sample
		var newFn = func(period time.Duration) (*stats.Stats, error) {
			return &stats.Stats{
				Uptime:       30,
				MemoryUsage:  12345678,
				MemoryTotal:  17179869184,
				StorageUsage: 2000000000,
				StorageTotal: 1000000000000,
				NumCPU:       2,
				PerCPUUsage:  []float64{0.5, 30},
			}, nil
		}

		BeforeEach(func() {
			stats.Sample = newFn
		})

		AfterEach(func() {
			stats.Sample = origFn
		})

		It("responds with 200 OK and host info", func() {
			doRequest("")
			Expect(res.Code).To(Equal(http.StatusOK))
			Expect(res.Body.String()).To(MatchJSON(`{"info":{
				"uptime": 30,
				"memory_usage": 12345678,
				"memory_total": 17179869184,
				"storage_usage": 2000000000,
				"storage_total": 1000000000000,
				"num_cpu": 2,
				"per_cpu_usage": [0.5, 30]
			}}`))
		})

		It("defaults to a sample period of 100ms", func() {
			stats.Sample = func(period time.Duration) (*stats.Stats, error) {
				Expect(period).To(Equal(100 * time.Millisecond))
				return newFn(period)
			}

			doRequest("")
			Expect(res.Code).To(Equal(http.StatusOK))

			doRequest("10d") // Unparseable duration string.
			Expect(res.Code).To(Equal(http.StatusOK))
		})

		It("supports a configurable sample period", func() {
			stats.Sample = func(period time.Duration) (*stats.Stats, error) {
				Expect(period).To(Equal(2 * time.Second))
				return newFn(period)
			}

			doRequest("2s")
			Expect(res.Code).To(Equal(http.StatusOK))
		})

		Context("when stats.Sample() returns an error", func() {
			BeforeEach(func() {
				stats.Sample = func(period time.Duration) (*stats.Stats, error) {
					return nil, errors.New("sampling failed")
				}
			})

			It("responds with 500 Internal Server Error", func() {
				doRequest("")
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error": "sampling failed"}`))
			})
		})
	})

	Describe("POST /images", func() {
		doRequest := func(params map[string]string, files map[string]string) {
			req, err = NewFileRequest(hostSecret, "POST", "/images", params, files)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		Context("when build context is uploaded as a file", func() {
			It("calls provider.BuildImage() with uploaded build context", func() {
				params := map[string]string{
					"name": "image-name",
				}
				files := map[string]string{
					"file": "test file",
				}
				Expect(prov.BuildImageCalled).To(BeFalse())
				doRequest(params, files)
				Expect(prov.BuildImageCalled).To(BeTrue())
				Expect(prov.ReceivedImageName).To(Equal("image-name"))
			})
		})

		Context("when build context is given as remote URL", func() {
			It("calls provider.BuildImage() with remote URL", func() {
				params := map[string]string{
					"name":   "image-name",
					"remote": "git://github.com/foo/example",
				}
				Expect(prov.BuildImageCalled).To(BeFalse())
				doRequest(params, nil)
				Expect(prov.BuildImageCalled).To(BeTrue())
				Expect(prov.ReceivedImageName).To(Equal("image-name"))
				Expect(prov.ReceivedImageRemote).To(Equal("git://github.com/foo/example"))
			})
		})

		Context("BuildImage() returns successfully", func() {
			It("responds with 200 OK", func() {
				params := map[string]string{
					"name": "image-name",
				}
				files := map[string]string{
					"file": "test file",
				}
				prov.BuildImageOutput = "test output"
				doRequest(params, files)
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(Equal("test output"))
			})
		})

		Context("BuildImage() returns an error", func() {
			It("responds with 500 Internal Server Error", func() {
				params := map[string]string{
					"name": "image-name",
				}
				files := map[string]string{
					"file": "test file",
				}

				prov.BuildImageError = errors.New("Could not build image.")
				doRequest(params, files)
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Could not build image."}`))
			})
		})
	})

	Describe("DELETE /images", func() {
		doRequest := func() {
			params := &url.Values{}
			params.Set("image", "nitrousio/ubuntu:12.04")
			req, err = NewRequestWithSecret(hostSecret, "DELETE", "/images", params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.RemoveImage()", func() {
			Expect(prov.RemoveImageCalled).To(BeFalse())
			doRequest()
			Expect(prov.RemoveImageCalled).To(BeTrue())
			Expect(prov.ReceivedImageName).To(Equal("nitrousio/ubuntu:12.04"))
		})

		Context("RemoveImage() returns successfully", func() {
			BeforeEach(func() {
				doRequest()
			})

			It("responds with 200 OK", func() {
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(MatchJSON(`{"status":"ok"}`))
			})
		})

		Context("RemoveImage() returns an error", func() {
			BeforeEach(func() {
				prov.RemoveImageError = errors.New("Could not remove image.")
				doRequest()
			})

			It("responds with 500 Internal Server Error", func() {
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Could not remove image."}`))
			})
		})

		Context("RemoveImage() returns ErrImageInUse", func() {
			BeforeEach(func() {
				prov.RemoveImageError = provider.ErrImageInUse
				doRequest()
			})

			It("responds with 409 Conflict", func() {
				Expect(res.Code).To(Equal(http.StatusConflict))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Image is being used by one or more containers"}`))
			})
		})
	})

	Describe("POST /images/pull", func() {
		doRequest := func(params *url.Values) *httptest.ResponseRecorder {
			req, err := NewRequestWithSecret(hostSecret, "POST", "/images/pull", params)
			Expect(err).To(BeNil())
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			return res
		}

		It("calls provider.PullImage() and responds with 200 OK", func() {
			Expect(prov.PullImageCalled).To(BeFalse())
			params := &url.Values{}
			params.Set("repository", "busybox")
			res := doRequest(params)
			Expect(prov.PullImageCalled).To(BeTrue())
			Expect(prov.ReceivedRepository).To(Equal("busybox"))
			Expect(res.Code).To(Equal(http.StatusOK))
		})

		Context("PullImage() returns an error", func() {
			It("responds with 500 Internal Server Error", func() {
				prov.PullImageError = errors.New("Could not pull image.")
				params := &url.Values{}
				params.Set("repository", "busybox")
				res := doRequest(params)
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Could not pull image."}`))
			})
		})

		Context("when 'update' param is false", func() {
			Context("when image already exists", func() {
				BeforeEach(func() {
					prov.InspectImageReturn = &provider.ImageInfo{
						ID:   "cafebabe",
						Name: "busybox",
						Tag:  "latest",
					}
				})

				It("does not pull and responds with HTTP 304 Not Modified", func() {
					Expect(prov.PullImageCalled).To(BeFalse())
					params := &url.Values{}
					params.Set("repository", "busybox")
					params.Set("update", "false")
					res := doRequest(params)
					Expect(prov.PullImageCalled).To(BeFalse())
					Expect(res.Code).To(Equal(http.StatusNotModified))
				})
			})

			Context("when image does not exist", func() {
				BeforeEach(func() {
					prov.InspectImageError = errors.New("Image doesn't exist.")
				})

				It("calls provider.PullImage() and responds with 200 OK", func() {
					Expect(prov.PullImageCalled).To(BeFalse())
					params := &url.Values{}
					params.Set("repository", "busybox")
					params.Set("update", "false")
					res := doRequest(params)
					Expect(prov.PullImageCalled).To(BeTrue())
					Expect(prov.ReceivedRepository).To(Equal("busybox"))
					Expect(res.Code).To(Equal(http.StatusOK))
				})
			})
		})
	})

	Describe("POST /images/push", func() {
		doRequest := func(image, destination string) {
			params := &url.Values{}
			params.Set("image", image)
			params.Set("destination", destination)
			req, err = NewRequestWithSecret(hostSecret, "POST", "/images/push", params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.PushImage() and responds with 200 OK", func() {
			Expect(prov.PushImageCalled).To(BeFalse())
			doRequest("nitrousio/ubuntu:14.04", "quay.io/me/ubuntu")
			Expect(prov.PushImageCalled).To(BeTrue())
			Expect(prov.ReceivedImageName).To(Equal("nitrousio/ubuntu:14.04"))
			Expect(prov.ReceivedImageDestination).To(Equal("quay.io/me/ubuntu"))
			Expect(res.Code).To(Equal(http.StatusOK))
		})

		Context("PushImage() returns an error", func() {
			BeforeEach(func() {
				prov.PushImageError = errors.New("Could not push image.")
			})

			It("responds with 500 Internal Server Error", func() {
				doRequest("nitrousio/ubuntu:14.04", "quay.io/me/ubuntu")
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Could not push image."}`))
			})
		})

		It("responds with 401 Unauthorized", func() {
			prov.PushImageError = provider.ErrImagePushAuthRequired
			doRequest("nitrousio/ubuntu:14.04", "quay.io/me/ubuntu")
			Expect(res.Code).To(Equal(http.StatusUnauthorized))
			Expect(res.Body.String()).To(MatchJSON(fmt.Sprintf(`{"error":"%s"}`, provider.ErrImagePushAuthRequired.Error())))
		})
	})

	Describe("POST /images/tag", func() {
		doRequest := func(image, tag string, force bool) {
			params := &url.Values{}
			params.Set("image", image)
			params.Set("tag", tag)
			params.Set("force", strconv.FormatBool(force))
			req, err = NewRequestWithSecret(hostSecret, "POST", "/images/tag", params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		It("calls provider.TagImage()", func() {
			Expect(prov.TagImageCalled).To(BeFalse())
			doRequest("ubuntu", "quay.io/me/ubuntu:14.04", true)
			Expect(prov.TagImageCalled).To(BeTrue())
			Expect(prov.ReceivedImageName).To(Equal("ubuntu"))
			Expect(prov.ReceivedTag).To(Equal("quay.io/me/ubuntu:14.04"))
			Expect(prov.ReceivedTagForce).To(BeTrue())
		})

		Context("TagImage() returns successfully", func() {
			It("responds with 200 OK", func() {
				doRequest("ubuntu", "quay.io/me/ubuntu:14.04", true)
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(MatchJSON(`{"status":"ok"}`))
			})
		})

		Context("TagImage() returns an error", func() {
			BeforeEach(func() {
				prov.TagImageError = errors.New("Could not tag image.")
			})

			It("responds with 500 Internal Server Error", func() {
				doRequest("ubuntu", "quay.io/me/ubuntu:14.04", true)
				Expect(res.Code).To(Equal(http.StatusInternalServerError))
				Expect(res.Body.String()).To(MatchJSON(`{"error":"Could not tag image."}`))
			})
		})
	})

	Describe("GET /images/inspect", func() {
		doRequest := func(image string) *httptest.ResponseRecorder {
			params := &url.Values{}
			params.Set("image", image)
			req, err := NewRequestWithSecret(hostSecret, "GET", "/images/inspect", params)
			Expect(err).To(BeNil())

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			return res
		}

		It("responds with the image info", func() {
			expected := &provider.ImageInfo{ID: "ID1"}
			prov.InspectImageReturn = expected
			res := doRequest("ubuntu")
			Expect(res.Code).To(Equal(http.StatusOK), "failed to inspect image")

			returned := map[string]*provider.ImageInfo{}
			err := json.NewDecoder(res.Body).Decode(&returned)
			Expect(err).To(BeNil(), "failed to decode response as valid image info")
			Expect(returned["image"]).To(Equal(expected), "failed to encode valid response")
		})

		It("responds with 422 Status when the image is empty", func() {
			res := doRequest("")
			Expect(res.Code).To(Equal(422))
			Expect(res.Body.String()).To(MatchJSON(`{"error":"image is not valid"}`))
		})

		It("responds with 500 Internal Server Error when we failed to inspect the image", func() {
			prov.InspectImageError = errors.New("could not find image")
			res := doRequest("ubuntu")
			Expect(res.Code).To(Equal(http.StatusInternalServerError))
			Expect(res.Body.String()).To(MatchJSON(`{"error":"could not find image"}`))
		})
	})

	Describe("GET /containers/:id/attach/ws", func() {
		doRequest := func(containerID, token string) (*websocket.Conn, error) {
			ts := httptest.NewServer(handler)
			defer ts.Close()
			params := &url.Values{}
			params.Set("token", token)
			wsURL, err := url.Parse(fmt.Sprintf("ws://%s/containers/%s/attach/ws", ts.Listener.Addr().String(), containerID))
			if err != nil {
				return nil, err
			}
			wsURL.RawQuery = params.Encode()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), http.Header{})
			return conn, err
		}

		It("requires a container token", func() {
			token := ""
			_, err := doRequest("test-container", token)
			Expect(err).To(MatchError(websocket.ErrBadHandshake))
		})

		It("requires a valid container token", func() {
			prov.VerifyTokenError = errors.New("invalid token")
			token := "some token"
			_, err := doRequest("test-container", token)
			Expect(err).To(MatchError(websocket.ErrBadHandshake))
			Expect(prov.ReceivedID).To(Equal("test-container"))
			Expect(prov.ReceivedToken).To(Equal(token))
			Expect(prov.ReceivedPermissions).To(Equal([]string{"attach"}))
		})

		Context("for invalid containers", func() {
			testCases := []struct {
				id        string
				container *provider.ContainerInfo
			}{
				{"empty", nil},
				{"missing", &provider.ContainerInfo{ID: "aaa", State: provider.ContainerStateRunning}},
				{"stopped", &provider.ContainerInfo{ID: "stopped", State: provider.ContainerStateStopped}},
			}

			for _, tc := range testCases {
				local := tc
				Context(fmt.Sprintf("Attach to container %v", local.id), func() {
					BeforeEach(func() {
						if local.container != nil {
							prov.Containers = []*provider.ContainerInfo{local.container}
						}
					})

					It("returns an error", func() {
						_, err := doRequest(local.id, "valid-token")
						Expect(err).To(MatchError(websocket.ErrBadHandshake))
					})
				})
			}
		})

		Context("for valid containers", func() {
			It("calls provider's attach function and gets its output", func() {
				prov.Containers = []*provider.ContainerInfo{{ID: "container-id", State: provider.ContainerStateRunning}}
				prov.WriteToStdout = "attach output"

				conn, err := doRequest("container-id", "container-token")
				Expect(err).To(BeNil())
				defer conn.Close()

				Expect(prov.AttachToContainerCalled).To(BeTrue())
				Expect(prov.ReceivedID).To(Equal("container-id"))
				Expect(prov.ReceivedToken).To(Equal("container-token"))

				messageType, msg, err := conn.ReadMessage()
				Expect(err).To(BeNil())
				Expect(messageType).To(Equal(websocket.TextMessage))
				Expect(string(msg)).To(Equal("attach output"))
			})

			It("writes the error to WebSocket when there is an error", func() {
				prov.Containers = []*provider.ContainerInfo{{ID: "container-id", State: provider.ContainerStateRunning}}
				prov.AttachToContainerError = errors.New("some attach error")

				conn, err := doRequest("container-id", "container-token")
				Expect(err).To(BeNil())
				defer conn.Close()

				Expect(prov.AttachToContainerCalled).To(BeTrue())

				// Read from WebSocket until we get a non-empty text message.
				for {
					messageType, msg, err := conn.ReadMessage()
					Expect(err).To(BeNil())
					if messageType != websocket.TextMessage || string(msg) == "" {
						continue
					}
					Expect(string(msg)).To(Equal("some attach error"))
					break
				}
			})
		})
	})

	itHandlesCORSPreflight := func(url string) {
		It("handles CORS preflight", func() {
			req, err := NewRequest("OPTIONS", url, nil)
			Expect(err).To(BeNil())
			req.Header.Set("Access-Control-Request-Headers", "Foo")
			req.Header.Add("Access-Control-Request-Headers", "Bar")
			handler.ServeHTTP(res, req)
			Expect(res.Code).To(Equal(http.StatusOK))
			Expect(res.Header()["Access-Control-Allow-Origin"]).To(ContainElement("*"))
			Expect(res.Header()["Access-Control-Allow-Methods"]).To(ContainElement("*"))
			Expect(res.Header()["Access-Control-Allow-Headers"]).To(ContainElement("Foo, Bar"))
			Expect(res.Header()["Access-Control-Allow-Credentials"]).To(ContainElement("true"))
		})
	}

	Describe("OPTIONS /containers/:id/shell", func() {
		itHandlesCORSPreflight("/containers/1/shell")
	})

	Describe("GET /containers/:id/shell", func() {
		doRequest := func(containerID, command, token string) (*websocket.Conn, error) {
			ts := httptest.NewServer(handler)
			defer ts.Close()
			params := url.Values{}
			params.Set("command", command)
			params.Set("token", token)
			wsURL, err := url.Parse(fmt.Sprintf("ws://%s/containers/%s/shell", ts.Listener.Addr().String(), containerID))
			if err != nil {
				return nil, err
			}
			wsURL.RawQuery = params.Encode()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), http.Header{})
			return conn, err
		}

		It("requires a valid container token", func() {
			_, err := doRequest("test-container", "whoami", "")
			Expect(err).To(Equal(websocket.ErrBadHandshake))

			prov.VerifyTokenError = errors.New("invalid token")
			token := "some token"
			_, err = doRequest("test-container", "whoami", token)
			Expect(err).To(Equal(websocket.ErrBadHandshake))
			Expect(prov.ReceivedToken).To(Equal(token))
			Expect(prov.ReceivedPermissions).To(Equal([]string{"shell"}))
		})
	})

	Describe("Unauthorized requests", func() {
		It("requires an authorization header", func() {
			req, err := NewRequestWithSecret(hostSecret, "GET", "/containers", nil)
			Expect(err).To(BeNil())
			req.Header.Del("X-Freighter-Secret")
			handler.ServeHTTP(res, req)

			Expect(res.Code).To(Equal(http.StatusUnauthorized))
			Expect(res.Body.String()).To(ContainSubstring("missing_secret"))
		})

		It("requires valid token in authorization header", func() {
			req, err := NewRequestWithSecret(hostSecret, "GET", "/containers", nil)
			Expect(err).To(BeNil())
			req.Header.Set("X-Freighter-Secret", "secretion")
			handler.ServeHTTP(res, req)
			Expect(res.Code).To(Equal(http.StatusUnauthorized))
			Expect(res.Body.String()).To(ContainSubstring("invalid_secret"))
		})
	})
})
