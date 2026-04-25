package polymarket

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// realisticFrame approximates the on-wire size of a price_change event.
// Pool.accept hashes the entire frame so size matters for hash time.
func realisticFrame(i int) []byte {
	return []byte(fmt.Sprintf(
		`{"event_type":"price_change","price_changes":[{"asset_id":"%s","side":"BUY","price":"0.%04d","size":"%d"}]}`,
		yesTID, 4900+i%50, 1+i%20))
}

// BenchmarkPoolAcceptUnique drives the path where every frame is new — Mutex
// + map insert + hash.
func BenchmarkPoolAcceptUnique(b *testing.B) {
	p, _ := newStubPool(1)
	frames := make([][]byte, b.N)
	for i := range frames {
		frames[i] = realisticFrame(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.members[0].filter(frames[i])
	}
}

// BenchmarkPoolAcceptDuplicate replays one frame — every call hashes + map
// lookup hits and drops. Steady-state cost in a 2-member pool.
func BenchmarkPoolAcceptDuplicate(b *testing.B) {
	p, _ := newStubPool(1)
	frame := realisticFrame(42)
	p.members[0].filter(frame)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.members[0].filter(frame)
	}
}

// BenchmarkPoolAcceptParallel measures contention with many goroutines hitting
// the same shared mutex. Captures the cost of the single-mutex design at scale.
func BenchmarkPoolAcceptParallel(b *testing.B) {
	p, _ := newStubPool(1)
	var seq atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := seq.Add(1)
			p.members[0].filter(realisticFrame(int(i)))
		}
	})
}

// BenchmarkPoolAcceptMixed alternates unique and duplicate frames in a 2:1
// ratio (approximating two redundant streams converging).
func BenchmarkPoolAcceptMixed(b *testing.B) {
	p, _ := newStubPool(1)
	uniq := make([][]byte, 1024)
	for i := range uniq {
		uniq[i] = realisticFrame(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fr := uniq[i%len(uniq)]
		p.members[0].filter(fr)
		p.members[0].filter(fr) // sibling stream
	}
}
