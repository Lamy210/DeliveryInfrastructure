package server

import (
    "context"
    "encoding/json"
    "errors"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "log"
    "net/http"
    "io"
    "os"
    "strings"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/jackc/pgconn"
    "deliveryinfra/internal/rate"
)

type Server struct {
    db *pgxpool.Pool
    est rate.Estimator
}

func New(db *pgxpool.Pool) http.Handler {
    s := &Server{db: db, est: rate.NewDummy()}
    r := chi.NewRouter()
    // Observability: Request ID and basic logger
    r.Use(requestIDMiddleware)
    r.Use(middleware.Logger)
    r.Get("/healthz", s.handleHealth)
    r.Post("/shipments", s.handleCreateShipment)
    r.Get("/rates", s.handleGetRates)
    r.Get("/trackers/{code}", s.handleGetTracker)
    r.Post("/trackers/{code}/events", s.handlePostTrackerEvent)
    r.Post("/webhooks/{source}", s.handleWebhook)
    return r
}

// NewWithEstimator allows injecting a custom Estimator implementation.
func NewWithEstimator(db *pgxpool.Pool, est rate.Estimator) http.Handler {
    if est == nil {
        est = rate.NewDummy()
    }
    s := &Server{db: db, est: est}
    r := chi.NewRouter()
    // Observability: Request ID and basic logger
    r.Use(requestIDMiddleware)
    r.Use(middleware.Logger)
    r.Get("/healthz", s.handleHealth)
    r.Post("/shipments", s.handleCreateShipment)
    r.Get("/rates", s.handleGetRates)
    r.Get("/trackers/{code}", s.handleGetTracker)
    r.Post("/trackers/{code}/events", s.handlePostTrackerEvent)
    r.Post("/webhooks/{source}", s.handleWebhook)
    return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
}

// Rates
type RateRequest struct {
    FromCountry string  `json:"from_country"`
    ToCountry   string  `json:"to_country"`
    WeightOz    float64 `json:"weight_oz"`
    CarrierCode string  `json:"carrier_code"`
}
type RateResponse struct {
    Currency string  `json:"currency"`
    Amount   float64 `json:"amount"`
    Carrier  string  `json:"carrier"`
}

func (s *Server) handleGetRates(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query()
    req := RateRequest{
        FromCountry: q.Get("from_country"),
        ToCountry:   q.Get("to_country"),
        CarrierCode: q.Get("carrier_code"),
    }
    var weightOz float64
    if w := q.Get("weight_oz"); w != "" {
        // parse float
        if f, err := parseFloat(w); err == nil {
            weightOz = f
        }
    }
    currency, amount, carrier := s.est.Estimate(req.FromCountry, req.ToCountry, req.CarrierCode, weightOz)
    res := RateResponse{Currency: currency, Amount: amount, Carrier: carrier}
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(res)
}

// Shipments
type ShipmentCreateRequest struct {
    OrgSlug          string          `json:"org_slug"`
    OrderExternalID  string          `json:"order_external_id"`
    CarrierCode      string          `json:"carrier_code"`
    RateCurrency     string          `json:"rate_currency"`
    ShipTo           json.RawMessage `json:"ship_to"`
    ShipFrom         json.RawMessage `json:"ship_from"`
    Package          json.RawMessage `json:"package"`
    Metadata         json.RawMessage `json:"metadata"`
}

type ShipmentCreateResponse struct {
    ShipmentID  string `json:"shipment_id"`
    LabelURL    string `json:"label_url"`
    Status      string `json:"status"`
    CreatedAt   string `json:"created_at"`
}

