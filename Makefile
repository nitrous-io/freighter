NO_COLOR=\033[0m
OK_COLOR=\033[32;01m
ERROR_COLOR=\033[31;01m
WARN_COLOR=\033[33;01m
BIN_NAME=freighterd
REVISION=$(shell git rev-parse --short HEAD)
BASE_VERSION=$(shell cat VERSION)
VERSION=$(BASE_VERSION)-$(REVISION)

all: build

install-deps:
	@script/install-deps

build:
	@echo "$(OK_COLOR)==> Building revision $(VERSION)...$(NO_COLOR)"
	@script/build $(VERSION)

release:
	@echo "$(OK_COLOR)==> Building revision $(VERSION)...$(NO_COLOR)"
	@./script/run-build $(VERSION)
	@echo "$(OK_COLOR)==> Releasing revision $(VERSION)...$(NO_COLOR)"
	@./script/run-release $(VERSION)
.PHONY: release

format:
	goop go fmt ./...

test:
	@echo "$(OK_COLOR)==> Testing...$(NO_COLOR)"
	@script/test $(TEST)

ci: image
	@echo "$(OK_COLOR)==> Running within docker...$(NO_COLOR)"
	@script/ci $(VERSION)

image:
	@echo "$(OK_COLOR)==> Building image...$(NO_COLOR)"
	@script/image $(VERSION)

run: build
	@echo "$(OK_COLOR)==> Running...$(NO_COLOR)"
	@.vendor/bin/$(BIN_NAME)

certs:
	$(eval CA ?= /etc/freighterd/ca.crt)
	$(eval KEY ?= /etc/freighterd/key.crt)
	$(eval CERT ?= /etc/freighterd/cert.crt)
	@echo "$(OK_COLOR)==> Generating certs...$(NO_COLOR)"
	openssl req -new \
		-days 30 -x509 -extensions v3_ca -subj "/CN=freighterd.local/" \
		-newkey rsa:2048 -nodes -keyout $(KEY) \
		-out $(CA)
	openssl req -new \
		-days 30 -x509 -subj "/CN=freighterd.local/" \
		-key $(KEY) \
		-out $(CERT) >/dev/null

.PHONY: all install-deps build format test run
