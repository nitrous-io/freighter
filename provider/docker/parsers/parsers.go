package parsers

import (
	"regexp"
	"strings"
)

// ParseRepoAndTag extracts the repo name and tag from a repository string.
// Examples:
//    ubuntu -> "ubuntu" + ""
//    ubuntu:14.04 -> "ubuntu" + "14.04"
//    nitrousio/ubuntu:14.04 -> "nitrousio/ubuntu" + "14.04"
//    example.com:5000/ubuntu:14.04 -> "example.com:5000/ubuntu" + "14.04"
//    example.com:5000/nitrousio/ubuntu:14.04 -> "example.com:5000/nitrousio/ubuntu" + "14.04"
// Stolen from https://github.com/docker/docker/blob/403df1765abfd3ac62b7f6b8bd709fb0d691d8c8/pkg/parsers/parsers.go#L72.
func ParseRepoAndTag(repos string) (string, string) {
	n := strings.LastIndex(repos, ":")
	if n < 0 {
		return repos, ""
	}
	tag := repos[n+1:]
	if !strings.Contains(tag, "/") {
		return repos[:n], tag
	}
	return repos, ""
}

var hostnameRe = regexp.MustCompile(`\A[^.]+\.[^.:]+(:[0-9]+)?\z`)

// ParseRegistry extracts the registry host (in "hostname:port" format).
// Examples:
//    nitrousio/ubuntu:14.04 -> ""
//    example.com/ubuntu:14.04 -> "example.com"
//    example.com:5000/ubuntu:14.04 -> "example.com:5000"
//    example.com:5000/nitrousio/ubuntu:14.04 -> "example.com:5000"
var ParseRegistry = func(repos string) string {
	parts := strings.Split(repos, "/")
	switch len(parts) {
	case 2:
		if hostnameRe.Match([]byte(parts[0])) {
			return parts[0]
		}
	case 3:
		return parts[0]
	default:
	}
	return ""
}
