package polymarket

import (
	"context"
	"strconv"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/ntlsxq/polymarket-go/book"
	"github.com/ntlsxq/polymarket-go/clob"
)

type WSEventLogger interface {
	LogWSEvent(ts time.Time, stream, event string, raw []byte)
}

type userSubscribeAuth struct {
	ApiKey     string `json:"apiKey"`
	Secret     string `json:"secret"`
	Passphrase string `json:"passphrase"`
}

type userSubscribe struct {
	Auth    userSubscribeAuth `json:"auth"`
	Markets []string          `json:"markets"`
	Type    string            `json:"type"`
}

type TraderSide string

const (
	TraderSideMaker TraderSide = "MAKER"
	TraderSideTaker TraderSide = "TAKER"
)

type FillStatus string

const (
	FillStatusMatched   FillStatus = "MATCHED"
	FillStatusMined     FillStatus = "MINED"
	FillStatusConfirmed FillStatus = "CONFIRMED"
	FillStatusRetrying  FillStatus = "RETRYING"
	FillStatusFailed    FillStatus = "FAILED"
)

type FillSide string

const (
	FillSideBuy  FillSide = "BUY"
	FillSideSell FillSide = "SELL"
)

type OrderEventType string

const (
	OrderEventPlacement    OrderEventType = "PLACEMENT"
	OrderEventUpdate       OrderEventType = "UPDATE"
	OrderEventCancellation OrderEventType = "CANCELLATION"
)

type OrderType string

const (
	OrderTypeGTC OrderType = "GTC"
	OrderTypeGTD OrderType = "GTD"
	OrderTypeFOK OrderType = "FOK"
)

type OrderEvent struct {
	ID              string         `json:"id"`
	Owner           string         `json:"owner"`
	Market          string         `json:"market"`
	TokenID         string         `json:"asset_id"`
	Side            FillSide       `json:"side"`
	OrderOwner      string         `json:"order_owner"`
	OriginalSize    string         `json:"original_size"`
	SizeMatched     string         `json:"size_matched"`
	Price           string         `json:"price"`
	AssociateTrades []string       `json:"associate_trades"`
	Outcome         string         `json:"outcome"`
	Type            OrderEventType `json:"type"`
	CreatedAt       string         `json:"created_at"`
	Expiration      string         `json:"expiration"`
	OrderType       OrderType      `json:"order_type"`
	Status          string         `json:"status"`
	MakerAddress    string         `json:"maker_address"`
	Timestamp       string         `json:"timestamp"`
}

// MakerOrder is the counterparty-maker entry inside a Fill/Trade.
// Re-exported from clob so consumers of polymarket don't need both imports.
type MakerOrder = clob.MakerOrder

type Fill struct {
	ID              string       `json:"id"`
	TakerOrderID    string       `json:"taker_order_id"`
	Market          string       `json:"market"`
	AssetID         string       `json:"asset_id"`
	Side            FillSide     `json:"side"`
	Size            string       `json:"size"`
	Price           string       `json:"price"`
	FeeRateBps      string       `json:"fee_rate_bps"`
	Status          FillStatus   `json:"status"`
	Outcome         string       `json:"outcome"`
	TraderSide      TraderSide   `json:"trader_side"`
	TransactionHash string       `json:"transaction_hash"`
	Timestamp       string       `json:"timestamp"`
	MakerOrders     []MakerOrder `json:"maker_orders"`
}

type UserWS struct {
	creds         *clob.ApiCreds
	conditionIDs  []string
	events        WSEventLogger
	onFill        func(Fill)
	onOrder       func(OrderEvent)
	onReconnect   func()
	onConnect     func()
	onDisconnect  func()
	filter        func(raw []byte) bool
	books         *book.Manager
	onPriceChange func()

	connected bool
}

func NewUserWS(creds *clob.ApiCreds, conditionIDs []string, onFill func(Fill)) *UserWS {
	return &UserWS{
		creds:        creds,
		conditionIDs: conditionIDs,
		onFill:       onFill,
	}
}

// SetOnOrder installs the handler for PLACEMENT/CANCELLATION/UPDATE events.
// Can be called post-construction so a Pool can fan out one handler to all
// members.
func (u *UserWS) SetOnOrder(fn func(OrderEvent)) { u.onOrder = fn }

// SetOnFill replaces the fill handler set at construction. Mainly for Pool
// fan-out after the fact.
func (u *UserWS) SetOnFill(fn func(Fill)) { u.onFill = fn }

func (u *UserWS) SetOnReconnect(fn func()) {
	u.onReconnect = fn
}

func (u *UserWS) SetEventLog(el WSEventLogger) {
	u.events = el
}

// SetFilter installs a pre-dispatch hook — dispatchRaw drops frames where
// filter returns false. Pair with a shared Deduper to run N UserWS instances
// for redundancy without duplicating order/fill side effects.
func (u *UserWS) SetFilter(fn func(raw []byte) bool) { u.filter = fn }

