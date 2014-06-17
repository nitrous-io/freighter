FROM ubuntu:trusty
MAINTAINER Nitrous.IO <eng@nitrous.io>

ADD ./build/freighterd /usr/local/sbin/freighterd

EXPOSE 65333

ENTRYPOINT ["/usr/local/sbin/freighterd"]
