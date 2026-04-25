package book

import (
	"fmt"
	"hash/maphash"
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

// TestTradeDedupShardResetReadmitsHash crafts three hashes that all land in
// the same shard, so we can deterministically assert: with per-shard cap=1,
// the second hash forces a reset and the first hash is re-admitted afterwards.
func TestTradeDedupShardResetReadmitsHash(t *testing.T) {
	d := newTradeDedup(dedupShardCount) // 1 per shard
	var collide []string
	target := uint64(0xFFFF)
	for i := 0; len(collide) < 3 && i < 10_000; i++ {
		h := fmt.Sprintf("h%d", i)
		sh := maphash.String(d.seed, h) & dedupShardMask
		if target == 0xFFFF {
			target = sh
			collide = append(collide, h)
			continue
		}
		if sh == target {
			collide = append(collide, h)
		}
	}
	if len(collide) != 3 {
		t.Fatalf("could not find 3 colliding hashes (found %d)", len(collide))
	}

	if !d.markSeen(collide[0]) {
		t.Fatal("first must accept")
	}
	if d.markSeen(collide[0]) {
		t.Fatal("immediate replay must reject")
	}
	// Second hash hits the same shard at len==1>=cap=1; shard resets, accepts.
	if !d.markSeen(collide[1]) {
		t.Fatal("second hash to same shard must accept after reset")
	}
	// Original hash was wiped; should be re-admitted.
	if !d.markSeen(collide[0]) {
		t.Fatal("after reset, old hash must be re-admitted")
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
	if got := d.capacity(); got != defaultSeenTradesCapacity {
		t.Fatalf("default capacity = %d, want %d", got, defaultSeenTradesCapacity)
	}
}
