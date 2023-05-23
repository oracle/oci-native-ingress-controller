/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */
package ingress

import (
	"testing"

	"bitbucket.oci.oraclecorp.com/oke/oci-native-ingress-controller/pkg/util"
	. "github.com/onsi/gomega"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/loadbalancer"
)

func TestCompareSameTcpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := util.GetDefaultHeathChecker()
	healthChecker := &loadbalancer.HealthChecker{
		Protocol:         common.String(util.DefaultHealthCheckProtocol),
		Port:             common.Int(util.DefaultHealthCheckPort),
		TimeoutInMillis:  common.Int(util.DefaultHealthCheckTimeOutMilliSeconds),
		IntervalInMillis: common.Int(util.DefaultHealthCheckIntervalMilliSeconds),
		Retries:          common.Int(util.DefaultHealthCheckRetries),
	}
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(true))
}

func TestCompareDifferentTcpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := util.GetDefaultHeathChecker()
	details.Port = common.Int(7070)

	healthChecker := &loadbalancer.HealthChecker{
		Protocol:         common.String(util.DefaultHealthCheckProtocol),
		Port:             common.Int(util.DefaultHealthCheckPort),
		TimeoutInMillis:  common.Int(util.DefaultHealthCheckTimeOutMilliSeconds),
		IntervalInMillis: common.Int(util.DefaultHealthCheckIntervalMilliSeconds),
		Retries:          common.Int(util.DefaultHealthCheckRetries),
	}
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(false))
}

func TestCompareSameHttpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := getHeathCheckerDetails()
	healthChecker := getHeathChecker()
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(true))
}

func TestCompareDifferentHttpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)

	details := getHeathCheckerDetails()
	healthChecker := getHeathChecker()
	healthChecker.Port = common.Int(9090)
	healthChecker.ResponseBodyRegex = common.String("/")

	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(false))
}

func TestCompareTcpAndHttpHealthCheckers(t *testing.T) {
	RegisterTestingT(t)
	details := util.GetDefaultHeathChecker()
	healthChecker := getHeathChecker()
	val := compareHealthCheckers(details, healthChecker)
	Expect(val).Should(Equal(false))
}

func getHeathCheckerDetails() *loadbalancer.HealthCheckerDetails {
	return &loadbalancer.HealthCheckerDetails{
		Protocol:          common.String(util.ProtocolHTTP),
		UrlPath:           common.String("/health"),
		Port:              common.Int(8080),
		ReturnCode:        common.Int(200),
		Retries:           common.Int(3),
		TimeoutInMillis:   common.Int(5000),
		IntervalInMillis:  common.Int(10000),
		ResponseBodyRegex: common.String("*"),
	}
}

func getHeathChecker() *loadbalancer.HealthChecker {
	return &loadbalancer.HealthChecker{
		Protocol:          common.String(util.ProtocolHTTP),
		UrlPath:           common.String("/health"),
		Port:              common.Int(8080),
		ReturnCode:        common.Int(200),
		Retries:           common.Int(3),
		TimeoutInMillis:   common.Int(5000),
		IntervalInMillis:  common.Int(10000),
		ResponseBodyRegex: common.String("*"),
	}
}
