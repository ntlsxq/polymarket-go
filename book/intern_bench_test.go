package book

import (
	"fmt"
	"testing"
)

const benchTokenLen = 78 // production Polymarket tokenIDs are 78-byte hex.

// realisticTokens generates n unique 78-character tokenID strings.
func realisticTokens(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%078d", uint64(7000000000000+i*37))
	}
	return out
}

// BenchmarkInternerID measures the one-time string→uint32 lookup.
// Consumers pay this once per token at translation boundaries (e.g.
// when receiving a WS event) and use the uint32 in hot loops.
func BenchmarkInternerID(b *testing.B) {
	tokens := realisticTokens(1024)
	in := NewInterner(tokens)
	var sink uint32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id, _ := in.ID(tokens[i&0x3FF])
		sink += id
	}
	_ = sink
}

// BenchmarkConsumerHotMapString is the baseline cost a high-throughput
// consumer pays per lookup if they keep `map[string]V` keyed by raw
// 78-byte tokenID. This is what zaremboeb's scan loop hits 4M times
// per scan tick.
func BenchmarkConsumerHotMapString(b *testing.B) {
	tokens := realisticTokens(1024)
	m := make(map[string]uint64, len(tokens))
	for i, t := range tokens {
		m[t] = uint64(i)
	}
	var sink uint64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += m[tokens[i&0x3FF]]
	}
	_ = sink
}

// BenchmarkConsumerHotMapUint32 is the same lookup after the consumer
// has translated tokenID → uint32 ID once (via Interner) and switched
// their hot map to map[uint32]V.
func BenchmarkConsumerHotMapUint32(b *testing.B) {
	tokens := realisticTokens(1024)
	in := NewInterner(tokens)
	m := make(map[uint32]uint64, len(tokens))
	ids := make([]uint32, len(tokens))
	for i, t := range tokens {
		id, _ := in.ID(t)
		ids[i] = id
		m[id] = uint64(i)
	}
	var sink uint64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += m[ids[i&0x3FF]]
	}
	_ = sink
}
