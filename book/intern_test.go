package book

import (
	"sync"
	"testing"
)

func TestInternerRoundTrip(t *testing.T) {
	tokens := []string{
		"71321045679252212594626385532706912750332728571942532289631379312455583992563",
		"10000000000000000000000000000000000000000000000000000000000000000000000000001",
		"33333333333333333333333333333333333333333333333333333333333333333333333333333",
	}
	in := NewInterner(tokens)
	if in.Len() != 3 {
		t.Fatalf("Len = %d, want 3", in.Len())
	}
	for i, tok := range tokens {
		id, ok := in.ID(tok)
		if !ok {
			t.Fatalf("ID(%q) miss", tok)
		}
		if id != uint32(i) {
			t.Fatalf("ID(%q) = %d, want %d", tok, id, i)
		}
		if got := in.String(id); got != tok {
			t.Fatalf("String(%d) = %q, want %q", id, got, tok)
		}
	}
}

func TestInternerDeduplicatesInput(t *testing.T) {
	in := NewInterner([]string{"a", "b", "a", "c", "b"})
	if in.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (deduped)", in.Len())
	}
	// First occurrence wins for ID assignment.
	if id, _ := in.ID("a"); id != 0 {
		t.Fatalf("a ID = %d, want 0", id)
	}
	if id, _ := in.ID("b"); id != 1 {
		t.Fatalf("b ID = %d, want 1", id)
	}
	if id, _ := in.ID("c"); id != 2 {
		t.Fatalf("c ID = %d, want 2", id)
	}
}

func TestInternerMissReturnsFalse(t *testing.T) {
	in := NewInterner([]string{"a"})
	if _, ok := in.ID("ghost"); ok {
		t.Fatalf("unknown token must return ok=false")
	}
	if got := in.String(99); got != "" {
		t.Fatalf("out-of-range ID must return empty, got %q", got)
	}
}

func TestInternerMustIDPanicsOnMiss(t *testing.T) {
	in := NewInterner([]string{"a"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustID must panic on unknown token")
		}
	}()
	_ = in.MustID("ghost")
}

func TestInternerEmpty(t *testing.T) {
	in := NewInterner(nil)
	if in.Len() != 0 {
		t.Fatalf("empty Interner Len = %d", in.Len())
	}
	if _, ok := in.ID(""); ok {
		t.Fatal("empty Interner must miss everything")
	}
}

// TestInternerConcurrentReads asserts the read API is race-free under
// many concurrent goroutines. Interner is immutable after construction
// so this should always pass under -race.
func TestInternerConcurrentReads(t *testing.T) {
	tokens := make([]string, 256)
	for i := range tokens {
		tokens[i] = string(rune('a' + i%26)) + string(rune('a'+i/26))
	}
	in := NewInterner(tokens)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				tok := tokens[i%len(tokens)]
				id, ok := in.ID(tok)
				if !ok {
					t.Errorf("ID miss for %q", tok)
					return
				}
				if got := in.String(id); got != tok {
					t.Errorf("round-trip broke: %q != %q", got, tok)
					return
				}
			}
		}()
	}
	wg.Wait()
}
