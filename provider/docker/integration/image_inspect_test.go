package integration

import (
	"os"

	"github.com/nitrous-io/freighter/provider"
	"github.com/nitrous-io/freighter/provider/docker"
	. "github.com/nitrous-io/freighter/test"
	"github.com/nitrous-io/freighter/util"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("InspectImage", func() {
	var (
		prov    *docker.DockerProvider
		datadir string
	)

	BeforeEach(func() {
		var err error
		datadir = os.Getenv("FREIGHTERD_BASE")

		if datadir == "" {
			datadir, err = TempDir()
			Expect(err).To(BeNil())
		}

		logger := &util.Logger{Level: util.LogLevelQuiet}
		prov, err = docker.NewDockerProvider(logger, datadir)
		Expect(err).To(BeNil())
	})

	It("should be able to lookup information for a local image", func() {
		imageList := CreateImages(1, "inspect", false, `FROM scratch
				MAINTAINER Callum Jones <callum@callumj.com>
				ENV captain crunch
				ENV mickey mouse
				EXPOSE 139 22 20 21
				VOLUME ["/mnt"]
				CMD /bin/bash -c "echo hi"
			`)
		defer RemoveImages(imageList)

		img, err := prov.InspectImage(imageList[0], false)
		Expect(err).To(BeNil())
		Expect(img.ID).ToNot(BeEmpty())
		Expect(img.Name).To(Equal("inspect"))
		Expect(img.Tag).To(Equal("n1"))
		Expect(img.Config).ToNot(BeNil())
		Expect(img.Config.Image).To(Equal(img.ID))
		Expect(img.Config.Env).To(Equal([]string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"captain=crunch",
			"mickey=mouse",
		}))
		Expect(img.Config.ExposedPorts).To(HaveKey("139/tcp"))
		Expect(img.Config.ExposedPorts).To(HaveKey("22/tcp"))
		Expect(img.Config.ExposedPorts).To(HaveKey("20/tcp"))
		Expect(img.Config.ExposedPorts).To(HaveKey("21/tcp"))
		Expect(img.Config.Volumes).To(HaveKey("/mnt"))
		Expect(img.Config.Cmd).To(ContainElement(`/bin/bash -c "echo hi"`))
	})

	It("should not perform remote image lookup when instructed not to", func() {
		img, err := prov.InspectImage("centos", false)
		Expect(err).ToNot(BeNil())
		Expect(err).To(MatchError(provider.ErrImageNotFound))
		Expect(img).To(BeNil())
	})

	It("should return information about a remote image", func() {
		img, err := prov.InspectImage(NitrousImage, true)
		Expect(err).To(BeNil())
		Expect(img.ID).ToNot(BeEmpty())
		Expect(img.Name).To(Equal("nitrousio/ubuntu-bare"))
		Expect(img.Tag).To(Equal("trusty"))
		Expect(img.Config).ToNot(BeNil())
		Expect(img.Config.Env).To(Equal([]string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"container=docker",
			"NITROUS_IMAGE_VERSION=1",
			"NITROUS_USERNAME=action",
			"NITROUS_SSH_ENABLED=true",
		}))
	})
})
