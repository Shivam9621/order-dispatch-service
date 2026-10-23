package metrics

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Metrics holds runtime statistics updated atomically by workers and the API.
type Metrics struct {
	OrdersSubmitted int64 `json:"orders_submitted"`
	OrdersDelivered int64 `json:"orders_delivered"`
	OrdersCancelled int64 `json:"orders_cancelled"`
	ActiveWorkers   int64 `json:"active_workers"`
	QueueDepth      int   `json:"queue_depth"`
	WorkerCount     int   `json:"worker_count"`
}

// Collector provides thread-safe atomic counters for system telemetry.
type Collector struct {
	ordersSubmitted atomic.Int64
	ordersDelivered atomic.Int64
	ordersCancelled atomic.Int64
	activeWorkers   atomic.Int64
	queueDepthFunc  func() int
	workerCount     int
}

// NewCollector creates a new Collector.
func NewCollector(workerCount int, queueDepthFunc func() int) *Collector {
	return &Collector{
		workerCount:    workerCount,
		queueDepthFunc: queueDepthFunc,
	}
}

func (c *Collector) IncSubmitted() {
	c.ordersSubmitted.Add(1)
}

func (c *Collector) IncDelivered() {
	c.ordersDelivered.Add(1)
}

func (c *Collector) IncCancelled() {
	c.ordersCancelled.Add(1)
}

func (c *Collector) IncActiveWorkers() {
	c.activeWorkers.Add(1)
}

func (c *Collector) DecActiveWorkers() {
	c.activeWorkers.Add(-1)
}

// Snapshot returns a current view of metrics.
func (c *Collector) Snapshot() Metrics {
	depth := 0
	if c.queueDepthFunc != nil {
		depth = c.queueDepthFunc()
	}

	return Metrics{
		OrdersSubmitted: c.ordersSubmitted.Load(),
		OrdersDelivered: c.ordersDelivered.Load(),
		OrdersCancelled: c.ordersCancelled.Load(),
		ActiveWorkers:   c.activeWorkers.Load(),
		QueueDepth:      depth,
		WorkerCount:     c.workerCount,
	}
}

// Handler returns an HTTP handler for GET /metrics.
func (c *Collector) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(c.Snapshot())
	}
}
