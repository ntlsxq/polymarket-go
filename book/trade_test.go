package book

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTradeDedupEmptyHashAlwaysAccept(t *testing.T) {
	d := newTradeDedup(8)
	if !d.markSeen("") {
		t.Fatal("empty hash must be treated as unique")
	}
	if !d.markSeen("") {
		t.Fatal("empty hash must continue to be treated as unique")
	}
}

func TestTradeDedupNewVsReplay(t *testing.T) {
	d := newTradeDedup(8)
	if !d.markSeen("0xabc") {
		t.Fatal("first occurrence must be accepted")
	}
	if d.markSeen("0xabc") {
		t.Fatal("replay must be rejected")
	}
}

func TestTradeDedupCapacityResetOnFull(t *testing.T) {
	cap := 4
	d := newTradeDedup(cap)
	for i := 0; i < cap; i++ {
		hash := fmt.Sprintf("h%d", i)
		if !d.markSeen(hash) {
			t.Fatalf("inserting #%d must succeed", i)
		}
	}
	// Next insert triggers reset; all prior hashes lose memory.
	if !d.markSeen("h-overflow") {
		t.Fatal("overflow insert must succeed")
	}
	// Old hash should be accepted again because reset wiped state.
	if !d.markSeen("h0") {
		t.Fatal("post-reset hash should be treated as new")
	}
}

func TestTradeDedupConcurrentSingleAcceptor(t *testing.T) {
	d := newTradeDedup(1024)
	const workers = 8
	const each = 100
	var wg sync.WaitGroup
	var accepts atomic.Int64

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if d.markSeen(fmt.Sprintf("h%d", i)) {
					accepts.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// Every distinct hash must be accepted exactly once across all workers.
	if accepts.Load() != int64(each) {
		t.Fatalf("accept count = %d, want %d", accepts.Load(), each)
	}
}

func TestTradeDedupDefaultCapacity(t *testing.T) {
	d := newTradeDedup(0)
	if d.capacity != defaultSeenTradesCapacity {
		t.Fatalf("default capacity = %d, want %d", d.capacity, defaultSeenTradesCapacity)
	}
}
