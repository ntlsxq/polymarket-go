package polymarket

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// Outbound subscribe payload for the market-WS endpoint.
type marketSubscribe struct {
	AssetsIDs            []string `json:"assets_ids"`
	Type                 string   `json:"type"`
	Operation            string   `json:"operation,omitempty"`
	InitialDump          bool     `json:"initial_dump,omitempty"`
	Level                int      `json:"level,omitempty"`
	CustomFeatureEnabled bool     `json:"custom_feature_enabled,omitempty"`
}

// Inbound market-WS event shapes. flexString preserves price/size as decimal
// strings even when Polymarket sends JSON numbers.

type wsBookLevel struct {
	Price flexString `json:"price"`
	Size  flexString `json:"size"`
}

type wsPriceChangeEntry struct {
	AssetID string `json:"asset_id"`
	Side    string `json:"side"`
	Price   string `json:"price"`
	Size    string `json:"size"`
}

type MarketWSEventType string

const (
	MarketWSEventBook           MarketWSEventType = "book"
	MarketWSEventPriceChange    MarketWSEventType = "price_change"
	MarketWSEventBestBidAsk     MarketWSEventType = "best_bid_ask"
	MarketWSEventLastTradePrice MarketWSEventType = "last_trade_price"
	MarketWSEventTickSizeChange MarketWSEventType = "tick_size_change"
	MarketWSEventNewMarket      MarketWSEventType = "new_market"
	MarketWSEventResolved       MarketWSEventType = "market_resolved"
)

type MarketWSBookEvent struct {
	AssetID string
	Bids    []MarketWSBookLevel
	Asks    []MarketWSBookLevel
}

type MarketWSBookLevel struct {
	Price string
	Size  string
}

type MarketWSPriceChange struct {
	AssetID string
	Side    string
	Price   string
	Size    string
}

type MarketWSPriceChangeEvent struct {
	Changes []MarketWSPriceChange
}

type MarketWSBestBidAskEvent struct {
	AssetID string
	BestBid string
	BestAsk string
}

type MarketWSLastTradePriceEvent struct {
	AssetID         string
	Side            string
	Price           string
	Size            string
	TransactionHash string
}

type MarketWSTickSizeChangeEvent struct {
	AssetID     string
	OldTickSize string
	NewTickSize string
}

type MarketWSNewMarketEvent struct {
	ConditionID           string
	Market                string
	Slug                  string
	GroupItemTitle        string
	Line                  string
	OrderPriceMinTickSize string
	AssetIDs              []string
	ClobTokenIDs          []string
}

type MarketWSResolvedEvent struct {
	ConditionID    string
	Market         string
	WinningAssetID string
	WinningOutcome string
	AssetIDs       []string
}

type MarketWSEvent struct {
	Type           MarketWSEventType
	Raw            []byte
	Book           *MarketWSBookEvent
	PriceChange    *MarketWSPriceChangeEvent
	BestBidAsk     *MarketWSBestBidAskEvent
	LastTradePrice *MarketWSLastTradePriceEvent
	TickSizeChange *MarketWSTickSizeChangeEvent
	NewMarket      *MarketWSNewMarketEvent
	Resolved       *MarketWSResolvedEvent
}

// wsItem is the union shape used by dispatch to decode every event type
// in one Unmarshal pass. Each event populates only its own subset of
// fields; goccy/go-json silently skips JSON keys we don't list and leaves
// unrelated struct fields zero. This collapses what was previously two
// passes (envelope-only, then typed-message) into one.
type wsItem struct {
	EventType string `json:"event_type"`

	AssetID string `json:"asset_id"`

	// book
	Bids []wsBookLevel `json:"bids"`
	Asks []wsBookLevel `json:"asks"`

	// price_change
	PriceChanges []wsPriceChangeEntry `json:"price_changes"`

	// best_bid_ask
	BestBid string `json:"best_bid"`
	BestAsk string `json:"best_ask"`

	// last_trade_price
	Side            string `json:"side"`
	Price           string `json:"price"`
	Size            string `json:"size"`
	TransactionHash string `json:"transaction_hash"`

	// tick_size_change
	TickSize        string `json:"tick_size"`
	MinimumTickSize string `json:"minimum_tick_size"`
	NewTickSize     string `json:"new_tick_size"`
	OldTickSize     string `json:"old_tick_size"`

	// new_market / market_resolved
	Market                string   `json:"market"`
	ConditionID           string   `json:"condition_id"`
	ConditionIDAlt        string   `json:"conditionId"`
	Slug                  string   `json:"slug"`
	GroupItemTitle        string   `json:"group_item_title"`
	Line                  string   `json:"line"`
	OrderPriceMinTickSize string   `json:"order_price_min_tick_size"`
	AssetsIDs             []string `json:"assets_ids"`
	AssetIDs              []string `json:"asset_ids"`
	ClobTokenIDs          []string `json:"clob_token_ids"`
	WinningAssetID        string   `json:"winning_asset_id"`
	WinningOutcome        string   `json:"winning_outcome"`
}

