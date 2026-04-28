package clob

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
)

const (
	CTFAddr                = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
	NegRiskCTFAddr         = "0xd91e80cF2E7be2e162c6513ceD06f1dD0dA35296"
	PUSDAddr               = "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB"
	GSNRelayHub            = "0xD216153c06E857cD7f72665E0aF1d7D82172F494"
	ProxyWalletFactoryAddr = "0xaB45c5A4B0c941a2F231C04C3f49182e1A254052"
	ProxyInitCodeHash      = "d21df8dc65880a8606f09fe0ce3df9b8869287ab0b058be05aa9e8af6330a00b"
	PUSDDecimals           = 6
)

const (
	// Polymarket's relayer submits RelayHub transactions with a 10M outer gas
	// limit. RelayHub's guard requires signedGasLimit + 350k reserve to fit
	// after tx intrinsic/calldata gas and the pre-guard execution cost.
	relayerOuterGasLimit    = 10_000_000
	relayHubGuardReserveGas = 350_000
	relayHubPreGuardGas     = 25_000
	txBaseGas               = 21_000
	txDataZeroGas           = 4
	txDataNonZeroGas        = 16
	maxUint256Hex           = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	callTypeCall            = 1
	relayerURL              = "https://relayer-v2.polymarket.com"
)

const ctfABIJSON = `[
	{"name":"splitPosition","type":"function","inputs":[{"name":"collateralToken","type":"address"},{"name":"parentCollectionId","type":"bytes32"},{"name":"conditionId","type":"bytes32"},{"name":"partition","type":"uint256[]"},{"name":"amount","type":"uint256"}],"outputs":[]},
	{"name":"mergePositions","type":"function","inputs":[{"name":"collateralToken","type":"address"},{"name":"parentCollectionId","type":"bytes32"},{"name":"conditionId","type":"bytes32"},{"name":"partition","type":"uint256[]"},{"name":"amount","type":"uint256"}],"outputs":[]},
	{"name":"redeemPositions","type":"function","inputs":[{"name":"collateralToken","type":"address"},{"name":"parentCollectionId","type":"bytes32"},{"name":"conditionId","type":"bytes32"},{"name":"indexSets","type":"uint256[]"}],"outputs":[]},
	{"name":"setApprovalForAll","type":"function","inputs":[{"name":"operator","type":"address"},{"name":"approved","type":"bool"}],"outputs":[]}
]`

const negRiskABIJSON = `[
	{"name":"convertPositions","type":"function","inputs":[{"name":"_marketId","type":"bytes32"},{"name":"_indexSet","type":"uint256"},{"name":"_amount","type":"uint256"}],"outputs":[]}
]`

const erc20ApproveABIJSON = `[
	{"name":"approve","type":"function","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
]`

const proxyWalletFactoryABIJSON = `[
	{"name":"proxy","type":"function","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"typeCode","type":"uint8"},{"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"}]}],"outputs":[{"name":"","type":"bytes[]"}]}
]`

const relayHubABIJSON = `[
	{"name":"relayCall","type":"function","inputs":[{"name":"from","type":"address"},{"name":"recipient","type":"address"},{"name":"encodedFunction","type":"bytes"},{"name":"transactionFee","type":"uint256"},{"name":"gasPrice","type":"uint256"},{"name":"gasLimit","type":"uint256"},{"name":"nonce","type":"uint256"},{"name":"signature","type":"bytes"},{"name":"approvalData","type":"bytes"}],"outputs":[]}
]`

var (
	ctfABI                abi.ABI
	negRiskABI            abi.ABI
	erc20ApproveABI       abi.ABI
	proxyWalletFactoryABI abi.ABI
	relayHubABI           abi.ABI
	abiInitOnce           sync.Once
	abiInitErr            error
)

func initABIs() {
	abiInitOnce.Do(func() {
		for _, p := range []struct {
			dst  *abi.ABI
			json string
			name string
		}{
			{&ctfABI, ctfABIJSON, "CTF"},
			{&negRiskABI, negRiskABIJSON, "NegRisk"},
			{&erc20ApproveABI, erc20ApproveABIJSON, "ERC20"},
			{&proxyWalletFactoryABI, proxyWalletFactoryABIJSON, "ProxyWalletFactory"},
			{&relayHubABI, relayHubABIJSON, "RelayHub"},
		} {
			a, err := abi.JSON(strings.NewReader(p.json))
			if err != nil {
				abiInitErr = fmt.Errorf("parse %s ABI: %w", p.name, err)
				return
			}
			*p.dst = a
		}
	})
}

var (
	BinaryPartition        = []*big.Int{big.NewInt(1), big.NewInt(2)}
	parentCollectionIDZero [32]byte
)

