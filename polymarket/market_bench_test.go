package polymarket

import (
	"math/rand/v2"
	"testing"
)

// BenchmarkFeePerShareCached: hot path. With current code the cost is the
// `int(math.Round(p*10000))` index computation plus one array load. Captures
// what we'd save by keying the LUT on the int32 price tick directly.
func BenchmarkFeePerShareCached(b *testing.B) {
	fp := NewFeeParams(0.072, 0)
	prices := make([]float64, 256)
	rng := rand.New(rand.NewPCG(7, 7))
	for i := range prices {
		// snap to a 0.01 tick — typical price grid
		prices[i] = float64(rng.IntN(99)+1) / 100.0
	}
	var sink float64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += fp.FeePerShare(prices[i&0xFF])
	}
	_ = sink
}

// BenchmarkFeePerShareUncached drives the closed-form path (cache miss) by
// using a FeeParams with no precomputed array.
func BenchmarkFeePerShareUncached(b *testing.B) {
	fp := FeeParams{Rate: 0.072}
	prices := make([]float64, 256)
	rng := rand.New(rand.NewPCG(7, 7))
	for i := range prices {
		prices[i] = float64(rng.IntN(99)+1) / 100.0
	}
	var sink float64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += fp.FeePerShare(prices[i&0xFF])
	}
	_ = sink
}

// BenchmarkFeePerShareConstant pins price to a known value so the branch
// predictor and cache are warm — tightest possible upper bound.
func BenchmarkFeePerShareConstant(b *testing.B) {
	fp := NewFeeParams(0.072, 0)
	var sink float64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += fp.FeePerShare(0.50)
	}
	_ = sink
}

// BenchmarkFeePerShareKey is the int32-tick-keyed variant — the hot path
// for callers that already hold a price key.
// Skips the math.Round + multiplication.
func BenchmarkFeePerShareKey(b *testing.B) {
	fp := NewFeeParams(0.072, 0)
	keys := make([]int32, 256)
	for i := range keys {
		keys[i] = int32(1 + i*39)
	}
	var sink float64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += fp.FeePerShareKey(keys[i&0xFF])
	}
	_ = sink
}
