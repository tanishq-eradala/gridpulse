package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Broker struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewBroker() *Broker { return &Broker{clients: map[chan []byte]struct{}{}} }
func (b *Broker) subscribe() chan []byte {
	ch := make(chan []byte, 16)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}
func (b *Broker) unsubscribe(ch chan []byte) { b.mu.Lock(); delete(b.clients, ch); b.mu.Unlock() }
func (b *Broker) publish(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- data:
		default: /* slow clients catch up on refresh */
		}
	}
}

type API struct {
	Store         *Store
	Broker        *Broker
	AllowedOrigin string
}

func (a API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/equipment", a.equipment)
	mux.HandleFunc("GET /api/readings", a.readings)
	mux.HandleFunc("POST /api/readings", a.ingest)
	mux.HandleFunc("GET /api/alerts", a.alerts)
	mux.HandleFunc("GET /api/summary", a.summary)
	mux.HandleFunc("GET /api/stream", a.stream)
	mux.HandleFunc("GET /metrics", a.metrics)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:5174" || origin == "http://127.0.0.1:5174" || (a.AllowedOrigin != "" && origin == a.AllowedOrigin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, ErrUnknownEquipment):
		status = http.StatusNotFound
		message = err.Error()
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
		message = err.Error()
	}
	respond(w, status, map[string]string{"error": message})
}
func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n < 1 || n > 500 {
		return 100
	}
	return n
}
func (a API) equipment(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.Equipment(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, items)
}
func (a API) readings(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("equipmentId")
	if id == "" {
		fail(w, fmt.Errorf("%w: equipmentId required", ErrInvalid))
		return
	}
	items, err := a.Store.Readings(r.Context(), id, limitParam(r))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, items)
}
func (a API) alerts(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.Alerts(r.Context(), limitParam(r))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, items)
}
func (a API) summary(w http.ResponseWriter, r *http.Request) {
	m, err := a.Store.Summary(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, m)
}
func (a API) ingest(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, fmt.Errorf("%w: JSON required", ErrInvalid))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var reading Reading
	if err := decoder.Decode(&reading); err != nil {
		fail(w, fmt.Errorf("%w: %v", ErrInvalid, err))
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(w, fmt.Errorf("%w: one object expected", ErrInvalid))
		return
	}
	inserted, changed, err := a.Store.Ingest(r.Context(), reading)
	if err != nil {
		fail(w, err)
		return
	}
	if inserted && a.Broker != nil {
		a.Broker.publish(map[string]any{"type": "reading", "reading": reading, "alerts": changed})
	}
	respond(w, http.StatusOK, map[string]any{"inserted": inserted, "alerts": changed})
}
func (a API) stream(w http.ResponseWriter, r *http.Request) {
	if a.Broker == nil {
		fail(w, errors.New("stream unavailable"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, errors.New("stream unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := a.Broker.subscribe()
	defer a.Broker.unsubscribe(ch)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
func (a API) metrics(w http.ResponseWriter, r *http.Request) {
	m, err := a.Store.Summary(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# TYPE gridpulse_readings_total counter\ngridpulse_readings_total %d\n", m.TotalReadings)
	fmt.Fprintf(w, "# TYPE gridpulse_active_alerts gauge\ngridpulse_active_alerts %d\n", m.ActiveAlerts)
	fmt.Fprintf(w, "# TYPE gridpulse_equipment gauge\ngridpulse_equipment %d\n", m.Equipment)
}