// SetOnConnect / SetOnDisconnect let a Pool aggregate connection state.
func (u *UserWS) SetOnConnect(fn func())    { u.onConnect = fn }
func (u *UserWS) SetOnDisconnect(fn func()) { u.onDisconnect = fn }

// SetBooks wires the book Manager so maker-side fills decrement the consumed
// level immediately. Same Manager as the MarketWS pool so IngestTrade dedup
// catches the same transaction_hash arriving via both feeds.
func (u *UserWS) SetBooks(b *book.Manager) { u.books = b }

// SetOnPriceChange fires after a fill successfully mutates the book (dedup
// passed). Mirrors MarketWS's callback so consumers wire one priceBus for
// both streams.
func (u *UserWS) SetOnPriceChange(fn func()) { u.onPriceChange = fn }

func (u *UserWS) Connected() bool { return u.connected }

func (u *UserWS) Run(ctx context.Context) {
	wsLoop(ctx, wsCallbacks{
		tag: "USER_WS",
		url: WSUser,
		onConnect: func(conn *websocket.Conn) error {
			sub := userSubscribe{
				Auth: userSubscribeAuth{
					ApiKey:     u.creds.ApiKey,
					Secret:     u.creds.ApiSecret,
					Passphrase: u.creds.ApiPassphrase,
				},
				Markets: u.conditionIDs,
				Type:    "user",
			}
			if err := conn.WriteJSON(sub); err != nil {
				return err
			}
			u.connected = true
			log.Info().Int("markets", len(u.conditionIDs)).Msg("[USER_WS] connected")
			return nil
		},
		onMessage: u.dispatchRaw,
		onUp: func() {
			if u.onConnect != nil {
				u.onConnect()
			}
		},
		onDown: func() {
			u.connected = false
			if u.onDisconnect != nil {
				u.onDisconnect()
			}
		},
		onReconnect: u.onReconnect,
	})
}

func (u *UserWS) dispatchRaw(raw []byte) {
	if u.filter != nil && !u.filter(raw) {
		return
	}
	recvTs := time.Now()
	var envelope struct {
		EventType string `json:"event_type"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}

	if u.events != nil {
		u.events.LogWSEvent(recvTs, "user", envelope.EventType, raw)
	}

	switch envelope.EventType {
	case "trade":
		u.dispatchTrade(raw)
	case "order":
		u.dispatchOrder(raw)
	}
}

func (u *UserWS) dispatchTrade(raw []byte) {
	var fill Fill
	if err := json.Unmarshal(raw, &fill); err != nil {
		log.Warn().Err(err).Msg("[USER_WS] failed to parse trade event")
		return
	}
	if fill.Status != FillStatusMatched {
		return
	}
	u.ingestFillToBook(fill)
	if u.onFill != nil {
		u.onFill(fill)
	}
}

// ingestFillToBook routes a matched Fill through book.Manager.IngestTrade,
// which dedupes by transaction_hash against the same set market-ws
// last_trade_price uses — so whichever feed wins the race is the one that
// decrements the level. Fires onPriceChange only on actual mutation (dedup
// returned true AND the level was present).
func (u *UserWS) ingestFillToBook(fill Fill) {
	if u.books == nil {
		return
	}
	trade, tokenID, ok := fillToTrade(fill)
	if !ok {
		return
	}
	if !u.books.IngestTrade(tokenID, trade) {
		return
	}
	if u.onPriceChange != nil {
		u.onPriceChange()
	}
}

// fillToTrade projects a UserWS Fill into the same book.Trade shape the
// market-ws last_trade_price handler produces. Fill.Side is the TAKER side
// on the top-level AssetID — matches book.ApplyTrade's "tradeSide" contract
// directly (no inversion).
func fillToTrade(fill Fill) (book.Trade, string, bool) {
	if fill.AssetID == "" {
		return book.Trade{}, "", false
	}
	side, ok := book.ParseSide(string(fill.Side))
	if !ok {
		return book.Trade{}, "", false
	}
	p, err := strconv.ParseFloat(fill.Price, 64)
	if err != nil || p <= 0 {
		return book.Trade{}, "", false
	}
	s, err := strconv.ParseFloat(fill.Size, 64)
	if err != nil || s <= 0 {
		return book.Trade{}, "", false
	}
	return book.Trade{
		Hash:  fill.TransactionHash,
		Side:  side,
		Price: p,
		Size:  s,
	}, fill.AssetID, true
}

func (u *UserWS) dispatchOrder(raw []byte) {
	var ev OrderEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		log.Warn().Err(err).Msg("[USER_WS] failed to parse order event")
		return
	}

	tokenLabel := ev.TokenID
	if len(tokenLabel) > 16 {
		tokenLabel = tokenLabel[:16] + "..."
	}
	log.Debug().
		Str("id", ev.ID[:min(len(ev.ID), 16)]).
		Str("token", tokenLabel).
		Str("type", string(ev.Type)).
		Str("status", ev.Status).
		Str("price", ev.Price).
		Str("size", ev.OriginalSize).
		Msg("[USER_WS] ORDER")

	if u.onOrder != nil {
		u.onOrder(ev)
	}
}
