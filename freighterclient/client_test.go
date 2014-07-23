package freighterclient_test

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/nitrous-io/freighter/freighterclient"
	"github.com/nitrous-io/freighter/provider"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
)

func TestClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "client")
}

type ServerTest struct {
	client    freighterclient.Client
	host      *freighterclient.Host
	server    *ghttp.Server
	handlerFn http.HandlerFunc
	tls       bool
}

func NewServerTest(handlerFn http.HandlerFunc) *ServerTest {
	st := &ServerTest{handlerFn: handlerFn}
	return st.Setup()
}

func (st *ServerTest) Setup() *ServerTest {
	if st.tls {
		st.server = ghttp.NewTLSServer()
	} else {
		st.server = ghttp.NewServer()
	}
	st.server.AppendHandlers(st.handlerFn)

	ip, port := func() (string, int) {
		serverAddr := st.server.HTTPTestServer.Listener.Addr().String()
		ip, portStr, err := net.SplitHostPort(serverAddr)
		Expect(err).To(BeNil())
		port, err := strconv.Atoi(portStr)
		Expect(err).To(BeNil())
		return ip, port
	}()
	st.host = &freighterclient.Host{Host: ip, Port: port, Secure: st.tls}

	if st.tls {
		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		c := &http.Client{Transport: tr}
		st.client = freighterclient.NewClient(c)
	} else {
		st.client = freighterclient.NewClient(nil)
	}

	return st
}

