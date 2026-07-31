/*
 *
 * * OCI Native Ingress Controller
 * *
 * * Copyright (c) 2023 Oracle America, Inc. and its affiliates.
 * * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 *
 */

package loadbalancer

import (
	"testing"
	"time"
)

func TestKeyedMutexSerializesSameKey(t *testing.T) {
	var locks keyedMutex
	unlockFirst := locks.lock("lb-1")

	acquired := make(chan struct{})
	go func() {
		unlockSecond := locks.lock("lb-1")
		close(acquired)
		unlockSecond()
	}()

	acquiredEarly := false
	select {
	case <-acquired:
		acquiredEarly = true
	case <-time.After(25 * time.Millisecond):
	}

	unlockFirst()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second lock for the same key was not released")
	}
	if acquiredEarly {
		t.Fatal("second lock for the same key acquired before the first was released")
	}
}

func TestKeyedMutexAllowsDifferentKeys(t *testing.T) {
	var locks keyedMutex
	unlockFirst := locks.lock("lb-1")
	defer unlockFirst()

	acquired := make(chan struct{})
	go func() {
		unlockSecond := locks.lock("lb-2")
		close(acquired)
		unlockSecond()
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("lock for a different key was blocked")
	}
}
