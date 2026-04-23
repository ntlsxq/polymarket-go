package polymarket

import (
	"context"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/ntlsxq/polymarket-go/clob"
)

type WSEventLogger interface {
	LogWSEvent(ts time.Time, stream, event string, raw []byte)
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
	creds        *clob.ApiCreds
	conditionIDs []string
	events       WSEventLogger
	onFill       func(Fill)
	onOrder      func(OrderEvent)
	onReconnect  func()

	connected bool
}

func NewUserWS(creds *clob.ApiCreds, conditionIDs []string, onFill func(Fill)) *UserWS {
	return &UserWS{
		creds:        creds,
		conditionIDs: conditionIDs,
		onFill:       onFill,
	}
}

func (u *UserWS) SetOnOrder(fn func(OrderEvent)) {
	u.onOrder = fn
}

func (u *UserWS) SetOnReconnect(fn func()) {
	u.onReconnect = fn
}

func (u *UserWS) SetEventLog(el WSEventLogger) {
	u.events = el
}

func (u *UserWS) Connected() bool { return u.connected }

func (u *UserWS) Run(ctx context.Context) {
	wsLoop(ctx, wsCallbacks{
		tag: "USER_WS",
		url: WSUser,
		onConnect: func(conn *websocket.Conn) error {
			sub := map[string]any{
				"auth": map[string]string{
					"apiKey":     u.creds.ApiKey,
					"secret":     u.creds.ApiSecret,
					"passphrase": u.creds.ApiPassphrase,
				},
				"markets": u.conditionIDs,
				"type":    "user",
			}
			if err := conn.WriteJSON(sub); err != nil {
				return err
			}
			u.connected = true
			log.Info().Int("markets", len(u.conditionIDs)).Msg("[USER_WS] connected")
			return nil
		},
		onMessage:   u.dispatchRaw,
		onDown:      func() { u.connected = false },
		onReconnect: u.onReconnect,
	})
}

func (u *UserWS) dispatchRaw(raw []byte) {
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
	if u.onFill != nil {
		u.onFill(fill)
	}
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
