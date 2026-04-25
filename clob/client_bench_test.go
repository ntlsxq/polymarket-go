package clob

import (
	"context"
	"crypto/ecdsa"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func benchClient(b *testing.B) (*Client, string) {
	b.Helper()
	priv, err := crypto.HexToECDSA("0101010101010101010101010101010101010101010101010101010101010101")
	if err != nil {
		b.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	c := &Client{
		host:    "https://clob.example",
		privKey: priv,
		address: addr,
		funder:  addr,
		chainID: 137,
		sigType: 0,
	}
	tokenID := "71321045679252212594626385532706912750332728571942532289631379312455583992563"
	return c, tokenID
}

// BenchmarkComputeOrderAmountsBuy measures the decimal.Decimal path used for
// every BUY order. Includes 3 NewFromFloat64, Round/Floor, Prod, and Int64
// conversions.
func BenchmarkComputeOrderAmountsBuy(b *testing.B) {
	rc := RoundingConfigs["0.01"]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = computeOrderAmounts(SideBuy, 100, 0.55, rc)
	}
}

func BenchmarkComputeOrderAmountsSell(b *testing.B) {
	rc := RoundingConfigs["0.01"]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = computeOrderAmounts(SideSell, 100, 0.55, rc)
	}
}

// BenchmarkBuildOrderEnd2End drives the entire BuildOrder call: option fan-in,
// rounding validation, decimal math, salt gen, big.Int alloc, sign. Captures
// what a market-maker pays per order placement.
func BenchmarkBuildOrderEnd2End(b *testing.B) {
	c, tokenID := benchClient(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.BuildOrder(ctx, tokenID, 0.55, 100,
			WithBuy(),
			WithMarket("0.01", false, 0),
			AsGTC(),
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildOrderWithFeeAdjust includes the fee-adjusted size path:
// math.Pow10, math.Ceil, BuyFeeRate. Common for FOK strategies.
func BenchmarkBuildOrderWithFeeAdjust(b *testing.B) {
	c, tokenID := benchClient(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.BuildOrder(ctx, tokenID, 0.55, 100,
			WithBuy(),
			WithMarket("0.01", false, 0),
			AsFOK(),
			WithFeeAdjustment(0.072, "0.01", 0.55),
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSignedOrderMarshal pins the per-order JSON shape conversion cost
// (big.Int.String() x6 + struct copy). Hits PostOrder/PostOrders.
func BenchmarkSignedOrderMarshal(b *testing.B) {
	o := fixtureOrder(42)
	so := &SignedOrder{Order: o, Signature: "0xdeadbeef"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = so.Marshal()
	}
}

// _ pin: ensure ecdsa import is used even if benches above are stripped.
var _ = (*ecdsa.PrivateKey)(nil)
var _ = (*common.Address)(nil)
