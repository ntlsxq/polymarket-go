package book

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// BenchmarkMarkSeenNew measures pure-insert path, exercising map grow.
func BenchmarkMarkSeenNew(b *testing.B) {
	d := newTradeDedup(b.N + 16)
	hashes := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		hashes[i] = fmt.Sprintf("0xhash%d", i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.markSeen(hashes[i])
	}
}

// BenchmarkMarkSeenReplay is the steady-state replay-rejection cost: same
// hash hits over and over (worst-case lock contention path).
func BenchmarkMarkSeenReplay(b *testing.B) {
	d := newTradeDedup(1024)
	d.markSeen("0xstable")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.markSeen("0xstable")
	}
}

// BenchmarkMarkSeenParallel measures contention when many goroutines push
// trades simultaneously. Captures Mutex serialization cost.
func BenchmarkMarkSeenParallel(b *testing.B) {
	d := newTradeDedup(1 << 20)
	var ctr atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := ctr.Add(1)
			d.markSeen(fmt.Sprintf("h%d", i))
		}
	})
}
