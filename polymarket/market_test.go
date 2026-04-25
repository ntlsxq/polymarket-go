package polymarket

import (
	"math"
	"testing"
)

func TestFeePerShareSymmetric(t *testing.T) {
	fp := NewFeeParams(0.072, 0)
	for _, p := range []float64{0.10, 0.30, 0.50, 0.70, 0.90} {
		a := fp.FeePerShare(p)
		b := fp.FeePerShare(1 - p)
		if math.Abs(a-b) > 1e-12 {
			t.Fatalf("fee not symmetric at p=%.4f: %.10f vs %.10f", p, a, b)
		}
	}
}

func TestFeePerShareEdgesZero(t *testing.T) {
	fp := NewFeeParams(0.072, 0)
	if v := fp.FeePerShare(0); v != 0 {
		t.Fatalf("p=0 must be zero, got %v", v)
	}
	if v := fp.FeePerShare(1); v != 0 {
		t.Fatalf("p=1 must be zero, got %v", v)
	}
}

// FeePerShareWithRate already folds Rebate in via fp.Rebate, so the cache
// must equal it without an extra rebate multiplication.
func TestFeePerShareCacheMatchesFormula(t *testing.T) {
	fp := NewFeeParams(0.072, 0.05)
	for i := 1; i < feeCacheSize-1; i++ {
		p := float64(i) / 10000.0
		cached := fp.FeePerShare(p)
		direct := fp.FeePerShareWithRate(p, fp.Rate)
		if math.Abs(cached-direct) > 1e-15 {
			t.Fatalf("cache/formula mismatch at p=%.4f: cached=%.16f direct=%.16f", p, cached, direct)
		}
	}
}

func TestFeePerShareFormula(t *testing.T) {
	rate := 0.072
	p := 0.5
	got := FeeParams{}.FeePerShareWithRate(p, rate)
	want := rate * p * (1 - p)
	if math.Abs(got-want) > 1e-15 {
		t.Fatalf("fee formula = %v, want %v", got, want)
	}
}

func TestFeePerShareApplysRebate(t *testing.T) {
	fp := FeeParams{Rebate: 0.5}
	got := fp.FeePerShareWithRate(0.5, 0.072)
	want := 0.072 * 0.5 * 0.5 * 0.5
	if math.Abs(got-want) > 1e-15 {
		t.Fatalf("rebate fee = %v, want %v", got, want)
	}
}
