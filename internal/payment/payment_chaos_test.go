package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/oreoluwa-bs/dinero/database"
	"github.com/oreoluwa-bs/dinero/internal/metrics"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"

	_ "github.com/mattn/go-sqlite3"
)

func testLogger() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})
	return slog.New(handler)
}

type chaosMockProvider struct {
	*provider.MockProvider
	shouldFail bool
}

func (p *chaosMockProvider) Charge(ctx context.Context, req provider.CreateCharge) error {
	if p.shouldFail {
		return provider.NetworkError
	}
	return p.MockProvider.Charge(ctx, req)
}

func setupChaosTestDB(t *testing.T) (*sql.DB, *repository.Queries) {
	t.Helper()

	db := database.NewDatabase(":memory:")
	if err := database.Up(db, "../../database/migrations"); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	return db, repository.New(db)
}

func TestWorkerCrash_Recovers(t *testing.T) {
	db, store := setupChaosTestDB(t)
	defer db.Close()

	mockProv := provider.NewMockProvider()
	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)
	svc := NewService(*store, mockProv, db, testLogger(), mtr)

	// Step 1: Create a payment in pending state
	_, err := store.CreatePayment(context.Background(), repository.CreatePaymentParams{
		Amount:    5000,
		Currency:  "USD",
		Reference: "crash_001",
		Status:    "pending",
		IdempotencyKey: sql.NullString{
			String: "idem_crash_001",
			Valid:  true,
		},
	})
	if err != nil {
		t.Fatalf("create payment failed: %v", err)
	}

	// Step 2: Simulate worker crash — set to processing with stale timestamp
	_, err = db.Exec(`
		UPDATE payments
		SET status = 'processing',
		    processing_started_at = datetime('now', '-10 minutes'),
		    attempts = 2
		WHERE reference = 'crash_001'
	`)
	if err != nil {
		t.Fatalf("simulate crash failed: %v", err)
	}

	// Step 3: Process the event — should detect stale lease and retry
	payload, _ := json.Marshal(map[string]string{
		"payment_idempotency_key": "idem_crash_001",
		"payment_reference":       "crash_001",
	})
	err = svc.HandlePaymentEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("handle event failed: %v", err)
	}

	// Step 4: Verify payment is NOT stuck in processing
	pm, err := store.GetPaymentByReference(context.Background(), "crash_001")
	if err != nil {
		t.Fatalf("get payment failed: %v", err)
	}

	if pm.Status == "processing" {
		t.Errorf("payment still stuck in processing after crash recovery")
	}

	if pm.Attempts != 3 {
		t.Errorf("expected attempts=3, got %d", pm.Attempts)
	}

	t.Logf("Crash recovery: payment status=%s, attempts=%d", pm.Status, pm.Attempts)
}

func TestDuplicateWebhook_IsIdempotent(t *testing.T) {
	db, store := setupChaosTestDB(t)
	defer db.Close()

	mockProv := provider.NewMockProvider()
	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)
	svc := NewService(*store, mockProv, db, testLogger(), mtr)

	// Step 1: Create a payment
	_, err := store.CreatePayment(context.Background(), repository.CreatePaymentParams{
		Amount:    5000,
		Currency:  "USD",
		Reference: "dup_001",
		Status:    "pending",
		IdempotencyKey: sql.NullString{
			String: "idem_dup_001",
			Valid:  true,
		},
	})
	if err != nil {
		t.Fatalf("create payment failed: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{
		"payment_idempotency_key": "idem_dup_001",
		"payment_reference":       "dup_001",
	})

	// Step 2: First processing — should succeed
	err = svc.HandlePaymentEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("first handle event failed: %v", err)
	}

	pm1, err := store.GetPaymentByReference(context.Background(), "dup_001")
	if err != nil {
		t.Fatalf("get payment failed: %v", err)
	}
	if pm1.Status != "completed" {
		t.Fatalf("first processing should complete, got status=%s", pm1.Status)
	}

	callCountAfterFirst := mockProv.ChargeCount("dup_001")
	if callCountAfterFirst != 1 {
		t.Fatalf("expected provider called once, got %d", callCountAfterFirst)
	}

	// Step 3: Second processing — duplicate webhook
	err = svc.HandlePaymentEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("second handle event failed: %v", err)
	}

	// Step 4: Verify provider was NOT called again
	callCountAfterSecond := mockProv.ChargeCount("dup_001")
	if callCountAfterSecond != 1 {
		t.Errorf("duplicate webhook caused extra provider call: expected 1, got %d", callCountAfterSecond)
	}

	pm2, err := store.GetPaymentByReference(context.Background(), "dup_001")
	if err != nil {
		t.Fatalf("get payment failed: %v", err)
	}
	if pm2.Status != "completed" {
		t.Errorf("duplicate webhook changed status: expected completed, got %s", pm2.Status)
	}

	t.Logf("Idempotency: provider called %d time(s), status=%s", callCountAfterSecond, pm2.Status)
}

