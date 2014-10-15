package freighterclient_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/nitrous-io/freighter/freighterclient"
	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/test"
	"github.com/onsi/gomega/ghttp"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type ServerTest struct {
	client    freighterclient.Client
	host      *freighterclient.Host
	server    *ghttp.Server
	handlerFn http.HandlerFunc
	tp        *http.Transport
}

func NewServerTest(handlerFn http.HandlerFunc) (*ServerTest, error) {
	st := &ServerTest{handlerFn: handlerFn}
	return st.Setup()
}

func NewServerTestTransport(fn http.HandlerFunc, tp *http.Transport) (*ServerTest, error) {
	st := &ServerTest{handlerFn: fn, tp: tp}
	return st.Setup()
}

func (st *ServerTest) Setup() (*ServerTest, error) {
	st.server = ghttp.NewTLSServer()
	st.server.AppendHandlers(st.handlerFn)

	ip, port, err := ipAndPort(st.server)
	if err != nil {
		return nil, err
	}

	host := freighterclient.NewHost(ip, port, "example.com", test.GoLocalhostCert)
	client, err := freighterclient.NewClientTransport(host, "mysecret", st.tp)

	if err != nil {
		return nil, err
	}

	st.host = host
	st.client = client
	return st, nil
}

func (st *ServerTest) Close() {
	if st.server.HTTPTestServer != nil {
		st.server.Close()
	}
}

