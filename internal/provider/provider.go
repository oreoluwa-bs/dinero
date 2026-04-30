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
	mu        sync.Mutex
	callCount map[string]int
}

func NewMockProvider() *MockProvider {
	return &MockProvider{
		callCount: make(map[string]int),
	}
}

func (p *MockProvider) Charge(ctx context.Context, req CreateCharge) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if req.Reference != "" {
		p.callCount[req.Reference]++
	}

	max := 500
	min := 50
	delay := time.Duration(rand.IntN(max-min)+min) * time.Millisecond

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return TimeoutError
	}

	if rand.N(10) > 5 {
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