type TxResult struct {
	TxHash   string
	Status   uint64
	GasUsed  uint64
	ErrorMsg string
}

type OnChainClient struct {
	relayerClient  *http.Client
	eth            *ethclient.Client
	privKey        *ecdsa.PrivateKey
	fromAddr       common.Address
	factoryAddr    common.Address
	ctfAddr        common.Address
	negRiskCTFAddr common.Address
	collateralAddr common.Address
	relayHub       common.Address
	relayerAPIKey  string
	approvedMu     sync.Mutex
	approved       map[common.Address]bool
}

type OnChainConfig struct {
	PrivateKey    *ecdsa.PrivateKey
	RelayerAPIKey string
	PolygonRPC    string
}

func NewOnChainClient(cfg OnChainConfig) (*OnChainClient, error) {
	initABIs()
	if abiInitErr != nil {
		return nil, abiInitErr
	}
	if cfg.RelayerAPIKey == "" {
		return nil, fmt.Errorf("OnChainConfig.RelayerAPIKey is required")
	}
	if cfg.PolygonRPC == "" {
		return nil, fmt.Errorf("OnChainConfig.PolygonRPC is required")
	}

	eth, err := ethclient.Dial(cfg.PolygonRPC)
	if err != nil {
		return nil, fmt.Errorf("dial polygon rpc: %w", err)
	}

	fromAddr := crypto.PubkeyToAddress(cfg.PrivateKey.PublicKey)

	return &OnChainClient{
		relayerClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		eth:            eth,
		privKey:        cfg.PrivateKey,
		fromAddr:       fromAddr,
		factoryAddr:    common.HexToAddress(ProxyWalletFactoryAddr),
		ctfAddr:        common.HexToAddress(CTFAddr),
		negRiskCTFAddr: common.HexToAddress(NegRiskCTFAddr),
		collateralAddr: common.HexToAddress(PUSDAddr),
		relayHub:       common.HexToAddress(GSNRelayHub),
		relayerAPIKey:  cfg.RelayerAPIKey,
		approved:       make(map[common.Address]bool),
	}, nil
}

func (oc *OnChainClient) Close() {
	if oc.eth != nil {
		oc.eth.Close()
	}
}

func (oc *OnChainClient) SplitPosition(ctx context.Context, conditionId string, amount int, negRisk bool) (string, error) {
	target, calldata, err := oc.packSplitMerge("splitPosition", conditionId, amount, negRisk)
	if err != nil {
		return "", err
	}
	txID, err := oc.sendProxyTx(ctx, target, calldata)
	if err != nil {
		return "", fmt.Errorf("splitPosition: %w", err)
	}
	log.Info().Str("tx", txID).Int("amount", amount).Msg("[ONCHAIN] split_sent")
	return txID, nil
}

func (oc *OnChainClient) MergePositions(ctx context.Context, conditionId string, amount int, negRisk bool) (string, error) {
	target, calldata, err := oc.packSplitMerge("mergePositions", conditionId, amount, negRisk)
	if err != nil {
		return "", err
	}
	txID, err := oc.sendProxyTx(ctx, target, calldata)
	if err != nil {
		return "", fmt.Errorf("mergePositions: %w", err)
	}
	log.Info().Str("tx", txID).Int("amount", amount).Msg("[ONCHAIN] merge_sent")
	return txID, nil
}

func (oc *OnChainClient) packSplitMerge(method, conditionId string, amount int, negRisk bool) (common.Address, []byte, error) {
	var condID [32]byte
	condBytes := common.FromHex(conditionId)
	copy(condID[32-len(condBytes):], condBytes)
	amountBig := big.NewInt(int64(amount))

	target := oc.ctfAddr
	if negRisk {
		target = oc.negRiskCTFAddr
	}
	calldata, err := ctfABI.Pack(method, oc.collateralAddr, parentCollectionIDZero, condID, BinaryPartition, amountBig)
	return target, calldata, err
}

func (oc *OnChainClient) packConvert(marketId string, indexSet int, amount int) (common.Address, []byte, error) {
	var mID [32]byte
	mBytes := common.FromHex(marketId)
	copy(mID[32-len(mBytes):], mBytes)

	calldata, err := negRiskABI.Pack("convertPositions", mID, big.NewInt(int64(indexSet)), big.NewInt(int64(amount)))
	return oc.negRiskCTFAddr, calldata, err
}

func (oc *OnChainClient) packRedeem(conditionId string) (common.Address, []byte, error) {
	var condID [32]byte
	condBytes := common.FromHex(conditionId)
	copy(condID[32-len(condBytes):], condBytes)

	calldata, err := ctfABI.Pack("redeemPositions", oc.collateralAddr, parentCollectionIDZero, condID, BinaryPartition)
	return oc.ctfAddr, calldata, err
}