func TestPoisonMessage_Terminal(t *testing.T) {
	db, store := setupChaosTestDB(t)
	defer db.Close()

	mockProv := provider.NewMockProvider()
	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)
	svc := NewService(*store, mockProv, db, testLogger(), mtr)

	// Step 1: Missing idempotency key — should return nil (Ack), not error (Nack)
	badPayload := []byte(`{"payment_reference": "bad_001"}`)

	err := svc.HandlePaymentEvent(context.Background(), badPayload)
	if err != nil {
		t.Errorf("poison message should return nil (Ack), got error: %v", err)
	}

	// Step 2: Completely invalid JSON — should return nil (Ack)
	worsePayload := []byte(`not json at all`)

	err = svc.HandlePaymentEvent(context.Background(), worsePayload)
	if err != nil {
		t.Errorf("invalid JSON should return nil (Ack), got error: %v", err)
	}

	// Step 3: Verify provider was never called
	if mockProv.ChargeCount("bad_001") != 0 {
		t.Errorf("provider should not be called for poison messages")
	}

	t.Log("Poison messages handled correctly — dropped without requeue")
}

func TestProviderFailure_RetryScheduled(t *testing.T) {
	db, store := setupChaosTestDB(t)
	defer db.Close()

	// Provider that always fails
	failProv := &chaosMockProvider{
		MockProvider: provider.NewMockProvider(),
		shouldFail:   true,
	}

	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)
	svc := NewService(*store, failProv, db, testLogger(), mtr)

	// Create payment
	_, err := store.CreatePayment(context.Background(), repository.CreatePaymentParams{
		Amount:    5000,
		Currency:  "USD",
		Reference: "fail_001",
		Status:    "pending",
		IdempotencyKey: sql.NullString{
			String: "idem_fail_001",
			Valid:  true,
		},
	})
	if err != nil {
		t.Fatalf("create payment failed: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{
		"payment_idempotency_key": "idem_fail_001",
		"payment_reference":       "fail_001",
	})

	// Process — should fail and schedule retry
	err = svc.HandlePaymentEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("handle event failed: %v", err)
	}

	pm, err := store.GetPaymentByReference(context.Background(), "fail_001")
	if err != nil {
		t.Fatalf("get payment failed: %v", err)
	}

	if pm.Status != "failed" {
		t.Errorf("expected status=failed, got %s", pm.Status)
	}

	if pm.Attempts != 1 {
		t.Errorf("expected attempts=1, got %d", pm.Attempts)
	}

	if !pm.NextRetryAt.Valid {
		t.Errorf("expected next_retry_at to be set")
	}

	t.Logf("Provider failure: status=%s, attempts=%d, next_retry_at=%s",
		pm.Status, pm.Attempts, pm.NextRetryAt.String)
}
