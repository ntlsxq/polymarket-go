package polymarket

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/ntlsxq/polymarket-go/book"
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

// Inbound market-WS event shapes. flexFloat lets price/size arrive as either
// string or number — Polymarket is inconsistent across event types.
type wsEnvelope struct {
	EventType string `json:"event_type"`
}

type wsBookLevel struct {
	Price flexFloat `json:"price"`
	Size  flexFloat `json:"size"`
}

type wsBookMsg struct {
	AssetID string        `json:"asset_id"`
	Bids    []wsBookLevel `json:"bids"`
	Asks    []wsBookLevel `json:"asks"`
}

type wsPriceChangeEntry struct {
	AssetID string `json:"asset_id"`
	Side    string `json:"side"`
	Price   string `json:"price"`
	Size    string `json:"size"`
}

type wsPriceChangeMsg struct {
	PriceChanges []wsPriceChangeEntry `json:"price_changes"`
}

type wsBestBidAskMsg struct {
	AssetID string `json:"asset_id"`
	BestBid string `json:"best_bid"`
	BestAsk string `json:"best_ask"`
}

type wsLastTradePriceMsg struct {
	AssetID         string `json:"asset_id"`
	Side            string `json:"side"`
	Price           string `json:"price"`
	Size            string `json:"size"`
	TransactionHash string `json:"transaction_hash"`
}

type wsTickSizeChangeMsg struct {
	AssetID         string `json:"asset_id"`
	TickSize        string `json:"tick_size"`
	MinimumTickSize string `json:"minimum_tick_size"`
}


const wsMarketURL = "wss://ws-subscriptions-clob.polymarket.com/ws/market"

type MarketWS struct {
	books            *book.Manager
	events           WSEventLogger
	onPriceChange    func()
	onTickSizeChange func(tokenID, newTickSize string)
	onConnect        func()
	onDisconnect     func()
	filter           func(raw []byte) bool

	connMu sync.Mutex
	conn   *websocket.Conn

	connected  atomic.Bool
	deadmanSec int
}

type WSOption func(*MarketWS)

func WithDeadman(sec int) WSOption         { return func(ws *MarketWS) { ws.deadmanSec = sec } }
func WithOnPriceChange(fn func()) WSOption { return func(ws *MarketWS) { ws.onPriceChange = fn } }
func WithOnTickSizeChange(fn func(tokenID, newTickSize string)) WSOption {
	return func(ws *MarketWS) { ws.onTickSizeChange = fn }
}
func WithOnConnect(fn func()) WSOption    { return func(ws *MarketWS) { ws.onConnect = fn } }
func WithOnDisconnect(fn func()) WSOption { return func(ws *MarketWS) { ws.onDisconnect = fn } }
func WithEventLog(el WSEventLogger) WSOption {
	return func(ws *MarketWS) { ws.events = el }
}

func (ws *MarketWS) SetEventLog(el WSEventLogger) { ws.events = el }

// SetFilter installs a pre-dispatch hook: Run calls filter(raw) and drops the
// frame on false. Used to plug a Deduper when this WS is one of N redundant
// streams sharing a source.
func (ws *MarketWS) SetFilter(fn func(raw []byte) bool) { ws.filter = fn }

// SetOnConnect / SetOnDisconnect let a Pool aggregate per-member connection
// state without going through the construction-time WSOption path.
func (ws *MarketWS) SetOnConnect(fn func())    { ws.onConnect = fn }
func (ws *MarketWS) SetOnDisconnect(fn func()) { ws.onDisconnect = fn }

