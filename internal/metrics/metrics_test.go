package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollector_AtomicMetrics(t *testing.T) {
	c := NewCollector(4, func() int { return 2 })

	c.IncSubmitted()
	c.IncSubmitted()
	c.IncDelivered()
	c.IncCancelled()
	c.IncActiveWorkers()

	snap := c.Snapshot()
	assert.Equal(t, int64(2), snap.OrdersSubmitted)
	assert.Equal(t, int64(1), snap.OrdersDelivered)
	assert.Equal(t, int64(1), snap.OrdersCancelled)
	assert.Equal(t, int64(1), snap.ActiveWorkers)
	assert.Equal(t, 2, snap.QueueDepth)
	assert.Equal(t, 4, snap.WorkerCount)

	c.DecActiveWorkers()
	assert.Equal(t, int64(0), c.Snapshot().ActiveWorkers)
}

func TestCollector_HTTPHandler(t *testing.T) {
	c := NewCollector(4, func() int { return 1 })
	c.IncSubmitted()

	handler := c.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var m Metrics
	err := json.NewDecoder(rr.Body).Decode(&m)
	require.NoError(t, err)
	assert.Equal(t, int64(1), m.OrdersSubmitted)
	assert.Equal(t, 1, m.QueueDepth)
	assert.Equal(t, 4, m.WorkerCount)
}
