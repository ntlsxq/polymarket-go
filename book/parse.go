package book

// ParseTick converts a Polymarket-shaped decimal string into an int32 tick
// key (i.e. ToInt(ParseFloat(s)) without the float64 round-trip).
//
// Accepts: integer literals "0", "1"; decimal literals like "0.55",
// "0.0001". Up to PriceMult-aligned precision (4 fractional digits).
// Extra digits beyond 4 are truncated (no rounding) — the caller is
// expected to feed prices already snapped to the tick grid by the wire
// protocol, so this never matters in practice.
//
// Returns (0, false) on syntactic errors or empty input. ok=true with
// key=0 only when the input was literally "0" or "0.000...".
func ParseTick(s string) (int32, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var (
		i        int
		intPart  int32
		fracPart int32
		fracLen  int
	)
	// Optional leading sign — Polymarket prices are always non-negative,
	// but tolerate "+0.5" defensively.
	if s[0] == '+' {
		i = 1
	}

	// Integer part.
	for i < len(s) && s[i] != '.' {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		intPart = intPart*10 + int32(c-'0')
		i++
		if intPart > 1 { // Polymarket prices are in [0, 1]; reject early.
			return 0, false
		}
	}

	// Optional fractional part.
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && fracLen < 4 {
			c := s[i]
			if c < '0' || c > '9' {
				return 0, false
			}
			fracPart = fracPart*10 + int32(c-'0')
			i++
			fracLen++
		}
		// Validate any trailing digits we don't store.
		for ; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
		}
	}

	for fracLen < 4 {
		fracPart *= 10
		fracLen++
	}
	key := intPart*PriceMult + fracPart
	if intPart == 1 && fracPart != 0 {
		// "1.5" etc. is out of [0, 1].
		return 0, false
	}
	return key, true
}
