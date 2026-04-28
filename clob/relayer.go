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

type relayerSignatureParams struct {
	GasPrice   string `json:"gasPrice"`
	RelayerFee string `json:"relayerFee"`
	GasLimit   string `json:"gasLimit"`
	Relay      string `json:"relay"`
	RelayHub   string `json:"relayHub"`
}

type relayerSubmitBody struct {
	Type            string                 `json:"type"`
	From            string                 `json:"from"`
	To              string                 `json:"to"`
	ProxyWallet     string                 `json:"proxyWallet"`
	Data            string                 `json:"data"`
	Value           string                 `json:"value"`
	Nonce           string                 `json:"nonce"`
	Signature       string                 `json:"signature"`
	SignatureParams relayerSignatureParams `json:"signatureParams"`
}

func (oc *OnChainClient) sendProxyTx(ctx context.Context, target common.Address, calldata []byte) (string, error) {
	return oc.sendProxyTxBatch(ctx, []proxyCallArg{{callTypeCall, target, big.NewInt(0), calldata}})
}

func (oc *OnChainClient) sendProxyTxBatch(ctx context.Context, calls []proxyCallArg) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

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

	log.Debug().Str("relay", relayAddr.Hex()).Str("nonce", rp.Nonce).Msg("[PROXY] relay_payload")

	est, err := oc.estimateProxyGas(ctx, encodedFunction)
	if err != nil {
		return "", fmt.Errorf("estimate proxy gas: %w", err)
	}
	gasLimit := est + gasSlack
	sigBytes, err := oc.signProxyRelay(encodedFunction, gasLimit, relayAddr, nonceBig)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	log.Debug().
		Uint64("estimate", est).
		Uint64("gasLimit", gasLimit).
		Int("ops", len(calls)).
		Msg("[ONCHAIN] gas_estimated")

	proxyWallet := deriveProxyWallet(oc.fromAddr)
	submitBody := relayerSubmitBody{
		Type:        "PROXY",
		From:        strings.ToLower(oc.fromAddr.Hex()),
		To:          strings.ToLower(oc.factoryAddr.Hex()),
		ProxyWallet: strings.ToLower(proxyWallet.Hex()),
		Data:        "0x" + hex.EncodeToString(encodedFunction),
		Value:       "0",
		Nonce:       rp.Nonce,
		Signature:   "0x" + hex.EncodeToString(sigBytes),
		SignatureParams: relayerSignatureParams{
			GasPrice:   "0",
			RelayerFee: "0",
			GasLimit:   fmt.Sprintf("%d", gasLimit),
			Relay:      strings.ToLower(relayAddr.Hex()),
			RelayHub:   strings.ToLower(oc.relayHub.Hex()),
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

func (oc *OnChainClient) signMaxGuardRelay(encodedFunction []byte, relayAddr common.Address, nonceBig *big.Int) (uint64, []byte, uint64, error) {
	gasLimit := uint64(relayerOuterGasLimit - relayHubGuardReserveGas - relayHubPreGuardGas - txBaseGas)
	var bestGas uint64
	var bestSig []byte
	var bestRelayCallGas uint64

	for i := 0; i < 12; i++ {
		sigBytes, err := oc.signProxyRelay(encodedFunction, gasLimit, relayAddr, nonceBig)
		if err != nil {
			return 0, nil, 0, fmt.Errorf("sign: %w", err)
		}
		relayCallData, err := oc.packRelayCall(encodedFunction, gasLimit, relayAddr, nonceBig, sigBytes)
		if err != nil {
			return 0, nil, 0, err
		}
		maxGas, relayCallGas, err := maxGuardRelayGasLimit(relayCallData)
		if err != nil {
			return 0, nil, 0, err
		}
		if gasLimit <= maxGas && gasLimit > bestGas {
			bestGas = gasLimit
			bestSig = sigBytes
			bestRelayCallGas = relayCallGas
		}
		if gasLimit == maxGas {
			return gasLimit, sigBytes, relayCallGas, nil
		}
		gasLimit = maxGas
	}

	if bestGas == 0 {
		return 0, nil, 0, fmt.Errorf("could not find relay gas limit passing RelayHub guard")
	}
	return bestGas, bestSig, bestRelayCallGas, nil
}

func (oc *OnChainClient) signProxyRelay(encodedFunction []byte, gasLimit uint64, relayAddr common.Address, nonceBig *big.Int) ([]byte, error) {
	zero := big.NewInt(0)
	gasLimitBig := new(big.Int).SetUint64(gasLimit)

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
		return nil, err
	}
	sigBytes[64] += 27
	return sigBytes, nil
}

func (oc *OnChainClient) packRelayCall(encodedFunction []byte, gasLimit uint64, relayAddr common.Address, nonceBig *big.Int, sigBytes []byte) ([]byte, error) {
	zero := big.NewInt(0)
	return relayHubABI.Pack(
		"relayCall",
		oc.fromAddr,
		oc.factoryAddr,
		encodedFunction,
		zero,
		zero,
		new(big.Int).SetUint64(gasLimit),
		nonceBig,
		sigBytes,
		[]byte{},
	)
}

func maxGuardRelayGasLimit(relayCallData []byte) (uint64, uint64, error) {
	relayCallGas := relayCallIntrinsicGas(relayCallData)
	requiredBeforeUserCall := relayCallGas + relayHubGuardReserveGas + relayHubPreGuardGas
	if requiredBeforeUserCall >= relayerOuterGasLimit {
		return 0, relayCallGas, fmt.Errorf("relay calldata too large: intrinsic=%d guard=%d preGuard=%d outer=%d", relayCallGas, relayHubGuardReserveGas, relayHubPreGuardGas, relayerOuterGasLimit)
	}
	return relayerOuterGasLimit - requiredBeforeUserCall, relayCallGas, nil
}

func relayCallIntrinsicGas(data []byte) uint64 {
	gas := uint64(txBaseGas)
	for _, b := range data {
		if b == 0 {
			gas += txDataZeroGas
		} else {
			gas += txDataNonZeroGas
		}
	}
	return gas
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
