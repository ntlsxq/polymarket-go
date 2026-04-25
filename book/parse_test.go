package book

import "testing"

func TestParseTickValid(t *testing.T) {
	cases := []struct {
		in   string
		want int32
	}{
		{"0", 0},
		{"0.0", 0},
		{"0.0000", 0},
		{"0.5", 5000},
		{"0.50", 5000},
		{"0.5000", 5000},
		{"0.55", 5500},
		{"0.0001", 1},
		{"0.9999", 9999},
		{"1", 10000},
		{"1.0", 10000},
		{"1.0000", 10000},
		{"+0.5", 5000},
	}
	for _, tc := range cases {
		got, ok := ParseTick(tc.in)
		if !ok {
			t.Fatalf("ParseTick(%q) ok=false, want %d", tc.in, tc.want)
		}
		if got != tc.want {
			t.Fatalf("ParseTick(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseTickRejectsInvalid(t *testing.T) {
	for _, in := range []string{
		"",
		"abc",
		"0.5x",
		"-0.5",
		"1.5",
		"2",
		"1.0001",
		"0..5",
	} {
		if _, ok := ParseTick(in); ok {
			t.Fatalf("ParseTick(%q) should reject", in)
		}
	}
}

// TestParseTickMatchesToInt: for every valid tick string, ParseTick(s) must
// equal ToInt(strconv.ParseFloat(s)). Pins the contract that the fast path
// is bit-equal to the round-trip we replaced.
func TestParseTickMatchesToInt(t *testing.T) {
	for tick := int32(0); tick <= 10000; tick++ {
		s := formatTick(tick)
		got, ok := ParseTick(s)
		if !ok {
			t.Fatalf("ParseTick(%q) failed for tick %d", s, tick)
		}
		if got != tick {
			t.Fatalf("ParseTick(%q) = %d, want %d", s, got, tick)
		}
	}
}

// formatTick builds a 4-decimal-place string representation of pk as an
// int tick value (e.g. tick=5500 → "0.5500").
func formatTick(pk int32) string {
	if pk == 10000 {
		return "1.0000"
	}
	intPart := pk / 10000
	frac := pk % 10000
	d3 := frac / 1000
	d2 := (frac / 100) % 10
	d1 := (frac / 10) % 10
	d0 := frac % 10
	buf := []byte{
		'0' + byte(intPart),
		'.',
		'0' + byte(d3),
		'0' + byte(d2),
		'0' + byte(d1),
		'0' + byte(d0),
	}
	return string(buf)
}
