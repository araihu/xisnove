package application

import (
	"context"
	"errors"
	"testing"

	"github.com/araihu/xisnove/domain"
)

func TestPromotionGateCancellationIsBoundedAndCleansUp(t *testing.T) {
	service := &DiscoveryService{}
	candidateID := domain.DiscoveryCandidateID("candidate-1")
	release, err := service.acquirePromotionGate(context.Background(), candidateID)
	if err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.acquirePromotionGate(canceled, candidateID); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire canceled promotion gate = %v, want context.Canceled", err)
	}
	release()

	service.promotionGatesMu.Lock()
	defer service.promotionGatesMu.Unlock()
	if len(service.promotionGates) != 0 {
		t.Fatalf("promotion gates after cancellation and release = %d, want 0", len(service.promotionGates))
	}
}