func (s *Server) handleCreateShipment(w http.ResponseWriter, r *http.Request) {
    var req ShipmentCreateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeErrorJSON(w, http.StatusBadRequest, "invalid_json", "invalid json")
        return
    }
    if req.OrgSlug == "" {
        writeErrorJSON(w, http.StatusBadRequest, "invalid_request", "org_slug required")
        return
    }

    // Defaults
    if req.RateCurrency == "" {
        req.RateCurrency = "USD"
    }
    if req.Metadata == nil {
        req.Metadata = json.RawMessage("{}")
    }

    ctx := r.Context()

    // Resolve org
    var orgID uuid.UUID
    err := s.db.QueryRow(ctx, "SELECT id FROM orgs WHERE slug = $1", req.OrgSlug).Scan(&orgID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            writeErrorJSON(w, http.StatusNotFound, "resource_not_found", "org not found")
            return
        }
        writeErrorJSON(w, http.StatusInternalServerError, "db_error", "db error")
        return
    }

    // Resolve order (optional)
    var orderID *uuid.UUID
    if req.OrderExternalID != "" {
        var oid uuid.UUID
        err = s.db.QueryRow(ctx, "SELECT id FROM orders WHERE org_id = $1 AND external_order_id = $2", orgID, req.OrderExternalID).Scan(&oid)
        if err == nil {
            orderID = &oid
        } else if !errors.Is(err, pgx.ErrNoRows) {
            writeErrorJSON(w, http.StatusInternalServerError, "db_error", "db error")
            return
        }
    }

    // Resolve carrier account (optional by carrier_code)
    var carrierAccountID *uuid.UUID
    if req.CarrierCode != "" {
        var caid uuid.UUID
        err = s.db.QueryRow(ctx, `
            SELECT ca.id
            FROM carrier_accounts ca
            JOIN carriers c ON c.id = ca.carrier_id
            WHERE ca.org_id = $1 AND c.code = $2
            LIMIT 1`, orgID, req.CarrierCode).Scan(&caid)
        if err == nil {
            carrierAccountID = &caid
        } else if !errors.Is(err, pgx.ErrNoRows) {
            http.Error(w, "db error", http.StatusInternalServerError)
            return
        }
    }

    // Naive rate using weight_oz from package
    var pkgMap map[string]any
    _ = json.Unmarshal(req.Package, &pkgMap)
    weightOz, _ := toFloat(pkgMap["weight_oz"]) // default 0
    rateAmount := 5.0 + weightOz*0.5

    shipmentID := uuid.New()
    now := time.Now().UTC()

    // Insert shipment
    _, err = s.db.Exec(ctx, `
        INSERT INTO shipments (
            id, org_id, order_id, carrier_account_id, status,
            rate_currency, rate_amount, ship_to, ship_from, package, metadata,
            created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, 'created',
            $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10::jsonb,
            $11, $11
        )
    `,
        shipmentID,
        orgID,
        orderID,
        carrierAccountID,
        req.RateCurrency,
        rateAmount,
        string(req.ShipTo),
        string(req.ShipFrom),
        string(req.Package),
        string(req.Metadata),
        now,
    )
    if err != nil {
        log.Println("insert shipment error:", err)
        writeErrorJSON(w, http.StatusInternalServerError, "db_error", "failed to create shipment")
        return
    }

    // Insert a placeholder label
    labelID := uuid.New()
    labelURL := "https://example.com/label/" + shipmentID.String() + ".pdf"
    _, err = s.db.Exec(ctx, `
        INSERT INTO labels (
            id, shipment_id, document_url, format, size, cost, currency, metadata, created_at
        ) VALUES (
            $1, $2, $3, 'pdf', '4x6', 0.00, $4, $5::jsonb, $6
        )
    `,
        labelID,
        shipmentID,
        labelURL,
        req.RateCurrency,
        "{}",
        now,
    )
    if err != nil {
        log.Println("insert label error:", err)
        writeErrorJSON(w, http.StatusInternalServerError, "db_error", "failed to create label")
        return
    }

    res := ShipmentCreateResponse{
        ShipmentID: shipmentID.String(),
        LabelURL:   labelURL,
        Status:     "created",
        CreatedAt:  now.Format(time.RFC3339),
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(res)
}

// Tracker detail
type TrackerResponse struct {
    Code        string          `json:"code"`
    Status      string          `json:"status"`
    LastEventAt string          `json:"last_event_at,omitempty"`
    LastEvent   json.RawMessage `json:"last_event,omitempty"`
}

