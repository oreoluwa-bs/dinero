package provider

import (
	"context"
	"errors"
	"math/rand/v2"
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

type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p MockProvider) Charge(ctx context.Context, req CreateCharge) error {
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
