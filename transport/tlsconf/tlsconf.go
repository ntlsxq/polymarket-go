package tlsconf

import (
	"crypto/tls"

	"github.com/ntlsxq/polymarket-go/transport/session"
)

type Params struct {
	ServerName   string
	NextProtos   []string
	Store        *session.Store
	KeyLogWriter interface {
		Write([]byte) (int, error)
	}
}

func New(p Params) *tls.Config {
	cfg := &tls.Config{
		ServerName:         p.ServerName,
		NextProtos:         p.NextProtos,
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		ClientSessionCache: p.Store,
	}
	if p.KeyLogWriter != nil {
		cfg.KeyLogWriter = p.KeyLogWriter
	}
	return cfg
}
