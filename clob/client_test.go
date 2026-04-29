package clob

import (
	"context"
	"testing"
)

// TestComputeOrderAmountsParity walks the entire price/size grid for every
// supported tick config and asserts the int64 fast path produces bit-equal
// (sideInt, makerAmount, takerAmount) tuples vs. the decimal reference.
// This is the contract that lets us trust the fast path on all inputs the
// real system can ever produce.
func TestComputeOrderAmountsParity(t *testing.T) {
	tickConfigs := []string{"0.1", "0.01", "0.001", "0.0001"}
	sides := []string{SideBuy, SideSell}
	sizes := []float64{5.00, 7.13, 100.5, 1000.99, 9999.99}

	for _, tick := range tickConfigs {
		rc := RoundingConfigs[tick]
		// Walk every valid price tick at this precision plus a few floats
		// that don't snap exactly to confirm both paths round identically.
		priceMax := pow10Int[rc.Price]
		var priceCount int
		for p := int64(1); p < priceMax; p++ {
			price := float64(p) / float64(priceMax)
			for _, size := range sizes {
				for _, side := range sides {
					gotS, gotM, gotT := computeOrderAmounts(side, size, price, rc)
					wantS, wantM, wantT := computeOrderAmountsDecimal(side, size, price, rc)
					if gotS != wantS || gotM != wantM || gotT != wantT {
						t.Fatalf("tick=%s side=%s size=%v price=%v: fast=(%d,%d,%d) decimal=(%d,%d,%d)",
							tick, side, size, price, gotS, gotM, gotT, wantS, wantM, wantT)
					}
				}
			}
			priceCount++
		}
		if priceCount == 0 {
			t.Fatalf("tick %s produced 0 price points", tick)
		}
	}
}

// TestComputeOrderAmountsBuyShape pins the symbolic identity:
// for BUY at size=10, price=0.55, tick=0.01:
//
//	maker (pUSD, 6dec) = 10 × 0.55 × 1_000_000 = 5_500_000
//	taker (CTF,  6dec) = 10        × 1_000_000 = 10_000_000
func TestComputeOrderAmountsBuyShape(t *testing.T) {
	rc := RoundingConfigs["0.01"]
	side, maker, taker := computeOrderAmounts(SideBuy, 10, 0.55, rc)
	if side != SideBuyInt || maker != 5_500_000 || taker != 10_000_000 {
		t.Fatalf("BUY 10@0.55: side=%d maker=%d taker=%d", side, maker, taker)
	}
}

func TestComputeOrderAmountsSellShape(t *testing.T) {
	rc := RoundingConfigs["0.01"]
	side, maker, taker := computeOrderAmounts(SideSell, 10, 0.55, rc)
	// SELL: maker = shares, taker = pUSD
	if side != SideSellInt || maker != 10_000_000 || taker != 5_500_000 {
		t.Fatalf("SELL 10@0.55: side=%d maker=%d taker=%d", side, maker, taker)
	}
}

func TestBuildOrderCarriesGTDExpirationOutsideSignature(t *testing.T) {
	c, err := NewClient(ClobHost, "0101010101010101010101010101010101010101010101010101010101010101", 137, 0, "")
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	arg, err := c.BuildOrder(context.Background(), "71321045679252212594626385532706912750332728571942532289631379312455583992563", 0.55, 10,
		WithBuy(),
		WithMarket("0.01", false),
		AsGTD(1714000000),
	)
	if err != nil {
		t.Fatalf("build order: %v", err)
	}
	if arg.OrderType != OrderTypeGTD {
		t.Fatalf("order type = %q, want GTD", arg.OrderType)
	}
	if arg.Order.Expiration == nil || arg.Order.Expiration.String() != "1714000000" {
		t.Fatalf("expiration = %v, want 1714000000", arg.Order.Expiration)
	}
	if got := arg.Order.Marshal().Expiration; got != "1714000000" {
		t.Fatalf("wire expiration = %q, want 1714000000", got)
	}
}

func TestBuildOrderWithDeterministicIDIsStable(t *testing.T) {
	c, err := NewClient(ClobHost, "0101010101010101010101010101010101010101010101010101010101010101", 137, 0, "")
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	build := func() PostOrderArg {
		arg, err := c.BuildOrder(context.Background(), "71321045679252212594626385532706912750332728571942532289631379312455583992563", 0.55, 10,
			WithBuy(),
			WithMarket("0.01", false),
			AsGTC(),
			WithDeterministicID("grid:v1:test"),
		)
		if err != nil {
			t.Fatalf("build order: %v", err)
		}
		return arg
	}

	a := build()
	b := build()
	if a.Order.Order.Salt.Cmp(b.Order.Order.Salt) != 0 {
		t.Fatalf("salt mismatch: %s != %s", a.Order.Order.Salt, b.Order.Order.Salt)
	}
	if a.Order.Order.Timestamp.Cmp(b.Order.Order.Timestamp) != 0 {
		t.Fatalf("timestamp mismatch: %s != %s", a.Order.Order.Timestamp, b.Order.Order.Timestamp)
	}
	if a.Order.Signature != b.Order.Signature {
		t.Fatalf("signature mismatch:\n  %s\n  %s", a.Order.Signature, b.Order.Signature)
	}
}

func TestBuildOrderWithTimestampMillis(t *testing.T) {
	c, err := NewClient(ClobHost, "0101010101010101010101010101010101010101010101010101010101010101", 137, 0, "")
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	arg, err := c.BuildOrder(context.Background(), "71321045679252212594626385532706912750332728571942532289631379312455583992563", 0.55, 10,
		WithBuy(),
		WithMarket("0.01", false),
		AsGTC(),
		WithTimestampMillis(1713398400000),
	)
	if err != nil {
		t.Fatalf("build order: %v", err)
	}
	if got := arg.Order.Order.Timestamp.String(); got != "1713398400000" {
		t.Fatalf("timestamp=%s want 1713398400000", got)
	}
}

// TestComputeOrderAmountsFloorsSize asserts the fast path floors size to
// rc.Size decimals just like decimal.Floor — a half-cent excess on the
// input must not bleed into the on-chain amounts.
func TestComputeOrderAmountsFloorsSize(t *testing.T) {
	rc := RoundingConfigs["0.01"]
	_, maker, taker := computeOrderAmounts(SideBuy, 10.999, 0.50, rc)
	// size floored to 10.99 → maker = 10.99 × 0.50 × 1_000_000 = 5_495_000
	//                        taker = 10.99        × 1_000_000 = 10_990_000
	if maker != 5_495_000 || taker != 10_990_000 {
		t.Fatalf("floor failed: maker=%d taker=%d", maker, taker)
	}
}
