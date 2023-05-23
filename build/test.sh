#!/bin/bash
#
# /*
# ** OCI Native Ingress Controller
# **
# ** Copyright (c) 2023 Oracle America, Inc. and its affiliates.
# ** Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
#  */
#

set -o errexit
set -o nounset
set -o pipefail

export CGO_ENABLED=0
export GO111MODULE=off

TARGETS=$(for d in "$@"; do echo ./$d/...; done)

echo "Running tests..."
go test -coverprofile=coverage.out -v -installsuffix "static" ${TARGETS}