// wsLastTradePriceMsg is the payload-only projection used by parseLastTradePrice
// for unit tests; its fields are a strict subset of wsItem.
type wsLastTradePriceMsg struct {
	AssetID         string `json:"asset_id"`
	Side            string `json:"side"`
	Price           string `json:"price"`
	Size            string `json:"size"`
	TransactionHash string `json:"transaction_hash"`
}

const wsMarketURL = "wss://ws-subscriptions-clob.polymarket.com/ws/market"

type MarketWS struct {
	tokenIDs      []string
	events        WSEventLogger
	onMarketEvent func(MarketWSEvent)
	onConnect     func()
	onDisconnect  func()
	filter        func(raw []byte) bool

	connMu sync.Mutex
	conn   *websocket.Conn

	connected  atomic.Bool
	deadmanSec int
}

type WSOption func(*MarketWS)

func WithDeadman(sec int) WSOption { return func(ws *MarketWS) { ws.deadmanSec = sec } }
func WithOnMarketEvent(fn func(MarketWSEvent)) WSOption {
	return func(ws *MarketWS) { ws.onMarketEvent = fn }
}
func WithOnConnect(fn func()) WSOption    { return func(ws *MarketWS) { ws.onConnect = fn } }
func WithOnDisconnect(fn func()) WSOption { return func(ws *MarketWS) { ws.onDisconnect = fn } }
func WithEventLog(el WSEventLogger) WSOption {
	return func(ws *MarketWS) { ws.events = el }
}

func (ws *MarketWS) SetEventLog(el WSEventLogger) { ws.events = el }

func (ws *MarketWS) SetOnMarketEvent(fn func(MarketWSEvent)) { ws.onMarketEvent = fn }

// SetFilter installs a pre-dispatch hook: Run calls filter(raw) and drops the
// frame on false.
func (ws *MarketWS) SetFilter(fn func(raw []byte) bool) { ws.filter = fn }

// SetOnConnect / SetOnDisconnect install connection state callbacks.
func (ws *MarketWS) SetOnConnect(fn func())    { ws.onConnect = fn }
func (ws *MarketWS) SetOnDisconnect(fn func()) { ws.onDisconnect = fn }

func NewMarketWS(tokenIDs []string, opts ...WSOption) *MarketWS {
	ws := &MarketWS{
		tokenIDs:   append([]string(nil), tokenIDs...),
		deadmanSec: 30,
	}
	for _, opt := range opts {
		opt(ws)
	}
	return ws
}

func (ws *MarketWS) SubscribeTokens(tokenIDs []string) {
	ws.connMu.Lock()
	defer ws.connMu.Unlock()
	if ws.conn == nil {
		return
	}
	ws.conn.WriteJSON(marketSubscribe{
		AssetsIDs: tokenIDs,
		Type:      "market",
		Operation: "subscribe",
	})
}

func (ws *MarketWS) UnsubscribeTokens(tokenIDs []string) {
	ws.connMu.Lock()
	defer ws.connMu.Unlock()
	if ws.conn == nil {
		return
	}
	ws.conn.WriteJSON(marketSubscribe{
		AssetsIDs: tokenIDs,
		Type:      "market",
		Operation: "unsubscribe",
	})
}

func (ws *MarketWS) Connected() bool { return ws.connected.Load() }

func (ws *MarketWS) Run(ctx context.Context) {
	batches := batchStrings(ws.tokenIDs, 100)

	wsLoop(ctx, wsCallbacks{
		tag: "WS",
		url: wsMarketURL,
		onConnect: func(conn *websocket.Conn) error {
			for i, batch := range batches {
				msg := marketSubscribe{
					AssetsIDs:            batch,
					Type:                 "market",
					InitialDump:          true,
					Level:                2,
					CustomFeatureEnabled: true,
				}
				if i > 0 {
					msg.Operation = "subscribe"
				}
				if err := conn.WriteJSON(msg); err != nil {
					return err
				}
				if i < len(batches)-1 {
					time.Sleep(50 * time.Millisecond)
				}
			}

			ws.connMu.Lock()
			ws.conn = conn
			ws.connMu.Unlock()
			ws.connected.Store(true)

			total := 0
			for _, b := range batches {
				total += len(b)
			}
			log.Info().Int("tokens", total).Msg("[WS] subscribed")
			return nil
		},
		onMessage: ws.dispatch,
		onUp: func() {
			if ws.onConnect != nil {
				ws.onConnect()
			}
		},
		onDown: func() {
			ws.connMu.Lock()
			ws.conn = nil
			ws.connMu.Unlock()
			ws.connected.Store(false)
			if ws.onDisconnect != nil {
				ws.onDisconnect()
			}
		},
		deadmanSec: ws.deadmanSec,
	})
}

