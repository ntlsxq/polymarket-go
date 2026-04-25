package book

import (
	"strconv"
	"testing"
)

// BenchmarkParseTick measures the custom int32 parser at typical Polymarket
// price strings. Compare against ParseFloatThenToInt below.
func BenchmarkParseTick(b *testing.B) {
	prices := []string{"0.5500", "0.0001", "0.9999", "0.55", "0.4321"}
	var sink int32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k, _ := ParseTick(prices[i&3])
		sink += k
	}
	_ = sink
}

// BenchmarkParseFloatThenToInt is the round-trip that ParseTick replaces:
// strconv.ParseFloat → math.Round → int32. This is the cost the WS hot
// path used to pay per price field.
func BenchmarkParseFloatThenToInt(b *testing.B) {
	prices := []string{"0.5500", "0.0001", "0.9999", "0.55", "0.4321"}
	var sink int32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, _ := strconv.ParseFloat(prices[i&3], 64)
		sink += ToInt(f)
	}
	_ = sink
}
