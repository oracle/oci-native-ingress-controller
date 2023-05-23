#!/bin/sh
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
export GO111MODULE=on

cd tools >/dev/null
go install github.com/golangci/golangci-lint/cmd/golangci-lint
cd - >/dev/null

echo -n "Running golangci-lint: "
ERRS=$(golangci-lint run "$@" 2>&1 || true)
if [ -n "${ERRS}" ]; then
    echo "FAIL"
    echo "${ERRS}"
    echo
    exit 1
fi
echo "PASS"
echo
