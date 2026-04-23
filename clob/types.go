package clob

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

const ClobHost = "https://clob.polymarket.com"

const (
	EndpointCreateAPIKey        = "/auth/api-key"
	EndpointDeriveAPIKey        = "/auth/derive-api-key"
	EndpointOrders              = "/data/orders"
	EndpointPostOrder           = "/order"
	EndpointPostOrders          = "/orders"
	EndpointCancelOrders        = "/orders"
	EndpointCancelAll           = "/cancel-all"
	EndpointGetBalanceAllowance = "/balance-allowance"
	EndpointGetTickSize         = "/tick-size"
	EndpointGetFeeRate          = "/fee-rate"
	EndpointPostHeartbeat       = "/v1/heartbeats"
	EndpointGetTrades           = "/data/trades"
)

type Trade struct {
	ID              string `json:"id"`
	TakerOrderID    string `json:"taker_order_id"`
	AssetID         string `json:"asset_id"`
	Side            string `json:"side"`
	Size            string `json:"size"`
	Price           string `json:"price"`
	FeeRateBps      string `json:"fee_rate_bps"`
	Status          string `json:"status"`
	TraderSide      string `json:"trader_side"`
	TransactionHash string `json:"transaction_hash"`
}

const (
	SideBuyInt  = 0
	SideSellInt = 1
	SideBuy     = "BUY"
	SideSell    = "SELL"
)

var ZeroAddress = common.HexToAddress("0x0000000000000000000000000000000000000000")

type ContractConfig struct {
	Exchange          common.Address
	Collateral        common.Address
	ConditionalTokens common.Address
}

var contractConfigs = map[int]ContractConfig{
	137: {
		Exchange:          common.HexToAddress("0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E"),
		Collateral:        common.HexToAddress("0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"),
		ConditionalTokens: common.HexToAddress("0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"),
	},
	80002: {
		Exchange:          common.HexToAddress("0xdFE02Eb6733538f8Ea35D585af8DE5958AD99E40"),
		Collateral:        common.HexToAddress("0x9c4e1703476e875070ee25b56a58b008cfb8fa78"),
		ConditionalTokens: common.HexToAddress("0x69308FB512518e39F9b16112fA8d994F4e2Bf8bB"),
	},
}

var negRiskConfigs = map[int]ContractConfig{
	137: {
		Exchange:          common.HexToAddress("0xC5d563A36AE78145C45a50134d48A1215220f80a"),
		Collateral:        common.HexToAddress("0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"),
		ConditionalTokens: common.HexToAddress("0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"),
	},
	80002: {
		Exchange:          common.HexToAddress("0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296"),
		Collateral:        common.HexToAddress("0x9c4e1703476e875070ee25b56a58b008cfb8fa78"),
		ConditionalTokens: common.HexToAddress("0x69308FB512518e39F9b16112fA8d994F4e2Bf8bB"),
	},
}

func GetContractConfig(chainID int, negRisk bool) (ContractConfig, bool) {
	m := contractConfigs
	if negRisk {
		m = negRiskConfigs
	}
	cfg, ok := m[chainID]
	return cfg, ok
}

type RoundConfig struct {
	Price  int
	Size   int
	Amount int
}

var RoundingConfigs = map[string]RoundConfig{
	"0.1":    {Price: 1, Size: 2, Amount: 3},
	"0.01":   {Price: 2, Size: 2, Amount: 4},
	"0.001":  {Price: 3, Size: 2, Amount: 5},
	"0.0001": {Price: 4, Size: 2, Amount: 6},
}

const TokenDecimals = 1_000_000

type OrderData struct {
	Salt          *big.Int
	Maker         common.Address
	Signer        common.Address
	Taker         common.Address
	TokenID       *big.Int
	MakerAmount   *big.Int
	TakerAmount   *big.Int
	Expiration    *big.Int
	Nonce         *big.Int
	FeeRateBps    *big.Int
	Side          int
	SignatureType int
}

type SignedOrder struct {
	Order     OrderData
	Signature string
}

func (so *SignedOrder) Dict() map[string]any {
	side := SideBuy
	if so.Order.Side == SideSellInt {
		side = SideSell
	}

	var salt any
	if so.Order.Salt.IsInt64() {
		salt = so.Order.Salt.Int64()
	} else {
		salt = so.Order.Salt
	}

	return map[string]any{
		"salt":          salt,
		"maker":         so.Order.Maker.Hex(),
		"signer":        so.Order.Signer.Hex(),
		"taker":         so.Order.Taker.Hex(),
		"tokenId":       so.Order.TokenID.String(),
		"makerAmount":   so.Order.MakerAmount.String(),
		"takerAmount":   so.Order.TakerAmount.String(),
		"expiration":    so.Order.Expiration.String(),
		"nonce":         so.Order.Nonce.String(),
		"feeRateBps":    so.Order.FeeRateBps.String(),
		"side":          side,
		"signatureType": so.Order.SignatureType,
		"signature":     so.Signature,
	}
}

type PostOrderArg struct {
	Order     *SignedOrder
	OrderType string
	PostOnly  bool
	DeferExec bool
}

type ApiCreds struct {
	ApiKey        string
	ApiSecret     string
	ApiPassphrase string
}
