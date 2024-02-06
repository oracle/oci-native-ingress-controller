#
#  OCI Native Ingress Controller
#
#  Copyright (c) 2023 Oracle America, Inc. and its affiliates.
#  Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
#

IMAGE_REPO_NAME=oci-native-ingress-controller

ifeq "$(REGISTRY)" ""
	REGISTRY  ?= ghcr.io/oracle
else
	REGISTRY	?= ${REGISTRY}
endif


ifeq "$(VERSION)" ""
    BUILD := $(shell git describe --exact-match 2> /dev/null || git describe --match=$(git rev-parse --short=8 HEAD) --always --dirty --abbrev=8)
    # Allow overriding for release versions else just equal the build (git hash)
    VERSION ?= ${BUILD}
else
    VERSION   ?= ${VERSION}
endif

IMAGE_URL=$(REGISTRY)/$(IMAGE_REPO_NAME)
IMAGE_TAG=$(VERSION)
IMAGE_PATH=$(IMAGE_URL):$(VERSION)

SRC_DIRS := pkg
LDFLAGS?="-X $(go list -m)/main.version=${VERSION}"

.PHONY : lint test build

all: lint test build

lint:
	golangci-lint run

vet:
	go vet ./...

staticcheck:
	# install if doesn't exist `go install honnef.co/go/tools/cmd/staticcheck@latest`
	staticcheck ./...

# static code analysis
sca: lint vet staticcheck

.PHONY: test
test: ./build/test.sh
	@./build/test.sh $(SRC_DIRS)

.PHONY: clean
clean:
	@rm -rf dist

version:
	@echo ${VERSION}

# Currently only supports amd
build: ./main.go
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) GO111MODULE=on go build -mod vendor -a -o dist/onic ./main.go

image:
	docker build --build-arg goos=$(GOOS) --build-arg goarch=$(GOARCH) -t ${IMAGE_PATH} -f Dockerfile .

push:
	docker push ${IMAGE_PATH}

build-push: image push

.PHONY: coverage
coverage: test
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out > coverage.txt