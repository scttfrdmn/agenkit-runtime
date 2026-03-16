// Package metrics defines the Prometheus metrics exported by agenkit-runtime.
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// PoolVMSlots tracks the current number of VM slots per host and state.
	PoolVMSlots = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "agenkit_pool_vm_slots",
		Help: "Current VM slots per host and state.",
	}, []string{"host", "state"})

	// MigrationSessionsTotal counts cumulative migration sessions by outcome.
	MigrationSessionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agenkit_migration_sessions_total",
		Help: "Cumulative migration sessions by outcome.",
	}, []string{"status"}) // "pending" | "failed"

	// SnapshotOpsTotal counts snapshot operations by type and outcome.
	SnapshotOpsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agenkit_snapshot_ops_total",
		Help: "Snapshot operations by type and outcome.",
	}, []string{"operation", "status"})
)

// Register registers all agenkit-runtime metrics with the default Prometheus
// registry. Call this once at daemon startup before serving /metrics.
func Register() {
	prometheus.MustRegister(PoolVMSlots, MigrationSessionsTotal, SnapshotOpsTotal)
}
