package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "time"
    "strings"

    "deliveryinfra/internal/config"
    "deliveryinfra/internal/db"
    "deliveryinfra/internal/rate"
    "deliveryinfra/internal/server"
)

func main() {
    cfg := config.Load()

    if strings.TrimSpace(cfg.DatabaseURL) == "" {
        log.Fatalf("DATABASE_URL not set. Please export DATABASE_URL before running.")
    }

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    pool, err := db.NewPool(ctx, cfg.DatabaseURL)
    if err != nil {
        log.Fatalf("failed to connect db: %v", err)
    }
    defer pool.Close()
    // Verify connectivity proactively
    if err := pool.Ping(ctx); err != nil {
        log.Fatalf("database ping failed: %v", err)
    }

    // Select rate provider from config
    provider := cfg.RateProvider
    est := rate.NewByName(provider)
    r := server.NewWithEstimator(pool, est)

    srv := &http.Server{
        Addr:              ":" + cfg.Port,
        Handler:           r,
        ReadTimeout:       10 * time.Second,
        ReadHeaderTimeout: 10 * time.Second,
        WriteTimeout:      20 * time.Second,
        IdleTimeout:       60 * time.Second,
    }

    if provider == "" {
        provider = "dummy"
    }
    log.Printf("api listening on :%s (RATE_PROVIDER=%s)", cfg.Port, provider)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Println("server error:", err)
        os.Exit(1)
    }
}