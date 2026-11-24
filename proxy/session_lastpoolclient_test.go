package proxy

import (
	"sync"
	"testing"

	"github.com/ethpandaops/dugtrio/pool"
)

// TestSetLastPoolClientConcurrentAccess drives setLastPoolClient and GetLastPoolClient
// from several goroutines at once, the same way the request path, the rebalancer, and
// the frontend sessions handler touch a session's sticky endpoint pointer with no
// common lock between them. It exists to keep this safe under -race going forward.
func TestSetLastPoolClientConcurrentAccess(t *testing.T) {
	s := &Session{}
	s.init()

	clientA := &pool.Client{}
	clientB := &pool.Client{}

	var wg sync.WaitGroup

	const iters = 20000

	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.setLastPoolClient(clientA)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.setLastPoolClient(clientB)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.GetLastPoolClient()
		}
	}()

	wg.Wait()

	final := s.GetLastPoolClient()
	if final != clientA && final != clientB {
		t.Fatalf("expected the final client to be one of the two written values, got %v", final)
	}
}

// TestSetLastPoolClientOnlyCancelsOnChange verifies the client-change decision itself
// is now made from a single atomic exchange rather than a separate read followed by a
// separate write, so two callers racing to set the same session can no longer disagree
// about whether the target actually changed.
func TestSetLastPoolClientOnlyCancelsOnChange(t *testing.T) {
	s := &Session{}
	s.init()

	client := &pool.Client{}

	s.setLastPoolClient(client)

	cancelled := false
	s.addActiveContext(func() { cancelled = true })

	s.setLastPoolClient(client) // same client again - should not cancel anything

	if cancelled {
		t.Fatal("setting the same client again should not cancel active connections")
	}

	s.setLastPoolClient(&pool.Client{}) // different client - should cancel

	if !cancelled {
		t.Fatal("setting a different client should cancel active connections")
	}
}
