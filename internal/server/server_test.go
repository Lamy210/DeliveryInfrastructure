package server

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHealthz(t *testing.T) {
    h := New(nil)
    req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
    if body := rr.Body.String(); body != "ok" {
        t.Fatalf("expected body 'ok', got %q", body)
    }
}

func TestGetRates(t *testing.T) {
    h := New(nil)
    req := httptest.NewRequest(http.MethodGet, "/rates?from_country=US&to_country=US&weight_oz=16&carrier_code=ups", nil)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
    var res struct {
        Currency string  `json:"currency"`
        Amount   float64 `json:"amount"`
        Carrier  string  `json:"carrier"`
    }
    if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
        t.Fatalf("failed to unmarshal: %v", err)
    }
    if res.Currency != "USD" || res.Carrier != "ups" {
        t.Fatalf("unexpected response: %+v", res)
    }
    // Expected amount: base 5 + weight (16*0.5=8) = 13 (same country, no extra), ups no surcharge
    if res.Amount < 12.9 || res.Amount > 13.1 {
        t.Fatalf("unexpected amount: %v", res.Amount)
    }
}

func TestRequestIDHeaderPresent(t *testing.T) {
    h := New(nil)
    req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
    if rid := rr.Header().Get("X-Request-ID"); rid == "" {
        t.Fatalf("expected X-Request-ID header to be set")
    }
}