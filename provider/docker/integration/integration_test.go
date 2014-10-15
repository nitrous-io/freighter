package integration

import (
	"os/exec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/nitrous-io/freighter/test"

	"testing"
)

func Test(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "provider/docker/integration")
}

var _ = BeforeSuite(func() {
	test.PullImage("tianon/true")
})

var _ = AfterSuite(func() {
	err := exec.Command("docker", "rmi", "tianon/true").Run()
	Expect(err).To(BeNil(), "failed to remove tianon/true:latest image")
})
