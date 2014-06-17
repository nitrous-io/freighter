FROM ubuntu:trusty
MAINTAINER Nitrous.IO <eng@nitrous.io>

RUN echo "deb http://archive.ubuntu.com/ubuntu trusty main universe" > /etc/apt/sources.list
RUN apt-get update

ADD ./build/freighterd /usr/local/sbin/freighterd

EXPOSE 65333

ENTRYPOINT ["/usr/local/sbin/freighterd"]
