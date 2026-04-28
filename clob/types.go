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
	EndpointGetOrder            = "/data/order/"
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

type MakerOrder struct {
	OrderID       string `json:"order_id"`
	Owner         string `json:"owner"`
	MakerAddress  string `json:"maker_address"`
	MatchedAmount string `json:"matched_amount"`
	Price         string `json:"price"`
	FeeRateBps    string `json:"fee_rate_bps"`
	AssetID       string `json:"asset_id"`
	Outcome       string `json:"outcome"`
	Side          string `json:"side"`
}

type Trade struct {
	ID              string       `json:"id"`
	TakerOrderID    string       `json:"taker_order_id"`
	Market          string       `json:"market"`
	AssetID         string       `json:"asset_id"`
	Side            string       `json:"side"`
	Size            string       `json:"size"`
	FeeRateBps      string       `json:"fee_rate_bps"`
	Price           string       `json:"price"`
	Status          string       `json:"status"`
	MatchTime       string       `json:"match_time"`
	MatchTimeNano   string       `json:"match_time_nano,omitempty"`
	LastUpdate      string       `json:"last_update,omitempty"`
	Outcome         string       `json:"outcome"`
	BucketIndex     int          `json:"bucket_index,omitempty"`
	Owner           string       `json:"owner"`
	MakerAddress    string       `json:"maker_address"`
	TransactionHash string       `json:"transaction_hash"`
	TraderSide      string       `json:"trader_side"`
	MakerOrders     []MakerOrder `json:"maker_orders,omitempty"`
	ErrMsg          string       `json:"err_msg,omitempty"`
}

// Order is the shape returned by GET /data/orders and GET /data/order/{id}.
type Order struct {
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	Owner           string   `json:"owner"`
	MakerAddress    string   `json:"maker_address"`
	Market          string   `json:"market"`
	AssetID         string   `json:"asset_id"`
	Side            string   `json:"side"`
	OriginalSize    string   `json:"original_size"`
	SizeMatched     string   `json:"size_matched"`
	Price           string   `json:"price"`
	Outcome         string   `json:"outcome"`
	Expiration      string   `json:"expiration"`
	OrderType       string   `json:"order_type"`
	AssociateTrades []string `json:"associate_trades"`
	CreatedAt       int64    `json:"created_at"`
}

const (
	SideBuyInt  = 0
	SideSellInt = 1
	SideBuy     = "BUY"
	SideSell    = "SELL"
)

// Order status enum returned by Polymarket CLOB.
const (
	OrderStatusLive      = "LIVE"
	OrderStatusMatched   = "MATCHED"
	OrderStatusDelayed   = "DELAYED"
	OrderStatusCanceled  = "CANCELED"
	OrderStatusUnmatched = "UNMATCHED"
)

// Order type enum.
const (
	OrderTypeGTC = "GTC"
	OrderTypeFOK = "FOK"
	OrderTypeGTD = "GTD"
	OrderTypeFAK = "FAK"
)

// Response from POST /order and POST /orders (one element per submitted order).
type PostOrderResponse struct {
	Success            bool     `json:"success"`
	OrderID            string   `json:"orderID"`
	Status             string   `json:"status"`
	MakingAmount       string   `json:"makingAmount,omitempty"`
	TakingAmount       string   `json:"takingAmount,omitempty"`
	TransactionsHashes []string `json:"transactionsHashes,omitempty"`
	TradeIDs           []string `json:"tradeIDs,omitempty"`
	ErrorMsg           string   `json:"errorMsg,omitempty"`
}

// CancelResponse is the wire shape returned by DELETE /orders and
// DELETE /cancel-all. Canceled lists the order IDs the matcher actually
// removed; NotCanceled maps each rejected order ID to a reason
// (e.g. "order not found", "owner mismatch"). Both endpoints share the
// same response shape, so callers can reuse this for either.
//
// Asymmetry is deliberate on the wire: Canceled is a flat array, NotCanceled
// is an id→reason map. Empty NotCanceled (={}) is the all-success case.
type CancelResponse struct {
	Canceled    []string          `json:"canceled"`
	NotCanceled map[string]string `json:"not_canceled"`
}

// BalanceAllowance is GET /balance-allowance response.
// Polymarket serves two shapes depending on API version: older { balance, allowance }
// (both strings in pUSD 6-decimal units) and newer { balance, allowances: {addr: ""} }.
type BalanceAllowance struct {
	Balance    string            `json:"balance"`
	Allowance  string            `json:"allowance,omitempty"`
	Allowances map[string]string `json:"allowances,omitempty"`
}

