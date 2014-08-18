package freighterclient

import "fmt"

const defaultPort = 65343

// Host represents a Freighter host.
type Host struct {
	// Host is the hostname or IP address of a Freighter host.
	Host string
	Port int

	// domain of the freighter host i.e. slug.nitrouspro.com
	// this must be the same as the server name we used to generate
	// certificates
	ServerName string

	// PEM encoded version of certificate that is requires for authentication
	CA   []byte
	Cert []byte
	Key  []byte
}

func NewHost(host string, port int, name string, ca, cert, key []byte) *Host {
	if port == 0 {
		port = defaultPort
	}
	return &Host{
		Host:       host,
		Port:       port,
		ServerName: name,
		CA:         ca,
		Cert:       cert,
		Key:        key,
	}
}

func (h *Host) BaseURL() string {
	port := h.Port
	if port == 0 {
		port = 65333
	}
	return fmt.Sprintf("https://%s:%d", h.Host, port)
}
