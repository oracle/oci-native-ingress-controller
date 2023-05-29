/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package metric

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/klog/v2"
)

const prometheusExporter = "prometheus"
const EndpointPath = "/metric"

func InitMetricsExporter(metricsBackend string) (*prometheus.Registry, error) {
	klog.Infof("Initializing metrics backend : %s", metricsBackend)
	reg := prometheus.NewRegistry()
	switch metricsBackend {
	// Prometheus is the only exporter for now
	case prometheusExporter:
		registerCollectors(reg)
	default:
		return nil, fmt.Errorf("unsupported metrics backend %v", metricsBackend)
	}
	return reg, nil
}
