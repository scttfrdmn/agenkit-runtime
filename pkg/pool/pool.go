package pool

import (
	"context"
	"fmt"
	"sync"
)

// Pool manages a fixed-size array of VM slots on a single host.
type Pool struct {
	mu   sync.Mutex
	vms  []*VM
	warm int // target number of pre-started VMs in the ready state
}

// NewPool creates a Pool with size VM slots.
func NewPool(size int) *Pool {
	vms := make([]*VM, size)
	for i := range vms {
		vms[i] = NewVM(i)
	}
	return &Pool{vms: vms}
}

// Size returns the total number of slots.
func (p *Pool) Size() int { return len(p.vms) }

// VM returns the VM at slot index i.
// The caller must not mutate the returned VM concurrently with Pool operations.
func (p *Pool) VM(i int) *VM { return p.vms[i] }

// Available returns the number of VMs currently in the ready state.
func (p *Pool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, vm := range p.vms {
		if vm.State() == VMStateReady {
			n++
		}
	}
	return n
}

// Acquire reserves a ready VM for the given sessionID.
// Returns an error if no VM is available.
func (p *Pool) Acquire(sessionID string) (*VM, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, vm := range p.vms {
		if vm.State() == VMStateReady {
			if err := vm.Assign(sessionID); err != nil {
				continue
			}
			return vm, nil
		}
	}
	return nil, fmt.Errorf("no VMs available in pool")
}

// Drain transitions all currently-assigned VMs into drain mode.
func (p *Pool) Drain(ctx context.Context, mode DrainMode) {
	p.mu.Lock()
	snapshot := make([]*VM, len(p.vms))
	copy(snapshot, p.vms)
	p.mu.Unlock()

	for _, vm := range snapshot {
		if vm.State() == VMStateReady || vm.State() == VMStateDraining {
			_, _ = vm.Drain(ctx, mode)
		}
	}
}

// ActiveSessions returns a map of slot index to session ID for all VMs
// that currently have an assigned session.
func (p *Pool) ActiveSessions() map[int]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[int]string)
	for _, vm := range p.vms {
		if vm.sessionID != "" {
			m[vm.slotIndex] = vm.sessionID
		}
	}
	return m
}

// Stats returns a snapshot of per-state VM counts.
func (p *Pool) Stats() map[VMState]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[VMState]int)
	for _, vm := range p.vms {
		m[vm.State()]++
	}
	return m
}