type TxBuilder struct {
	oc  *OnChainClient
	ops []txOp
	err error
}

type txOp struct {
	target   common.Address
	calldata []byte
}

func (oc *OnChainClient) NewTx() *TxBuilder {
	return &TxBuilder{oc: oc}
}

func (b *TxBuilder) Split(conditionId string, amount int, negRisk bool) *TxBuilder {
	if b.err != nil {
		return b
	}
	target, calldata, err := b.oc.packSplitMerge("splitPosition", conditionId, amount, negRisk)
	if err != nil {
		b.err = err
		return b
	}
	b.ops = append(b.ops, txOp{target, calldata})
	return b
}

func (b *TxBuilder) Merge(conditionId string, amount int, negRisk bool) *TxBuilder {
	if b.err != nil {
		return b
	}
	target, calldata, err := b.oc.packSplitMerge("mergePositions", conditionId, amount, negRisk)
	if err != nil {
		b.err = err
		return b
	}
	b.ops = append(b.ops, txOp{target, calldata})
	return b
}

func (b *TxBuilder) Convert(marketId string, indexSet int, amount int) *TxBuilder {
	if b.err != nil {
		return b
	}
	target, calldata, err := b.oc.packConvert(marketId, indexSet, amount)
	if err != nil {
		b.err = err
		return b
	}
	b.ops = append(b.ops, txOp{target, calldata})
	return b
}

func (b *TxBuilder) Redeem(conditionId string) *TxBuilder {
	if b.err != nil {
		return b
	}
	target, calldata, err := b.oc.packRedeem(conditionId)
	if err != nil {
		b.err = err
		return b
	}
	b.ops = append(b.ops, txOp{target, calldata})
	return b
}

func (b *TxBuilder) Approve(negRisk bool) *TxBuilder {
	if b.err != nil {
		return b
	}
	spender := b.oc.ctfAddr
	if negRisk {
		spender = b.oc.negRiskCTFAddr
	}
	maxUint256 := new(big.Int)
	maxUint256.SetString(maxUint256Hex, 16)
	calldata, err := erc20ApproveABI.Pack("approve", spender, maxUint256)
	if err != nil {
		b.err = err
		return b
	}
	b.ops = append(b.ops, txOp{b.oc.collateralAddr, calldata})

	if negRisk {
		cd, err := ctfABI.Pack("setApprovalForAll", b.oc.negRiskCTFAddr, true)
		if err != nil {
			b.err = err
			return b
		}
		b.ops = append(b.ops, txOp{b.oc.ctfAddr, cd})
	}
	return b
}

func (b *TxBuilder) Send(ctx context.Context) (string, error) {
	if b.err != nil {
		return "", b.err
	}
	if len(b.ops) == 0 {
		return "", fmt.Errorf("empty transaction")
	}
	calls := make([]proxyCallArg, len(b.ops))
	for i, op := range b.ops {
		calls[i] = proxyCallArg{callTypeCall, op.target, big.NewInt(0), op.calldata}
	}
	txID, err := b.oc.sendProxyTxBatch(ctx, calls)
	if err != nil {
		return "", err
	}
	log.Info().Int("ops", len(b.ops)).Str("tx", txID).Msg("[ONCHAIN] batch_sent")
	return txID, nil
}

func (oc *OnChainClient) EnsureApproval(ctx context.Context, spender common.Address) (string, error) {
	oc.approvedMu.Lock()
	if oc.approved[spender] {
		oc.approvedMu.Unlock()
		return "", nil
	}
	oc.approvedMu.Unlock()

	maxUint256 := new(big.Int)
	maxUint256.SetString(maxUint256Hex, 16)
	calldata, err := erc20ApproveABI.Pack("approve", spender, maxUint256)
	if err != nil {
		return "", err
	}
	txID, err := oc.sendProxyTx(ctx, oc.collateralAddr, calldata)
	if err != nil {
		return "", fmt.Errorf("approve: %w", err)
	}
	oc.approvedMu.Lock()
	oc.approved[spender] = true
	oc.approvedMu.Unlock()

	log.Info().Str("tx", txID).Str("spender", spender.Hex()).Msg("[ONCHAIN] approval_sent")
	return txID, nil
}

func (oc *OnChainClient) EnsureApprovalForSplit(ctx context.Context, negRisk bool) (string, error) {
	if negRisk {
		return oc.EnsureApproval(ctx, oc.negRiskCTFAddr)
	}
	return oc.EnsureApproval(ctx, oc.ctfAddr)
}
