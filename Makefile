# Package configuration
PROJECT = borges-export

DOCKER_ORG = erizocosmico

# Including ci Makefile
CI_REPOSITORY ?= https://github.com/src-d/ci.git
CI_PATH ?= $(shell pwd)/.ci
CI_VERSION ?= v1

MAKEFILE := $(CI_PATH)/Makefile.main
$(MAKEFILE):
	git clone --quiet --branch $(CI_VERSION) --depth 1 $(CI_REPOSITORY) $(CI_PATH);

-include $(MAKEFILE)

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags norwfs export.go
