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
	@script/docker-wrap script/test $(TEST)

citest:
	@echo "$(OK_COLOR)==> Testing...$(NO_COLOR)"
	@script/run-test

run: build
	@echo "$(OK_COLOR)==> Running...$(NO_COLOR)"
	@build/$(BIN_NAME)

.PHONY: all install-deps build format test run
