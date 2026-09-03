package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/platform/database"
)

func TestPostgresNotificationListener_IntegrationListensOnBothChannels(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "paysplit-listener-integration"
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()

	received := make(chan string, 3)
	listener, err := database.NewPostgresNotificationListener(pool, map[string]database.NotificationHandler{
		"bill_events": func(_ context.Context, payload string) error {
			received <- "bill:" + payload
			return nil
		},
		"group_events": func(_ context.Context, payload string) error {
			received <- "group:" + payload
			return nil
		},
		"user_events": func(_ context.Context, payload string) error {
			received <- "user:" + payload
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	listenerCtx, stopListener := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- listener.Run(listenerCtx) }()
	select {
	case <-listener.Ready():
	case <-ctx.Done():
		t.Fatal("listener did not become ready")
	}
	var listenerSessions int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_stat_activity
		WHERE application_name = 'paysplit-listener-integration'
		  AND state = 'idle'
		  AND query LIKE 'LISTEN %'
	`).Scan(&listenerSessions); err != nil {
		t.Fatalf("count listener sessions: %v", err)
	}
	if listenerSessions != 1 {
		t.Fatalf("LISTEN sessions = %d, want exactly 1", listenerSessions)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin notify transaction: %v", err)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_notify($1, $2)", "bill_events", "one"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("notify bill channel: %v", err)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_notify($1, $2)", "group_events", "two"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("notify group channel: %v", err)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_notify($1, $2)", "user_events", "three"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("notify user channel: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit notifications: %v", err)
	}

	for _, want := range []string{"bill:one", "group:two", "user:three"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("received %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %q", want)
		}
	}

	stopListener()
	if err := <-done; err != nil {
		t.Fatalf("listener shutdown: %v", err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("acquired connections after listener shutdown = %d, want 0", acquired)
	}
}
