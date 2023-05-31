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
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

const metricsEndpoint = "/metrics"

func registerCollectors(reg *prometheus.Registry) {
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{
		PidFn:        func() (int, error) { return os.Getpid(), nil },
		ReportErrors: true,
	}))
}

func RegisterMetrics(reg *prometheus.Registry, mux *http.ServeMux) {
	mux.Handle(
		metricsEndpoint,
		promhttp.InstrumentMetricHandler(
			reg,
			promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		),
	)
}

func ServeMetrics(port int, mux *http.ServeMux) {
	klog.Infof("Metrics server listening, address %s", strconv.Itoa(port)+EndpointPath)
	go func() {
		server := &http.Server{
			Addr:              fmt.Sprintf(":%v", port),
			Handler:           mux,
			ReadTimeout:       10 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      300 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		err := server.ListenAndServe()
		if err != nil {
			klog.Fatalf("Metrics: listen and server error: %w", err)
		}
	}()
}