var _ = Describe("client", func() {
	var (
		st     *ServerTest
		client freighterclient.Client
		host   *freighterclient.Host
		server *ghttp.Server
	)

	Describe("NewClient()", func() {
		It("implements Client", func() {
			var _ freighterclient.Client = freighterclient.NewClient(nil)
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
			st = NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
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
					containers, err := client.List(host)
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
					containers, err := client.List(host)
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
					containers, err := client.List(host)
					Expect(err).NotTo(BeNil())
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(containers).To(BeNil())
				})
			})
		})

		Context("when the response is not HTTP 200 OK", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				containers, err := client.List(host)
				Expect(err).NotTo(BeNil())
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
				containers, err := client.List(host)
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.RequestFailedError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(0))
				Expect(containers).To(BeNil())
			})
		})

		Context("when the request times out", func() {
			BeforeEach(func() {
				tr := &http.Transport{
					Dial: (&net.Dialer{
						Timeout: 1 * time.Nanosecond,
					}).Dial,
				}
				client = freighterclient.NewClient(&http.Client{Transport: tr})
			})

			It("returns a RequestFailedError", func() {
				containers, err := client.List(host)
				Expect(err).NotTo(BeNil())
				_, ok := err.(*freighterclient.RequestFailedError)
				Expect(ok).To(BeTrue())
				Expect(server.ReceivedRequests()).To(HaveLen(0))
				Expect(containers).To(BeNil())
			})
		})

		Context("when connecting to a Freighter host over TLS", func() {
			BeforeEach(func() {
				st.server.Close()
				st.tls = true
				st = st.Setup()
				client, host, server = st.client, st.host, st.server
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
					responseBody = `{
						"containers": [
							{
								"id":"cafebabe",
								"name":"yosemite",
								"image":"freighter.yosemite",
								"host":"172.17.0.1",
								"ssh_port":60001,
								"state":"running"
							}
						]
					}`
				})

				It("returns a slice of Containers", func() {
					containers, err := client.List(host)
					Expect(err).To(BeNil())
					Expect(server.ReceivedRequests()).To(HaveLen(1))
					Expect(containers).To(HaveLen(1))

					Expect(containers[0]).To(Equal(&freighterclient.Container{
						ID:    "cafebabe",
						Name:  "yosemite",
						Image: "freighter.yosemite",
						Host:  "172.17.0.1",
						State: "running",
					}))
				})
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
			st = NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
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
					config := freighterclient.NewDockerContainerConfig()
					config.Env = []string{"LANG=C"}
					container, err := client.Create(host, "yosemite", "ubuntu", config)
					Expect(err).To(BeNil())
					Expect(container).NotTo(BeNil())

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))

					Expect(receivedRequests[0].PostFormValue("name")).To(Equal("yosemite"))
					Expect(receivedRequests[0].PostFormValue("image")).To(Equal("ubuntu"))
					Expect(receivedRequests[0].PostFormValue("config")).To(Equal(
						`{"Config":{"Env":["LANG=C"]},"HostConfig":{}}`))

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
					container, err := client.Create(host, "yosemite", "ubuntu", nil)
					Expect(err).NotTo(BeNil())
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
					container, err := client.Create(host, "yosemite", "ubuntu", nil)
					Expect(err).NotTo(BeNil())
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
					container, err := client.Create(host, "yosemite", "ubuntu", nil)
					Expect(err).NotTo(BeNil())
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
				container, err := client.Create(host, "yosemite", "ubuntu", nil)
				Expect(err).NotTo(BeNil())
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

			st = NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
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
					container, err := client.Info(host, "deadbeef")
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
					container, err := client.Info(host, "deadbeef")
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
				container, err := client.Info(host, "deadbeef")
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
				container, err := client.Info(host, "deadbeef")
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
			fnUnderTest func(h *freighterclient.Host, id string) (*freighterclient.Container, error)
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
			st = NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		})

		itWorks := func() {
			It("makes an HTTP POST request", func() {
				fnUnderTest(host, id)

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
						container, err := fnUnderTest(host, id)
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
						container, err := fnUnderTest(host, id)
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
					container, err := fnUnderTest(host, id)
					Expect(err).To(MatchError(freighterclient.ErrContainerNotFound))
					Expect(container).To(BeNil())
				})
			})

			Context("when the response is not HTTP 200 OK nor HTTP 404 Not Found", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					container, err := fnUnderTest(host, id)
					Expect(err).To(MatchError(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
					Expect(container).To(BeNil())
				})
			})
		}

		Describe("Start()", func() {
			BeforeEach(func() {
				fnUnderTest = client.Start
				path = "/containers/" + id + "/start"
			})

			itWorks()
		})

		Describe("Stop()", func() {
			BeforeEach(func() {
				fnUnderTest = client.Stop
				path = "/containers/" + id + "/stop"
			})

			itWorks()
		})

		Describe("Restart()", func() {
			BeforeEach(func() {
				fnUnderTest = client.Restart
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
			st = NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
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
					container, err := client.Terminate(host, "cafefeed")
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
					container, err := client.Terminate(host, "cafefeed")
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
				container, err := client.Terminate(host, "cafefeed")
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
				container, err := client.Terminate(host, "cafefeed")
				Expect(err).To(MatchError(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
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
			st = NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		}

		teardownServer := func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		}

		Describe("AddPublicKeys()", func() {
			BeforeEach(func() {
				setupServer("POST", "/containers/yosemite/public_keys")
			})

			AfterEach(func() {
				teardownServer()
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
						err := client.AddPublicKeys(host, "yosemite", []string{"key1", "key2"})
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
					err := client.AddPublicKeys(host, "yosemite", []string{"key1", "key2"})
					Expect(err).To(Equal(freighterclient.ErrPubKeyOpsNotSupported))
				})
			})

			Context("when it's a general error", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					err := client.AddPublicKeys(host, "yosemite", []string{"key1", "key2"})
					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				})
			})
		})

		Describe("ClearPublicKeys()", func() {
			BeforeEach(func() {
				setupServer("DELETE", "/containers/yosemite/public_keys")
			})

			AfterEach(func() {
				teardownServer()
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("returns a nil error", func() {
					err := client.ClearPublicKeys(host, "yosemite")
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
					err := client.ClearPublicKeys(host, "yosemite")
					Expect(err).To(Equal(freighterclient.ErrPubKeyOpsNotSupported))
				})
			})

			Context("when it's a general error", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					err := client.ClearPublicKeys(host, "yosemite")
					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				})
			})
		})

		Describe("RemovePublicKey()", func() {
			BeforeEach(func() {
				setupServer("DELETE", "/containers/yosemite/public_keys/fprint1")
			})

			AfterEach(func() {
				teardownServer()
			})

			Context("when the response is HTTP 200 OK", func() {
				BeforeEach(func() {
					statusCode = http.StatusOK
				})

				It("returns a nil error", func() {
					err := client.RemovePublicKey(host, "yosemite", "fprint1")
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
					err := client.RemovePublicKey(host, "yosemite", "fprint1")
					Expect(err).To(Equal(freighterclient.ErrPubKeyOpsNotSupported))
				})
			})

			Context("when it's a general error", func() {
				BeforeEach(func() {
					statusCode = http.StatusInternalServerError
				})

				It("returns a StatusError", func() {
					err := client.RemovePublicKey(host, "yosemite", "fprint1")
					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				})
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
			st := NewServerTest(handlers)
			client, host := st.client, st.host

			w := bytes.NewBuffer(nil)
			err := client.Logs(host, "deadbeef", "true", "true", "all", w)
			Expect(err).To(BeNil(), "should successfully get the logs")
			Expect(w.String()).To(Equal("hello i am a log"), "should write logs to the writer")
		})
	})

	Describe("RegistryLogin()", func() {
		var (
			client freighterclient.Client
			host   *freighterclient.Host

			server       *ghttp.Server
			handlers     http.HandlerFunc
			statusCode   int
			responseBody string
		)

		BeforeEach(func() {
			handlers = ghttp.CombineHandlers(
				ghttp.VerifyRequest("POST", "/registry/auth"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			st := NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		})

		doRequest := func() error {
			p := &freighterclient.RegistryAuth{
				Username: "foo",
				Password: "123456",
				Email:    "foo@example.com",
				Registry: "hub.example.com",
			}
			return client.RegistryLogin(host, p)
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
				Expect(err).To(MatchError(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
		})
	})

	Describe("RegistryLogout()", func() {
		var (
			client freighterclient.Client
			host   *freighterclient.Host

			server       *ghttp.Server
			handlers     http.HandlerFunc
			statusCode   int
			responseBody string
		)

		BeforeEach(func() {
			handlers = ghttp.CombineHandlers(
				ghttp.VerifyRequest("DELETE", "/registry/auth"),
				ghttp.VerifyContentType("application/x-www-form-urlencoded"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			st := NewServerTest(handlers)
			client, host, server = st.client, st.host, st.server
		})

		AfterEach(func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		})

		doRequest := func() error {
			return client.RegistryLogout(host, "hub.example.com")
		}

		Context("when the response is HTTP 204 No Content", func() {
			BeforeEach(func() {
				statusCode = http.StatusNoContent
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

		Context("when the response is not HTTP 204, 404 and 422", func() {
			BeforeEach(func() {
				statusCode = http.StatusInternalServerError
			})

			It("returns a StatusError", func() {
				err := doRequest()
				Expect(err).To(MatchError(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
				Expect(server.ReceivedRequests()).To(HaveLen(1))
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

		setupServer := func(method, url, query string) {
			handlers = ghttp.CombineHandlers(
				func(w http.ResponseWriter, req *http.Request) {
					// Call ParseForm() so that server.ReceivedRequests() have
					// POST params populated.
					err := req.ParseForm()
					Expect(err).To(BeNil())
				},
				ghttp.VerifyRequest(method, url, query),
				ghttp.VerifyContentType("application/json"),
				ghttp.RespondWithPtr(&statusCode, &responseBody),
			)

			server = ghttp.NewServer()
			server.AppendHandlers(handlers)

			ip, port := IPAndPort(server)
			host = &freighterclient.Host{Host: ip, Port: port, Secure: false}
			client = freighterclient.NewClient(nil)
		}

		teardownServer := func() {
			if server.HTTPTestServer != nil {
				server.Close()
			}
		}

		Describe("ListImages()", func() {

			Context("with false showAll", func() {

				BeforeEach(func() {
					setupServer("GET", "/images", "show_all=false")
				})

				AfterEach(func() {
					teardownServer()
				})

				It("should pass through showAll false state", func() {
					client.ListImages(host, false)

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))
				})

			})

			Context("with true showAll", func() {

				BeforeEach(func() {
					setupServer("GET", "/images", "show_all=true")
				})

				AfterEach(func() {
					teardownServer()
				})

				It("should pass through showAll true state", func() {
					client.ListImages(host, true)

					receivedRequests := server.ReceivedRequests()
					Expect(receivedRequests).To(HaveLen(1))
				})

			})

			Context("with connect failures", func() {

				BeforeEach(func() {
					setupServer("GET", "/images", "show_all=true")
				})

				AfterEach(func() {
					teardownServer()
				})

				It("should surface request failures", func() {
					teardownServer()

					images, err := client.ListImages(host, true)
					Expect(err).NotTo(BeNil())
					_, ok := err.(*freighterclient.RequestFailedError)
					Expect(ok).To(BeTrue())
					Expect(server.ReceivedRequests()).To(HaveLen(0))
					Expect(images).To(BeNil())
				})

				It("should surface respone failures in StatusErr", func() {
					statusCode = http.StatusInternalServerError
					images, err := client.ListImages(host, true)

					Expect(err).To(Equal(&freighterclient.StatusError{Code: http.StatusInternalServerError}))
					Expect(images).To(BeNil())
				})

			})

			Context("parsing body", func() {

				BeforeEach(func() {
					setupServer("GET", "/images", "show_all=true")
				})

				AfterEach(func() {
					teardownServer()
				})

				It("should handle JSON decoding errors", func() {
					statusCode = http.StatusOK
					responseBody = `<<???>>`

					images, err := client.ListImages(host, true)
					Expect(err).To(Equal(freighterclient.ErrUnexpectedResponse))
					Expect(images).To(BeNil())
				})

				It("should be capable of extracting an Images response", func() {
					statusCode = http.StatusOK
					responseBody = `{
						"images": [
							{
								"id": "12",
								"parent_id": "19",
								"source": "source1",
								"source_tag": "sourcetag1",
								"created_at": 1902,
								"actual_size": 12,
								"virtual_size": 39
							},
							{
								"id": "13",
								"parent_id": "21",
								"source": "source2",
								"source_tag": "sourcetag2",
								"created_at": 1901,
								"actual_size": 10,
								"virtual_size": 48
							}
						]
					}`

					images, err := client.ListImages(host, true)
					Expect(err).To(BeNil())
					Expect(images).To(HaveLen(2))

					Expect(images[0]).To(Equal(&freighterclient.Image{
						ID:          "12",
						ParentID:    "19",
						Source:      "source1",
						SourceTag:   "sourcetag1",
						CreatedAt:   1902,
						ActualSize:  12,
						VirtualSize: 39,
					}))

					Expect(images[1]).To(Equal(&freighterclient.Image{
						ID:          "13",
						ParentID:    "21",
						Source:      "source2",
						SourceTag:   "sourcetag2",
						CreatedAt:   1901,
						ActualSize:  10,
						VirtualSize: 48,
					}))
				})

			})
		})
	})
})
