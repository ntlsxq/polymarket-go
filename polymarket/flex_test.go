package polymarket

import (
	"testing"

	"github.com/goccy/go-json"
)

func TestFlexFloatStringForm(t *testing.T) {
	var v flexFloat
	if err := json.Unmarshal([]byte(`"0.4321"`), &v); err != nil {
		t.Fatal(err)
	}
	if float64(v) != 0.4321 {
		t.Fatalf("got %v", v)
	}
}

func TestFlexFloatNumberForm(t *testing.T) {
	var v flexFloat
	if err := json.Unmarshal([]byte(`0.4321`), &v); err != nil {
		t.Fatal(err)
	}
	if float64(v) != 0.4321 {
		t.Fatalf("got %v", v)
	}
}

func TestFlexFloatNullEmpty(t *testing.T) {
	var v flexFloat
	if err := json.Unmarshal([]byte(`null`), &v); err != nil {
		t.Fatal(err)
	}
	if float64(v) != 0 {
		t.Fatalf("null should leave zero, got %v", v)
	}
	if err := json.Unmarshal([]byte(`""`), &v); err != nil {
		t.Fatal(err)
	}
	if float64(v) != 0 {
		t.Fatalf("empty string should leave zero, got %v", v)
	}
}

func TestFlexIntStringAndNumber(t *testing.T) {
	var s flexInt
	if err := json.Unmarshal([]byte(`"42"`), &s); err != nil {
		t.Fatal(err)
	}
	if int(s) != 42 {
		t.Fatalf("got %v", s)
	}
	var n flexInt
	if err := json.Unmarshal([]byte(`42`), &n); err != nil {
		t.Fatal(err)
	}
	if int(n) != 42 {
		t.Fatalf("got %v", n)
	}
}

func TestFlexStringFromNumber(t *testing.T) {
	var s flexString
	if err := json.Unmarshal([]byte(`42.5`), &s); err != nil {
		t.Fatal(err)
	}
	if string(s) != "42.5" {
		t.Fatalf("got %q", s)
	}
}
