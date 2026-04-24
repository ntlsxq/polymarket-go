package clob

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"github.com/goccy/go-json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"
)

func (oc *OnChainClient) WaitForConfirmed(ctx context.Context, txID string) (*TxResult, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			result, state, err := oc.pollTransaction(ctx, "/transaction?id="+txID)
			if err != nil {
				log.Debug().Err(err).Str("txID", txID).Msg("[ONCHAIN] poll_error")
				continue
			}
			if state == "" {
				continue
			}
			if result.Status == 0 {
				if result.ErrorMsg != "" {
					return result, fmt.Errorf("tx %s (hash %s) failed: %s", txID, result.TxHash, result.ErrorMsg)
				}
				return result, fmt.Errorf("tx %s (hash %s) reverted", txID, result.TxHash)
			}

			if state == "CONFIRMED" {
				log.Info().Str("txID", txID).Str("tx", result.TxHash).Str("state", state).Msg("[ONCHAIN] confirmed")
				return result, nil
			}

			log.Debug().Str("txID", txID).Str("state", state).Msg("[ONCHAIN] waiting")
		}
	}
}

func (oc *OnChainClient) pollTransaction(ctx context.Context, path string) (*TxResult, string, error) {
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", relayerURL+path, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	resp, err := oc.relayerClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("poll %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read poll body: %w", err)
	}

	if resp.StatusCode == 404 {
		return nil, "", nil
	}
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("relayer %s: %d %s", path, resp.StatusCode, string(body))
	}

	type relayerTx struct {
		State    string `json:"state"`
		TxHash   string `json:"transactionHash"`
		ErrorMsg string `json:"errorMsg"`
	}
	var txArr []relayerTx
	if err := json.Unmarshal(body, &txArr); err != nil {
		var single relayerTx
		if err2 := json.Unmarshal(body, &single); err2 != nil {
			return nil, "", fmt.Errorf("parse response: %w", err)
		}
		txArr = []relayerTx{single}
	}
	if len(txArr) == 0 {
		return nil, "", nil
	}

	tx := txArr[0]
	log.Debug().Str("state", tx.State).Str("txHash", tx.TxHash).Str("err", tx.ErrorMsg).Msg("[RELAYER] poll")

	state := strings.TrimPrefix(strings.ToUpper(tx.State), "STATE_")
	switch state {
	case "EXECUTED", "CONFIRMED", "MINED":
		return &TxResult{TxHash: tx.TxHash, Status: 1}, state, nil
	case "FAILED", "INVALID":
		return &TxResult{TxHash: tx.TxHash, Status: 0, ErrorMsg: tx.ErrorMsg}, state, nil
	default:
		return nil, "", nil
	}
}

type proxyCallArg struct {
	TypeCode uint8
	To       common.Address
	Value    *big.Int
	Data     []byte
}

func (oc *OnChainClient) sendProxyTx(ctx context.Context, target common.Address, calldata []byte) (string, error) {
	return oc.sendProxyTxBatch(ctx, []proxyCallArg{{callTypeCall, target, big.NewInt(0), calldata}})
}

