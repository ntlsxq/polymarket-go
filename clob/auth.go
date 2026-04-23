package clob

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"github.com/goccy/go-json"
	"math/big"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
)

func keccak256(data []byte) []byte {
	return crypto.Keccak256(data)
}

func encodePacked(parts ...[]byte) []byte {
	var buf []byte
	for _, p := range parts {
		buf = append(buf, p...)
	}
	return buf
}

func padTo32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)
	return padded
}

func encodeUint256(v *big.Int) []byte {
	return math.U256Bytes(new(big.Int).Set(v))
}

func encodeUint8(v int) []byte {
	return padTo32(big.NewInt(int64(v)).Bytes())
}

func encodeAddress(addr common.Address) []byte {
	return padTo32(addr.Bytes())
}

func encodeString(s string) []byte {
	return keccak256([]byte(s))
}

const (
	clobDomainName = "ClobAuthDomain"
	clobVersion    = "1"
	msgToSign      = "This message attests that I control the given wallet"
)

var clobAuthTypeHash = keccak256([]byte("ClobAuth(address address,string timestamp,uint256 nonce,string message)"))

func buildClobAuthDomainSeparator(chainID int) []byte {
	domainTypeHash := keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId)"))
	return keccak256(encodePacked(
		domainTypeHash,
		encodeString(clobDomainName),
		encodeString(clobVersion),
		encodeUint256(big.NewInt(int64(chainID))),
	))
}

func buildClobAuthStructHash(address common.Address, timestamp string, nonce int) []byte {
	return keccak256(encodePacked(
		clobAuthTypeHash,
		encodeAddress(address),
		encodeString(timestamp),
		encodeUint256(big.NewInt(int64(nonce))),
		encodeString(msgToSign),
	))
}

func signEIP712Digest(privKey *ecdsa.PrivateKey, domainSeparator, structHash []byte) (string, error) {
	digest := keccak256(encodePacked(
		[]byte{0x19, 0x01},
		domainSeparator,
		structHash,
	))

	sig, err := crypto.Sign(digest, privKey)
	if err != nil {
		return "", fmt.Errorf("crypto.Sign: %w", err)
	}
	sig[64] += 27

	return "0x" + common.Bytes2Hex(sig), nil
}

func signClobAuthMessage(privKey *ecdsa.PrivateKey, address common.Address, chainID int, timestamp int64, nonce int) (string, error) {
	domainSep := buildClobAuthDomainSeparator(chainID)
	structHash := buildClobAuthStructHash(address, strconv.FormatInt(timestamp, 10), nonce)
	return signEIP712Digest(privKey, domainSep, structHash)
}

func l1Headers(privKey *ecdsa.PrivateKey, address common.Address, chainID int, nonce int) (map[string]string, error) {
	timestamp := time.Now().Unix()
	sig, err := signClobAuthMessage(privKey, address, chainID, timestamp, nonce)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"POLY_ADDRESS":   address.Hex(),
		"POLY_SIGNATURE": sig,
		"POLY_TIMESTAMP": strconv.FormatInt(timestamp, 10),
		"POLY_NONCE":     strconv.Itoa(nonce),
	}, nil
}

func DeriveApiCreds(host string, privKey *ecdsa.PrivateKey, chainID int) (*ApiCreds, error) {
	address := crypto.PubkeyToAddress(privKey.PublicKey)

	creds, err := callAuthEndpoint(host, EndpointCreateAPIKey, "POST", privKey, address, chainID)
	if err != nil {
		creds, err = callAuthEndpoint(host, EndpointDeriveAPIKey, "GET", privKey, address, chainID)
		if err != nil {
			return nil, fmt.Errorf("derive api creds: %w", err)
		}
	}
	return creds, nil
}

func callAuthEndpoint(host, path, method string, privKey *ecdsa.PrivateKey, address common.Address, chainID int) (*ApiCreds, error) {
	headers, err := l1Headers(privKey, address, chainID, 0)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, strings.TrimRight(host, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("auth endpoint %s returned %d", path, resp.StatusCode)
	}

	var result struct {
		ApiKey     string `json:"apiKey"`
		Secret     string `json:"secret"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode auth response: %w", err)
	}

	return &ApiCreds{
		ApiKey:        result.ApiKey,
		ApiSecret:     result.Secret,
		ApiPassphrase: result.Passphrase,
	}, nil
}

func buildHMACSignature(secret, timestamp, method, requestPath, body string) (string, error) {
	secretBytes, err := base64.URLEncoding.DecodeString(secret)
	if err != nil {
		secretBytes, err = base64.RawURLEncoding.DecodeString(secret)
		if err != nil {
			return "", fmt.Errorf("decode api secret: %w", err)
		}
	}

	message := timestamp + method + requestPath
	if body != "" {
		message += body
	}

	h := hmac.New(sha256.New, secretBytes)
	h.Write([]byte(message))
	return base64.URLEncoding.EncodeToString(h.Sum(nil)), nil
}

func L2Headers(creds *ApiCreds, address common.Address, method, path, body string) (map[string]string, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := buildHMACSignature(creds.ApiSecret, timestamp, method, path, body)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"POLY_ADDRESS":    address.Hex(),
		"POLY_SIGNATURE":  sig,
		"POLY_TIMESTAMP":  timestamp,
		"POLY_API_KEY":    creds.ApiKey,
		"POLY_PASSPHRASE": creds.ApiPassphrase,
	}, nil
}

var orderTypeHash = keccak256([]byte(
	"Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)",
))

func buildCTFDomainSeparator(chainID int, exchangeAddr common.Address) []byte {
	domainTypeHash := keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	return keccak256(encodePacked(
		domainTypeHash,
		encodeString("Polymarket CTF Exchange"),
		encodeString("1"),
		encodeUint256(big.NewInt(int64(chainID))),
		encodeAddress(exchangeAddr),
	))
}

func buildOrderStructHash(order OrderData) []byte {
	return keccak256(encodePacked(
		orderTypeHash,
		encodeUint256(order.Salt),
		encodeAddress(order.Maker),
		encodeAddress(order.Signer),
		encodeAddress(order.Taker),
		encodeUint256(order.TokenID),
		encodeUint256(order.MakerAmount),
		encodeUint256(order.TakerAmount),
		encodeUint256(order.Expiration),
		encodeUint256(order.Nonce),
		encodeUint256(order.FeeRateBps),
		encodeUint8(order.Side),
		encodeUint8(order.SignatureType),
	))
}

func SignOrder(privKey *ecdsa.PrivateKey, chainID int, order OrderData, negRisk bool) (string, error) {
	cfg, ok := GetContractConfig(chainID, negRisk)
	if !ok {
		return "", fmt.Errorf("no contract config for chainID %d negRisk=%v", chainID, negRisk)
	}

	domainSep := buildCTFDomainSeparator(chainID, cfg.Exchange)
	structHash := buildOrderStructHash(order)
	return signEIP712Digest(privKey, domainSep, structHash)
}

func generateSalt() *big.Int {
	now := time.Now().UTC().UnixMilli()
	r := rand.Int63n(now + 1)
	return big.NewInt(r)
}

func saltFromID(id string) *big.Int {
	h := keccak256([]byte(id))
	n := new(big.Int).SetBytes(h[:8])
	return n.And(n, big.NewInt(0x7fffffffffffffff))
}
