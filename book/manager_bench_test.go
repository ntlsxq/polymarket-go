package book

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// BenchmarkOBForToken measures the per-event token lookup MarketWS does
// for every WS frame routed to a book.
func BenchmarkOBForToken(b *testing.B) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.OBForToken("yes-1")
	}
}

// BenchmarkIngestTradeUnique drives end-to-end trade application with a
// fresh hash each iteration. Captures dedup map insert + ApplyTrade cost.
func BenchmarkIngestTradeUnique(b *testing.B) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	ob := m.OBForToken("yes-1")
	ob.SetFromSnapshot(nil, []BookLevel{{0.55, float64(b.N + 1_000_000)}})
	hashes := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		hashes[i] = fmt.Sprintf("0x%d", i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.IngestTrade("yes-1", Trade{Hash: hashes[i], Side: SideBuy, Price: 0.55, Size: 1})
	}
}

// BenchmarkIngestTradeDuplicate replays the same hash; cost drops to dedup
// hash check only (no book mutation).
func BenchmarkIngestTradeDuplicate(b *testing.B) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	ob := m.OBForToken("yes-1")
	ob.SetFromSnapshot(nil, []BookLevel{{0.55, 1_000_000}})
	m.IngestTrade("yes-1", Trade{Hash: "0xstable", Side: SideBuy, Price: 0.55, Size: 1})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.IngestTrade("yes-1", Trade{Hash: "0xstable", Side: SideBuy, Price: 0.55, Size: 1})
	}
}

// BenchmarkIngestTradeParallel approximates production: multiple WS streams
// fan into one Manager.
func BenchmarkIngestTradeParallel(b *testing.B) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	ob := m.OBForToken("yes-1")
	ob.SetFromSnapshot(nil, []BookLevel{{0.55, 1e15}})
	var seq atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := seq.Add(1)
			m.IngestTrade("yes-1", Trade{
				Hash: fmt.Sprintf("0x%d", i), Side: SideBuy, Price: 0.55, Size: 1,
			})
		}
	})
}
