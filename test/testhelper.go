package test

import (
	"bytes"
	"fmt"
	"math/rand"
	"mime/multipart"

	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/gomega"
)

const TestSSHPrivKey = `-----BEGIN DSA PRIVATE KEY-----
MIIBuwIBAAKBgQDctzaGlWC8m4JDicszh9nIc5Ldfn2kcIEttt4NKVqNO2P93Ieq
hIOLdu/D/IvNzfsR2RRRvvw1TrponjnG0UrC0qNG6a/BJV1cukR97Qh7GkyNdSJF
bMgIayLNXljWh8fUsvmnyGQgQ+daowTEgQ+fogqCN9JojIj+84i3tGk6+QIVALgN
cbDIjXUdJv69w5rGpJ/KqcfpAoGAFTgo6lU8bAAyEJLMkSqK/6vMR5tk/0klmKnx
4lwfvj0gYSILVhz9sOyfsCyFCd+VZuXtmi6KKi4CIa6Uu5JTbsfDwNgiX7M/rm7y
gZ7To3ni1kAAyZcgzjIfbaQVn6zqGtubtipxHF19CAO7LkSyx/oFiDbBE+03KQab
udXHosgCgYBol+1yshjScPQbHvu6Pua721KvS6IDcj+TuyIds0nuhhkO8Wo+AGY6
6FwRgwPWGBsI3C+Fybv0x+s8ndEPg9GapFkW6rntq8idhSqA2pTcSphFl/+OTirH
1mYqJh5IhnW8NVs1zNeyyaYpk9ZKdGbOJIbpRQhHnFzwrJ/Fwhv6NAIVAIxUrm4g
6seZqrNmfVvKeSvRda2H
-----END DSA PRIVATE KEY-----`

const TestSSHPubKey = `ssh-dss AAAAB3NzaC1kc3MAAACBANy3NoaVYLybgkOJyzOH2chzkt1+faRwgS223g0pWo07Y/3ch6qEg4t278P8i83N+xHZFFG+/DVOumieOcbRSsLSo0bpr8ElXVy6RH3tCHsaTI11IkVsyAhrIs1eWNaHx9Sy+afIZCBD51qjBMSBD5+iCoI30miMiP7ziLe0aTr5AAAAFQC4DXGwyI11HSb+vcOaxqSfyqnH6QAAAIAVOCjqVTxsADIQksyRKor/q8xHm2T/SSWYqfHiXB++PSBhIgtWHP2w7J+wLIUJ35Vm5e2aLooqLgIhrpS7klNux8PA2CJfsz+ubvKBntOjeeLWQADJlyDOMh9tpBWfrOoa25u2KnEcXX0IA7suRLLH+gWINsET7TcpBpu51ceiyAAAAIBol+1yshjScPQbHvu6Pua721KvS6IDcj+TuyIds0nuhhkO8Wo+AGY66FwRgwPWGBsI3C+Fybv0x+s8ndEPg9GapFkW6rntq8idhSqA2pTcSphFl/+OTirH1mYqJh5IhnW8NVs1zNeyyaYpk9ZKdGbOJIbpRQhHnFzwrJ/Fwhv6NA== test@example.com`

const NitrousImage = "nitrousio/ubuntu-bare:trusty"

func NewRequestWithSecret(secret string, method string, urlStr string, params *url.Values) (*http.Request, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Freighter-Secret", secret)
	return req, nil
}

func NewRequest(method string, urlStr string, params *url.Values) (*http.Request, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func NewFormRequest(secret string, method string, urlStr string, params *url.Values) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Freighter-Secret", secret)
	return req, err
}

func NewFileRequest(secret string, method string, urlStr string, params map[string]string, files map[string]string) (*http.Request, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, contents := range files {
		part, err := writer.CreateFormFile(key, fmt.Sprintf("%s.txt", key))
		if err != nil {
			return nil, err
		}
		part.Write([]byte(contents))
	}

	for key, val := range params {
		_ = writer.WriteField(key, val)
	}

	err := writer.Close()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Freighter-Secret", secret)
	return req, nil
}

func TempFile(extension string) string {
	randomName := RandomName("freightertest")
	if extension == "" {
		return path.Join(os.TempDir(), randomName)
	}
	return path.Join(os.TempDir(), randomName+"."+extension)
}

func TempDir() (string, error) {
	dir := path.Join(os.TempDir(), RandomName("freightertest"))
	err := os.Mkdir(dir, 0755)
	if err != nil {
		return "", err
	}
	return dir, nil
}

func RandomName(name string) string {
	return fmt.Sprintf("%v-%v-%v", name, time.Now().UnixNano(), rand.Intn(1000))
}

var pulledImages = make(map[string]struct{})

func PullImage(name string) {
	if _, ok := pulledImages[name]; ok {
		return
	}
	start := time.Now()
	fmt.Printf("Pulling %s ...", name)
	err := exec.Command("docker", "pull", name).Run()
	fmt.Printf("Pulled after %.1fs\n", time.Since(start).Seconds())
	Expect(err).To(BeNil(), "failed to pull %s image", name)
	pulledImages[name] = struct{}{}
}

// createImages writes a dockerfile from contents and
// builds a docker image from it, returning the name of the image.
// If contents is zero value, it uses a default dockerfile.
func CreateImages(count int, name string, noEntropy bool, contents string) []string {
	if contents == "" || strings.Contains(contents, NitrousImage) {
		PullImage(NitrousImage)
	}
	dockerLoc, err := ioutil.TempDir("", "DockerTest")
	Expect(err).To(BeNil())
	dockerFile, err := os.Create(filepath.Join(dockerLoc, "Dockerfile"))
	Expect(err).To(BeNil())

	entropy := name
	if noEntropy {
		entropy = "thing"
	}

	if contents == "" {
		contents = fmt.Sprintf(`
				FROM %s
				MAINTAINER Nitrous.IO, hello@nitrous.io
				RUN echo '%s' >> /tmp/info
				CMD /bin/bash
			`, NitrousImage, entropy)
	}
	dockerFile.WriteString(contents)
	dockerFile.Close()

	list := []string{}
	for index := 1; index <= count; index++ {
		findableName := fmt.Sprintf("%v:n%v", name, index)
		out, err := exec.Command("docker", "build", "-t", findableName, dockerLoc).CombinedOutput()
		Expect(err).To(BeNil(), "Failed to build image, output: %s", string(out))
		list = append(list, findableName)
	}

	err = os.RemoveAll(dockerLoc)
	Expect(err).To(BeNil())

	return list
}

func RemoveImages(names []string) {
	args := append([]string{"rmi", "-f"}, names...)
	out, err := exec.Command("docker", args...).CombinedOutput()
	Expect(err).To(BeNil(), "Failed to remove images, output: %v", string(out))
}