func ipAndPort(s *ghttp.Server) (string, int, error) {
	serverAddr := s.HTTPTestServer.Listener.Addr().String()
	ip, portStr, err := net.SplitHostPort(serverAddr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	return ip, port, nil
}

var _ = Describe("client", func() {
	var (
		st           *ServerTest
		cl           freighterclient.Client
		host         *freighterclient.Host
		server       *ghttp.Server
		statusCode   int
		responseBody string
	)

	It("includes secret in the requests", func() {
		handlers := ghttp.CombineHandlers(
			ghttp.VerifyHeaderKV("X-Freighter-Secret", "mysecret"),
			func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"containers":[]}`))
			},
		)
		st, _ := NewServerTest(handlers)
		defer st.Close()

		containers, err := st.client.List()
		Expect(containers).To(BeEmpty())
		Expect(err).To(BeNil())
	})

	Describe("NewClientHTTP", func() {
		It("times out a long request", func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(1 * time.Millisecond)
					w.Write([]byte(`{"status": "toolong"}`))
				},
			)
			tp := &http.Transport{
				Dial: (&net.Dialer{
					Timeout: 1 * time.Nanosecond,
				}).Dial,
			}
			st, _ := NewServerTestTransport(handlers, tp)
			defer st.Close()

			_, err := st.client.List()
			_, ok := err.(*freighterclient.RequestFailedError)
			Expect(ok).To(BeTrue(), "should receive RequestFailedError")
		})
	})

	Describe("List()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/containers"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when the response is HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
			})

			Context("when the response has containers", func() {
				BeforeEach(func() {
					responseBody = `{
						"containers": [
							{
								"id":"cafebabe",
								"name":"yosemite",
								"image":"yosemite",
								"image_id":"abc123",
								"host":"172.17.0.1",
								"ports": {"22": ["49100"]},
								"state":"running",
								"cmd": ["/sbin/init"],
								"entrypoint": null,
								"exit_code": 0,
								"started_at": "2014-08-13T00:00:00Z",
								"finished_at": "2014-08-10T00:00:00Z"
							},
							{
								"id":"deadbeef",
								"name":"mavericks",
								"image":"mavericks",
								"image_id":"def456",
								"host":"172.17.0.2",
								"ports": {"22": ["49101"]},
								"state":"running",
								"cmd": ["/sbin/init"],
								"entrypoint": null,
								"exit_code": 0,
								"started_at": "2014-08-13T00:00:00Z",
								"finished_at": "2014-08-10T00:00:00Z"
							}
						]
					}`
				})

				It("returns a slice of Containers", func() {
					containers, err := cl.List()
					Expect(err).To(BeNil())
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(containers).To(HaveLen(2))

					Expect(containers[0]).To(Equal(&freighterclient.Container{
						ID:         "cafebabe",
						Name:       "yosemite",
						Image:      "yosemite",
						ImageID:    "abc123",
						Host:       "172.17.0.1",
						Ports:      map[string][]string{"22": []string{"49100"}},
						State:      "running",
						Cmd:        []string{"/sbin/init"},
						Entrypoint: nil,
						ExitCode:   0,
						StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
						FinishedAt: time.Date(2014, time.August, 10, 0, 0, 0, 0, time.UTC),
					}))

					Expect(containers[1]).To(Equal(&freighterclient.Container{
						ID:         "deadbeef",
						Name:       "mavericks",
						Image:      "mavericks",
						ImageID:    "def456",
						Host:       "172.17.0.2",
						Ports:      map[string][]string{"22": []string{"49101"}},
						State:      "running",
						Cmd:        []string{"/sbin/init"},
						Entrypoint: nil,
						ExitCode:   0,
						StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
						FinishedAt: time.Date(2014, time.August, 10, 0, 0, 0, 0, time.UTC),
					}))
				})
			})

			Context("when the response contains no containers", func() {
				BeforeEach(func() {
					responseBody = `{"containers": []}`
				})

				It("returns an empty slice", func() {
					containers, err := cl.List()
					Expect(err).To(BeNil())
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(containers).To(HaveLen(0))
				})
			})

			Context("when the response is empty", func() {
				BeforeEach(func() {
					responseBody = ``
				})

				It("returns ErrUnexpectedResponse", func() {
					_, err := cl.List()
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
				})
			})
		})

		Context("when the response is not HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				containers, err := cl.List()
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(containers).To(BeNil())
			})
		})

		Context("when the request fails", func() {
			BeforeEach(func() {
				// Close server to simulate a failed request.
				server.Close()
			})

			It("returns a RequestFailedError", func() {
				containers, err := cl.List()
				_, ok := err.(*freighterclient.RequestFailedError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(0))
				Expect(containers).To(BeNil())
			})
		})

	})

	Describe("Create()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					req.ParseForm()
				},
				ghttp.VerifyRequest("POST", "/containers"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when the response is HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
			})

			Context("when the response has a container", func() {
				BeforeEach(func() {
					responseBody = `{
						"container": {
							"id":"cafebabe",
							"name":"yosemite",
							"image":"ubuntu",
							"image_id":"abc123",
							"host":"172.17.0.1",
							"gateway":"172.17.0.0",
							"ports": {"22": ["49100"]},
							"state":"running",
							"cmd": ["/sbin/init"],
							"entrypoint": null,
							"exit_code": 0,
							"started_at": "2014-08-13T00:00:00Z",
							"finished_at": "2014-08-10T00:00:00Z"
						}
					}`
				})

				It("returns the Container", func() {
					config := &freighterclient.ContainerConfig{}
					config.Env = []string{"LANG=C"}
					container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", config)
					Expect(err).To(BeNil())
					Expect(container).NotTo(BeNil())

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))

					Expect(receivedRequests[0].PostFormValue("name")).To(Equal("yosemite"))
					Expect(receivedRequests[0].PostFormValue("image")).To(Equal("ubuntu"))
					Expect(receivedRequests[0].PostFormValue("secret")).To(Equal("s3cr3t"))
					Expect(receivedRequests[0].PostFormValue("config")).To(MatchJSON(`{"Env":["LANG=C"]}`))

					Expect(container).To(Equal(&freighterclient.Container{
						ID:         "cafebabe",
						Name:       "yosemite",
						Image:      "ubuntu",
						ImageID:    "abc123",
						Host:       "172.17.0.1",
						Gateway:    "172.17.0.0",
						Ports:      map[string][]string{"22": []string{"49100"}},
						State:      "running",
						Cmd:        []string{"/sbin/init"},
						Entrypoint: nil,
						ExitCode:   0,
						StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
						FinishedAt: time.Date(2014, time.August, 10, 0, 0, 0, 0, time.UTC),
					}))
				})
			})

			Context("when the response has a result of start_failed", func() {
				BeforeEach(func() {
					responseBody = `{
						"status": "start_failed",
						"error": "start failed: could not bind port 22",
						"container": {
							"id":"cafebabe",
							"name":"yosemite",
							"image":"ubuntu",
							"image_id":"abc123",
							"host":"",
							"gateway":"",
							"ports": null,
							"state":"stopped",
							"cmd": ["/sbin/init"],
							"entrypoint": null,
							"exit_code": 0,
							"started_at": "2014-08-13T00:00:00Z"
						}
					}`
				})

				It("returns the Container and a StartFailedError", func() {
					config := &freighterclient.ContainerConfig{}
					config.Env = []string{"LANG=C"}
					container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", config)
					Expect(err).NotTo(BeNil())
					e, ok := err.(*freighterclient.StartFailedError)
					Expect(ok).To(BeTrue())
					Expect(e.Error()).To(ContainSubstring("start failed: could not bind port 22"))
					Expect(container).NotTo(BeNil())

					reqs := server.ReceivedRequests()
					Expect(reqs).To(HaveLen(1))

					Expect(reqs[0].PostFormValue("name")).To(Equal("yosemite"))
					Expect(reqs[0].PostFormValue("image")).To(Equal("ubuntu"))
					Expect(reqs[0].PostFormValue("secret")).To(Equal("s3cr3t"))
					Expect(reqs[0].PostFormValue("config")).To(MatchJSON(`{"Env":["LANG=C"]}`))

					Expect(container).To(Equal(&freighterclient.Container{
						ID:         "cafebabe",
						Name:       "yosemite",
						Image:      "ubuntu",
						ImageID:    "abc123",
						Host:       "",
						Gateway:    "",
						Ports:      nil,
						State:      "stopped",
						Cmd:        []string{"/sbin/init"},
						Entrypoint: nil,
						ExitCode:   0,
						StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
					}))
				})
			})

			Context("when the response is empty", func() {
				BeforeEach(func() {
					responseBody = ``
				})

				It("returns ErrUnexpectedResponse", func() {
					container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", nil)
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})
		})

		Context("when the response is HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
			})

			It("returns ErrImageNotFound", func() {
				container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", nil)
				Expect(err).To(Equal(freighterclient.ErrImageNotFound))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})

		Context("when the response is HTTP 422 Unprocessable Entity", func() {
			BeforeEach(func() {
				statusCode = freighterclient.StatusUnprocessableEntity
			})

			Context("when the response body is JSON with an 'error' key", func() {
				BeforeEach(func() {
					responseBody = `{ "error": "invalid name" }`
				})

				It("returns an InvalidParamError", func() {
					container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", nil)
					e, ok := err.(*freighterclient.InvalidParamError)
					Expect(ok).To(BeTrue())
					Expect(e.Msg).To(Equal("invalid name"))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})

			Context("when the response body is not JSON", func() {
				BeforeEach(func() {
					responseBody = `error: "not really JSON"`
				})

				It("returns ErrUnexpectedResponse", func() {
					container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", nil)
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})
		})

		Context("when the response has an unexpected status code", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				container, err := cl.Create("yosemite", "ubuntu", "s3cr3t", nil)
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("Commit()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					req.ParseForm()
				},
				ghttp.VerifyRequest("POST", "/containers/mycon/commit"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("successful response", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `{
					"image": {
						"id": "12",
						"name": "trusty-arm",
						"tag": "latest",
						"created_at": 1902,
						"actual_size": 12,
						"virtual_size": 39
					}
				}`
			})

			It("returns the image", func() {
				image, err := cl.Commit("mycon", "trusty-arm")
				Expect(err).To(BeNil())
				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].PostFormValue("name")).To(Equal("trusty-arm"))
				Expect(image).To(Equal(&freighterclient.Image{
					ID:          "12",
					Name:        "trusty-arm",
					Tag:         "latest",
					CreatedAt:   1902,
					ActualSize:  12,
					VirtualSize: 39,
				}))
			})
		})

		Context("unsuccessful response", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				container, err := cl.Commit("mycon", "trusty-arm")
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("Config()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/containers/deadbeef/config"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		It("returns container and image configs when response is HTTP 200 OK", func() {
			statusCode = http.StatusOK
			responseBody = `{
				"config": {
					"Privileged": false,
					"Cmd": ["/bin/yes"],
					"Env": ["NITROUS_IMAGE_VERSION=1.2.3", "PAGER=less"],
					"Mounts": ["/var/lib/freighter/containers/testcon/ssh:/nitrous/ssh:ro", "music"],
					"PortBindings": ["4040:5050", "4000:3000"],
					"PublishAll": true
				}
			}`
			config, err := cl.Config("deadbeef")
			Expect(err).To(BeNil())
			Expect(server.ReceivedRequests()).To(HaveLen(1))
			Expect(config).To(Equal(&freighterclient.ContainerConfig{
				Privileged:   false,
				Cmd:          []string{"/bin/yes"},
				Env:          []string{"NITROUS_IMAGE_VERSION=1.2.3", "PAGER=less"},
				PortBindings: []string{"4040:5050", "4000:3000"},
				Mounts:       []string{"/var/lib/freighter/containers/testcon/ssh:/nitrous/ssh:ro", "music"},
				PublishAll:   true,
			}))
		})

		It("returns ErrContainerNotFound when the response is HTTP 404 Not Found", func() {
			statusCode = http.StatusNotFound
			config, err := cl.Config("deadbeef")
			Expect(err).To(Equal(freighterclient.ErrContainerNotFound))
			Expect(server.ReceivedRequests()).To(HaveLen(1))
			Expect(config).To(BeNil())
		})
	})

	Describe("Info()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/containers/deadbeef"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when the response is HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
			})

			Context("when the response has a container", func() {
				BeforeEach(func() {
					responseBody = `{
						"container": {
							"id":"deadbeef",
							"name":"mavericks",
							"image":"ubuntu-upstart",
							"image_id":"abc123",
							"host":"172.17.0.2",
							"gateway":"172.17.0.0",
							"ports": {"22": ["49100"]},
							"state":"running",
							"cmd": ["/sbin/init"],
							"entrypoint": null,
							"exit_code": 0,
							"started_at": "2014-08-13T00:00:00Z",
							"finished_at": "2014-08-10T00:00:00Z"
						}
					}`
				})

				It("returns the Container", func() {
					container, err := cl.Info("deadbeef")
					Expect(err).To(BeNil())
					Expect(container).NotTo(BeNil())
					Expect(server.ReceivedRequests()).To(HaveLen(1))

					Expect(container).To(Equal(&freighterclient.Container{
						ID:         "deadbeef",
						Name:       "mavericks",
						Image:      "ubuntu-upstart",
						ImageID:    "abc123",
						Host:       "172.17.0.2",
						Gateway:    "172.17.0.0",
						Ports:      map[string][]string{"22": []string{"49100"}},
						State:      "running",
						Cmd:        []string{"/sbin/init"},
						Entrypoint: nil,
						ExitCode:   0,
						StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
						FinishedAt: time.Date(2014, time.August, 10, 0, 0, 0, 0, time.UTC),
					}))
				})
			})

			Context("when the response is empty", func() {
				BeforeEach(func() {
					responseBody = ``
				})

				It("returns ErrUnexpectedResponse", func() {
					container, err := cl.Info("deadbeef")
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})
		})

		Context("when the response is HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
			})

			It("returns ErrContainerNotFound", func() {
				container, err := cl.Info("deadbeef")
				Expect(err).To(Equal(freighterclient.ErrContainerNotFound))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})

		Context("when the response is not HTTP 200 OK nor HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				container, err := cl.Info("deadbeef")
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("Start()", func() {
		BeforeEach(func() {
			var err error
			st, err = NewServerTest(ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					req.ParseForm()
				},
				ghttp.VerifyRequest("POST", "/containers/deadbeef/start"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			))
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
			statusCode = http.StatusOK
			responseBody = `{
				"container": {
					"id": "deadbeef",
					"name":"mavericks",
					"image":"ubuntu-upstart",
					"image_id":"abc123",
					"host":"172.17.0.2",
					"gateway":"172.17.0.0",
						"ports": {"22": ["49100"]},
					"state":"running",
					"cmd": ["/sbin/init"],
					"entrypoint": null,
					"exit_code": 0,
					"started_at": "2014-08-13T00:00:00Z",
					"finished_at": "2014-08-10T00:00:00Z"
				}
			}`
		})

		AfterEach(func() {
			st.Close()
		})

		It("returns the container", func() {
			config := &freighterclient.ContainerConfig{}
			config.Env = []string{"LANG=C"}
			container, err := cl.Start("deadbeef", config)
			Expect(err).To(BeNil())
			Expect(container).NotTo(BeNil())

			Expect(container).To(Equal(&freighterclient.Container{
				ID:         "deadbeef",
				Name:       "mavericks",
				Image:      "ubuntu-upstart",
				ImageID:    "abc123",
				Host:       "172.17.0.2",
				Gateway:    "172.17.0.0",
				Ports:      map[string][]string{"22": []string{"49100"}},
				State:      "running",
				Cmd:        []string{"/sbin/init"},
				Entrypoint: nil,
				ExitCode:   0,
				StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
				FinishedAt: time.Date(2014, time.August, 10, 0, 0, 0, 0, time.UTC),
			}))

			reqs := server.ReceivedRequests()
			Expect(reqs).To(HaveLen(1))
			Expect(reqs[0].PostFormValue("config")).To(MatchJSON(`{"Env":["LANG=C"]}`))
		})

		Context("when the response is empty", func() {
			BeforeEach(func() {
				responseBody = ``
			})

			It("returns ErrUnexpectedResponse", func() {
				container, err := cl.Start("deadbeef", nil)
				Expect(err).To(MatchError(freighterclient.ErrUnexpectedResponse))
				Expect(container).To(BeNil())
			})
		})

		Context("when the response is HTTP 404 Not Found", func() {
			BeforeEach(func() {
				responseBody = ``
				statusCode = http.StatusNotFound
			})

			It("returns ErrContainerNotFound", func() {
				container, err := cl.Start("deadbeef", nil)
				Expect(err).To(MatchError(freighterclient.ErrContainerNotFound))
				Expect(container).To(BeNil())
			})
		})

		Context("when the response is not HTTP 200 OK nor HTTP 404 Not Found", func() {
			BeforeEach(func() {
				responseBody = ``
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				container, err := cl.Start("deadbeef", nil)
				Expect(err).To(MatchError(&freighterclient.StatusError{
					Code: http.StatusInternalServerError,
					Body: "",
				}))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("container control actions", func() {
		var (
			fnUnderTest func(id string) (*freighterclient.Container, error)
			path        string

			id = "facecafe"
		)

		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		itWorks := func() {
			It("makes an HTTP POST request", func() {
				fnUnderTest(id)

				Expect(server.ReceivedRequests()).To(HaveLen(1))
				req := server.ReceivedRequests()[0]
				Expect(req.Method).To(Equal("POST"))
				Expect(req.URL.Path).To(Equal(path))
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				Context("when the response has a container", func() {
					BeforeEach(func() {
						responseBody = `{
						"container": {
							"id":"` + id + `",
							"name":"mavericks",
							"image":"ubuntu-upstart",
							"image_id":"abc123",
							"host":"172.17.0.2",
							"gateway":"172.17.0.0",
							"ports": {"22": ["49100"]},
							"state":"running",
							"cmd": ["/sbin/init"],
							"entrypoint": null,
							"exit_code": 0,
							"started_at": "2014-08-13T00:00:00Z",
							"finished_at": "2014-08-10T00:00:00Z"
						}
					}`
					})

					It("returns the Container", func() {
						container, err := fnUnderTest(id)
						Expect(err).To(BeNil())
						Expect(container).NotTo(BeNil())

						Expect(container).To(Equal(&freighterclient.Container{
							ID:         id,
							Name:       "mavericks",
							Image:      "ubuntu-upstart",
							ImageID:    "abc123",
							Host:       "172.17.0.2",
							Gateway:    "172.17.0.0",
							Ports:      map[string][]string{"22": []string{"49100"}},
							State:      "running",
							Cmd:        []string{"/sbin/init"},
							Entrypoint: nil,
							ExitCode:   0,
							StartedAt:  time.Date(2014, time.August, 13, 0, 0, 0, 0, time.UTC),
							FinishedAt: time.Date(2014, time.August, 10, 0, 0, 0, 0, time.UTC),
						}))
					})
				})

				Context("when the response is empty", func() {
					BeforeEach(func() {
						responseBody = ``
					})

					It("returns ErrUnexpectedResponse", func() {
						container, err := fnUnderTest(id)
						Expect(err).To(MatchError(freighterclient.ErrUnexpectedResponse))
						Expect(container).To(BeNil())
					})
				})
			})

			Context("when the response is HTTP 404 Not Found", func() {
				BeforeEach(func() {
					statusCode = http.StatusNotFound
				})

				It("returns ErrContainerNotFound", func() {
					container, err := fnUnderTest(id)
					Expect(err).To(MatchError(freighterclient.ErrContainerNotFound))
					Expect(container).To(BeNil())
				})
			})

			Context("when the response is not HTTP 200 OK nor HTTP 404 Not Found", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					container, err := fnUnderTest(id)
					Expect(err).To(MatchError(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
					Expect(container).To(BeNil())
				})
			})
		}

		Describe("Stop()", func() {
			BeforeEach(func() {
				fnUnderTest = cl.Stop
				path = "/containers/" + id + "/stop"
			})

			itWorks()
		})

		Describe("Restart()", func() {
			BeforeEach(func() {
				fnUnderTest = cl.Restart
				path = "/containers/" + id + "/restart"
			})

			itWorks()
		})
	})

	Describe("Terminate()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("DELETE", "/containers/cafefeed"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when the response is HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
			})

			Context("when the response has a container", func() {
				BeforeEach(func() {
					responseBody = `{"container":{"id":"cafefeed"}}`
				})

				It("returns the Container", func() {
					container, err := cl.Terminate("cafefeed")
					Expect(err).To(BeNil())
					Expect(container).NotTo(BeNil())
					Expect(server.ReceivedRequests()).To(HaveLen(1))

					Expect(container.ID).To(Equal("cafefeed"))
				})
			})

			Context("when the response is empty", func() {
				BeforeEach(func() {
					responseBody = ``
				})

				It("returns ErrUnexpectedResponse", func() {
					container, err := cl.Terminate("cafefeed")
					Expect(err).To(MatchError(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})
		})

		Context("when the response is HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
			})

			It("returns ErrContainerNotFound", func() {
				container, err := cl.Terminate("cafefeed")
				Expect(err).To(MatchError(freighterclient.ErrContainerNotFound))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})

		Context("when the response is not HTTP 200 OK nor HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				container, err := cl.Terminate("cafefeed")
				Expect(err).To(MatchError(&freighterclient.StatusError{
					Code: http.StatusInternalServerError,
					Body: responseBody,
				}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("Public Key Operations", func() {
		setupServer := func(method, url string) {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					req.ParseForm()
				},
				ghttp.VerifyRequest(method, url),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		}

		Describe("AddPublicKeys()", func() {
			BeforeEach(func() {
				setupServer("POST", "/containers/yosemite/public_keys")
			})

			AfterEach(func() {
				st.Close()
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				Context("when the response has public keys", func() {
					BeforeEach(func() {
						responseBody = `{
							"public_keys": [
								"fprint1":"key1",
								"fprint2":"key2"
							]
						}`
					})

					It("returns a nil error", func() {
						err := cl.AddPublicKeys("yosemite", []string{"key1", "key2"})
						Expect(err).To(BeNil())

						receivedRequests := server.ReceivedRequests()
						Expect(receivedRequests).To(HaveLen(1))
						Expect(receivedRequests[0].Form["public_key[]"]).To(Equal([]string{"key1", "key2"}))
					})
				})
			})

			Context("when the error is 'pub keys not supported'", func() {
				BeforeEach(func() {
					statusCode = http.StatusBadRequest
					responseBody = fmt.Sprintf(`{"error":"%s"}`, provider.ErrPubKeyOpsNotSupported.Error())
				})

				It("returns a ErrPubKeyOpsNotSupported error", func() {
					err := cl.AddPublicKeys("yosemite", []string{"key1", "key2"})
					Expect(err).To(Equal(freighterclient.ErrPubKeyOpsNotSupported))
				})
			})

			Context("when it's a general error", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					err := cl.AddPublicKeys("yosemite", []string{"key1", "key2"})
					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})

		Describe("ClearPublicKeys()", func() {
			BeforeEach(func() {
				setupServer("DELETE", "/containers/yosemite/public_keys")
			})

			AfterEach(func() {
				st.Close()
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("returns a nil error", func() {
					err := cl.ClearPublicKeys("yosemite")
					Expect(err).To(BeNil())

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))
				})
			})

			Context("when the error is 'pub keys not supported'", func() {
				BeforeEach(func() {
					statusCode = http.StatusBadRequest
					responseBody = fmt.Sprintf(`{"error":"%s"}`, provider.ErrPubKeyOpsNotSupported.Error())
				})

				It("returns a ErrPubKeyOpsNotSupported error", func() {
					err := cl.ClearPublicKeys("yosemite")
					Expect(err).To(Equal(freighterclient.ErrPubKeyOpsNotSupported))
				})
			})

			Context("when it's a general error", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					err := cl.ClearPublicKeys("yosemite")
					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})

		Describe("RemovePublicKey()", func() {
			BeforeEach(func() {
				setupServer("DELETE", "/containers/yosemite/public_keys/fprint1")
			})

			AfterEach(func() {
				st.Close()
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("returns a nil error", func() {
					err := cl.RemovePublicKey("yosemite", "fprint1")
					Expect(err).To(BeNil())

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))
				})
			})

			Context("when the error is 'pub keys not supported'", func() {
				BeforeEach(func() {
					statusCode = http.StatusBadRequest
					responseBody = fmt.Sprintf(`{"error":"%s"}`, provider.ErrPubKeyOpsNotSupported.Error())
				})

				It("returns a ErrPubKeyOpsNotSupported error", func() {
					err := cl.RemovePublicKey("yosemite", "fprint1")
					Expect(err).To(Equal(freighterclient.ErrPubKeyOpsNotSupported))
				})
			})

			Context("when it's a general error", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					err := cl.RemovePublicKey("yosemite", "fprint1")
					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})
	})

	Describe("ListVolumes()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/volumes"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when the response has volumes", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `{"volumes": ["music", "repos"]}`
			})

			It("returns slice of strings", func() {
				vols, err := cl.ListVolumes()
				Expect(err).To(BeNil())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(vols).To(HaveLen(2))
				Expect(vols[0]).To(Equal("music"))
				Expect(vols[1]).To(Equal("repos"))
			})
		})

		Context("when the response has no volumes", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `{"volumes": []}`
			})

			It("returns slice of strings", func() {
				vols, err := cl.ListVolumes()
				Expect(err).To(BeNil())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(vols).To(HaveLen(0))
			})
		})

		Context("when the response fails", func() {
			BeforeEach(func() {
				server.Close() // simulate failure
			})

			It("returns error", func() {
				vols, err := cl.ListVolumes()
				_, ok := err.(*freighterclient.RequestFailedError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(0))
				Expect(vols).To(BeNil())
			})
		})
	})

	Describe("CreateVolume()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					req.ParseForm()
				},
				ghttp.VerifyRequest("POST", "/volumes"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		Context("on success", func() {
			BeforeEach(func() {
				statusCode = http.StatusCreated
				responseBody = `{"volume":{"slug":"repos","size":0}}`
			})

			It("returns nil", func() {
				vol, err := cl.CreateVolume("repos")
				Expect(err).To(BeNil())
				receivedRequests := server.ReceivedRequests()
				Expect(receivedRequests).To(HaveLen(1))
				Expect(receivedRequests[0].PostFormValue("slug")).To(Equal("repos"))
				Expect(vol.Slug).To(Equal("repos"))
				Expect(vol.Size).To(Equal(uint64(0)))
			})
		})

		Context("on error", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
				responseBody = ""
			})

			It("returns error", func() {
				vol, err := cl.CreateVolume("repos")
				Expect(vol).To(BeNil())
				Expect(err).To(MatchError(freighterclient.ErrVolumeNotFound))
				receivedRequests := server.ReceivedRequests()
				Expect(receivedRequests).To(HaveLen(1))
			})
		})
	})

	Describe("RemoveVolume()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("DELETE", "/volumes/music"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		})

		Context("on success", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = ""
			})

			It("returns nil", func() {
				err := cl.RemoveVolume("music")
				Expect(err).To(BeNil())
			})
		})

		Context("on error", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
				responseBody = ""
			})

			It("returns error", func() {
				err := cl.RemoveVolume("music")
				Expect(err).To(MatchError(freighterclient.ErrVolumeNotFound))
			})
		})
	})

	Describe("GetVolumeInfo()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/volumes/music"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			st, _ = NewServerTest(handlers)
			cl, host, server = st.client, st.host, st.server
		})

		Context("on success", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `{"Volume": {"Slug":"music","Size":12345}}`
			})

			It("returns nil", func() {
				vi, err := cl.GetVolumeInfo("music")
				Expect(err).To(BeNil())
				Expect(vi).NotTo(BeNil())
				Expect(vi.Slug).To(Equal("music"))
				Expect(vi.Size).To(Equal(uint64(12345)))
			})
		})
	})

	Describe("RegistryLogin()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("POST", "/registry/auth"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			st, _ := NewServerTest(handlers)
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		doRequest := func() error {
			p := &freighterclient.RegistryAuth{
				Username: "foo",
				Password: "123456",
				Email:    "foo@example.com",
				Registry: "hub.example.com",
			}
			return cl.RegistryLogin(p)
		}

		Context("when the response is HTTP 201 Created", func() {
			BeforeEach(func() {
				statusCode = http.StatusCreated
			})

			It("does not return any error", func() {
				err := doRequest()
				Expect(err).To(BeNil())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})

		Context("when the response is HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
			})

			It("returns ErrEndpointNotFound", func() {
				err := doRequest()
				Expect(err).To(MatchError(freighterclient.ErrEndpointNotFound))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})

		Context("when the response is HTTP 422 Unprocessable Entity", func() {
			BeforeEach(func() {
				statusCode = freighterclient.StatusUnprocessableEntity
			})

			It("returns ErrInvalidParams", func() {
				err := doRequest()
				Expect(err).To(MatchError(freighterclient.ErrInvalidParams))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})

		Context("when the response is not HTTP 200, 404 or 422", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				err := doRequest()
				Expect(err).To(MatchError(&freighterclient.StatusError{
					Code: http.StatusInternalServerError,
					Body: responseBody,
				}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})
	})

	Describe("RegistryLogout()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("DELETE", "/registry/auth"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			st, _ := NewServerTest(handlers)
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		doRequest := func() error {
			return cl.RegistryLogout("hub.example.com")
		}

		Context("when the response is HTTP 204 No Content", func() {
			BeforeEach(func() {
				statusCode = http.StatusNoContent
			})

			It("does not return any error", func() {
				err := doRequest()
				Expect(err).To(BeNil())

				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].FormValue("registry")).To(Equal("hub.example.com"))
			})
		})

		Context("when the response is HTTP 404 Not Found", func() {
			BeforeEach(func() {
				statusCode = http.StatusNotFound
			})

			It("returns ErrEndpointNotFound", func() {
				err := doRequest()
				Expect(err).To(MatchError(freighterclient.ErrEndpointNotFound))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})

		Context("when the response is HTTP 422 Unprocessable Entity", func() {
			BeforeEach(func() {
				statusCode = freighterclient.StatusUnprocessableEntity
			})

			It("returns ErrInvalidParams", func() {
				err := doRequest()
				Expect(err).To(MatchError(freighterclient.ErrInvalidParams))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})

		Context("when the response is not HTTP 204, 404 and 422", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				err := doRequest()
				Expect(err).To(MatchError(&freighterclient.StatusError{
					Code: http.StatusInternalServerError,
					Body: responseBody,
				}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})
	})

	Describe("HostInfo()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/info"),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			st, _ = NewServerTest(handlers)
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when the response is HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `{"info":{
					"uptime": 30,
					"memory_usage": 12345678,
					"memory_total": 17179869184,
					"storage_usage": 2000000000,
					"storage_total": 1000000000000,
					"num_cpu": 2,
					"per_cpu_usage": [0.5, 30]
				}}`
			})

			It("returns the HostInfo", func() {
				info, err := cl.HostInfo(1 * time.Second)
				Expect(err).To(BeNil())
				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].FormValue("period")).To(Equal("1s"))
				Expect(info).To(Equal(&freighterclient.HostInfo{
					Uptime:       30,
					MemoryUsage:  12345678,
					MemoryTotal:  17179869184,
					StorageUsage: 2000000000,
					StorageTotal: 1000000000000,
					NumCPU:       2,
					PerCPUUsage:  []float64{0.5, 30},
				}))

			})
		})

		Context("when the response is empty", func() {
			BeforeEach(func() {
				responseBody = ``
			})

			It("returns ErrUnexpectedResponse", func() {
				info, err := cl.HostInfo(1 * time.Second)
				Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(info).To(BeNil())
			})
		})

		Context("when the response is not HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				info, err := cl.HostInfo(1 * time.Second)
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(info).To(BeNil())
			})
		})
	})

	Describe("BuildImage()", func() {
		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					err := req.ParseMultipartForm(1 << 32)
					Expect(err).To(BeNil())
					Expect(req.Header.Get("Content-Type")).To(
						ContainSubstring("multipart/form-data; boundary="))
				},
				ghttp.VerifyRequest("POST", "/images"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			st, _ = NewServerTest(handlers)
			cl, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			st.Close()
		})

		Context("when Freighter builds the image successfully", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `
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
			})

			It("should stream the response to the given writer", func() {
				outstream := bytes.NewBuffer(nil)
				err := cl.BuildImage("myimage:tag", bytes.NewBufferString("dockerfile"), outstream, "")
				Expect(err).To(BeNil())

				Expect(outstream.String()).To(Equal(responseBody))

				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].FormValue("name")).To(Equal("myimage:tag"))
				file, _, err := reqs[0].FormFile("file")
				Expect(file).NotTo(BeNil())
				b, err := ioutil.ReadAll(file)
				Expect(err).To(BeNil())
				Expect(b).To(Equal([]byte("dockerfile")))
			})
		})

		Context("when Freighter returns an error when building the image", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `
					{"stream":"Step 0 : FROM broken\n"}
					{"stream":" ---\u003e 4300eb9d3c8d\n"}
					{"error":"Error...", "errorDetail":{"code": 123, "message": "Error pulling 'broken'"}}
				`
			})

			It("should return the error in the stream", func() {
				outstream := bytes.NewBuffer(nil)
				err := cl.BuildImage("myimage:tag", bytes.NewBufferString("dockerfile"), outstream, "")
				Expect(err).To(HaveOccurred())

				Expect(err.Error()).To(Equal("Error pulling 'broken'"))
				Expect(outstream.String()).To(Equal(responseBody))
			})
		})

		Context("with failures communicating with endpoint", func() {
			It("should surface request failures", func() {
				st.Close()

				err := cl.BuildImage("myimage:tag", bytes.NewBufferString("dockerfile"), ioutil.Discard, "")
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.RequestFailedError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(0))
			})

			It("should surface response failures in StatusErr", func() {
				statusCode = http.StatusInternalServerError
				err := cl.BuildImage("myimage:tag", bytes.NewBufferString("dockerfile"), ioutil.Discard, "")
				Expect(err).To(Equal(&freighterclient.StatusError{
					Code: http.StatusInternalServerError,
					Body: responseBody,
				}))
			})
		})

		Context("when remote URL is provided", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `
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
			})

			It("should make the request with remote param set", func() {
				outstream := bytes.NewBuffer(nil)
				err := cl.BuildImage("myimage:tag", nil, outstream, "git://github.com/foo/example.git")
				Expect(err).To(BeNil())

				Expect(outstream.String()).To(Equal(responseBody))

				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].FormValue("name")).To(Equal("myimage:tag"))
				Expect(reqs[0].FormValue("remote")).To(Equal("git://github.com/foo/example.git"))
				file, _, err := reqs[0].FormFile("file")
				Expect(file).To(BeNil())
			})
		})
	})

	Describe("InspectImage()", func() {
		It("returns image data", func() {
			responseBody := `{
				"image": {
					"id": "12",
					"name": "myimage",
					"tag": "tag",
					"created_at": 1902,
					"actual_size": 12,
					"virtual_size": 39,
					"config": {
						"env":["PATH=/sbin","SHELLSHOCK=maybe"]
					}
				}
			}`
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/images/inspect"),
				ghttp.RespondWith(http.StatusOK, responseBody),
			)

			st, _ := NewServerTest(handlers)
			cl, server := st.client, st.server

			image, err := cl.InspectImage("myimage:tag")
			Expect(err).To(BeNil(), "failed to inspect image")
			conf := &freighterclient.DockerConfig{Env: []string{"PATH=/sbin", "SHELLSHOCK=maybe"}}
			Expect(image).To(Equal(&freighterclient.Image{
				ID:          "12",
				Name:        "myimage",
				Tag:         "tag",
				CreatedAt:   1902,
				ActualSize:  12,
				VirtualSize: 39,
				Config:      conf,
			}))

			reqs := server.ReceivedRequests()
			Expect(reqs).To(HaveLen(1))
			Expect(reqs[0].FormValue("image")).To(Equal("myimage:tag"), "failed to inspect correct image")
			Expect(reqs[0].FormValue("remote")).To(Equal("true"), "expect remote to be included")
		})

		It("returns error when json is malformed", func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/images/inspect"),
				ghttp.RespondWith(http.StatusOK, "<????>"),
			)

			st, _ := NewServerTest(handlers)
			cl := st.client

			_, err := cl.InspectImage("myimage:tag")
			Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
		})

		It("returns error when we can't fetch image data", func() {
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/images/inspect"),
				ghttp.RespondWith(http.StatusInternalServerError, ""),
			)

			st, _ := NewServerTest(handlers)
			cl := st.client

			image, err := cl.InspectImage("myimage:tag")
			Expect(err).To(Equal(&freighterclient.StatusError{
				Code: http.StatusInternalServerError,
				Body: "",
			}))
			Expect(image).To(BeNil())
		})
	})

	Describe("Image Operations", func() {
		setupServer := func(method, url, contentType string) {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					req.ParseForm()
				},
				ghttp.VerifyRequest(method, url),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)
			if contentType != "" {
				handlers = ghttp.CombineHandlers(handlers, ghttp.VerifyContentType(contentType))
			}

			var err error
			st, err = NewServerTest(handlers)
			Expect(err).To(BeNil(), "test server failed to start")
			cl, host, server = st.client, st.host, st.server
		}

		Describe("RemoveImage()", func() {
			BeforeEach(func() {
				setupServer("DELETE", "/images", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				st.Close()
			})

			Context("Successful response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("return nil", func() {
					err := cl.RemoveImage("imageid")
					Expect(err).To(BeNil())
					reqs := server.ReceivedRequests()
					Expect(reqs).To(HaveLen(1))
					Expect(reqs[0].FormValue("image")).To(Equal("imageid"))
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("returns ErrImageInUse when Freighter responds with HTTP 409", func() {
					statusCode = http.StatusConflict

					err := cl.RemoveImage("imageid")
					Expect(err).To(MatchError(freighterclient.ErrImageInUse))
				})

				It("should surface request failures", func() {
					st.Close()

					err := cl.RemoveImage("imageid")
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					err := cl.RemoveImage("imageid")
					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})

		Describe("PushImage()", func() {
			BeforeEach(func() {
				setupServer("POST", "/images/push", "application/x-www-form-urlencoded")
				statusCode = http.StatusOK
			})

			AfterEach(func() {
				st.Close()
			})

			It("makes a request to Freighter to push an image", func() {
				statusCode = http.StatusOK
				responseBody = `
				{"status":"The push refers to a repository [hub.docker.com/me/imageid] (len: 1)"}
				{"status":"Sending image list"}
				{"status":"Pushing repository hub.docker.com/me/imageid (1 tags)"}
				{"status":"Image 511136ea3c5a already pushed, skipping"}
				{"status":"Pushing","progressDetail":{},"id":"2e9d5ac58122"}{"status":"Buffering to disk","progressDetail":{"current":8192,"start":1412070481},"progress":"8.192 kB","id":"2e9d5ac58122"}{"status":"Buffering to disk","progressDetail":{"start":1412070481},"id":"2e9d5ac58122"}{"status":"Pushing","progressDetail":{"current":512,"total":8192,"start":1412070481},"progress":"[===\u003e                                               ]    512 B/8.192 kB 29s","id":"2e9d5ac58122"}
				`

				out := bytes.NewBuffer(nil)
				err := cl.PushImage("imageid", "hub.docker.com/me/imageid", out)
				Expect(err).To(BeNil())

				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].PostFormValue("image")).To(Equal("imageid"))
				Expect(reqs[0].PostFormValue("destination")).To(Equal("hub.docker.com/me/imageid"))
				Expect(out.String()).To(Equal(responseBody))
			})

			It("returns error when docker failed to push image", func() {
				statusCode = http.StatusOK
				responseBody = `
				{"status":"The push refers to a repository [scratch] (len: 1)"}
				{"status":"Sending image list"}
				{"errorDetail":{"message":"Error: Status 401 trying to push repository scratch: \"\""},"error":"Error: Status 401 trying to push repository scratch: \"\""}
				`
				out := bytes.NewBuffer(nil)
				err := cl.PushImage("myimage:tag", "scratch", out)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("Error: Status 401 trying to push repository scratch: \"\""))
				Expect(out.String()).To(Equal(responseBody))
			})

			Context("Error Responses", func() {
				It("returns ErrImagePushAuthRequired when Freighter responds with HTTP 401", func() {
					statusCode = http.StatusUnauthorized

					err := cl.PushImage("imageid", "hub.docker.com/me/imageid", ioutil.Discard)
					Expect(err).To(MatchError(freighterclient.ErrImagePushAuthRequired))
				})

				It("should surface request failures", func() {
					st.Close()

					err := cl.PushImage("imageid", "hub.docker.com/me/imageid", ioutil.Discard)
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError

					err := cl.PushImage("imageid", "hub.docker.com/me/imageid", ioutil.Discard)
					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})

		Describe("TagImage()", func() {
			BeforeEach(func() {
				setupServer("POST", "/images/tag", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				st.Close()
			})

			Context("Successful response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("should return nil error", func() {
					err := cl.TagImage("imageid", "mytag", true)
					Expect(err).To(BeNil())

					reqs := server.ReceivedRequests()
					Expect(reqs).To(HaveLen(1))
					Expect(reqs[0].PostFormValue("image")).To(Equal("imageid"))
					Expect(reqs[0].PostFormValue("tag")).To(Equal("mytag"))
					Expect(reqs[0].PostFormValue("force")).To(Equal("true"))
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					st.Close()

					err := cl.TagImage("imageid", "mytag", true)
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					err := cl.TagImage("imageid", "mytag", true)

					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})

		Describe("PullImage", func() {
			BeforeEach(func() {
				setupServer("POST", "/images/pull", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				st.Close()
			})

			It("sends 'repository' and 'update' as POST data", func() {
				statusCode = http.StatusOK
				responseBody = `
				{"status":"Pulling..."}
				{"status":"Pulling", "progress":"1 B/ 100 B", "progressDetail":{"current":1, "total":100}}
				`

				err := cl.PullImage("myimage:tag", true, bytes.NewBuffer(nil))
				Expect(err).To(BeNil())
				reqs := st.server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].PostFormValue("repository")).To(Equal("myimage:tag"))
				Expect(reqs[0].PostFormValue("update")).To(Equal("true"))
			})

			It("returns error when docker failed to pull image", func() {
				statusCode = http.StatusOK
				responseBody = `
				{"status":"Pulling..."}
				{"status":"Pulling", "progress":"1 B/ 100 B", "progressDetail":{"current":1, "total":100}}
				{"error":"no reason at all"}
				`
				stdout := bytes.NewBuffer(nil)
				err := cl.PullImage("myimage:tag", true, stdout)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("no reason at all"))
				Expect(stdout.String()).To(Equal(responseBody))
			})

			Context("when response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
					responseBody = `
					{"status":"Pulling..."}
					{"status":"Pulling", "progress":"1 B/ 100 B", "progressDetail":{"current":1, "total":100}}
					`
				})

				It("should return nil error", func() {
					stdout := bytes.NewBuffer(nil)
					err := cl.PullImage("myimage:tag", true, stdout)
					Expect(err).To(BeNil())

					Expect(stdout.String()).To(Equal(responseBody))

					reqs := server.ReceivedRequests()
					Expect(reqs).To(HaveLen(1))
					Expect(reqs[0].PostFormValue("repository")).To(Equal("myimage:tag"))
				})
			})

			Context("when response is HTTP 304 Not Modified", func() {
				BeforeEach(func() {
					statusCode = http.StatusNotModified
					responseBody = ``
				})

				It("should return nil error", func() {
					stdout := bytes.NewBuffer(nil)
					err := cl.PullImage("myimage:tag", false, stdout)
					Expect(err).To(BeNil())

					Expect(stdout.String()).To(Equal(""))

					reqs := server.ReceivedRequests()
					Expect(reqs).To(HaveLen(1))
					Expect(reqs[0].PostFormValue("repository")).To(Equal("myimage:tag"))
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					st.Close()

					err := cl.PullImage("myimage:tag", true, ioutil.Discard)
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					err := cl.PullImage("myimage:tag", true, ioutil.Discard)
					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
				})
			})
		})

		Describe("ListImages()", func() {
			BeforeEach(func() {
				setupServer("GET", "/images", "application/json")
			})

			AfterEach(func() {
				st.Close()
			})

			Context("Successful Response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
					responseBody = `{
						"images": [
							{
								"id": "12",
								"name": "source1",
								"tag": "sourcetag1",
								"created_at": 1902,
								"actual_size": 12,
								"virtual_size": 39
							},
							{
								"id": "13",
								"parent_id": "21",
								"name": "source2",
								"tag": "sourcetag2",
								"created_at": 1901,
								"actual_size": 10,
								"virtual_size": 48
							}
						]
					}`
				})

				It("should be capable of extracting an Images response", func() {
					images, err := cl.ListImages()
					Expect(err).To(BeNil())
					Expect(images).To(HaveLen(2))

					Expect(images[0]).To(Equal(&freighterclient.Image{
						ID:          "12",
						Name:        "source1",
						Tag:         "sourcetag1",
						CreatedAt:   1902,
						ActualSize:  12,
						VirtualSize: 39,
					}))

					Expect(images[1]).To(Equal(&freighterclient.Image{
						ID:          "13",
						Name:        "source2",
						Tag:         "sourcetag2",
						CreatedAt:   1901,
						ActualSize:  10,
						VirtualSize: 48,
					}))
				})
			})

			Context("Error Response - JSON decoding Errors", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
					responseBody = `<<???>>`
				})

				It("should handle JSON decoding errors", func() {
					images, err := cl.ListImages()
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(images).To(BeNil())
				})
			})

			Context("Error Response - failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					st.Close()

					images, err := cl.ListImages()
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
					Expect(images).To(BeNil())
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					images, err := cl.ListImages()

					Expect(err).To(Equal(&freighterclient.StatusError{
						Code: http.StatusInternalServerError,
						Body: responseBody,
					}))
					Expect(images).To(BeNil())
				})
			})
		})
	})
})
