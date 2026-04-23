package polymarket

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

type wsCallbacks struct {
	tag         string
	url         string
	onConnect   func(conn *websocket.Conn) error
	onMessage   func(raw []byte)
	onUp        func()
	onDown      func()
	onReconnect func()
	deadmanSec  int
}

func wsLoop(ctx context.Context, cb wsCallbacks) {
	backoff := 1.0
	firstConnect := true
	for {
		if ctx.Err() != nil {
			return
		}
		err := wsSession(ctx, cb, &backoff, firstConnect)
		firstConnect = false
		if cb.onDown != nil {
			cb.onDown()
		}
		if ctx.Err() != nil {
			return
		}
		log.Warn().Err(err).Msgf("[%s] reconnecting in %.0fs", cb.tag, backoff)
		select {
		case <-time.After(time.Duration(backoff * float64(time.Second))):
		case <-ctx.Done():
			return
		}
		backoff = math.Min(backoff*2, 30)
	}
}

func wsSession(ctx context.Context, cb wsCallbacks, backoff *float64, firstConnect bool) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, cb.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := cb.onConnect(conn); err != nil {
		return err
	}

	*backoff = 1.0
	if cb.onUp != nil {
		cb.onUp()
	}

	if !firstConnect && cb.onReconnect != nil {
		cb.onReconnect()
	}

	inner, cancel := context.WithCancel(ctx)
	defer cancel()
	go wsPinger(inner, conn, cb.tag)
	if cb.deadmanSec > 0 {
		lastMsg := make(chan struct{}, 1)
		go wsDeadman(inner, conn, cb.tag, cb.deadmanSec, lastMsg)
		return wsReadLoop(ctx, conn, cb.onMessage, lastMsg)
	}
	return wsReadLoop(ctx, conn, cb.onMessage, nil)
}

func wsReadLoop(ctx context.Context, conn *websocket.Conn, onMessage func([]byte), touch chan<- struct{}) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if touch != nil {
			select {
			case touch <- struct{}{}:
			default:
			}
		}
		if string(raw) == "PONG" {
			continue
		}
		onMessage(raw)
	}
}

func wsPinger(ctx context.Context, conn *websocket.Conn, tag string) {
	t := time.NewTicker(8 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			conn.WriteMessage(websocket.TextMessage, []byte("PING"))
		}
	}
}

func wsDeadman(ctx context.Context, conn *websocket.Conn, tag string, sec int, touch <-chan struct{}) {
	timeout := time.Duration(sec) * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-touch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		case <-timer.C:
			log.Warn().Msgf("[%s] deadman timeout", tag)
			conn.Close()
			return
		}
	}
}
