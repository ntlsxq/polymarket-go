package polymarket

const WSUser = "wss://ws-subscriptions-clob.polymarket.com/ws/user"

const WSMarket = "wss://ws-subscriptions-clob.polymarket.com/ws/market"

var AllCoins = []string{"bitcoin", "ethereum", "solana", "xrp"}

var CoinShort = map[string]string{
	"bitcoin":  "BTC",
	"ethereum": "ETH",
	"solana":   "SOL",
	"xrp":      "XRP",
}
