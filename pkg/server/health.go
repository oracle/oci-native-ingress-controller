/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
)

type HealthChecker struct {
	mu               sync.RWMutex
	cachesSynced     atomic.Bool
	controllersReady atomic.Bool
}

type HealthStatus struct {
	Status           string `json:"status"`
	CachesSynced     bool   `json:"cachesSynced"`
	ControllersReady bool   `json:"controllersReady"`
}

var globalHealthChecker *HealthChecker

func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		cachesSynced:     atomic.Bool{},
		controllersReady: atomic.Bool{},
	}
}

func GetHealthChecker() *HealthChecker {
	if globalHealthChecker == nil {
		globalHealthChecker = NewHealthChecker()
	}
	return globalHealthChecker
}

func (hc *HealthChecker) SetCachesSynced(synced bool) {
	hc.cachesSynced.Store(synced)
}

func (hc *HealthChecker) SetControllersReady(ready bool) {
	hc.controllersReady.Store(ready)
}

// Readiness check - pod should receive traffic
func (hc *HealthChecker) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	status := HealthStatus{
		Status:           "unhealthy",
		CachesSynced:     hc.cachesSynced.Load(),
		ControllersReady: hc.controllersReady.Load(),
	}

	if status.CachesSynced && status.ControllersReady {
		status.Status = "healthy"
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Liveness check - pod should be restarted if unhealthy
func (hc *HealthChecker) HandleLiveness(w http.ResponseWriter, r *http.Request) {
	status := HealthStatus{
		Status:           "alive",
		CachesSynced:     hc.cachesSynced.Load(),
		ControllersReady: hc.controllersReady.Load(),
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
