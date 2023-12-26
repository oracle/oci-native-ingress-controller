#
#  OCI Native Ingress Controller
#
#  Copyright (c) 2023 Oracle America, Inc. and its affiliates.
#  Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
#

# For open source
FROM golang:1.19-alpine as builder

WORKDIR /workspace

COPY . ./

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
#RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GO111MODULE=on go build -mod vendor -a -o dist/onic ./main.go

# For Open source
FROM oraclelinux:7-slim

LABEL author="OKE Foundations Team"

WORKDIR /usr/local/bin/oci-native-ingress-controller

# copy license files
COPY LICENSE.txt .
COPY THIRD_PARTY_LICENSES.txt .

# Copy the manager binary
COPY --from=builder /workspace/dist/onic .

ENTRYPOINT ["/usr/local/bin/oci-native-ingress-controller/onic"]
