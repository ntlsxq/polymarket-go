package book

// Side is the order/trade direction on the price ladder: BUY sits on bids,
// SELL sits on asks. Distinct from outcome side (YES/NO), which is handled
// by BookPair.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// ParseSide maps a wire string to a typed Side. The second return is false
// for any value outside {"BUY","SELL"} — callers treat that as a drop.
func ParseSide(s string) (Side, bool) {
	switch Side(s) {
	case SideBuy:
		return SideBuy, true
	case SideSell:
		return SideSell, true
	}
	return "", false
}
