package pool

import (
	"context"
	"testing"
	"time"
)

func TestVMStateTransitions(t *testing.T) {
	vm := NewVM(0)

	if vm.State() != VMStateAbsent {
		t.Fatalf("expected absent, got %s", vm.State())
	}

	// absent → provisioned
	if err := vm.Provision(1234); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if vm.State() != VMStateProvisioned {
		t.Fatalf("expected provisioned, got %s", vm.State())
	}

	// provisioned → ready
	if err := vm.MarkReady(); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if vm.State() != VMStateReady {
		t.Fatalf("expected ready, got %s", vm.State())
	}

	// ready → draining (graceful has deadline=0 so transitions to idle immediately)
	ctx := context.Background()
	done, err := vm.Drain(ctx, DrainGraceful)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if vm.State() != VMStateDraining {
		t.Fatalf("expected draining immediately after Drain(), got %s", vm.State())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not complete in time")
	}
	if vm.State() != VMStateIdle {
		t.Fatalf("expected idle after drain completes, got %s", vm.State())
	}

	// idle → deprovisioned
	if err := vm.Deprovision(); err != nil {
		t.Fatalf("deprovision: %v", err)
	}
	if vm.State() != VMStateDeprovisioned {
		t.Fatalf("expected deprovisioned, got %s", vm.State())
	}
}

func TestVMInvalidTransitions(t *testing.T) {
	vm := NewVM(0)

	// cannot mark ready from absent
	if err := vm.MarkReady(); err == nil {
		t.Fatal("expected error marking absent VM ready")
	}

	// cannot drain from absent
	if _, err := vm.Drain(context.Background(), DrainGraceful); err == nil {
		t.Fatal("expected error draining absent VM")
	}

	// cannot deprovision from absent
	if err := vm.Deprovision(); err == nil {
		t.Fatal("expected error deprovisioning absent VM")
	}

	// provision once, try to provision again from provisioned state
	if err := vm.Provision(1); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := vm.Provision(2); err == nil {
		t.Fatal("expected error provisioning already-provisioned VM")
	}
}

func TestPoolAcquireRelease(t *testing.T) {
	p := NewPool(3)

	// nothing ready yet
	if _, err := p.Acquire("session-1"); err == nil {
		t.Fatal("expected error acquiring from empty pool")
	}

	// provision and mark slot 0 ready
	if err := p.VM(0).Provision(100); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if err := p.VM(0).MarkReady(); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	vm, err := p.Acquire("session-1")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if vm.SessionID() != "session-1" {
		t.Fatalf("expected session-1, got %s", vm.SessionID())
	}

	// NOTE: Acquire does not change VM state; slot remains VMStateReady.
	// Drain the slot to transition it out of service.
	ctx := context.Background()
	done, err := vm.Drain(ctx, DrainGraceful)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain timeout")
	}
	if err := vm.Deprovision(); err != nil {
		t.Fatalf("deprovision: %v", err)
	}
	// Slot is now deprovisioned → pool has no ready VMs
	if _, err := p.Acquire("no-ready"); err == nil {
		t.Fatal("expected error after slot drained and deprovisioned")
	}

	// Re-provision makes slot available again
	if err := vm.Provision(101); err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	if err := vm.MarkReady(); err != nil {
		t.Fatalf("re-mark ready: %v", err)
	}
	if _, err := p.Acquire("session-2"); err != nil {
		t.Fatalf("acquire after re-provision: %v", err)
	}
}

func TestPoolStats(t *testing.T) {
	p := NewPool(4)

	// all absent
	stats := p.Stats()
	if stats[VMStateAbsent] != 4 {
		t.Fatalf("expected 4 absent, got %d", stats[VMStateAbsent])
	}

	// provision slots 0 and 1
	for i := 0; i < 2; i++ {
		if err := p.VM(i).Provision(i + 1); err != nil {
			t.Fatalf("provision slot %d: %v", i, err)
		}
	}
	stats = p.Stats()
	if stats[VMStateAbsent] != 2 || stats[VMStateProvisioned] != 2 {
		t.Fatalf("unexpected stats after provision: %+v", stats)
	}

	// mark slot 0 ready
	if err := p.VM(0).MarkReady(); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	stats = p.Stats()
	if stats[VMStateReady] != 1 || stats[VMStateProvisioned] != 1 || stats[VMStateAbsent] != 2 {
		t.Fatalf("unexpected stats after mark ready: %+v", stats)
	}
}

func TestPoolActiveSessions(t *testing.T) {
	p := NewPool(3)

	// provision and ready all slots
	for i := 0; i < 3; i++ {
		if err := p.VM(i).Provision(i + 1); err != nil {
			t.Fatalf("provision slot %d: %v", i, err)
		}
		if err := p.VM(i).MarkReady(); err != nil {
			t.Fatalf("mark ready slot %d: %v", i, err)
		}
	}

	if sessions := p.ActiveSessions(); len(sessions) != 0 {
		t.Fatalf("expected 0 active sessions, got %d", len(sessions))
	}

	// Assign sessions directly to specific slots (bypassing Pool.Acquire which
	// always picks the first available slot in order).
	if err := p.VM(0).Assign("session-a"); err != nil {
		t.Fatalf("assign slot 0: %v", err)
	}
	if err := p.VM(2).Assign("session-b"); err != nil {
		t.Fatalf("assign slot 2: %v", err)
	}

	sessions := p.ActiveSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 active sessions, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0] != "session-a" {
		t.Errorf("slot 0: expected session-a, got %s", sessions[0])
	}
	if sessions[2] != "session-b" {
		t.Errorf("slot 2: expected session-b, got %s", sessions[2])
	}
}

func TestPoolDrain(t *testing.T) {
	p := NewPool(3)

	// provision and ready all 3 slots
	for i := 0; i < 3; i++ {
		if err := p.VM(i).Provision(i + 1); err != nil {
			t.Fatalf("provision slot %d: %v", i, err)
		}
		if err := p.VM(i).MarkReady(); err != nil {
			t.Fatalf("mark ready slot %d: %v", i, err)
		}
	}

	// assign a session to slot 0
	if err := p.VM(0).Assign("session-x"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	ctx := context.Background()
	p.Drain(ctx, DrainGraceful)

	// DrainGraceful has deadline=0: goroutines complete immediately.
	time.Sleep(50 * time.Millisecond)

	stats := p.Stats()
	if stats[VMStateIdle] != 3 {
		t.Fatalf("expected 3 idle after drain, got stats: %+v", stats)
	}
}

func TestPoolAvailable(t *testing.T) {
	p := NewPool(5)

	if p.Available() != 0 {
		t.Fatalf("expected 0 available before provisioning, got %d", p.Available())
	}

	for i := 0; i < 3; i++ {
		if err := p.VM(i).Provision(i + 1); err != nil {
			t.Fatalf("provision slot %d: %v", i, err)
		}
		if err := p.VM(i).MarkReady(); err != nil {
			t.Fatalf("mark ready slot %d: %v", i, err)
		}
	}

	if p.Available() != 3 {
		t.Fatalf("expected 3 available, got %d", p.Available())
	}

	// Acquire does NOT change VM state; Available() still reflects VMStateReady count.
	if _, err := p.Acquire("session-q"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if p.Available() != 3 {
		t.Fatalf("expected 3 available after acquire (state unchanged), got %d", p.Available())
	}

	// Drain slot 0 → moves to idle → Available drops by 1.
	done, err := p.VM(0).Drain(context.Background(), DrainGraceful)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain timeout")
	}
	if p.Available() != 2 {
		t.Fatalf("expected 2 available after draining slot 0, got %d", p.Available())
	}
}
