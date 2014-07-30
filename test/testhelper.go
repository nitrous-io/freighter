package test

import (
	"bytes"
	"fmt"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
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

func NewRequest(method string, urlStr string, params *url.Values) (*http.Request, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}
	return http.NewRequest(method, u.String(), nil)
}

func NewFormRequest(method string, urlStr string, params *url.Values) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, strings.NewReader(params.Encode()))
	if err == nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req, err
}

func NewFileRequest(method string, urlStr string, params map[string]string, files map[string]string) (*http.Request, error) {
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
