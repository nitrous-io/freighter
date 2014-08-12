FROM nitrousio/ubuntu-dind:14.04
MAINTAINER Nitrous.IO <eng@nitrous.io>

ENV GOPATH /opt
ENV PATH /opt/bin:/usr/local/go/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
  build-essential \
  curl \
  git-core \
  mercurial

RUN curl -s https://storage.googleapis.com/golang/go1.3.linux-amd64.tar.gz \
  | tar -C /usr/local -xz

RUN go get github.com/nitrous-io/goop

ADD .buildkey /root/.ssh/id_rsa
RUN chmod 0600 /root/.ssh/id_rsa

ADD util/ssh_config /etc/ssh/ssh_config
ADD . /opt/src/github.com/nitrous-io/freighter

EXPOSE 65333

WORKDIR /opt/src/github.com/nitrous-io/freighter

RUN mkdir -p /etc/freighterd
RUN make certs CA=/etc/freighterd/ca.crt CERT=/etc/freighterd/cert.crt KEY=/etc/freighterd/key.crt

RUN goop install && goop exec make build
CMD make run
