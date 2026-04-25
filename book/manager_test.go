package book

import (
	"fmt"
	"sync/atomic"
	"testing"
)

func tokens(key, yesTID, noTID string) []Token {
	return []Token{
		{Key: key, ID: yesTID, IsYes: true},
		{Key: key, ID: noTID, IsYes: false},
	}
}

func TestManagerRoutesTokensToBooks(t *testing.T) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))

	yesOB := m.OBForToken("yes-1")
	noOB := m.OBForToken("no-1")
	if yesOB == nil || noOB == nil {
		t.Fatalf("missing OB: yes=%p no=%p", yesOB, noOB)
	}
	if yesOB == noOB {
		t.Fatalf("yes and no must be distinct OrderBooks")
	}

	bp := m.Get("k1")
	if bp.ForToken(true) != yesOB || bp.ForToken(false) != noOB {
		t.Fatalf("bookpair routing mismatch")
	}
}

func TestManagerUnknownTokenReturnsNil(t *testing.T) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	if ob := m.OBForToken("ghost"); ob != nil {
		t.Fatalf("unknown token should return nil, got %p", ob)
	}
	if bp := m.BookForToken("ghost"); bp != nil {
		t.Fatalf("unknown token should return nil book pair, got %p", bp)
	}
}

func TestManagerAddMarketIsIdempotent(t *testing.T) {
	m := NewManager(nil)
	if !m.AddMarket("k1", "yes", "no", "0.01") {
		t.Fatal("first add must return true")
	}
	if m.AddMarket("k1", "yes", "no", "0.01") {
		t.Fatal("second add for same key must return false")
	}
	if got := m.GetTickSize("yes"); got != "0.01" {
		t.Fatalf("tick size lookup = %q, want %q", got, "0.01")
	}
}

func TestManagerIngestTradeMutatesBook(t *testing.T) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	ob := m.OBForToken("yes-1")
	ob.SetFromSnapshot(nil, []BookLevel{{0.55, 5}})

	ok := m.IngestTrade("yes-1", Trade{
		Hash: "0xfeed", Side: SideBuy, Price: 0.55, Size: 2,
	})
	if !ok {
		t.Fatal("first trade must be accepted")
	}
	asks := snapshotAsks(ob)
	if len(asks) != 1 || !approxEq(asks[0].Size, 3) {
		t.Fatalf("level not decremented: %+v", asks)
	}
}

func TestManagerIngestTradeDedupesByHash(t *testing.T) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	ob := m.OBForToken("yes-1")
	ob.SetFromSnapshot(nil, []BookLevel{{0.55, 5}})

	ok1 := m.IngestTrade("yes-1", Trade{Hash: "0xdup", Side: SideBuy, Price: 0.55, Size: 1})
	ok2 := m.IngestTrade("yes-1", Trade{Hash: "0xdup", Side: SideBuy, Price: 0.55, Size: 1})

	if !ok1 {
		t.Fatal("first ingest must be accepted")
	}
	if ok2 {
		t.Fatal("replay ingest must be rejected")
	}
	asks := snapshotAsks(ob)
	if !approxEq(asks[0].Size, 4) {
		t.Fatalf("dup decremented twice: size=%v want 4", asks[0].Size)
	}
}

func TestManagerIngestTradeUnknownTokenNoop(t *testing.T) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	if ok := m.IngestTrade("ghost", Trade{Hash: "0xa"}); ok {
		t.Fatal("ingest on unknown token must return false")
	}
}

// TestManagerConcurrentIngest ensures the dedup set is correct under
// fan-in from multiple WS goroutines.
func TestManagerConcurrentIngest(t *testing.T) {
	m := NewManager(tokens("k1", "yes-1", "no-1"))
	ob := m.OBForToken("yes-1")
	ob.SetFromSnapshot(nil, []BookLevel{{0.55, 1_000_000}})

	const workers = 8
	const each = 100
	var accepts atomic.Int64
	donec := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < each; i++ {
				if m.IngestTrade("yes-1", Trade{
					Hash:  fmt.Sprintf("0x%d", i),
					Side:  SideBuy,
					Price: 0.55,
					Size:  1,
				}) {
					accepts.Add(1)
				}
			}
			donec <- struct{}{}
		}()
	}
	for i := 0; i < workers; i++ {
		<-donec
	}
	if got := accepts.Load(); got != int64(each) {
		t.Fatalf("accepts=%d, want %d (each unique hash exactly once)", got, each)
	}
}
