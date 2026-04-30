package server

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/oreoluwa-bs/dinero/database"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"

	_ "github.com/mattn/go-sqlite3"
)

type testProvider struct {
	shouldFail bool
	delay      time.Duration
	callCount  int
}

func (p *testProvider) Charge(ctx context.Context, req provider.CreateCharge) error {
	p.callCount++
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	if p.shouldFail {
		return provider.NetworkError
	}
	return nil
}

type mockPublisher struct {
	calls []publishCall
}
type publishCall struct {
	exchange   string
	routingKey string
	body       []byte
}

func (m *mockPublisher) Publish(_ context.Context, exchange, routingKey string, body []byte) error {
	m.calls = append(m.calls, publishCall{exchange, routingKey, body})
	return nil
}

func setupTestDB(t *testing.T) (*repository.Queries, *sql.DB) {
	t.Helper()

	db := database.NewDatabase(":memory:")
	if err := database.Up(db, "../../database/migrations"); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	return repository.New(db), db
}

func setupLogger() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{})
	logger := slog.New(handler)
	return logger
}

func TestHappyPath_Green(t *testing.T) {
	store, db := setupTestDB(t)
	defer db.Close()

	logger := setupLogger()

	srv := NewServer(&testProvider{}, *store, db, &mockPublisher{}, logger, nil, nil)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/charges", "application/json",
		bytes.NewReader([]byte(`{"amount":5000,"currency":"USD","reference":"txn_001"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM payments WHERE reference = 'txn_001' AND status = 'pending'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 pending payment, got %d", count)
	}
}

func TestDuplicateReference_ShouldFail_Red(t *testing.T) {
	store, db := setupTestDB(t)
	defer db.Close()

	logger := setupLogger()
	srv := NewServer(&testProvider{}, *store, db, &mockPublisher{}, logger, nil, nil)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	body := []byte(`{"amount":5000,"currency":"USD","reference":"dup_001"}`)

	resp1, err := ts.Client().Post(ts.URL+"/charges", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Errorf("first request: expected 202, got %d", resp1.StatusCode)
	}

	resp2, err := ts.Client().Post(ts.URL+"/charges", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("second request: expected 200, got %d", resp2.StatusCode)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM payments WHERE reference = 'dup_001'`).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Errorf("RED — duplicate reference created %d payments (expected 1)", count)
	}
}

func TestTimeout_ShouldFail_Red(t *testing.T) {
	store, db := setupTestDB(t)
	defer db.Close()

	logger := setupLogger()
	p := &testProvider{delay: 100 * time.Millisecond}
	srv := NewServer(p, *store, db, &mockPublisher{}, logger, nil, nil)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	body := []byte(`{"amount":1000,"currency":"USD","reference":"timeout_001"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/charges", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp1, _ := ts.Client().Do(req)
	if resp1 != nil {
		resp1.Body.Close()
	}

	time.Sleep(150 * time.Millisecond)

	resp2, err := ts.Client().Post(ts.URL+"/charges", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("second request: expected 200, got %d", resp2.StatusCode)
	}
}
