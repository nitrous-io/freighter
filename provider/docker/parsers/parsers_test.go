package parsers_test

import (
	"fmt"
	"testing"

	"github.com/nitrous-io/freighter/provider/docker/parsers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func Test(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "parsers")
}

var _ = Describe("parsers", func() {
	Describe("ParseRepoAndTag", func() {
		testCases := []struct {
			input, repo, tag string
		}{
			{input: "ubuntu", repo: "ubuntu", tag: ""},
			{input: "ubuntu:14.04", repo: "ubuntu", tag: "14.04"},
			{input: "nitrousio/ubuntu", repo: "nitrousio/ubuntu", tag: ""},
			{input: "nitrousio/ubuntu:14.04", repo: "nitrousio/ubuntu", tag: "14.04"},
			{input: "example.com:5000/ubuntu", repo: "example.com:5000/ubuntu", tag: ""},
			{input: "example.com:5000/ubuntu:14.04", repo: "example.com:5000/ubuntu", tag: "14.04"},
			{input: "example.com:5000/nitrousio/ubuntu", repo: "example.com:5000/nitrousio/ubuntu", tag: ""},
			{input: "example.com:5000/nitrousio/ubuntu:14.04", repo: "example.com:5000/nitrousio/ubuntu", tag: "14.04"},
		}
		for _, tc2 := range testCases {
			tc := tc2
			It(fmt.Sprintf("parses %#v to (%#v, %#v)", tc.input, tc.repo, tc.tag), func() {
				repo, tag := parsers.ParseRepoAndTag(tc.input)
				Expect(repo).To(Equal(tc.repo))
				Expect(tag).To(Equal(tc.tag))
			})
		}
	})

	Describe("ParseRegistry", func() {
		testCases := []struct {
			input, expected string
		}{
			{input: "ubuntu", expected: ""},
			{input: "ubuntu:14.04", expected: ""},
			{input: "nitrousio/ubuntu", expected: ""},
			{input: "nitrousio/ubuntu:latest", expected: ""},
			{input: "example.com/ubuntu", expected: "example.com"},
			{input: "example.com:5000/ubuntu", expected: "example.com:5000"},
			{input: "example.com:5000/ubuntu:latest", expected: "example.com:5000"},
			{input: "example.com:5000/nitrousio/ubuntu", expected: "example.com:5000"},
			{input: "example.com:5000/nitrousio/ubuntu:14.04", expected: "example.com:5000"},
		}
		for _, tc2 := range testCases {
			tc := tc2
			It(fmt.Sprintf("extracts registry host %#v from %#v", tc.expected, tc.input), func() {
				registry := parsers.ParseRegistry(tc.input)
				Expect(registry).To(Equal(tc.expected))
			})
		}
	})
})
