package book

type BookPair struct {
	Yes *OrderBook
	No  *OrderBook
}

func NewBookPair() *BookPair {
	return &BookPair{
		Yes: NewOrderBook(),
		No:  NewOrderBook(),
	}
}

func (bp *BookPair) Side(side string) *OrderBook {
	if side == "YES" {
		return bp.Yes
	}
	return bp.No
}

func (bp *BookPair) ForToken(isYes bool) *OrderBook {
	if isYes {
		return bp.Yes
	}
	return bp.No
}
