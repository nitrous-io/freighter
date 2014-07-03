package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gorilla/mux"
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

var _ = Describe("server", func() {
	var (
		handler *mux.Router
		res     *httptest.ResponseRecorder
		req     *http.Request
		err     error
		svr     *server.Server
		prov    *MockProvider
		cb      *MockCallback
	)

	BeforeEach(func() {
		prov = &MockProvider{}
		cb = &MockCallback{}
		logger := &util.Logger{Level: util.LogLevelQuiet}
		svr = server.NewServer(logger, prov, cb)
		handler = svr.Router()
		res = httptest.NewRecorder()
	})

	Describe("GET /", func() {
		BeforeEach(func() {
			req, err = NewRequest("GET", "/", nil)
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
			req, err = NewRequest("GET", "/containers", nil)
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
					{ID: "aaa", Name: "foo", Image: "ubuntu", Host: "1.2.3.4", State: provider.ContainerStateRunning},
					{ID: "bbb", Name: "bar", Image: "centos", Host: "1.2.3.5", State: provider.ContainerStateStopped},
				}
				doRequest()
			})

			It("responds with 200 ok with container info array", func() {
				Expect(res.Code).To(Equal(http.StatusOK))
				Expect(res.Body.String()).To(MatchJSON(`{"containers":[
				  {"id":"aaa","name":"foo","image":"ubuntu","host":"1.2.3.4","state":"running"},
					{"id":"bbb","name":"bar","image":"centos","host":"1.2.3.5","state":"stopped"}
				]}`))
			})
		})
	})

	Describe("POST /containers", func() {
		var (
			params      *url.Values
			name        string
			image       string
			publicKey   string
			publicKey2  string
			callbackURL string
			state       string
		)

		BeforeEach(func() {
			name = "fragrant-wind-69"
			image = "ubuntu"
			publicKey = "ssh-rsa Foo"
			publicKey2 = "ssh-rsa Bar"
			callbackURL = "http://www.example.com/cb"
			params = &url.Values{}
			params.Set("name", name)
			params.Set("image", image)
			params.Set("callback_url", callbackURL)
			params.Set("public_key[]", publicKey)
			params.Add("public_key[]", publicKey2)
			state = RandomName("state")
			params.Set("state", state)
		})

		doRequest := func() {
			req, err = NewFormRequest("POST", "/containers", params)
			Expect(err).To(BeNil())
			handler.ServeHTTP(res, req)
		}

		Context("when params are valid", func() {
			It("responds with 202 accepted immediately", func() {
				doRequest()
				Expect(res.Code).To(Equal(http.StatusAccepted))
				Expect(res.Body.String()).To(MatchJSON(`{"status":"accepted"}`))
			})

			It("calls provider.Create() asynchronously", func() {
				doRequest()
				Expect(prov.CreateCalled).To(BeFalse())
				Wait()
				Expect(prov.CreateCalled).To(BeTrue())
				Expect(prov.ReceivedName).To(Equal(name))
				Expect(prov.ReceivedImage).To(Equal(image))
			})

			Context("when provider.Create()", func() {
				Context("returns an error", func() {
					BeforeEach(func() {
						prov.CreateError = errors.New("pc load letter")
						doRequest()
					})

					It("calls callback with error", func() {
						Expect(cb.InvokeCalled).To(BeFalse())
						Wait()
						Expect(cb.InvokeCalled).To(BeTrue())
						Expect(cb.To).To(Equal(callbackURL))
						Expect(cb.Payload).To(MatchJSON(`{"error":"pc load letter", "state": "` + state + `"}`))
					})
				})

				Context("returns a container info with no error", func() {
					BeforeEach(func() {
						prov.CreateContainerInfo = &provider.ContainerInfo{ID: "aaa", Name: name, Image: "ubuntu", Host: "1.2.3.4", State: provider.ContainerStateRunning}
						doRequest()
					})

					It("calls callback with container info", func() {
						Expect(cb.InvokeCalled).To(BeFalse())
						Wait()
						Expect(cb.InvokeCalled).To(BeTrue())
						Expect(cb.To).To(Equal(callbackURL))
						Expect(cb.Payload).To(MatchJSON(`{"result":"created", "container":{
							"id":"aaa","name":"fragrant-wind-69","image":"ubuntu","host":"1.2.3.4","state":"running"
						}, "state": "` + state + `"}`))
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
						Wait()
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
				Wait()
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
				Wait()
				Expect(prov.CreateCalled).To(BeFalse())
			})
		})
	})

	Describe("GET /containers/:container_id", func() {
		BeforeEach(func() {
			prov.Containers = []*provider.ContainerInfo{
				{ID: "aaa", Name: "foo", Image: "ubuntu", Host: "1.2.3.4", State: provider.ContainerStateRunning},
			}
		})

		doRequest := func(id string) {
			req, err = NewRequest("GET", fmt.Sprintf("/containers/%v", id), nil)
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
					"id":"aaa","name":"foo","image":"ubuntu","host":"1.2.3.4","state":"running"
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

	Describe("Container Control", func() {
		var (
			provFuncCalled   *bool
			provFuncError    *error
			expectedResponse string
			doRequest        func(string)
			id               = "aaa"
			name             = "foobar"
			image            = "ubuntu"
			host             = "1.2.3.4"
			callbackURL      = "http://www.example.com/cb"
			state            string
			params           *url.Values
		)

		BeforeEach(func() {
			prov.Containers = []*provider.ContainerInfo{
				{ID: id, Name: name, Image: image, Host: host},
			}
			params = &url.Values{}
			params.Set("callback_url", callbackURL)
			state = RandomName("state")
			params.Set("state", state)
		})

		itWorks := func() {
			Context("when container by the given id exists", func() {
				It("responds with 202 accepted immediately", func() {
					doRequest(id)
					Expect(res.Code).To(Equal(http.StatusAccepted))
					Expect(res.Body.String()).To(MatchJSON(`{"status":"accepted"}`))
				})

				It("calls provider func asynchronously", func() {
					doRequest(id)
					Expect(*provFuncCalled).To(BeFalse())
					Wait()
					Expect(*provFuncCalled).To(BeTrue())
					Expect(prov.ReceivedID).To(Equal(id))
				})

				Context("when provider func", func() {
					Context("returns an error", func() {
						BeforeEach(func() {
							*provFuncError = errors.New("pc load letter")
							doRequest(id)
						})

						It("calls callback with error", func() {
							Expect(cb.InvokeCalled).To(BeFalse())
							Wait()
							Expect(cb.InvokeCalled).To(BeTrue())
							Expect(cb.To).To(Equal(callbackURL))
							Expect(cb.Payload).To(MatchJSON(`{"error":"pc load letter", "state":"` + state + `"}`))
						})
					})

					Context("returns a container info with no error", func() {
						BeforeEach(func() {
							doRequest(id)
						})

						It("calls callback with no error", func() {
							Expect(cb.InvokeCalled).To(BeFalse())
							Wait()
							Expect(cb.InvokeCalled).To(BeTrue())
							Expect(cb.To).To(Equal(callbackURL))
							Expect(cb.Payload).To(MatchJSON(expectedResponse))
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
						"host": "",
						"state": ""
					},
					"state": "` + state + `"
				}`

				doRequest = func(id string) {
					req, err = NewRequest("DELETE", fmt.Sprintf("/containers/%v", id), params)
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
						"host": "` + host + `",
						"state": "running"
					},
					"state": "` + state + `"
				}`

				doRequest = func(id string) {
					req, err = NewFormRequest("POST", fmt.Sprintf("/containers/%v/start", id), params)
					Expect(err).To(BeNil())
					handler.ServeHTTP(res, req)
				}
			})

			itWorks()
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
						"host": "",
						"state": "stopped"
					},
					"state": "` + state + `"
				}`

				doRequest = func(id string) {
					req, err = NewFormRequest("POST", fmt.Sprintf("/containers/%v/stop", id), params)
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
						"host": "` + host + `",
						"state": "running"
					},
					"state": "` + state + `"
				}`

				doRequest = func(id string) {
					req, err = NewFormRequest("POST", fmt.Sprintf("/containers/%v/restart", id), params)
					Expect(err).To(BeNil())
					handler.ServeHTTP(res, req)
				}
			})

			itWorks()
		})
	})
})