// Position is GET /positions (data-api) row.
type Position struct {
	ProxyWallet        string  `json:"proxyWallet"`
	Asset              string  `json:"asset"`
	ConditionID        string  `json:"conditionId"`
	Size               float64 `json:"size"`
	AvgPrice           float64 `json:"avgPrice"`
	InitialValue       float64 `json:"initialValue"`
	CurrentValue       float64 `json:"currentValue"`
	CashPnl            float64 `json:"cashPnl"`
	PercentPnl         float64 `json:"percentPnl"`
	TotalBought        float64 `json:"totalBought"`
	RealizedPnl        float64 `json:"realizedPnl"`
	PercentRealizedPnl float64 `json:"percentRealizedPnl"`
	CurPrice           float64 `json:"curPrice"`
	Redeemable         bool    `json:"redeemable"`
	Mergeable          bool    `json:"mergeable"`
	Title              string  `json:"title"`
	Slug               string  `json:"slug"`
	Icon               string  `json:"icon"`
	EventSlug          string  `json:"eventSlug"`
	Outcome            string  `json:"outcome"`
	OutcomeIndex       int     `json:"outcomeIndex"`
	OppositeOutcome    string  `json:"oppositeOutcome"`
	OppositeAsset      string  `json:"oppositeAsset"`
	EndDate            string  `json:"endDate"`
	NegativeRisk       bool    `json:"negativeRisk"`
}

var ZeroAddress = common.HexToAddress("0x0000000000000000000000000000000000000000")

type ContractConfig struct {
	Exchange          common.Address
	Collateral        common.Address
	ConditionalTokens common.Address
}

var contractConfigs = map[int]ContractConfig{
	137: {
		Exchange:          common.HexToAddress("0xE111180000d2663C0091e4f400237545B87B996B"),
		Collateral:        common.HexToAddress("0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB"),
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
		Exchange:          common.HexToAddress("0xe2222d279d744050d28e00520010520000310F59"),
		Collateral:        common.HexToAddress("0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB"),
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
	TokenID       *big.Int
	MakerAmount   *big.Int
	TakerAmount   *big.Int
	Side          int
	SignatureType int
	Timestamp     *big.Int
	Metadata      [32]byte
	Builder       [32]byte
}

type SignedOrder struct {
	Order OrderData
	// Expiration is a POST /order wire field for GTD handling. It is not part
	// of the CLOB V2 EIP-712 signed order.
	Expiration *big.Int
	Signature  string
}

// SignedOrderJSON is the wire shape of a signed order accepted by the CLOB
// /order and /orders endpoints. Salt is emitted as a JSON number (big.Int's
// MarshalJSON renders decimal digits) — every other amount is a decimal
// string per Polymarket's API.
type SignedOrderJSON struct {
	Salt          *big.Int `json:"salt"`
	Maker         string   `json:"maker"`
	Signer        string   `json:"signer"`
	TokenID       string   `json:"tokenId"`
	MakerAmount   string   `json:"makerAmount"`
	TakerAmount   string   `json:"takerAmount"`
	Side          string   `json:"side"`
	Expiration    string   `json:"expiration"`
	SignatureType int      `json:"signatureType"`
	Timestamp     string   `json:"timestamp"`
	Metadata      string   `json:"metadata"`
	Builder       string   `json:"builder"`
	Signature     string   `json:"signature"`
}

func (so *SignedOrder) Marshal() SignedOrderJSON {
	side := SideBuy
	if so.Order.Side == SideSellInt {
		side = SideSell
	}
	return SignedOrderJSON{
		Salt:          so.Order.Salt,
		Maker:         so.Order.Maker.Hex(),
		Signer:        so.Order.Signer.Hex(),
		TokenID:       so.Order.TokenID.String(),
		MakerAmount:   so.Order.MakerAmount.String(),
		TakerAmount:   so.Order.TakerAmount.String(),
		Side:          side,
		Expiration:    signedOrderExpiration(so.Expiration),
		SignatureType: so.Order.SignatureType,
		Timestamp:     so.Order.Timestamp.String(),
		Metadata:      "0x" + common.Bytes2Hex(so.Order.Metadata[:]),
		Builder:       "0x" + common.Bytes2Hex(so.Order.Builder[:]),
		Signature:     so.Signature,
	}
}

func signedOrderExpiration(expiration *big.Int) string {
	if expiration == nil {
		return "0"
	}
	return expiration.String()
}

type PostOrderArg struct {
	Order     *SignedOrder
	OrderType string
	PostOnly  bool
	DeferExec bool
}

// PostOrderRequest is the wire shape for POST /order. PostOrders submits
// a JSON array of these.
type PostOrderRequest struct {
	Order     SignedOrderJSON `json:"order"`
	Owner     string          `json:"owner"`
	OrderType string          `json:"orderType"`
	PostOnly  bool            `json:"postOnly"`
	DeferExec bool            `json:"deferExec"`
}

type ApiCreds struct {
	ApiKey        string
	ApiSecret     string
	ApiPassphrase string
}
