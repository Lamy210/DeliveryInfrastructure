package server

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "testing"

    "deliveryinfra/internal/db"
)

func TestPostTrackerEventAndGet(t *testing.T) {
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        t.Skip("DATABASE_URL not set; skipping integration test")
        return
    }

    pool, err := db.NewPool(t.Context(), dbURL)
    if err != nil {
        t.Fatalf("failed to connect db: %v", err)
    }
    defer pool.Close()

    h := New(pool)

    code := "ITESTTRACK001"

    // Post event (tracker will be auto-created if absent)
    payload := map[string]any{
        "status":      "in_transit",
        "description": "Departed facility",
        "location":    map[string]any{"country": "US"},
        "occurred_at": "",
        "raw":         map[string]any{"carrier": "TEST"},
    }
    body, _ := json.Marshal(payload)
    req := httptest.NewRequest(http.MethodPost, "/trackers/"+code+"/events", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
    }

    // Get tracker and verify last event status
    req2 := httptest.NewRequest(http.MethodGet, "/trackers/"+code, nil)
    rr2 := httptest.NewRecorder()
    h.ServeHTTP(rr2, req2)
    if rr2.Code != http.StatusOK {
        t.Fatalf("expected 200 on get, got %d; body=%s", rr2.Code, rr2.Body.String())
    }
    var res struct{
        Code        string          `json:"code"`
        Status      string          `json:"status"`
        LastEventAt string          `json:"last_event_at"`
        LastEvent   json.RawMessage `json:"last_event"`
    }
    if err := json.Unmarshal(rr2.Body.Bytes(), &res); err != nil {
        t.Fatalf("unmarshal failed: %v", err)
    }
    if res.Code != code || res.Status != "in_transit" || res.LastEventAt == "" || len(res.LastEvent) == 0 {
        t.Fatalf("unexpected tracker response: %+v", res)
    }
}