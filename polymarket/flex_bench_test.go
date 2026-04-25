package polymarket

import (
	"strconv"
	"testing"

	"github.com/goccy/go-json"
)

// BenchmarkFlexFloatStringForm hits the predominant Polymarket shape:
// price/size as quoted decimals. Captures the json.Unmarshal +
// strconv.ParseFloat round-trip cost — baseline for a custom int-tick parser.
func BenchmarkFlexFloatStringForm(b *testing.B) {
	raw := []byte(`"0.4321"`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v flexFloat
		_ = json.Unmarshal(raw, &v)
	}
}

func BenchmarkFlexFloatNumberForm(b *testing.B) {
	raw := []byte(`0.4321`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v flexFloat
		_ = json.Unmarshal(raw, &v)
	}
}

// BenchmarkParseFloatRaw is the irreducible cost of strconv.ParseFloat we
// pay per price/size in price_change events.
func BenchmarkParseFloatRaw(b *testing.B) {
	s := "0.4321"
	var sink float64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := strconv.ParseFloat(s, 64)
		sink += v
	}
	_ = sink
}

// BenchmarkParseTickInt32Direct shows the headroom for a custom decimal-tick
// parser that goes string -> int32 (price * 10_000) without float64. This is
// the candidate replacement for ParseFloat on the WS hot path.
func BenchmarkParseTickInt32Direct(b *testing.B) {
	s := "0.4321"
	var sink int32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += parseTickInt32Hint(s)
	}
	_ = sink
}

// parseTickInt32Hint is a reference implementation used only for the bench
// so we can compare the ceiling against ParseFloat. Not exported into
// production code.
func parseTickInt32Hint(s string) int32 {
	var (
		intPart  int32
		fracPart int32
		fracLen  int
		i        int
	)
	for i < len(s) && s[i] != '.' {
		intPart = intPart*10 + int32(s[i]-'0')
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && fracLen < 4 {
			fracPart = fracPart*10 + int32(s[i]-'0')
			i++
			fracLen++
		}
	}
	for fracLen < 4 {
		fracPart *= 10
		fracLen++
	}
	return intPart*10000 + fracPart
}
