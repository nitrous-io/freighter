package freighterclient_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/nitrous-io/freighter/freighterclient"
	"github.com/nitrous-io/freighter/provider"
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
	host := freighterclient.NewHost(ip, port, "example.com", localhostCert,
		localhostCert, localhostKey)
	client, err := freighterclient.NewClientTransport(host, st.tp)
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
		st     *ServerTest
		cl     freighterclient.Client
		host   *freighterclient.Host
		server *ghttp.Server
	)

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
		var (
			statusCode   int
			responseBody string
		)

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
								"image":"freighter.yosemite",
								"host":"172.17.0.1",
								"ssh_port":60001,
								"state":"running"
							},
							{
								"id":"deadbeef",
								"name":"mavericks",
								"image":"freighter.mavericks",
								"host":"172.17.0.2",
								"ssh_port":60002,
								"state":"running"
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
						ID:    "cafebabe",
						Name:  "yosemite",
						Image: "freighter.yosemite",
						Host:  "172.17.0.1",
						State: "running",
					}))

					Expect(containers[1]).To(Equal(&freighterclient.Container{
						ID:    "deadbeef",
						Name:  "mavericks",
						Image: "freighter.mavericks",
						Host:  "172.17.0.2",
						State: "running",
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
		var (
			statusCode   int
			responseBody string
		)

		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					// Call ParseForm() so that server.ReceivedRequests() have
					// POST params populated.
					err := req.ParseForm()
					Expect(err).To(BeNil())
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
							"host":"172.17.0.1",
							"ssh_port":60001,
							"state":"running"
						}
					}`
				})

				It("returns the Container", func() {
					config := &freighterclient.ContainerConfig{}
					config.Env = []string{"LANG=C"}
					container, err := cl.Create("yosemite", "ubuntu", config)
					Expect(err).To(BeNil())
					Expect(container).NotTo(BeNil())

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))

					Expect(receivedRequests[0].PostFormValue("name")).To(Equal("yosemite"))
					Expect(receivedRequests[0].PostFormValue("image")).To(Equal("ubuntu"))
					Expect(receivedRequests[0].PostFormValue("config")).To(Equal(
						`{"Env":["LANG=C"],"Mounts":null,"PortBindings":null}`))

					Expect(container).To(Equal(&freighterclient.Container{
						ID:    "cafebabe",
						Name:  "yosemite",
						Image: "ubuntu",
						Host:  "172.17.0.1",
						State: "running",
					}))
				})
			})

			Context("when the response is empty", func() {
				BeforeEach(func() {
					responseBody = ``
				})

				It("returns ErrUnexpectedResponse", func() {
					container, err := cl.Create("yosemite", "ubuntu", nil)
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})
		})

		Context("when the response is not HTTP 422 Unprocessable Entity", func() {
			BeforeEach(func() {
				statusCode = freighterclient.StatusUnprocessableEntity
			})

			Context("when the response body is JSON with an 'error' key", func() {
				BeforeEach(func() {
					responseBody = `{ "error": "invalid name" }`
				})

				It("returns InvalidParamError", func() {
					container, err := cl.Create("yosemite", "ubuntu", nil)
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
					container, err := cl.Create("yosemite", "ubuntu", nil)
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(container).To(BeNil())
				})
			})
		})

		Context("when the response is not HTTP 200 OK nor HTTP 422 Unprocessable Entity", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				container, err := cl.Create("yosemite", "ubuntu", nil)
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("Info()", func() {
		var (
			statusCode   int
			responseBody string
		)

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
							"host":"172.17.0.2",
							"ssh_port":60002,
							"state":"running"
						}
					}`
				})

				It("returns the Container", func() {
					container, err := cl.Info("deadbeef")
					Expect(err).To(BeNil())
					Expect(container).NotTo(BeNil())
					Expect(server.ReceivedRequests()).To(HaveLen(1))

					Expect(container).To(Equal(&freighterclient.Container{
						ID:    "deadbeef",
						Name:  "mavericks",
						Image: "ubuntu-upstart",
						Host:  "172.17.0.2",
						State: "running",
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

	Describe("container control actions", func() {
		var (
			fnUnderTest func(id string) (*freighterclient.Container, error)
			path        string

			statusCode   int
			responseBody string

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
							"host":"172.17.0.2",
							"ssh_port":60002,
							"state":"running"
						}
					}`
					})

					It("returns the Container", func() {
						container, err := fnUnderTest(id)
						Expect(err).To(BeNil())
						Expect(container).NotTo(BeNil())

						Expect(container).To(Equal(&freighterclient.Container{
							ID:    id,
							Name:  "mavericks",
							Image: "ubuntu-upstart",
							Host:  "172.17.0.2",
							State: "running",
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
					}))
					Expect(container).To(BeNil())
				})
			})
		}

		Describe("Start()", func() {
			BeforeEach(func() {
				fnUnderTest = cl.Start
				path = "/containers/" + id + "/start"
			})

			itWorks()
		})

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
		var (
			statusCode   int
			responseBody string
		)

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
				}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(container).To(BeNil())
			})
		})
	})

	Describe("Public Key Operations", func() {
		var (
			statusCode   int
			responseBody string
		)

		setupServer := func(method, url string) {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					// Call ParseForm() so that server.ReceivedRequests() have
					// POST params populated.
					err := req.ParseForm()
					Expect(err).To(BeNil())
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
					}))
				})
			})
		})
	})

	Describe("ListVolumes()", func() {
		var (
			statusCode   int
			responseBody string
		)

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
		var (
			statusCode   int
			responseBody string
		)

		BeforeEach(func() {
			handlers := ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					// Call ParseForm() so that server.ReceivedRequests() have
					// POST params populated.
					err := req.ParseForm()
					Expect(err).To(BeNil())
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
		var (
			statusCode   int
			responseBody string
		)

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
		var (
			statusCode   int
			responseBody string
		)

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

	Describe("Logs()", func() {
		It("gets logs of the container", func() {
			params := url.Values{}
			params.Set("follow", "true")
			params.Set("timestamps", "true")
			params.Set("tail", "all")
			handlers := ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/containers/deadbeef/logs", params.Encode()),
				ghttp.RespondWith(http.StatusOK, "hello i am a log"),
			)

			st, _ := NewServerTest(handlers)
			defer st.Close()

			w := bytes.NewBuffer(nil)
			err := st.client.Logs("deadbeef", "true", "true", "all", w)
			Expect(err).To(BeNil(), "should successfully get the logs")
			Expect(w.String()).To(Equal("hello i am a log"), "should write logs to the writer")
		})
	})

	Describe("RegistryLogin()", func() {
		var (
			statusCode   int
			responseBody string
		)

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
				}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})
	})

	Describe("RegistryLogout()", func() {
		var (
			statusCode   int
			responseBody string
		)

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
				}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})
	})

	Describe("HostInfo()", func() {
		var (
			statusCode   int
			responseBody string
		)

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
				info, err := cl.HostInfo()
				Expect(err).To(BeNil())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
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
				info, err := cl.HostInfo()
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
				info, err := cl.HostInfo()
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.StatusError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(info).To(BeNil())
			})
		})
	})

	Describe("BuildImage()", func() {
		var (
			client freighterclient.Client
			host   *freighterclient.Host

			server       *ghttp.Server
			handlers     http.HandlerFunc
			statusCode   int
			responseBody string

			filename = os.TempDir() + "/Dockerfile.test"
		)

		teardownServer := func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		}

		BeforeEach(func() {
			handlers = ghttp.CombineHandlers(
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
			client, host, server = st.client, st.host, st.server

			file, err := os.Create(filename)
			Expect(err).To(BeNil())
			defer file.Close()
			_, err = file.Write([]byte("my dockerfile"))
			Expect(err).To(BeNil())
		})

		AfterEach(func() {
			teardownServer()
			os.Remove(filename)
		})

		Context("Successful Response", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `{
					"image": {
						"id": "12",
						"name": "myimage",
						"tag": "tag",
						"created_at": 1902,
						"actual_size": 12,
						"virtual_size": 39
					},
					"output": "image build output"
				}`
			})

			It("should be capable of extracting an Image response", func() {
				image, err := client.BuildImage(host, "myimage:tag", filename)
				Expect(err).To(BeNil())

				Expect(image).To(Equal(&freighterclient.Image{
					ID:          "12",
					Name:        "myimage",
					Tag:         "tag",
					CreatedAt:   1902,
					ActualSize:  12,
					VirtualSize: 39,
				}))

				reqs := server.ReceivedRequests()
				Expect(reqs).To(HaveLen(1))
				Expect(reqs[0].FormValue("name")).To(Equal("myimage:tag"))
				file, _, err := reqs[0].FormFile("file")
				Expect(file).NotTo(BeNil())
				b, err := ioutil.ReadAll(file)
				Expect(err).To(BeNil())
				Expect(b).To(Equal([]byte("my dockerfile")))
			})
		})

		Context("Error Response - JSON Parsing Error", func() {
			BeforeEach(func() {
				statusCode = http.StatusOK
				responseBody = `<<???>>`
			})

			It("should handle JSON decoding errors", func() {
				image, err := client.BuildImage(host, "myimage:tag", filename)
				Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
				Expect(image).To(BeNil())
			})
		})

		Context("with failures communicating with endpoint", func() {
			It("should surface request failures", func() {
				teardownServer()

				image, err := client.BuildImage(host, "myimage:tag", filename)
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.RequestFailedError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(0))
				Expect(image).To(BeNil())
			})

			It("should surface response failures in StatusErr", func() {
				statusCode = http.StatusInternalServerError
				image, err := client.BuildImage(host, "myimage:tag", filename)

				Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				Expect(image).To(BeNil())
			})
		})
	})

	Describe("Image Operations", func() {
		var (
			client freighterclient.Client
			host   *freighterclient.Host

			server       *ghttp.Server
			handlers     http.HandlerFunc
			statusCode   int
			responseBody string
		)

		setupServer := func(method, url, contentType string) {
			handlers = ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					err := req.ParseForm()
					Expect(err).To(BeNil())
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
			client, host, server = st.client, st.host, st.server
		}

		teardownServer := func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		}

		Describe("RemoveImage()", func() {
			BeforeEach(func() {
				setupServer("DELETE", "/images/imageid", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				teardownServer()
			})

			Context("Successful response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("should return nil error", func() {
					err := client.RemoveImage(host, "imageid")
					Expect(err).To(BeNil())
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					teardownServer()

					err := client.RemoveImage(host, "imageid")
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					err := client.RemoveImage(host, "imageid")

					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				})
			})
		})

		Describe("PushImage()", func() {
			BeforeEach(func() {
				setupServer("POST", "/images/push", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				teardownServer()
			})

			Context("Successful response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("should return nil error", func() {
					err := client.PushImage(host, "imageid", "hub.docker.com")
					Expect(err).To(BeNil())
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					teardownServer()

					err := client.PushImage(host, "imageid", "hub.docker.com")
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					err := client.PushImage(host, "imageid", "hub.docker.com")

					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				})
			})
		})

		Describe("TagImage()", func() {
			BeforeEach(func() {
				setupServer("POST", "/images/tag", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				teardownServer()
			})

			Context("Successful response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("should return nil error", func() {
					err := client.TagImage(host, "imageid", "mytag", true)
					Expect(err).To(BeNil())
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					teardownServer()

					err := client.TagImage(host, "imageid", "mytag", true)
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					err := client.TagImage(host, "imageid", "mytag", true)

					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				})
			})
		})

		Describe("PullImage", func() {
			BeforeEach(func() {
				setupServer("POST", "/images/pull", "application/x-www-form-urlencoded")
			})

			AfterEach(func() {
				teardownServer()
			})

			Context("Successful response", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
					responseBody = `{
						"image": {
							"id": "12",
							"name": "myimage",
							"tag": "tag",
							"created_at": 1902,
							"actual_size": 12,
							"virtual_size": 39
						},
						"output": "image build output"
					}`
				})

				It("should return nil error", func() {
					image, err := client.PullImage(host, "myimage:tag")
					Expect(err).To(BeNil())

					Expect(image).To(Equal(&freighterclient.Image{
						ID:          "12",
						Name:        "myimage",
						Tag:         "tag",
						CreatedAt:   1902,
						ActualSize:  12,
						VirtualSize: 39,
					}))
				})
			})

			Context("Error Response - JSON parsing error", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
					responseBody = `<<???>>`
				})

				It("should handle JSON decoding errors", func() {
					image, err := client.PullImage(host, "myimage:tag")
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(image).To(BeNil())
				})
			})

			Context("Error Response - with failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					teardownServer()

					image, err := client.PullImage(host, "myimage:tag")
					Expect(err).NotTo(BeNil())
					Expect(image).To(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					image, err := client.PullImage(host, "myimage:tag")

					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
					Expect(image).To(BeNil())
				})
			})
		})

		Describe("ListImages()", func() {
			BeforeEach(func() {
				setupServer("GET", "/images", "application/json")
			})

			AfterEach(func() {
				teardownServer()
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
					images, err := client.ListImages(host)
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
					images, err := client.ListImages(host)
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(images).To(BeNil())
				})
			})

			Context("Error Response - failures communicating with endpoint", func() {
				It("should surface request failures", func() {
					teardownServer()

					images, err := client.ListImages(host)
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
					Expect(images).To(BeNil())
				})

				It("should surface response failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					images, err := client.ListImages(host)

					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
					Expect(images).To(BeNil())
				})
			})
		})
	})
})

// These certs are the same as the certs embeded with net/http/httptest.
// If there's mismatched certs error, look at net/http/httptest/server.go to
// check if the cert has changed.
//
// localhostCert is a PEM-encoded TLS cert with SAN IPs
// "127.0.0.1" and "[::1]", expiring at the last second of 2049 (the end
// of ASN.1 time).
// generated from src/pkg/crypto/tls:
// go run generate_cert.go  --rsa-bits 512 --host 127.0.0.1,::1,example.com --ca --start-date "Jan 1 00:00:00 1970" --duration=1000000h
var localhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIIBdzCCASOgAwIBAgIBADALBgkqhkiG9w0BAQUwEjEQMA4GA1UEChMHQWNtZSBD
bzAeFw03MDAxMDEwMDAwMDBaFw00OTEyMzEyMzU5NTlaMBIxEDAOBgNVBAoTB0Fj
bWUgQ28wWjALBgkqhkiG9w0BAQEDSwAwSAJBAN55NcYKZeInyTuhcCwFMhDHCmwa
IUSdtXdcbItRB/yfXGBhiex00IaLXQnSU+QZPRZWYqeTEbFSgihqi1PUDy8CAwEA
AaNoMGYwDgYDVR0PAQH/BAQDAgCkMBMGA1UdJQQMMAoGCCsGAQUFBwMBMA8GA1Ud
EwEB/wQFMAMBAf8wLgYDVR0RBCcwJYILZXhhbXBsZS5jb22HBH8AAAGHEAAAAAAA
AAAAAAAAAAAAAAEwCwYJKoZIhvcNAQEFA0EAAoQn/ytgqpiLcZu9XKbCJsJcvkgk
Se6AbGXgSlq+ZCEVo0qIwSgeBqmsJxUu7NCSOwVJLYNEBO2DtIxoYVk+MA==
-----END CERTIFICATE-----`)

// localhostKey is the private key for localhostCert.
var localhostKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIBPAIBAAJBAN55NcYKZeInyTuhcCwFMhDHCmwaIUSdtXdcbItRB/yfXGBhiex0
0IaLXQnSU+QZPRZWYqeTEbFSgihqi1PUDy8CAwEAAQJBAQdUx66rfh8sYsgfdcvV
NoafYpnEcB5s4m/vSVe6SU7dCK6eYec9f9wpT353ljhDUHq3EbmE4foNzJngh35d
AekCIQDhRQG5Li0Wj8TM4obOnnXUXf1jRv0UkzE9AHWLG5q3AwIhAPzSjpYUDjVW
MCUXgckTpKCuGwbJk7424Nb8bLzf3kllAiA5mUBgjfr/WtFSJdWcPQ4Zt9KTMNKD
EUO0ukpTwEIl6wIhAMbGqZK3zAAFdq8DD2jPx+UJXnh0rnOkZBzDtJ6/iN69AiEA
1Aq8MJgTaYsDQWyU/hDq5YkDJc9e9DSCvUIzqxQWMQE=
-----END RSA PRIVATE KEY-----`)
