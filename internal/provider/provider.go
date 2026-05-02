package provider

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

type CreateCharge struct {
	Amount    int64
	Currency  string
	Reference string
}

type Provider interface {
	Charge(ctx context.Context, req CreateCharge) error
}

var TimeoutError = errors.New("Timeout Error")
var NetworkError = errors.New("Network Error")

type MockProvider struct {
	mu          sync.Mutex
	callCount   map[string]int
	delayMs     int
	failureRate float64
}

// NewMockProvider creates a mock provider.
// If delayMs is 0, delay is random (50-500ms).
// If failureRate is negative, failure rate is random (~50%).
func NewMockProvider(delayMs int, failureRate float64) *MockProvider {
	return &MockProvider{
		callCount:   make(map[string]int),
		delayMs:     delayMs,
		failureRate: failureRate,
	}
}

func (p *MockProvider) Charge(ctx context.Context, req CreateCharge) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if req.Reference != "" {
		p.callCount[req.Reference]++
	}

	var delay time.Duration
	if p.delayMs > 0 {
		delay = time.Duration(p.delayMs) * time.Millisecond
	} else {
		max := 500
		min := 50
		delay = time.Duration(rand.IntN(max-min)+min) * time.Millisecond
	}

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return TimeoutError
	}

	var shouldFail bool
	if p.failureRate >= 0 {
		shouldFail = rand.Float64() < p.failureRate
	} else {
		shouldFail = rand.N(10) > 5
	}

	if shouldFail {
		return NetworkError
	}

	return nil
}

func (p *MockProvider) ChargeCount(reference string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount[reference]
}

func (p *MockProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount = make(map[string]int)
}
