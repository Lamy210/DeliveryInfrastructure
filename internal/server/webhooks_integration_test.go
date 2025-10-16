package server

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "testing"

    "deliveryinfra/internal/db"
)

func TestWebhookDummy_IngestsEvent(t *testing.T) {
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        t.Skip("DATABASE_URL not set; skipping integration test")
        return
    }
    secret := "testsecret"
    os.Setenv("DUMMY_WEBHOOK_SECRET", secret)

    pool, err := db.NewPool(t.Context(), dbURL)
    if err != nil {
        t.Fatalf("failed to connect db: %v", err)
    }
    defer pool.Close()

    h := New(pool)

    code := "WHTEST001"
    payload := map[string]any{
        "code":        code,
        "status":      "in_transit",
        "description": "Arrived at facility",
        "location":    map[string]any{"country": "US"},
        "occurred_at": "",
    }
    body, _ := json.Marshal(payload)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    sig := hex.EncodeToString(mac.Sum(nil))

    req := httptest.NewRequest(http.MethodPost, "/webhooks/dummy", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Signature", sig)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
    }

    // Verify tracker exists and last event status
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

func TestWebhookDummy_InvalidSignature(t *testing.T) {
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        t.Skip("DATABASE_URL not set; skipping integration test")
        return
    }
    os.Setenv("DUMMY_WEBHOOK_SECRET", "testsecret")

    pool, err := db.NewPool(t.Context(), dbURL)
    if err != nil {
        t.Fatalf("failed to connect db: %v", err)
    }
    defer pool.Close()

    h := New(pool)

    payload := map[string]any{
        "code":        "WHTEST002",
        "status":      "in_transit",
        "description": "Departed",
        "location":    map[string]any{"country": "US"},
        "occurred_at": "",
    }
    body, _ := json.Marshal(payload)

    // Wrong signature
    req := httptest.NewRequest(http.MethodPost, "/webhooks/dummy", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Signature", "deadbeef")
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusUnauthorized {
        t.Fatalf("expected 401, got %d; body=%s", rr.Code, rr.Body.String())
    }
}

func TestWebhookDummy_IdempotentDuplicate(t *testing.T) {
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        t.Skip("DATABASE_URL not set; skipping integration test")
        return
    }
    secret := "testsecret"
    os.Setenv("DUMMY_WEBHOOK_SECRET", secret)

    pool, err := db.NewPool(t.Context(), dbURL)
    if err != nil {
        t.Fatalf("failed to connect db: %v", err)
    }
    defer pool.Close()

    h := New(pool)

    code := "WHTEST003"
    occurred := "2025-01-01T00:00:00Z"
    status := "in_transit"
    desc := "Processed"
    payload := map[string]any{
        "code":        code,
        "status":      status,
        "description": desc,
        "location":    map[string]any{"country": "US"},
        "occurred_at": occurred,
    }
    body, _ := json.Marshal(payload)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    sig := hex.EncodeToString(mac.Sum(nil))

    // First send
    req1 := httptest.NewRequest(http.MethodPost, "/webhooks/dummy", bytes.NewReader(body))
    req1.Header.Set("Content-Type", "application/json")
    req1.Header.Set("X-Signature", sig)
    rr1 := httptest.NewRecorder()
    h.ServeHTTP(rr1, req1)
    if rr1.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d; body=%s", rr1.Code, rr1.Body.String())
    }

    // Second duplicate send
    req2 := httptest.NewRequest(http.MethodPost, "/webhooks/dummy", bytes.NewReader(body))
    req2.Header.Set("Content-Type", "application/json")
    req2.Header.Set("X-Signature", sig)
    rr2 := httptest.NewRecorder()
    h.ServeHTTP(rr2, req2)
    if rr2.Code != http.StatusOK {
        t.Fatalf("expected 200 on duplicate, got %d; body=%s", rr2.Code, rr2.Body.String())
    }

    // Verify only one event stored for the occurred/status/description
    var count int
    err = pool.QueryRow(t.Context(), `
        SELECT COUNT(*)
        FROM tracking_events e
        JOIN trackers t ON t.id = e.tracker_id
        WHERE t.carrier_tracking_code = $1
          AND e.occurred_at = $2
          AND COALESCE(e.status, '') = $3
          AND COALESCE(e.description, '') = $4
    `, code, occurred, status, desc).Scan(&count)
    if err != nil {
        t.Fatalf("count query failed: %v", err)
    }
    if count != 1 {
        t.Fatalf("expected 1 event, got %d", count)
    }
}

func TestWebhookKarrio_IngestsEventWithAlternateFields(t *testing.T) {
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        t.Skip("DATABASE_URL not set; skipping integration test")
        return
    }
    secret := "karriosecret"
    os.Setenv("KARRIO_WEBHOOK_SECRET", secret)

    pool, err := db.NewPool(t.Context(), dbURL)
    if err != nil {
        t.Fatalf("failed to connect db: %v", err)
    }
    defer pool.Close()

    h := New(pool)

    code := "KRTEST001"
    // Karrio-like payload with alternate keys
    payload := map[string]any{
        "tracking_number": code,
        "tracking_status": "in_transit",
        "message": "Departed facility",
        "event_time": "",
        "event": map[string]any{
            "location": map[string]any{"country": "JP"},
        },
    }
    body, _ := json.Marshal(payload)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    sig := hex.EncodeToString(mac.Sum(nil))

    req := httptest.NewRequest(http.MethodPost, "/webhooks/karrio", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Signature", sig)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
    }

    // Verify tracker exists and last event status
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