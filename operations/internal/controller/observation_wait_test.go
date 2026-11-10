package controller

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestForegroundObservationWaitsForBackgroundHostCycle(t *testing.T) {
	h := &HTTP{hostCheckSlots: make(chan struct{}, 4)}
	background, err := h.acquireObservation(context.Background(), "host", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	acquired := make(chan func(), 1)
	failed := make(chan error, 1)
	go func() {
		release, err := h.waitForObservation(ctx, "host")
		if err != nil {
			failed <- err
			return
		}
		acquired <- release
	}()
	select {
	case <-acquired:
		t.Fatal("foreground overlapped the running host check")
	case err := <-failed:
		t.Fatal("normal contention became an error", err)
	case <-time.After(30 * time.Millisecond):
	}
	background()
	select {
	case release := <-acquired:
		release()
	case err := <-failed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("foreground did not continue after the host became available")
	}
	if len(h.hostCheckSlots) != 0 || len(h.observationHosts) != 0 {
		t.Fatal("foreground check leaked a slot or host gate")
	}
}

func TestWaitingObservationHonorsCancellationAtConcurrencyLimit(t *testing.T) {
	h := &HTTP{hostCheckSlots: make(chan struct{}, 4)}
	for n := 0; n < cap(h.hostCheckSlots); n++ {
		h.hostCheckSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.waitForObservation(ctx, "host"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not respect its request deadline: %v", err)
	}
	if len(h.hostCheckSlots) != cap(h.hostCheckSlots) || len(h.observationHosts) != 0 {
		t.Fatal("waiting changed the occupied slots or reserved the host")
	}
}

func TestCanceledForegroundWaitPreservesTheRunningHostGate(t *testing.T) {
	h := &HTTP{hostCheckSlots: make(chan struct{}, 4)}
	background, err := h.acquireObservation(context.Background(), "host", true)
	if err != nil {
		t.Fatal(err)
	}
	defer background()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = h.waitForObservation(ctx, "host"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not stop when canceled: %v", err)
	}
	if len(h.hostCheckSlots) != 1 || len(h.observationHosts) != 1 {
		t.Fatal("canceling a waiter released the active background check")
	}
}
