FROM nitrousio/ubuntu-dind:14.04
MAINTAINER Nitrous.IO <eng@nitrous.io>

RUN apt-get update && apt-get install -y --no-install-recommends \
  build-essential \
  curl \
  git \
  libsqlite3-dev \
  mercurial \
  sqlite3 \
  sudo 

RUN mkdir /goroot && curl https://storage.googleapis.com/golang/go1.2.2.linux-amd64.tar.gz | tar xvzf - -C /goroot --strip-components=1
RUN mkdir /gopath
ENV GOROOT /goroot
ENV VENDOR_PATH /gopath/src/github.com/nitrous-io/freighter/_vendor
ENV GOPATH $VENDOR_PATH:/gopath
ENV PATH $PATH:$GOROOT/bin:$GOPATH/bin

ADD ./ /opt/src/github.com/nitrous-io/freighter

WORKDIR /opt/src/github.com/nitrous-io/freighter

RUN ["make", "build"]
RUN ["cp", "/opt/src/github.com/nitrous-io/freighter/build/freighterd", "/usr/local/sbin/"]

WORKDIR /gopath
RUN ["rm", "-r", "/opt/src/github.com/nitrous-io/freighter"]

EXPOSE 65333

CMD ["/usr/local/sbin/freighterd"]
 
