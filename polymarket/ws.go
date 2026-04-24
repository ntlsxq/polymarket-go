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
	ws.conn.WriteJSON(map[string]any{
		"assets_ids": tokenIDs,
		"type":       "market",
		"operation":  "subscribe",
	})
}

func (ws *MarketWS) UnsubscribeTokens(tokenIDs []string) {
	ws.connMu.Lock()
	defer ws.connMu.Unlock()
	if ws.conn == nil {
		return
	}
	ws.conn.WriteJSON(map[string]any{
		"assets_ids": tokenIDs,
		"type":       "market",
		"operation":  "unsubscribe",
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
				msg := map[string]any{
					"assets_ids":             batch,
					"type":                   "market",
					"initial_dump":           true,
					"level":                  2,
					"custom_feature_enabled": true,
				}
				if i > 0 {
					msg["operation"] = "subscribe"
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
		var msg map[string]any
		if json.Unmarshal(item, &msg) != nil {
			continue
		}
		if ws.events != nil {
			evType, _ := msg["event_type"].(string)
			ws.events.LogWSEvent(recvTs, "market", evType, item)
		}
		if ws.handle(msg) {
			changed = true
		}
	}
	if changed && ws.onPriceChange != nil {
		ws.onPriceChange()
	}
}

func (ws *MarketWS) handle(msg map[string]any) bool {
	switch msg["event_type"] {
	case "book":
		aid, _ := msg["asset_id"].(string)
		ob := ws.books.OBForToken(aid)
		if ob == nil {
			return false
		}
		ob.SetFromSnapshot(parseLevels(msg["bids"]), parseLevels(msg["asks"]))
		return true

	case "price_change":
		changes, _ := msg["price_changes"].([]any)
		changed := false
		for _, c := range changes {
			ch, _ := c.(map[string]any)
			aid, _ := ch["asset_id"].(string)
			ob := ws.books.OBForToken(aid)
			if ob == nil {
				continue
			}
			p, _ := strconv.ParseFloat(ch["price"].(string), 64)
			s := 0.0
			if sv, ok := ch["size"].(string); ok {
				s, _ = strconv.ParseFloat(sv, 64)
			}
			side, _ := ch["side"].(string)
			ob.UpdateLevel(side, p, s)
			changed = true
		}
		return changed

	case "best_bid_ask":
		aid, _ := msg["asset_id"].(string)
		ob := ws.books.OBForToken(aid)
		if ob == nil {
			return false
		}
		bb, _ := strconv.ParseFloat(firstString(msg, "best_bid"), 64)
		ba, _ := strconv.ParseFloat(firstString(msg, "best_ask"), 64)
		ob.ReconcileTop(bb, ba)
		return true

	case "tick_size_change":
		aid, _ := msg["asset_id"].(string)
		tick := firstString(msg, "tick_size", "minimum_tick_size")
		if aid != "" && tick != "" {
			ws.books.SetTickSize(aid, tick)
			if ws.onTickSizeChange != nil {
				ws.onTickSizeChange(aid, tick)
			}
			return true
		}

	}
	return false
}

func parseLevels(v any) []book.BookLevel {
	arr, _ := v.([]any)
	out := make([]book.BookLevel, 0, len(arr))
	for _, e := range arr {
		m, _ := e.(map[string]any)
		p := anyFloat(m["price"])
		s := anyFloat(m["size"])
		if p > 0 {
			out = append(out, book.BookLevel{Price: p, Size: s})
		}
	}
	return out
}

func anyFloat(v any) float64 {
	switch x := v.(type) {
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case float64:
		return x
	}
	return 0
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
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
