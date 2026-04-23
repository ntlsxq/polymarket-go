package polymarket

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"

	"github.com/ntlsxq/polymarket-go/book"
)

type WSPool struct {
	instances []*MarketWS
	books     *book.Manager
	connCount atomic.Int32
	mu        sync.Mutex
}

func NewWSPool(books *book.Manager, instances ...*MarketWS) *WSPool {
	p := &WSPool{
		instances: instances,
		books:     books,
	}
	for _, ws := range instances {
		ws.onConnect = p.onConnect
		ws.onDisconnect = p.onDisconnect
	}
	return p
}

func (p *WSPool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i, ws := range p.instances {
		wg.Add(1)
		go func(idx int, w *MarketWS) {
			defer wg.Done()
			log.Info().Int("instance", idx).Msg("[WS] pool member started")
			w.Run(ctx)
		}(i, ws)
	}
	wg.Wait()
}

func (p *WSPool) Connected() bool {
	return p.connCount.Load() > 0
}

func (p *WSPool) SubscribeTokens(tokenIDs []string) {
	for _, ws := range p.instances {
		ws.SubscribeTokens(tokenIDs)
	}
}

func (p *WSPool) UnsubscribeTokens(tokenIDs []string) {
	for _, ws := range p.instances {
		ws.UnsubscribeTokens(tokenIDs)
	}
}

func (p *WSPool) SetEventLog(el WSEventLogger) {
	for _, ws := range p.instances {
		ws.SetEventLog(el)
	}
}

func (p *WSPool) onConnect() {
	n := p.connCount.Add(1)
	log.Info().Int32("active", n).Msg("[WS] connection up")
}

func (p *WSPool) onDisconnect() {
	n := p.connCount.Add(-1)
	log.Warn().Int32("active", n).Msg("[WS] connection down")
	if n <= 0 {

		p.books.ClearAllAtomics()
		log.Warn().Msg("[WS] all connections lost — books cleared")
	}
}