func (oc *OnChainClient) sendProxyTxBatch(ctx context.Context, calls []proxyCallArg) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	zero := big.NewInt(0)

	encodedFunction, err := proxyWalletFactoryABI.Pack("proxy", calls)
	if err != nil {
		return "", fmt.Errorf("pack proxy: %w", err)
	}

	rpReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/relay-payload?address=%s&type=PROXY", relayerURL, oc.fromAddr.Hex()), nil)
	if err != nil {
		return "", fmt.Errorf("create relay-payload request: %w", err)
	}
	rpResp, err := oc.relayerClient.Do(rpReq)
	if err != nil {
		return "", fmt.Errorf("relay-payload: %w", err)
	}
	defer rpResp.Body.Close()
	rpBody, err := io.ReadAll(rpResp.Body)
	if err != nil {
		return "", fmt.Errorf("read relay-payload body: %w", err)
	}
	if rpResp.StatusCode != 200 {
		return "", fmt.Errorf("relay-payload %d: %s", rpResp.StatusCode, string(rpBody))
	}

	var rp struct {
		Address string `json:"address"`
		Nonce   string `json:"nonce"`
	}
	if err := json.Unmarshal(rpBody, &rp); err != nil {
		return "", fmt.Errorf("parse relay-payload: %w", err)
	}

	relayAddr := common.HexToAddress(rp.Address)
	nonceBig := new(big.Int)
	nonceBig.SetString(rp.Nonce, 10)
	gasLimit := uint64(len(calls))*gasPerCall + proxyWrapperOverhead
	gasLimitBig := new(big.Int).SetUint64(gasLimit)

	log.Debug().Str("relay", relayAddr.Hex()).Str("nonce", rp.Nonce).Msg("[PROXY] relay_payload")

	relayHash := crypto.Keccak256(encodePacked(
		[]byte("rlx:"),
		oc.fromAddr.Bytes(),
		oc.factoryAddr.Bytes(),
		encodedFunction,
		encodeUint256(zero),
		encodeUint256(zero),
		encodeUint256(gasLimitBig),
		encodeUint256(nonceBig),
		oc.relayHub.Bytes(),
		relayAddr.Bytes(),
	))

	ethSignedHash := crypto.Keccak256(encodePacked(
		[]byte("\x19Ethereum Signed Message:\n32"),
		relayHash,
	))
	sigBytes, err := crypto.Sign(ethSignedHash, oc.privKey)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	sigBytes[64] += 27

	proxyWallet := deriveProxyWallet(oc.fromAddr)
	submitBody := map[string]any{
		"type":        "PROXY",
		"from":        strings.ToLower(oc.fromAddr.Hex()),
		"to":          strings.ToLower(oc.factoryAddr.Hex()),
		"proxyWallet": strings.ToLower(proxyWallet.Hex()),
		"data":        "0x" + hex.EncodeToString(encodedFunction),
		"value":       "0",
		"nonce":       rp.Nonce,
		"signature":   "0x" + hex.EncodeToString(sigBytes),
		"signatureParams": map[string]string{
			"gasPrice":   "0",
			"relayerFee": "0",
			"gasLimit":   fmt.Sprintf("%d", gasLimit),
			"relay":      strings.ToLower(relayAddr.Hex()),
			"relayHub":   strings.ToLower(oc.relayHub.Hex()),
		},
	}

	bodyBytes, _ := json.Marshal(submitBody)
	log.Debug().RawJSON("body", bodyBytes).Msg("[RELAYER] submit")

	subReq, err := http.NewRequestWithContext(ctx, "POST", relayerURL+"/submit", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create submit request: %w", err)
	}
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Header.Set("RELAYER_API_KEY", oc.relayerAPIKey)
	subReq.Header.Set("RELAYER_API_KEY_ADDRESS", strings.ToLower(oc.fromAddr.Hex()))

	subResp, err := oc.relayerClient.Do(subReq)
	if err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}
	defer subResp.Body.Close()
	respBody, err := io.ReadAll(subResp.Body)
	if err != nil {
		return "", fmt.Errorf("read submit body: %w", err)
	}

	if subResp.StatusCode != 200 && subResp.StatusCode != 201 {
		return "", fmt.Errorf("submit %d: %s", subResp.StatusCode, string(respBody))
	}

	var sr struct {
		TransactionID string `json:"transactionID"`
		TxHash        string `json:"transactionHash"`
	}
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return "", fmt.Errorf("parse submit response: %w", err)
	}
	txID := sr.TransactionID
	if txID == "" {
		txID = sr.TxHash
	}
	if txID == "" {
		return "", fmt.Errorf("no transaction ID: %s", string(respBody))
	}

	log.Debug().Str("txID", txID).Msg("[ONCHAIN] relayer_ok")
	return txID, nil
}

func deriveProxyWallet(signer common.Address) common.Address {
	factory := common.HexToAddress(ProxyWalletFactoryAddr)
	salt := crypto.Keccak256(signer.Bytes())
	initCodeHash := common.FromHex(ProxyInitCodeHash)

	data := make([]byte, 0, 85)
	data = append(data, 0xff)
	data = append(data, factory.Bytes()...)
	data = append(data, salt...)
	data = append(data, initCodeHash...)
	return common.BytesToAddress(crypto.Keccak256(data)[12:])
}
