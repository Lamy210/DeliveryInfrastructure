package server

import (
    "encoding/json"
    "errors"
    "strings"
)

// Normalizer maps provider-specific webhook payloads into TrackerEventRequest.
type Normalizer interface {
    Normalize(source string, body []byte) (code string, req TrackerEventRequest, err error)
}

// ErrMissingCode is returned when a payload cannot produce a tracker code.
var ErrMissingCode = errors.New("missing tracker code")

// NewNormalizer selects a normalizer for the given source.
// Currently returns DefaultNormalizer for all sources.
func NewNormalizer(source string) Normalizer { return &DefaultNormalizer{} }

// DefaultNormalizer attempts to extract common fields from diverse payloads.
type DefaultNormalizer struct{}

func (n *DefaultNormalizer) Normalize(source string, body []byte) (string, TrackerEventRequest, error) {
    var payload map[string]any
    if err := json.Unmarshal(body, &payload); err != nil {
        return "", TrackerEventRequest{}, err
    }
    code := getString(payload, []string{"code", "tracking_number", "tracker_code", "tracking_code", "id"})
    code = strings.TrimSpace(code)
    if code == "" {
        return "", TrackerEventRequest{}, ErrMissingCode
    }

    status := getString(payload, []string{"status", "event.status", "tracking_status"})
    description := getString(payload, []string{"description", "event.description", "message", "event.message"})
    occurredAt := getString(payload, []string{"occurred_at", "event.occurred_at", "event_time", "timestamp"})

    // Location: try common keys and marshal to json.RawMessage
    var locRaw json.RawMessage = json.RawMessage("{}")
    if v := getAny(payload, []string{"location", "event.location", "address", "place"}); v != nil {
        if b, err := json.Marshal(v); err == nil {
            locRaw = json.RawMessage(b)
        }
    }

    req := TrackerEventRequest{
        Status:      status,
        Description: description,
        Location:    locRaw,
        OccurredAt:  occurredAt,
        Raw:         json.RawMessage(body),
    }
    return code, req, nil
}

// getString returns the first non-empty string from the candidate keys.
// Supports dot-path navigation for nested maps.
func getString(m map[string]any, keys []string) string {
    for _, k := range keys {
        if v := getPath(m, k); v != nil {
            if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
                return s
            }
        }
    }
    return ""
}

// getAny returns the first non-nil value from the candidate keys.
func getAny(m map[string]any, keys []string) any {
    for _, k := range keys {
        if v := getPath(m, k); v != nil {
            return v
        }
    }
    return nil
}

// getPath navigates a dot-separated key into nested maps.
func getPath(m map[string]any, path string) any {
    parts := strings.Split(path, ".")
    var cur any = m
    for _, p := range parts {
        mm, ok := cur.(map[string]any)
        if !ok {
            return nil
        }
        v, ok := mm[p]
        if !ok {
            return nil
        }
        cur = v
    }
    return cur
}