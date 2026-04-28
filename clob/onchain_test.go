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

func TestPackSplitMergeNegRiskUsesCollateralAdapter(t *testing.T) {
	oc := newTestOnChainClient(t)

	target, calldata, err := oc.packSplitMerge("splitPosition", "0x624ce52f1aa210d37e00578591aa41843dc5322d76626397631eb739f4715731", 5_000_000, true)
	if err != nil {
		t.Fatalf("packSplitMerge: %v", err)
	}
	if target != common.HexToAddress(NegRiskCtfCollateralAdapterAddr) {
		t.Fatalf("target=%s want %s", target.Hex(), NegRiskCtfCollateralAdapterAddr)
	}

	args, err := ctfABI.Methods["splitPosition"].Inputs.Unpack(calldata[4:])
	if err != nil {
		t.Fatalf("unpack splitPosition: %v", err)
	}
	if got, want := args[0].(common.Address), common.HexToAddress(PUSDAddr); got != want {
		t.Fatalf("collateral=%s want pUSD %s", got.Hex(), want.Hex())
	}
}

func TestPackConvertUsesNegRiskCollateralAdapter(t *testing.T) {
	oc := newTestOnChainClient(t)

	target, _, err := oc.packConvert("0x624ce52f1aa210d37e00578591aa41843dc5322d76626397631eb739f4715731", 1, 5_000_000)
	if err != nil {
		t.Fatalf("packConvert: %v", err)
	}
	if target != common.HexToAddress(NegRiskCtfCollateralAdapterAddr) {
		t.Fatalf("target=%s want %s", target.Hex(), NegRiskCtfCollateralAdapterAddr)
	}
}

func TestTxBuilderApproveNegRiskUsesCollateralAdapter(t *testing.T) {
	oc := newTestOnChainClient(t)

	tx := oc.NewTx().Approve(true)
	if tx.err != nil {
		t.Fatalf("Approve: %v", tx.err)
	}
	if len(tx.ops) != 2 {
		t.Fatalf("ops=%d want 2", len(tx.ops))
	}
	if tx.ops[0].target != common.HexToAddress(PUSDAddr) {
		t.Fatalf("approve target=%s want pUSD", tx.ops[0].target.Hex())
	}
	approveArgs, err := erc20ApproveABI.Methods["approve"].Inputs.Unpack(tx.ops[0].calldata[4:])
	if err != nil {
		t.Fatalf("unpack approve: %v", err)
	}
	if got, want := approveArgs[0].(common.Address), common.HexToAddress(NegRiskCtfCollateralAdapterAddr); got != want {
		t.Fatalf("spender=%s want %s", got.Hex(), want.Hex())
	}

	if tx.ops[1].target != common.HexToAddress(CTFAddr) {
		t.Fatalf("setApprovalForAll target=%s want CTF", tx.ops[1].target.Hex())
	}
	approvalArgs, err := ctfABI.Methods["setApprovalForAll"].Inputs.Unpack(tx.ops[1].calldata[4:])
	if err != nil {
		t.Fatalf("unpack setApprovalForAll: %v", err)
	}
	if got, want := approvalArgs[0].(common.Address), common.HexToAddress(NegRiskCtfCollateralAdapterAddr); got != want {
		t.Fatalf("operator=%s want %s", got.Hex(), want.Hex())
	}
	if got := approvalArgs[1].(bool); !got {
		t.Fatalf("approved=%v want true", got)
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
		ctfAddr:                      common.HexToAddress(CTFAddr),
		ctfCollateralAdapterAddr:     common.HexToAddress(CtfCollateralAdapterAddr),
		negRiskCTFAddr:               common.HexToAddress(NegRiskCTFAddr),
		negRiskCollateralAdapterAddr: common.HexToAddress(NegRiskCtfCollateralAdapterAddr),
		collateralAddr:               common.HexToAddress(PUSDAddr),
	}
}
