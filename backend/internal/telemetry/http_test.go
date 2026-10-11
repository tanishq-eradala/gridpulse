package telemetry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPIngestionAndPrometheusMetrics(t *testing.T) {
	store := testStore(t)
	broker := NewBroker()
	handler := API{Store: store, Broker: broker}.Handler()
	post := func(reading Reading) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(reading)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/readings", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	bad := sample(0, 300)
	if got := post(bad).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid reading status=%d", got)
	}
	unknown := sample(0, 65)
	unknown.EquipmentID = "missing-unit"
	if got := post(unknown).Code; got != http.StatusNotFound {
		t.Fatalf("unknown equipment status=%d", got)
	}
	ch := broker.subscribe()
	defer broker.unsubscribe(ch)
	good := sample(1, 65)
	if got := post(good).Code; got != http.StatusOK {
		t.Fatalf("valid reading status=%d", got)
	}
	select {
	case <-ch:
	default:
		t.Fatal("broker did not publish ingested reading")
	}
	duplicate := post(good)
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"inserted":false`) {
		t.Fatalf("duplicate response=%d %s", duplicate.Code, duplicate.Body.String())
	}
	select {
	case <-ch:
		t.Fatal("duplicate was published")
	default:
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), "gridpulse_readings_total 1") {
		t.Fatalf("metrics=%s", recorder.Body.String())
	}
}

func TestConfiguredOriginOnlyAllowsExactMatch(t *testing.T) {
	handler := API{Store: testStore(t), AllowedOrigin: "http://127.0.0.1:5186"}.Handler()
	for _, origin := range []string{"http://127.0.0.1:5186", "https://example.invalid", ""} {
		request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		got := recorder.Header().Get("Access-Control-Allow-Origin")
		if origin == "http://127.0.0.1:5186" && got != origin {
			t.Fatalf("configured origin was not allowed: %q", got)
		}
		if origin != "http://127.0.0.1:5186" && got != "" {
			t.Fatalf("unexpected origin allowed: %q", got)
		}
	}
}
