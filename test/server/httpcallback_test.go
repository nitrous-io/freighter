package servertest

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"

	"github.com/nitrous-io/freighter/server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTPCallback", func() {
	var (
		cb *server.HTTPCallback
	)

	BeforeEach(func() {
		cb = &server.HTTPCallback{}
	})

	Describe("Invoke()", func() {
		var (
			payload = `{"foo": "bar"}`
			serv    *httptest.Server
			req     *http.Request
			reqbody string
		)

		AfterEach(func() {
			if serv != nil {
				serv.Close()
			}
		})

		doAction := func() {
			cb.Invoke(serv.URL, payload)
		}

		itWorks := func() {
			It("makes an http request", func() {
				doAction()
				Expect(req).NotTo(BeNil())
				Expect(req.Method).To(Equal("POST"))
				Expect(reqbody).To(Equal(payload))
			})
		}

		Context("when a given url is for a http server without tls/ssl", func() {
			BeforeEach(func() {
				serv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := ioutil.ReadAll(r.Body)
					reqbody = string(b)
					req = r
				}))
			})

			itWorks()
		})

		Context("when a given url is for a https server", func() {
			BeforeEach(func() {
				serv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := ioutil.ReadAll(r.Body)
					reqbody = string(b)
					req = r
				}))
			})

			itWorks()
		})
	})
})