func NewMarketWS(books *book.Manager, opts ...WSOption) *MarketWS {
	ws := &MarketWS{
		books:      books,
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
	batches := batchStrings(ws.books.AllTokenIDs(), 100)

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

func (ws *MarketWS) dispatch(raw []byte) {
	if ws.filter != nil && !ws.filter(raw) {
		return
	}
	recvTs := time.Now()
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		items = []json.RawMessage{raw}
	}
	changed := false
	for _, item := range items {
		var env wsEnvelope
		if json.Unmarshal(item, &env) != nil {
			continue
		}
		if ws.events != nil {
			ws.events.LogWSEvent(recvTs, "market", env.EventType, item)
		}
		if ws.handle(env.EventType, item) {
			changed = true
		}
	}
	if changed && ws.onPriceChange != nil {
		ws.onPriceChange()
	}
}

func (ws *MarketWS) handle(evType string, raw json.RawMessage) bool {
	switch evType {
	case "book":
		var msg wsBookMsg
		if json.Unmarshal(raw, &msg) != nil {
			return false
		}
		return ws.handleBook(msg)
	case "price_change":
		var msg wsPriceChangeMsg
		if json.Unmarshal(raw, &msg) != nil {
			return false
		}
		return ws.handlePriceChange(msg)
	case "best_bid_ask":
		var msg wsBestBidAskMsg
		if json.Unmarshal(raw, &msg) != nil {
			return false
		}
		return ws.handleBestBidAsk(msg)
	case "last_trade_price":
		var msg wsLastTradePriceMsg
		if json.Unmarshal(raw, &msg) != nil {
			return false
		}
		return ws.handleLastTradePrice(msg)
	case "tick_size_change":
		var msg wsTickSizeChangeMsg
		if json.Unmarshal(raw, &msg) != nil {
			return false
		}
		return ws.handleTickSizeChange(msg)
	}
	return false
}

func (ws *MarketWS) handleBook(msg wsBookMsg) bool {
	ob := ws.books.OBForToken(msg.AssetID)
	if ob == nil {
		return false
	}
	ob.SetFromSnapshot(toBookLevels(msg.Bids), toBookLevels(msg.Asks))
	return true
}

func (ws *MarketWS) handlePriceChange(msg wsPriceChangeMsg) bool {
	changed := false
	for _, ch := range msg.PriceChanges {
		ob := ws.books.OBForToken(ch.AssetID)
		if ob == nil {
			continue
		}
		side, ok := book.ParseSide(ch.Side)
		if !ok {
			continue
		}
		p, _ := strconv.ParseFloat(ch.Price, 64)
		s, _ := strconv.ParseFloat(ch.Size, 64)
		ob.UpdateLevel(side, p, s)
		changed = true
	}
	return changed
}

func (ws *MarketWS) handleBestBidAsk(msg wsBestBidAskMsg) bool {
	ob := ws.books.OBForToken(msg.AssetID)
	if ob == nil {
		return false
	}
	bb, _ := strconv.ParseFloat(msg.BestBid, 64)
	ba, _ := strconv.ParseFloat(msg.BestAsk, 64)
	ob.ReconcileTop(bb, ba)
	return true
}

func (ws *MarketWS) handleLastTradePrice(msg wsLastTradePriceMsg) bool {
	trade, tokenID, ok := parseLastTradePrice(msg)
	if !ok {
		return false
	}
	return ws.books.IngestTrade(tokenID, trade)
}

func (ws *MarketWS) handleTickSizeChange(msg wsTickSizeChangeMsg) bool {
	tick := msg.TickSize
	if tick == "" {
		tick = msg.MinimumTickSize
	}
	if msg.AssetID == "" || tick == "" {
		return false
	}
	ws.books.SetTickSize(msg.AssetID, tick)
	if ws.onTickSizeChange != nil {
		ws.onTickSizeChange(msg.AssetID, tick)
	}
	return true
}

// parseLastTradePrice extracts a typed book.Trade from a market-ws
// last_trade_price frame. Returns ok=false when any required field is
// missing or invalid, so the dispatcher can drop the frame without
// touching the book.
func parseLastTradePrice(msg wsLastTradePriceMsg) (book.Trade, string, bool) {
	side, ok := book.ParseSide(msg.Side)
	if !ok {
		return book.Trade{}, "", false
	}
	p, _ := strconv.ParseFloat(msg.Price, 64)
	s, _ := strconv.ParseFloat(msg.Size, 64)
	if p <= 0 || s <= 0 {
		return book.Trade{}, "", false
	}
	return book.Trade{
		Hash:  msg.TransactionHash,
		Side:  side,
		Price: p,
		Size:  s,
	}, msg.AssetID, true
}

func toBookLevels(in []wsBookLevel) []book.BookLevel {
	out := make([]book.BookLevel, 0, len(in))
	for _, e := range in {
		if e.Price > 0 {
			out = append(out, book.BookLevel{Price: float64(e.Price), Size: float64(e.Size)})
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
