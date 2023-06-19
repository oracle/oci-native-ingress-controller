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
	"github.com/prometheus/client_golang/prometheus"
)

type IngressCollector struct {
	prometheus.Collector

	ingressSyncCount           prometheus.Gauge
	ingressSyncTime            prometheus.Gauge
	ingressStateBuildTime      prometheus.Gauge
	ingressBackendCreationTime prometheus.Gauge
	ingressBackendSyncTime     prometheus.Gauge
	ingressListenerSyncTime    prometheus.Gauge

	ingressAddCount    prometheus.Gauge
	ingressUpdateCount prometheus.Gauge
	ingressDeleteCount prometheus.Gauge

	constLabels prometheus.Labels
	labels      prometheus.Labels
}

func NewIngressCollector(controllerClass string, reg *prometheus.Registry) *IngressCollector {
	if reg == nil {
		return nil
	}
	constLabels := prometheus.Labels{
		"controller_class": controllerClass,
	}

	ic := &IngressCollector{
		constLabels: constLabels,
		labels: prometheus.Labels{
			"class": controllerClass,
		},
		ingressSyncCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_sync_count",
				Help:        "The count of ingress sync carried out by ingress controller",
				ConstLabels: constLabels,
			}),
		ingressSyncTime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_sync_time",
				Help:        "The time taken for ingress sync carried out by ingress controller",
				ConstLabels: constLabels,
			}),
		ingressStateBuildTime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_state_build_time",
				Help:        "The processing duration of building ingress state",
				ConstLabels: constLabels,
			}),
		ingressBackendCreationTime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_backend_creation_time",
				Help:        "The processing duration of building backend in LB",
				ConstLabels: constLabels,
			}),
		ingressBackendSyncTime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_backend_sync_time",
				Help:        "The duration of syncing backend in LB",
				ConstLabels: constLabels,
			}),
		ingressListenerSyncTime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_listener_sync_time",
				Help:        "The duration of syncing listener in LB",
				ConstLabels: constLabels,
			}),
		ingressAddCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_addition_count",
				Help:        "The count of new ingress add operation",
				ConstLabels: constLabels,
			}),
		ingressUpdateCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_updation_count",
				Help:        "The count of ingress update operation",
				ConstLabels: constLabels,
			}),
		ingressDeleteCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "ingress_deletion_count",
				Help:        "The count of delete ingress operation",
				ConstLabels: constLabels,
			}),
	}
	reg.MustRegister(ic)
	return ic
}

func (ic *IngressCollector) IncrementSyncCount() {
	ic.ingressSyncCount.Inc()
}

func (ic *IngressCollector) AddIngressSyncTime(f float64) {
	ic.ingressSyncTime.Set(f)
}
func (ic *IngressCollector) AddStateBuildTime(f float64) {
	ic.ingressStateBuildTime.Set(f)
}

func (ic *IngressCollector) AddBackendCreationTime(f float64) {
	ic.ingressBackendCreationTime.Set(f)
}

func (ic *IngressCollector) AddIngressBackendSyncTime(f float64) {
	ic.ingressBackendSyncTime.Set(f)
}

func (ic *IngressCollector) AddIngressListenerSyncTime(f float64) {
	ic.ingressListenerSyncTime.Set(f)
}

func (ic *IngressCollector) IncrementIngressAddOperation() {
	ic.ingressAddCount.Inc()
}

func (ic *IngressCollector) IncrementIngressUpdateOperation() {
	ic.ingressUpdateCount.Inc()

}

func (ic *IngressCollector) IncrementIngressDeleteOperation() {
	ic.ingressDeleteCount.Inc()
}

// Describe implements prometheus.Collector
func (ic IngressCollector) Describe(ch chan<- *prometheus.Desc) {
	ic.ingressSyncCount.Describe(ch)
	ic.ingressSyncTime.Describe(ch)
	ic.ingressStateBuildTime.Describe(ch)
	ic.ingressBackendCreationTime.Describe(ch)
	ic.ingressBackendSyncTime.Describe(ch)
	ic.ingressListenerSyncTime.Describe(ch)
	ic.ingressAddCount.Describe(ch)
	ic.ingressUpdateCount.Describe(ch)
	ic.ingressDeleteCount.Describe(ch)
}

// Collect implements the prometheus.Collector interface.
func (ic IngressCollector) Collect(ch chan<- prometheus.Metric) {
	ic.ingressSyncCount.Collect(ch)
	ic.ingressSyncTime.Collect(ch)
	ic.ingressStateBuildTime.Collect(ch)
	ic.ingressBackendCreationTime.Collect(ch)
	ic.ingressBackendSyncTime.Collect(ch)
	ic.ingressListenerSyncTime.Collect(ch)
	ic.ingressAddCount.Collect(ch)
	ic.ingressUpdateCount.Collect(ch)
	ic.ingressDeleteCount.Collect(ch)
}
