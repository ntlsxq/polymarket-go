package clob

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/goccy/go-json"
	"github.com/govalues/decimal"
	"github.com/rs/zerolog/log"
)

type Client struct {
	host       string
	httpClient *http.Client
	privKey    *ecdsa.PrivateKey
	address    common.Address
	funder     common.Address
	creds      *ApiCreds
	chainID    int
	sigType    int

	tickSizeMu        sync.RWMutex
	tickSizeCache     map[string]string
	tickSizeCacheTime map[string]time.Time
	tickSizeTTL       time.Duration
}

type Option func(*Client)

func WithTransport(rt http.RoundTripper) Option {
	return func(c *Client) {
		c.httpClient = &http.Client{Timeout: 30 * time.Second, Transport: rt}
	}
}

func NewClient(host, privateKeyHex string, chainID, sigType int, funderAddr string, opts ...Option) (*Client, error) {
	pk := strings.TrimPrefix(privateKeyHex, "0x")
	privKey, err := crypto.HexToECDSA(pk)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	address := crypto.PubkeyToAddress(privKey.PublicKey)

	var funder common.Address
	if funderAddr != "" {
		funder = common.HexToAddress(funderAddr)
	} else {
		funder = address
	}

	c := &Client{
		host: strings.TrimRight(host, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		privKey:           privKey,
		address:           address,
		funder:            funder,
		chainID:           chainID,
		sigType:           sigType,
		tickSizeCache:     make(map[string]string),
		tickSizeCacheTime: make(map[string]time.Time),
		tickSizeTTL:       5 * time.Minute,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func (c *Client) InitAuth() error {
	creds, err := DeriveApiCreds(c.host, c.privKey, c.chainID)
	if err != nil {
		return err
	}
	c.creds = creds
	return nil
}

func (c *Client) Creds() *ApiCreds { return c.creds }

func (c *Client) Funder() string { return strings.ToLower(c.funder.Hex()) }

func (c *Client) doRequest(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	var bodyBytes []byte
	var bodyStr string
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyStr = string(bodyBytes)
	}

	signPath := path
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		signPath = path[:idx]
	}

	headers, err := L2Headers(c.creds, c.address, method, signPath, bodyStr)
	if err != nil {
		return nil, fmt.Errorf("build L2 headers: %w", err)
	}

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.host+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "go_clob_client")
	if method == "GET" {
		req.Header.Set("Accept-Encoding", "gzip")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var respBody []byte
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, grErr := gzip.NewReader(resp.Body)
		if grErr != nil {
			return nil, fmt.Errorf("gzip reader: %w", grErr)
		}
		defer gr.Close()
		respBody, err = io.ReadAll(gr)
	} else {
		respBody, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CLOB %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	if method == "POST" && strings.Contains(path, "/order") && len(respBody) > 0 {
		log.Debug().Str("path", signPath).RawJSON("resp", respBody).Msg("[CLOB] order_response")
	}

	return json.RawMessage(respBody), nil
}

func (c *Client) doGet(ctx context.Context, path string) (json.RawMessage, error) {
	return c.doRequest(ctx, "GET", path, nil)
}

func (c *Client) doPost(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.doRequest(ctx, "POST", path, body)
}

func (c *Client) doDelete(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.doRequest(ctx, "DELETE", path, body)
}

type OrderOption func(*orderOpts)

type orderOpts struct {
	id                 string
	side               string
	tickSize           string
	negRisk            bool
	feeRateBps         int64
	orderType          string
	postOnly           bool
	deferExec          bool
	feeAdjust          bool
	feeAdjustRate      float64
	feeAdjustTickSize  string
	feeAdjustRefPrice  float64
}

func WithID(id string) OrderOption {
	return func(o *orderOpts) { o.id = id }
}

func WithBuy() OrderOption  { return func(o *orderOpts) { o.side = SideBuy } }
func WithSell() OrderOption { return func(o *orderOpts) { o.side = SideSell } }

func WithMarket(tickSize string, negRisk bool, feeRateBps int64) OrderOption {
	return func(o *orderOpts) {
		o.tickSize = tickSize
		o.negRisk = negRisk
		o.feeRateBps = feeRateBps
	}
}

func AsGTC() OrderOption       { return func(o *orderOpts) { o.orderType = "GTC" } }
func AsFOK() OrderOption       { return func(o *orderOpts) { o.orderType = "FOK" } }
func AsPostOnly() OrderOption  { return func(o *orderOpts) { o.postOnly = true } }
func AsDeferExec() OrderOption { return func(o *orderOpts) { o.deferExec = true } }

// WithFeeAdjustment treats the passed size as the NET shares the caller
// wants to receive after the contract fee on BUY — analogous to Polymarket's
// "Fee-Adjusted Shares" UI toggle. BuildOrder oversizes the gross takerAmount
// by 1/(1 - BuyFeeRate(refPrice, feeRate)) and ceils UP to tickSize's size
// precision. No-op on SELL.
//
// refPrice is the EXPECTED FILL price (e.g. current top ask), not the order's
// limit price — fee oversizing must track where the fill actually happens.
// Pass 0 to fall back to the order's limit price (useful when limit == ask).
//
// feeRate is the schedule rate as a fraction (e.g. 0.072 for crypto_fees_v2),
// not the on-chain advisory FeeRateBps. The generic post-adjust validations
// (tick alignment, MinBuyShares) run unconditionally on every BuildOrder call.
func WithFeeAdjustment(feeRate float64, tickSize string, refPrice float64) OrderOption {
	return func(o *orderOpts) {
		o.feeAdjust = true
		o.feeAdjustRate = feeRate
		o.feeAdjustTickSize = tickSize
		o.feeAdjustRefPrice = refPrice
	}
}

// MinBuyShares is the platform minimum share count for market-BUY orders.
const MinBuyShares = 5.0

// BuyFeeRate returns the CTFExchange BUY taker fee as a fraction of gross
// shares (i.e. shares_received = gross × (1 - BuyFeeRate)). Derived from
// the docs formula fee_usdc = rate × p × (1-p) × size, divided by price
// to convert USDC → shares on the bought side:
//
//	fee_rate_on_shares = rate × (1 - p)
//
// Makers pay no fees; only FOK takers. Rate is the per-category taker rate
// (0.072 for crypto). Returns 0 for price outside (0, 1) or rate ≤ 0.
// See https://docs.polymarket.com/trading/fees.
func BuyFeeRate(price, rate float64) float64 {
	if rate <= 0 || price <= 0 || price >= 1 {
		return 0
	}
	return rate * (1 - price)
}

// BuildOrder signs an order and returns a PostOrderArg ready for PostOrders.
// Required options: WithBuy/WithSell, WithMarket, AsGTC/AsFOK.
func (c *Client) BuildOrder(ctx context.Context, tokenID string, price, size float64, opts ...OrderOption) (PostOrderArg, error) {
	_ = ctx
	var oo orderOpts
	for _, opt := range opts {
		opt(&oo)
	}

	if oo.side != SideBuy && oo.side != SideSell {
		return PostOrderArg{}, fmt.Errorf("side required: use WithBuy() or WithSell()")
	}
	if oo.orderType == "" {
		return PostOrderArg{}, fmt.Errorf("order type required: use AsGTC() or AsFOK()")
	}
	rc, ok := RoundingConfigs[oo.tickSize]
	if !ok {
		return PostOrderArg{}, fmt.Errorf("unknown tick size %q: use WithMarket()", oo.tickSize)
	}

	if oo.feeAdjust && oo.side == SideBuy {
		adjRC, adjOK := RoundingConfigs[oo.feeAdjustTickSize]
		if !adjOK {
			return PostOrderArg{}, fmt.Errorf("WithFeeAdjustment: unknown tick size %q", oo.feeAdjustTickSize)
		}
		refPrice := oo.feeAdjustRefPrice
		if refPrice <= 0 || refPrice >= 1 {
			refPrice = price
		}
		fr := BuyFeeRate(refPrice, oo.feeAdjustRate)
		if fr >= 1 {
			fr = 0
		}
		mult := math.Pow10(adjRC.Size)
		size = math.Ceil(size/(1.0-fr)*mult) / mult
	}

	sizeMult := math.Pow10(rc.Size)
	if scaled := size * sizeMult; math.Abs(scaled-math.Round(scaled)) > 1e-9 {
		return PostOrderArg{}, fmt.Errorf("size %.6f not aligned to tick size %q (precision %d)", size, oo.tickSize, rc.Size)
	}
	if oo.side == SideBuy && size < MinBuyShares {
		return PostOrderArg{}, fmt.Errorf("BUY size %.4f below minimum %g shares", size, MinBuyShares)
	}

	orderSide, makerAmt, takerAmt := computeOrderAmounts(oo.side, size, price, rc)

	tokenIDBig := new(big.Int)
	if _, ok := tokenIDBig.SetString(tokenID, 10); !ok {
		return PostOrderArg{}, fmt.Errorf("invalid tokenID: %s", tokenID)
	}

	salt := generateSalt()
	if oo.id != "" {
		salt = saltFromID(oo.id)
	}

	order := OrderData{
		Salt:          salt,
		Maker:         c.funder,
		Signer:        c.address,
		Taker:         ZeroAddress,
		TokenID:       tokenIDBig,
		MakerAmount:   big.NewInt(makerAmt),
		TakerAmount:   big.NewInt(takerAmt),
		Expiration:    big.NewInt(0),
		Nonce:         big.NewInt(0),
		FeeRateBps:    big.NewInt(oo.feeRateBps),
		Side:          orderSide,
		SignatureType: c.sigType,
	}

	sig, err := SignOrder(c.privKey, c.chainID, order, oo.negRisk)
	if err != nil {
		return PostOrderArg{}, fmt.Errorf("sign order: %w", err)
	}

	return PostOrderArg{
		Order:     &SignedOrder{Order: order, Signature: sig},
		OrderType: oo.orderType,
		PostOnly:  oo.postOnly,
		DeferExec: oo.deferExec,
	}, nil
}

func computeOrderAmounts(side string, size, price float64, rc RoundConfig) (sideInt int, makerAmount, takerAmount int64) {
	dPrice, _ := decimal.NewFromFloat64(price)
	dPrice = dPrice.Round(rc.Price)
	dSize, _ := decimal.NewFromFloat64(size)
	dSize = dSize.Floor(rc.Size)
	dTokenDecimals, _ := decimal.New(TokenDecimals, 0)

	var maker, taker decimal.Decimal
	if side == SideBuy {
		m, _ := decimal.Prod(dSize, dPrice, dTokenDecimals)
		maker = m.Round(0)
		t, _ := dSize.Mul(dTokenDecimals)
		taker = t.Round(0)
		sideInt = SideBuyInt
	} else {
		m, _ := dSize.Mul(dTokenDecimals)
		maker = m.Round(0)
		t, _ := decimal.Prod(dSize, dPrice, dTokenDecimals)
		taker = t.Round(0)
		sideInt = SideSellInt
	}

	makerAmount, _, _ = maker.Int64(0)
	takerAmount, _, _ = taker.Int64(0)
	return sideInt, makerAmount, takerAmount
}

func (c *Client) PostOrder(ctx context.Context, signedOrder *SignedOrder, orderType string) (*PostOrderResponse, error) {
	if signedOrder == nil {
		return nil, fmt.Errorf("PostOrder: signedOrder is nil")
	}

	body := PostOrderRequest{
		Order:     signedOrder.Marshal(),
		Owner:     c.creds.ApiKey,
		OrderType: orderType,
	}

	raw, err := c.doPost(ctx, EndpointPostOrder, body)
	if err != nil {
		return nil, err
	}

	var result PostOrderResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse post_order response: %w", err)
	}
	return &result, nil
}

func (c *Client) PostOrders(ctx context.Context, orders []PostOrderArg) ([]PostOrderResponse, error) {
	if len(orders) > 15 {
		return nil, fmt.Errorf("PostOrders: max 15 orders per batch, got %d", len(orders))
	}

	body := make([]PostOrderRequest, len(orders))
	for i, arg := range orders {
		body[i] = PostOrderRequest{
			Order:     arg.Order.Marshal(),
			Owner:     c.creds.ApiKey,
			OrderType: arg.OrderType,
			PostOnly:  arg.PostOnly,
			DeferExec: arg.DeferExec,
		}
	}

	raw, err := c.doPost(ctx, EndpointPostOrders, body)
	if err != nil {
		return nil, err
	}

	var result []PostOrderResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse post_orders response: %w", err)
	}
	return result, nil
}

func (c *Client) CancelOrders(ctx context.Context, orderIDs []string) error {
	_, err := c.doDelete(ctx, EndpointCancelOrders, orderIDs)
	return err
}

func (c *Client) CancelAll(ctx context.Context) error {
	_, err := c.doDelete(ctx, EndpointCancelAll, nil)
	return err
}
