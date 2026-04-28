package clob

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestPackSplitMergeUsesPUSDAsCollateral(t *testing.T) {
	oc := newTestOnChainClient(t)

	target, calldata, err := oc.packSplitMerge("splitPosition", "0x624ce52f1aa210d37e00578591aa41843dc5322d76626397631eb739f4715731", 5_000_000, false)
	if err != nil {
		t.Fatalf("packSplitMerge: %v", err)
	}
	if target != common.HexToAddress(CTFAddr) {
		t.Fatalf("target=%s want %s", target.Hex(), CTFAddr)
	}

	args, err := ctfABI.Methods["splitPosition"].Inputs.Unpack(calldata[4:])
	if err != nil {
		t.Fatalf("unpack splitPosition: %v", err)
	}
	if got, want := args[0].(common.Address), common.HexToAddress(PUSDAddr); got != want {
		t.Fatalf("collateral=%s want pUSD %s", got.Hex(), want.Hex())
	}
	if got, want := args[4].(*big.Int), big.NewInt(5_000_000); got.Cmp(want) != 0 {
		t.Fatalf("amount=%s want %s", got, want)
	}
}

func TestPackRedeemUsesPUSDAsCollateral(t *testing.T) {
	oc := newTestOnChainClient(t)

	target, calldata, err := oc.packRedeem("0x624ce52f1aa210d37e00578591aa41843dc5322d76626397631eb739f4715731")
	if err != nil {
		t.Fatalf("packRedeem: %v", err)
	}
	if target != common.HexToAddress(CTFAddr) {
		t.Fatalf("target=%s want %s", target.Hex(), CTFAddr)
	}

	args, err := ctfABI.Methods["redeemPositions"].Inputs.Unpack(calldata[4:])
	if err != nil {
		t.Fatalf("unpack redeemPositions: %v", err)
	}
	if got, want := args[0].(common.Address), common.HexToAddress(PUSDAddr); got != want {
		t.Fatalf("collateral=%s want pUSD %s", got.Hex(), want.Hex())
	}
}

func TestMaxGuardRelayGasLimit(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x00}
	got, intrinsic, err := maxGuardRelayGasLimit(data)
	if err != nil {
		t.Fatalf("maxGuardRelayGasLimit: %v", err)
	}

	wantIntrinsic := uint64(txBaseGas + 2*txDataZeroGas + 2*txDataNonZeroGas)
	if intrinsic != wantIntrinsic {
		t.Fatalf("intrinsic=%d want %d", intrinsic, wantIntrinsic)
	}
	want := uint64(relayerOuterGasLimit - wantIntrinsic - relayHubGuardReserveGas - relayHubPreGuardGas)
	if got != want {
		t.Fatalf("gasLimit=%d want %d", got, want)
	}
}

func newTestOnChainClient(t *testing.T) *OnChainClient {
	t.Helper()
	initABIs()
	if abiInitErr != nil {
		t.Fatalf("init ABIs: %v", abiInitErr)
	}
	return &OnChainClient{
		ctfAddr:        common.HexToAddress(CTFAddr),
		negRiskCTFAddr: common.HexToAddress(NegRiskCTFAddr),
		collateralAddr: common.HexToAddress(PUSDAddr),
	}
}
