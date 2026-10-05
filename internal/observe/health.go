package observe

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

type Health struct {
	ready atomic.Bool
}

func (h *Health) SetReady(value bool) { h.ready.Store(value) }

func (h *Health) Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *Health) Ready(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	body := map[string]any{"ready": true}
	if !h.ready.Load() {
		status = http.StatusServiceUnavailable
		body["ready"] = false
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
