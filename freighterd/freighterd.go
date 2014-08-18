package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strconv"

	"github.com/nitrous-io/freighter/provider/docker"
	"github.com/nitrous-io/freighter/server"
	"github.com/nitrous-io/freighter/util"
)

var (
	version     string
	defaultConf string
)

func init() {
	defaultConf = path.Join("etc", "freighterd", "config.json")
}

func main() {
	var (
		flVersion    = flag.Bool("v", false, "Display the version and exit")
		flConfigPath = flag.String("c", defaultConf, "Path to the configuration file. Default to "+defaultConf)
	)
	flag.Parse()

	if *flVersion {
		fmt.Printf("freighterd version %v\n", version)
		os.Exit(0)
	}

	logger := &util.Logger{Level: util.LogLevelDebug}

	defer func() {
		if r := recover(); r != nil {
			logger.FatalExit(r)
		}
	}()

	conf := &server.Config{}
	// parse config file
	confFile, err := os.Open(*flConfigPath)
	if err != nil {
		logger.Info("Missing config file. Use default config")
		conf = server.DefaultConfig
	} else {
		if err := json.NewDecoder(confFile).Decode(conf); err != nil {
			logger.FatalExit(err)
		}
	}

	// setup data path
	if _, err := os.Stat(conf.DataPath); err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(conf.DataPath, 0755); err != nil {
				logger.FatalExit(err)
			}
		} else {
			logger.FatalExit(err)
		}
	}

	// initialze the docker provider
	prov, err := docker.NewDockerProvider(logger, conf.DataPath, "freighter")
	if err != nil {
		logger.FatalExit(err)
	}

	// save current pid
	pid := setPid(logger, conf.DataPath)
	logger.Infof("freighterd (rev: %v, pid: %v) started", version, pid)

	// start the API server
	svr := server.NewServer(logger, prov, conf)
	svr.Start()
}

func setPid(logger *util.Logger, dataPath string) int {
	file, err := os.Create(path.Join(dataPath, "freighter.pid"))
	if err != nil {
		logger.FatalExit(err)
	}

	pid := os.Getpid()
	_, err = file.WriteString(strconv.Itoa(pid))
	if err != nil {
		logger.FatalExit(err)
	}

	err = file.Close()
	if err != nil {
		logger.FatalExit(err)
	}
	return pid
}

// given:
// - a certificate authority
// - a certificate signed by the CA
// - a private key for the certificate
// return a proper tls config that can be used with tls.NewListener to secure a
// net.Listener
func getTLSConfig(logger *util.Logger, caPath, certPath, keyPath string) *tls.Config {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		logger.FatalExit(fmt.Errorf(
			"Couldn't load X509 key pair (%s, %s): %s. Key encrypted?",
			certPath, keyPath, err))
	}
	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: []tls.Certificate{cert},
	}
	certPool := x509.NewCertPool()
	file, err := ioutil.ReadFile(caPath)
	if err != nil {
		logger.FatalExit(fmt.Errorf("Couldn't read CA certificate %s: %s", caPath, err))
	}
	certPool.AppendCertsFromPEM(file)

	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConfig.ClientCAs = certPool
	return tlsConfig
}
