package docker_test

import (
	"io/ioutil"
	"net/http/httptest"

	"github.com/nitrous-io/freighter/pkg/dockertest"
	"github.com/nitrous-io/freighter/provider/docker"
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
	logger := &util.Logger{Level: util.LogLevelQuiet}
	runningContainer, _ := ioutil.ReadFile("./testdata/running_container.json")
	stoppedContainer, _ := ioutil.ReadFile("./testdata/stopped_container.json")
	containers, _ := ioutil.ReadFile("./testdata/containers.json")

	Describe(".Start", func() {
		It("starts the container when it's stopped", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("POST", "containers/start", dockertest.NewResponse(204, ""))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))
			// for final getInfo
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))

			info, err := prov.Start("containerid", "")
			Expect(err).To(BeNil(), "container failed to be started")
			Expect(info.Name).To(Equal("freighter.containerid"))
			Expect(info.State).To(Equal("running"))
		})

		It("does nothing when the container is running", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))
			// for final getInfo
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))

			info, err := prov.Start("containerid", "")
			Expect(err).To(BeNil(), "container failed to be started")
			Expect(info.Name).To(Equal("freighter.containerid"))
			Expect(info.State).To(Equal("running"))
		})
	})

	Describe(".Stop", func() {
		It("stops the container when it's running", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))
			s.AddResponse("POST", "containers/stop", dockertest.NewResponse(204, ""))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			// for final getInfo
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))

			info, err := prov.Stop("containerid")
			Expect(err).To(BeNil(), "container failed to be stopped")
			Expect(info.Name).To(Equal("freighter.containerid"))
			Expect(info.State).To(Equal("stopped"))
		})

		It("does nothing when the container is stopped", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			// for final getInfo
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))

			info, err := prov.Stop("containerid")
			Expect(err).To(BeNil(), "container failed to be stopped")
			Expect(info.Name).To(Equal("freighter.containerid"))
			Expect(info.State).To(Equal("stopped"))
		})
	})

	Describe(".List", func() {
		It("returns a list of all containers managed by freighter", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers", dockertest.NewResponse(200, string(containers)))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))

			list, err := prov.List()
			Expect(err).To(BeNil(), "failed to get list of freighter's containers")
			Expect(len(list)).To(Equal(2))
			Expect(list[0].State).To(Equal("running"))
			Expect(list[1].State).To(Equal("stopped"))
		})
	})

	Describe(".Restart", func() {
		runningContainer, _ := ioutil.ReadFile("./testdata/running_container.json")

		It("restarts a container", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))
			s.AddResponse("POST", "containers/restart", dockertest.NewResponse(204, ""))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))

			info, err := prov.Restart("container-id")
			Expect(err).To(BeNil(), "container failed to be restarted")
			Expect(info.State).To(Equal("running"))
		})
	})

	Describe(".Terminate", func() {
		It("terminates a stopped container", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("DELETE", "containers/id", dockertest.NewResponse(204, ""))

			info, err := prov.Terminate("container-id")
			Expect(err).To(BeNil(), "container failed to be terminated")
			Expect(info.State).To(Equal(""))
		})

		It("stops and terminates a running container", func() {
			ts := newTestServer()
			defer ts.Close()
			s := ts.server
			prov, err := docker.NewDockerProvider(logger, "datapath", "freighter")
			Expect(err).To(BeNil(), "failed to initialize docker provider")

			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(runningContainer)))
			s.AddResponse("POST", "containers/stop", dockertest.NewResponse(204, ""))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("GET", "containers/id", dockertest.NewResponse(200, string(stoppedContainer)))
			s.AddResponse("DELETE", "containers/id", dockertest.NewResponse(204, ""))

			info, err := prov.Terminate("container-id")
			Expect(err).To(BeNil(), "container failed to be terminated")
			Expect(info.State).To(Equal(""))
		})
	})
})

type testServer struct {
	*httptest.Server
	server *dockertest.Server
}

func newTestServer() *testServer {
	s := dockertest.NewServer()
	ts := &testServer{Server: httptest.NewServer(s.Router), server: s}
	*(docker.DockerHost) = ts.URL
	return ts
}
