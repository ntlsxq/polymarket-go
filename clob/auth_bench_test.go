package clob

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// BenchmarkEncodePackedSmall measures the per-field append-loop allocation
// pattern. encodePacked is called once per struct hash; replacing it with a
// pre-allocated buffer is a Tier-2 optimization candidate.
func BenchmarkEncodePackedSmall(b *testing.B) {
	a := make([]byte, 32)
	c := make([]byte, 32)
	d := make([]byte, 32)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encodePacked(a, c, d)
	}
}

// BenchmarkEncodePackedLarge approximates the order-struct shape: type-hash +
// 12 × 32-byte fields = 13 parts. Pin the alloc count we'd save with a
// fixed-size buffer.
func BenchmarkEncodePackedLarge(b *testing.B) {
	parts := make([][]byte, 13)
	for i := range parts {
		parts[i] = make([]byte, 32)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encodePacked(parts...)
	}
}

func BenchmarkBuildOrderStructHash(b *testing.B) {
	o := fixtureOrder(42)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildOrderStructHash(o)
	}
}

func BenchmarkBuildClobAuthDomainSeparator(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildClobAuthDomainSeparator(137)
	}
}

func BenchmarkBuildCTFDomainSeparator(b *testing.B) {
	addr := common.HexToAddress("0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildCTFDomainSeparator(137, addr)
	}
}

// BenchmarkSignEIP712Digest is the bare crypto.Sign cost — the irreducible
// floor for SignOrder. Useful to know what fraction of order-signing latency
// comes from secp256k1 vs encoding.
func BenchmarkSignEIP712Digest(b *testing.B) {
	priv, err := crypto.HexToECDSA("0101010101010101010101010101010101010101010101010101010101010101")
	if err != nil {
		b.Fatal(err)
	}
	domain := buildClobAuthDomainSeparator(137)
	hash := buildOrderStructHash(fixtureOrder(42))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = signEIP712Digest(priv, domain, hash)
	}
}

// BenchmarkSignOrderEnd2End drives the full path BuildOrder hits per signed
// order: domain + struct hash + crypto.Sign + hex encode.
func BenchmarkSignOrderEnd2End(b *testing.B) {
	priv, err := crypto.HexToECDSA("0101010101010101010101010101010101010101010101010101010101010101")
	if err != nil {
		b.Fatal(err)
	}
	o := fixtureOrder(42)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SignOrder(priv, 137, o, false)
	}
}

func BenchmarkBuildHMACSignature(b *testing.B) {
	secret := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = buildHMACSignature(secret, "1700000000", "POST", "/order", `{"x":1}`)
	}
}

// BenchmarkPadTo32 isolates the per-field padTo32 alloc — happens 3-4 times
// per address-encoding plus 12 times per uint256 in the order struct hash.
func BenchmarkPadTo32(b *testing.B) {
	src := []byte{0x01, 0x02, 0x03, 0x04}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = padTo32(src)
	}
}

// BenchmarkEncodeUint256 measures the big.Int copy + math.U256Bytes path.
// Each order struct hash makes ≥6 of these calls.
func BenchmarkEncodeUint256(b *testing.B) {
	v := big.NewInt(1234567890)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encodeUint256(v)
	}
}
