package server

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "testing"
)

// helper to parse standardized error
type stdError struct {
    Error struct {
        Code    string `json:"code"`
        Message string `json:"message"`
    } `json:"error"`
}

func TestWebhook_UnsupportedSource_ErrorJSON(t *testing.T) {
    h := New(nil)
    req := httptest.NewRequest(http.MethodPost, "/webhooks/unknown", nil)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusNotFound {
        t.Fatalf("expected 404, got %d; body=%s", rr.Code, rr.Body.String())
    }
    var e stdError
    if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
        t.Fatalf("unmarshal error: %v", err)
    }
    if e.Error.Code != "unsupported_source" {
        t.Fatalf("unexpected error code: %s", e.Error.Code)
    }
}

func TestWebhook_InvalidSignatureFormat_ErrorJSON(t *testing.T) {
    h := New(nil)
    os.Setenv("DUMMY_WEBHOOK_SECRET", "dummysecret")
    req := httptest.NewRequest(http.MethodPost, "/webhooks/dummy", nil)
    req.Header.Set("X-Signature", "ZZZ") // invalid hex
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusUnauthorized {
        t.Fatalf("expected 401, got %d; body=%s", rr.Code, rr.Body.String())
    }
    var e stdError
    if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
        t.Fatalf("unmarshal error: %v", err)
    }
    if e.Error.Code != "invalid_signature_format" {
        t.Fatalf("unexpected error code: %s", e.Error.Code)
    }
}

func TestGetTracker_MissingCode_ErrorJSON(t *testing.T) {
    h := New(nil)
    // space decodes to empty after trim
    req := httptest.NewRequest(http.MethodGet, "/trackers/%20", nil)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusBadRequest {
        t.Fatalf("expected 400, got %d; body=%s", rr.Code, rr.Body.String())
    }
    var e stdError
    if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
        t.Fatalf("unmarshal error: %v", err)
    }
    if e.Error.Code != "invalid_request" {
        t.Fatalf("unexpected error code: %s", e.Error.Code)
    }
}