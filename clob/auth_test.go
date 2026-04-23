package clob

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func fixtureOrder(salt int64) OrderData {
	tokenID, _ := new(big.Int).SetString("71321045679252212594626385532706912750332728571942532289631379312455583992563", 10)
	return OrderData{
		Salt:          big.NewInt(salt),
		Maker:         common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Signer:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Taker:         ZeroAddress,
		TokenID:       tokenID,
		MakerAmount:   big.NewInt(5_000_000),
		TakerAmount:   big.NewInt(2_500_000),
		Expiration:    big.NewInt(0),
		Nonce:         big.NewInt(0),
		FeeRateBps:    big.NewInt(0),
		Side:          SideSellInt,
		SignatureType: 0,
	}
}

func TestStructHashDeterminism(t *testing.T) {
	o := fixtureOrder(42)

	h1 := buildOrderStructHash(o)
	h2 := buildOrderStructHash(o)

	if !bytes.Equal(h1, h2) {
		t.Fatalf("struct hash non-deterministic: %x != %x", h1, h2)
	}
	if len(h1) != 32 {
		t.Fatalf("struct hash wrong length: %d", len(h1))
	}
}

func TestSaltDrivesHash(t *testing.T) {
	h1 := buildOrderStructHash(fixtureOrder(1))
	h2 := buildOrderStructHash(fixtureOrder(2))

	if bytes.Equal(h1, h2) {
		t.Fatalf("different salts produced same hash: %x", h1)
	}
}

func TestSignOrderDeterminism(t *testing.T) {

	priv, err := crypto.HexToECDSA("0101010101010101010101010101010101010101010101010101010101010101")
	if err != nil {
		t.Fatalf("load key: %v", err)
	}

	o := fixtureOrder(42)

	s1, err := SignOrder(priv, 137, o, false)
	if err != nil {
		t.Fatalf("sign 1: %v", err)
	}
	s2, err := SignOrder(priv, 137, o, false)
	if err != nil {
		t.Fatalf("sign 2: %v", err)
	}

	if s1 != s2 {
		t.Fatalf("signature non-deterministic:\n  %s\n  %s", s1, s2)
	}
}

func TestSaltChangesSignature(t *testing.T) {
	priv, err := crypto.HexToECDSA("0101010101010101010101010101010101010101010101010101010101010101")
	if err != nil {
		t.Fatalf("load key: %v", err)
	}

	s1, err := SignOrder(priv, 137, fixtureOrder(1), false)
	if err != nil {
		t.Fatalf("sign 1: %v", err)
	}
	s2, err := SignOrder(priv, 137, fixtureOrder(2), false)
	if err != nil {
		t.Fatalf("sign 2: %v", err)
	}

	if s1 == s2 {
		t.Fatal("different salts produced same signature")
	}
}

func TestCurrentGenerateSaltIsRandom(t *testing.T) {
	a := generateSalt()
	b := generateSalt()
	if a.Cmp(b) == 0 {
		t.Fatalf("generateSalt was deterministic — test fixture broken: %s", a.String())
	}
}
