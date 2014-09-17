NO_COLOR=\033[0m
OK_COLOR=\033[32;01m
ERROR_COLOR=\033[31;01m
WARN_COLOR=\033[33;01m
REVISION=$(shell git rev-parse --short HEAD)
BASE_VERSION=$(shell cat VERSION)
VERSION=$(BASE_VERSION)+$(BUILD_NUMBER)-$(REVISION)
APP=freighterd

all: build

install-deps:
	@script/install-deps

build:
	@echo "$(OK_COLOR)==> Building revision $(VERSION)...$(NO_COLOR)"
	@script/build $(APP) $(VERSION)

format:
	goop go fmt ./...

test:
	@echo "$(OK_COLOR)==> Testing...$(NO_COLOR)"
	@script/test $(TEST)

ci: image
	@echo "$(OK_COLOR)==> Running within docker...$(NO_COLOR)"
	@script/ci $(APP) $(VERSION)

image:
	@echo "$(OK_COLOR)==> Building image...$(NO_COLOR)"
	@script/image $(APP) $(VERSION)

run: build
	@echo "$(OK_COLOR)==> Running...$(NO_COLOR)"
	@./build/$(APP)

release: ci
	@echo "$(OK_COLOR)==> Releasing freighter version $(VERSION)...$(NO_COLOR)"
	@./script/wrap-release-docker $(APP) $(VERSION)

.PHONY: all install-deps build format test run release image
