package db

import (
    "context"
    "errors"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
    if databaseURL == "" {
        return nil, errors.New("DATABASE_URL is not set")
    }
    cfg, err := pgxpool.ParseConfig(databaseURL)
    if err != nil {
        return nil, err
    }
    // Conservative pool sizing and timeouts
    cfg.MaxConns = 5
    cfg.MinConns = 0
    cfg.MaxConnLifetime = 30 * time.Minute
    cfg.MaxConnIdleTime = 5 * time.Minute
    cfg.HealthCheckPeriod = 30 * time.Second
    // Safe runtime params
    cfg.ConnConfig.RuntimeParams["application_name"] = "deliveryinfra-api"
    cfg.ConnConfig.RuntimeParams["search_path"] = "public"
    cfg.ConnConfig.RuntimeParams["client_encoding"] = "UTF8"
    cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
    // Set conservative statement timeouts if supported (server-side params)
    // Note: These may be ignored depending on server configuration
    cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"                         // 5s
    cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"     // 5s

    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    if err != nil {
        return nil, err
    }
    return pool, nil
}