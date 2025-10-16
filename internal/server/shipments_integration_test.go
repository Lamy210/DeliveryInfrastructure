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

func TestCreateShipmentIntegration(t *testing.T) {
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

    // Ensure demo org exists
    _, _ = pool.Exec(t.Context(), `
        INSERT INTO orgs (slug, name)
        SELECT 'demo', 'Demo Org'
        WHERE NOT EXISTS (SELECT 1 FROM orgs WHERE slug = 'demo')
    `)

    h := New(pool)

    payload := map[string]any{
        "org_slug":          "demo",
        "order_external_id": "",
        "carrier_code":      "",
        "rate_currency":     "USD",
        "ship_to":           map[string]any{"country": "US"},
        "ship_from":         map[string]any{"country": "US"},
        "package":           map[string]any{"weight_oz": 5},
        "metadata":          map[string]any{},
    }
    body, _ := json.Marshal(payload)
    req := httptest.NewRequest(http.MethodPost, "/shipments", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
    }
    var res struct{
        ShipmentID string `json:"shipment_id"`
        LabelURL   string `json:"label_url"`
        Status     string `json:"status"`
    }
    if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
        t.Fatalf("failed to unmarshal: %v", err)
    }
    if res.ShipmentID == "" || res.Status != "created" || res.LabelURL == "" {
        t.Fatalf("unexpected response: %+v", res)
    }
    // Clean up inserted shipment cascades labels
    _, _ = pool.Exec(t.Context(), `DELETE FROM shipments WHERE id = $1`, res.ShipmentID)
}