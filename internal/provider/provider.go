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

var NetworkError = errors.New("Network Error")

type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p MockProvider) Charge(ctx context.Context, req CreateCharge) error {
	max := 500
	min := 50
	time.Sleep(time.Duration(rand.IntN(max-min) + min))

	if rand.N(10) > 5 {
		return NetworkError
	}

	return nil
}