// dispatch routes one wire frame through the typed market-event callback.
// Polymarket emits both wrapped arrays ([{...},{...}]) and bare objects
// ({...}); branch on the first non-whitespace byte to avoid double parses.
func (ws *MarketWS) dispatch(raw []byte) {
	if ws.filter != nil && !ws.filter(raw) {
		return
	}
	recvTs := time.Now()

	if first := firstNonSpace(raw); first == '[' {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return
		}
		for _, item := range items {
			ws.dispatchOne(recvTs, item)
		}
		return
	}

	ws.dispatchOne(recvTs, raw)
}

// firstNonSpace returns the first non-whitespace byte of b, or 0 when b
// is whitespace-only or empty.
func firstNonSpace(b []byte) byte {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return c
		}
	}
	return 0
}

// dispatchOne decodes a single item once into wsItem (union of all event
// shapes) and emits a typed event. No orderbook state lives in this package.
func (ws *MarketWS) dispatchOne(recvTs time.Time, raw json.RawMessage) {
	var it wsItem
	if json.Unmarshal(raw, &it) != nil {
		return
	}
	if ws.events != nil {
		ws.events.LogWSEvent(recvTs, "market", it.EventType, raw)
	}
	if ws.onMarketEvent != nil {
		ws.onMarketEvent(marketWSEventFromItem(&it, raw))
	}
}

func marketWSEventFromItem(it *wsItem, raw []byte) MarketWSEvent {
	ev := MarketWSEvent{
		Type: MarketWSEventType(it.EventType),
		Raw:  append([]byte(nil), raw...),
	}
	switch ev.Type {
	case MarketWSEventBook:
		ev.Book = &MarketWSBookEvent{
			AssetID: it.AssetID,
			Bids:    toBookLevels(it.Bids),
			Asks:    toBookLevels(it.Asks),
		}
	case MarketWSEventPriceChange:
		changes := make([]MarketWSPriceChange, 0, len(it.PriceChanges))
		for i := range it.PriceChanges {
			ch := it.PriceChanges[i]
			changes = append(changes, MarketWSPriceChange{
				AssetID: ch.AssetID,
				Side:    ch.Side,
				Price:   ch.Price,
				Size:    ch.Size,
			})
		}
		ev.PriceChange = &MarketWSPriceChangeEvent{Changes: changes}
	case MarketWSEventBestBidAsk:
		ev.BestBidAsk = &MarketWSBestBidAskEvent{
			AssetID: it.AssetID,
			BestBid: it.BestBid,
			BestAsk: it.BestAsk,
		}
	case MarketWSEventLastTradePrice:
		ev.LastTradePrice = &MarketWSLastTradePriceEvent{
			AssetID:         it.AssetID,
			Side:            it.Side,
			Price:           it.Price,
			Size:            it.Size,
			TransactionHash: it.TransactionHash,
		}
	case MarketWSEventTickSizeChange:
		tick := it.TickSize
		if tick == "" {
			tick = it.NewTickSize
		}
		if tick == "" {
			tick = it.MinimumTickSize
		}
		ev.TickSizeChange = &MarketWSTickSizeChangeEvent{
			AssetID:     it.AssetID,
			OldTickSize: it.OldTickSize,
			NewTickSize: tick,
		}
	case MarketWSEventNewMarket:
		ev.NewMarket = &MarketWSNewMarketEvent{
			ConditionID:           firstNonEmpty(it.ConditionID, it.ConditionIDAlt),
			Market:                it.Market,
			Slug:                  it.Slug,
			GroupItemTitle:        it.GroupItemTitle,
			Line:                  it.Line,
			OrderPriceMinTickSize: it.OrderPriceMinTickSize,
			AssetIDs:              firstNonEmptySlice(it.AssetsIDs, it.AssetIDs),
			ClobTokenIDs:          it.ClobTokenIDs,
		}
	case MarketWSEventResolved:
		ev.Resolved = &MarketWSResolvedEvent{
			ConditionID:    firstNonEmpty(it.ConditionID, it.ConditionIDAlt),
			Market:         it.Market,
			WinningAssetID: it.WinningAssetID,
			WinningOutcome: it.WinningOutcome,
			AssetIDs:       firstNonEmptySlice(it.AssetsIDs, it.AssetIDs),
		}
	}
	return ev
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, v := range values {
		if len(v) > 0 {
			return append([]string(nil), v...)
		}
	}
	return nil
}

func toBookLevels(in []wsBookLevel) []MarketWSBookLevel {
	out := make([]MarketWSBookLevel, 0, len(in))
	for _, e := range in {
		price := string(e.Price)
		size := string(e.Size)
		if price != "" && size != "" {
			out = append(out, MarketWSBookLevel{Price: price, Size: size})
		}
	}
	return out
}

func batchStrings(s []string, size int) [][]string {
	var batches [][]string
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		batches = append(batches, s[i:end])
	}
	return batches
}