func (s *Server) handleGetTracker(w http.ResponseWriter, r *http.Request) {
    code := chi.URLParam(r, "code")
    if strings.TrimSpace(code) == "" {
        writeErrorJSON(w, http.StatusBadRequest, "invalid_request", "code required")
        return
    }
    ctx := r.Context()
    var (
        status       *string
        lastEventAt  *time.Time
        lastEventRaw *string
    )
    err := s.db.QueryRow(ctx, `
        SELECT t.status,
               t.last_event_at,
               (SELECT to_jsonb(e) FROM tracking_events e
                 WHERE e.tracker_id = t.id
                 ORDER BY e.occurred_at DESC
                 LIMIT 1) AS last_event
        FROM trackers t
        WHERE t.carrier_tracking_code = $1
    `, code).Scan(&status, &lastEventAt, &lastEventRaw)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            writeErrorJSON(w, http.StatusNotFound, "resource_not_found", "not found")
            return
        }
        writeErrorJSON(w, http.StatusInternalServerError, "db_error", "db error")
        return
    }
    resp := TrackerResponse{Code: code}
    if status != nil {
        resp.Status = *status
    }
    if lastEventAt != nil {
        resp.LastEventAt = lastEventAt.UTC().Format(time.RFC3339)
    }
    if lastEventRaw != nil {
        resp.LastEvent = json.RawMessage(*lastEventRaw)
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

// Tracker event ingestion
type TrackerEventRequest struct {
    Status      string          `json:"status"`
    Description string          `json:"description"`
    Location    json.RawMessage `json:"location"`
    OccurredAt  string          `json:"occurred_at"`
    Raw         json.RawMessage `json:"raw"`
}

type TrackerEventResponse struct {
    Code        string          `json:"code"`
    Status      string          `json:"status"`
    OccurredAt  string          `json:"occurred_at"`
}

func (s *Server) handlePostTrackerEvent(w http.ResponseWriter, r *http.Request) {
    code := chi.URLParam(r, "code")
    if strings.TrimSpace(code) == "" {
        writeErrorJSON(w, http.StatusBadRequest, "invalid_request", "code required")
        return
    }
    var req TrackerEventRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeErrorJSON(w, http.StatusBadRequest, "invalid_json", "invalid json")
        return
    }
    // Default values
    if req.Location == nil {
        req.Location = json.RawMessage("{}")
    }
    if req.Raw == nil {
        req.Raw = json.RawMessage("{}")
    }
    var occurred time.Time
    if strings.TrimSpace(req.OccurredAt) == "" {
        occurred = time.Now().UTC()
    } else {
        if t, err := time.Parse(time.RFC3339, req.OccurredAt); err == nil {
            occurred = t.UTC()
        } else {
            writeErrorJSON(w, http.StatusBadRequest, "invalid_occurred_at", "invalid occurred_at")
            return
        }
    }

    if err := s.insertTrackerEvent(r.Context(), code, req, occurred); err != nil {
        writeErrorJSON(w, http.StatusInternalServerError, "db_error", "db error")
        return
    }

    resp := TrackerEventResponse{
        Code:       code,
        Status:     orDefault(req.Status, "unknown"),
        OccurredAt: occurred.Format(time.RFC3339),
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

// insertTrackerEvent ensures tracker exists, inserts event, and updates tracker status/last_event_at.
func (s *Server) insertTrackerEvent(ctx context.Context, code string, req TrackerEventRequest, occurred time.Time) error {
    tx, err := s.db.Begin(ctx)
    if err != nil {
        return err
    }
    defer func() { _ = tx.Rollback(ctx) }()

    var trackerID uuid.UUID
    err = tx.QueryRow(ctx, `SELECT id FROM trackers WHERE carrier_tracking_code = $1`, code).Scan(&trackerID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            trackerID = uuid.New()
            _, err = tx.Exec(ctx, `
                INSERT INTO trackers (id, carrier_tracking_code, status, last_event_at, metadata)
                VALUES ($1, $2, COALESCE($3, 'unknown'), $4, '{}'::jsonb)
            `, trackerID, code, nullIfEmpty(req.Status), occurred)
            if err != nil {
                return err
            }
        } else {
            return err
        }
    }

    // Idempotency: skip insert if same tracker_id + occurred_at + status + description already exists
    var exists bool
    err = tx.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM tracking_events
            WHERE tracker_id = $1
              AND occurred_at = $2
              AND (status IS NOT DISTINCT FROM $3)
              AND (description IS NOT DISTINCT FROM $4)
        )
    `, trackerID, occurred, nullIfEmpty(req.Status), req.Description).Scan(&exists)
    if err != nil {
        return err
    }
    if !exists {
        _, err = tx.Exec(ctx, `
            INSERT INTO tracking_events (tracker_id, occurred_at, status, description, location, raw)
            VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)
        `, trackerID, occurred, nullIfEmpty(req.Status), req.Description, string(req.Location), string(req.Raw))
        if err != nil {
            // If unique violation occurred due to race, treat as idempotent success
            var pgErr *pgconn.PgError
            if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
                // continue without error
            } else {
                return err
            }
        }
    }

    _, err = tx.Exec(ctx, `UPDATE trackers SET status = COALESCE($2, status), last_event_at = $3 WHERE id = $1`, trackerID, nullIfEmpty(req.Status), occurred)
    if err != nil {
        return err
    }
    return tx.Commit(ctx)
}

// handleWebhook ingests provider-specific webhook events. For now supports "dummy" and reads HMAC secret from env.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
    source := chi.URLParam(r, "source")
    if strings.TrimSpace(source) == "" {
        writeErrorJSON(w, http.StatusBadRequest, "invalid_request", "source required")
        return
    }
    var secretEnv string
    switch strings.ToLower(strings.TrimSpace(source)) {
    case "dummy":
        secretEnv = "DUMMY_WEBHOOK_SECRET"
    case "karrio":
        secretEnv = "KARRIO_WEBHOOK_SECRET"
    default:
        writeErrorJSON(w, http.StatusNotFound, "unsupported_source", "unsupported source")
        return
    }
    secret := os.Getenv(secretEnv)
    if strings.TrimSpace(secret) == "" {
        writeErrorJSON(w, http.StatusUnauthorized, "secret_not_configured", "webhook secret not configured")
        return
    }

    // Read raw body for signature verification
    body, err := io.ReadAll(r.Body)
    if err != nil {
        writeErrorJSON(w, http.StatusBadRequest, "read_error", "read error")
        return
    }
    sigHeader := r.Header.Get("X-Signature")
    sigHeader = strings.TrimSpace(sigHeader)
    sigHeader = strings.TrimPrefix(sigHeader, "sha256=")
    if sigHeader == "" {
        writeErrorJSON(w, http.StatusUnauthorized, "missing_signature", "missing signature")
        return
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))
    provided, err := hex.DecodeString(sigHeader)
    if err != nil {
        writeErrorJSON(w, http.StatusUnauthorized, "invalid_signature_format", "invalid signature format")
        return
    }
    if !hmac.Equal([]byte(expected), []byte(hex.EncodeToString(provided))) {
        writeErrorJSON(w, http.StatusUnauthorized, "signature_mismatch", "signature mismatch")
        return
    }

    // Normalize provider payload into tracker event
    code, req, nerr := NewNormalizer(source).Normalize(source, body)
    if nerr != nil {
        if errors.Is(nerr, ErrMissingCode) {
            writeErrorJSON(w, http.StatusBadRequest, "invalid_request", "code required")
        } else {
            writeErrorJSON(w, http.StatusBadRequest, "invalid_json", "invalid json")
        }
        return
    }

    var occurred time.Time
    if strings.TrimSpace(req.OccurredAt) == "" {
        occurred = time.Now().UTC()
    } else {
        if t, err := time.Parse(time.RFC3339, req.OccurredAt); err == nil {
            occurred = t.UTC()
        } else {
            writeErrorJSON(w, http.StatusBadRequest, "invalid_occurred_at", "invalid occurred_at")
            return
        }
    }
    if err := s.insertTrackerEvent(r.Context(), code, req, occurred); err != nil {
        writeErrorJSON(w, http.StatusInternalServerError, "db_error", "db error")
        return
    }
    resp := TrackerEventResponse{Code: code, Status: orDefault(req.Status, "unknown"), OccurredAt: occurred.Format(time.RFC3339)}
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

// writeErrorJSON writes a standardized JSON error response:
// {"error": {"code": string, "message": string}}
func writeErrorJSON(w http.ResponseWriter, status int, code string, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]any{
        "error": map[string]string{
            "code":    code,
            "message": message,
        },
    })
}

// requestIDMiddleware ensures X-Request-ID is set on the response.
// If provided in the request header, it is propagated; otherwise a UUID is generated.
func requestIDMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        rid := strings.TrimSpace(r.Header.Get("X-Request-ID"))
        if rid == "" {
            rid = uuid.New().String()
        }
        w.Header().Set("X-Request-ID", rid)
        next.ServeHTTP(w, r)
    })
}

func nullIfEmpty(s string) *string {
    if strings.TrimSpace(s) == "" {
        return nil
    }
    return &s
}

func orDefault(s, d string) string {
    if strings.TrimSpace(s) == "" {
        return d
    }
    return s
}

func toFloat(v any) (float64, bool) {
    switch t := v.(type) {
    case float64:
        return t, true
    case float32:
        return float64(t), true
    case int:
        return float64(t), true
    case int64:
        return float64(t), true
    case json.Number:
        f, err := t.Float64()
        if err == nil {
            return f, true
        }
        return 0, false
    default:
        return 0, false
    }
}

func parseFloat(s string) (float64, error) {
    var n json.Number = json.Number(s)
    return n.Float64()
}