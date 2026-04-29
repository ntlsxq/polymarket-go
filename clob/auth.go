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
	ctfVersion     = "2"
	msgToSign      = "This message attests that I control the given wallet"
)

// Pre-computed at init: type hashes and constant-string keccaks. EIP-712
// encoding hashes constant strings every call; doing it once here saves
// 5–6 keccak256 invocations + the corresponding allocs per signed order.
var (
	clobAuthTypeHash         = keccak256([]byte("ClobAuth(address address,string timestamp,uint256 nonce,string message)"))
	orderTypeHash            = keccak256([]byte("Order(uint256 salt,address maker,address signer,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint8 side,uint8 signatureType,uint256 timestamp,bytes32 metadata,bytes32 builder)"))
	eip712DomainTypeHashClob = keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId)"))
	eip712DomainTypeHashCTF  = keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

	keccakClobDomainName = keccak256([]byte(clobDomainName))
	keccakClobVersion    = keccak256([]byte(clobVersion))
	keccakMsgToSign      = keccak256([]byte(msgToSign))
	keccakCTFName        = keccak256([]byte("Polymarket CTF Exchange"))
	keccakCTFVersion     = keccak256([]byte(ctfVersion))
)

// writeUint256 writes v as a 32-byte big-endian uint256 into dst[0:32].
// dst must be exactly 32 bytes; FillBytes zero-pads short values.
func writeUint256(dst []byte, v *big.Int) {
	v.FillBytes(dst)
}

// writeAddress writes addr into the last 20 bytes of dst[0:32], leaving
// dst[0:12] untouched (caller must pre-zero or use a fresh stack array).
func writeAddress(dst []byte, addr common.Address) {
	copy(dst[12:32], addr[:])
}

// writeUint8 writes v into the last byte of dst[0:32]. Caller pre-zeros.
func writeUint8(dst []byte, v int) {
	dst[31] = byte(v)
}

func buildClobAuthDomainSeparator(chainID int) []byte {
	var buf [4 * 32]byte
	copy(buf[0:32], eip712DomainTypeHashClob)
	copy(buf[32:64], keccakClobDomainName)
	copy(buf[64:96], keccakClobVersion)
	writeUint256(buf[96:128], big.NewInt(int64(chainID)))
	return keccak256(buf[:])
}

func buildClobAuthStructHash(address common.Address, timestamp string, nonce int) []byte {
	var buf [5 * 32]byte
	copy(buf[0:32], clobAuthTypeHash)
	writeAddress(buf[32:64], address)
	// timestamp is dynamic — keccak it, but write directly into buf.
	tsHash := keccak256([]byte(timestamp))
	copy(buf[64:96], tsHash)
	writeUint256(buf[96:128], big.NewInt(int64(nonce)))
	copy(buf[128:160], keccakMsgToSign)
	return keccak256(buf[:])
}

func signEIP712Digest(privKey *ecdsa.PrivateKey, domainSeparator, structHash []byte) (string, error) {
	var buf [2 + 32 + 32]byte
	buf[0] = 0x19
	buf[1] = 0x01
	copy(buf[2:34], domainSeparator)
	copy(buf[34:66], structHash)
	digest := keccak256(buf[:])

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

func buildCTFDomainSeparator(chainID int, exchangeAddr common.Address) []byte {
	var buf [5 * 32]byte
	copy(buf[0:32], eip712DomainTypeHashCTF)
	copy(buf[32:64], keccakCTFName)
	copy(buf[64:96], keccakCTFVersion)
	writeUint256(buf[96:128], big.NewInt(int64(chainID)))
	writeAddress(buf[128:160], exchangeAddr)
	return keccak256(buf[:])
}

func buildOrderStructHash(order OrderData) []byte {
	var buf [12 * 32]byte
	copy(buf[0:32], orderTypeHash)
	writeUint256(buf[32:64], order.Salt)
	writeAddress(buf[64:96], order.Maker)
	writeAddress(buf[96:128], order.Signer)
	writeUint256(buf[128:160], order.TokenID)
	writeUint256(buf[160:192], order.MakerAmount)
	writeUint256(buf[192:224], order.TakerAmount)
	writeUint8(buf[224:256], order.Side)
	writeUint8(buf[256:288], order.SignatureType)
	writeUint256(buf[288:320], order.Timestamp)
	copy(buf[320:352], order.Metadata[:])
	copy(buf[352:384], order.Builder[:])
	return keccak256(buf[:])
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

func timestampFromID(id string) *big.Int {
	h := keccak256([]byte("timestamp:" + id))
	n := new(big.Int).SetBytes(h[:8])
	// Keep generated timestamps in a stable, bounded V2-era range. The
	// timestamp is part of order uniqueness, not expiry.
	const baseMillis int64 = 1713398400000
	const rangeMillis int64 = 2 * 365 * 24 * 60 * 60 * 1000
	return big.NewInt(baseMillis + int64(n.Uint64()%uint64(rangeMillis)))
}
