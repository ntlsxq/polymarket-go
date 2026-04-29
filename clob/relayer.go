package clob

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/goccy/go-json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"
)

var (
	ErrBatchTooLarge          = errors.New("relayer batch too large")
	ErrRelayerGuardGas        = errors.New("relayer guard gas")
	ErrEstimateReverted       = errors.New("relayer estimate reverted")
	ErrInnerRelayedCallFailed = errors.New("relayer inner call failed")
	ErrRelayerRateLimited     = errors.New("relayer rate limited")
)

type RelayerError struct {
	Kind   error
	Report *TxPreflightReport
	Err    error
}

func (e *RelayerError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Kind.Error()
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if e.Report != nil {
		msg += fmt.Sprintf(" (ops=%d estimate=%d signed_gas=%d outer_gas=%d approx_max=%d guard_headroom=%d)",
			e.Report.Ops,
			e.Report.InnerGasEstimate,
			e.Report.SignedGasLimit,
			e.Report.OuterGasLimit,
			e.Report.ApproxMaxSignedGas,
			e.Report.ApproxGuardHeadroom,
		)
	}
	return msg
}

func (e *RelayerError) Unwrap() error {
	return e.Err
}

func (e *RelayerError) Is(target error) bool {
	return target == e.Kind || errors.Is(e.Err, target)
}

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
				oc.fillTxResult(ctx, result)
				if result.ErrorMsg != "" {
					return result, classifyRelayerFailure(result, fmt.Errorf("tx %s (hash %s) failed: %s", txID, result.TxHash, result.ErrorMsg))
				}
				return result, classifyRelayerFailure(result, fmt.Errorf("tx %s (hash %s) reverted", txID, result.TxHash))
			}

			if state == "CONFIRMED" {
				oc.fillTxResult(ctx, result)
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
		return &TxResult{TxHash: tx.TxHash, RelayerState: state, Status: 1}, state, nil
	case "FAILED", "INVALID":
		return &TxResult{TxHash: tx.TxHash, RelayerState: state, Status: 0, ErrorMsg: tx.ErrorMsg}, state, nil
	default:
		return nil, "", nil
	}
}

func (oc *OnChainClient) fillTxResult(ctx context.Context, result *TxResult) {
	if result == nil || result.TxHash == "" || oc.eth == nil {
		return
	}
	hash := common.HexToHash(result.TxHash)
	receipt, err := oc.eth.TransactionReceipt(ctx, hash)
	if err == nil && receipt != nil {
		result.Status = receipt.Status
		result.GasUsed = receipt.GasUsed
	}
	tx, _, err := oc.eth.TransactionByHash(ctx, hash)
	if err == nil && tx != nil {
		result.GasLimit = tx.Gas()
	}
}

func classifyRelayerFailure(result *TxResult, err error) error {
	msg := ""
	if result != nil {
		msg = strings.ToLower(result.ErrorMsg)
	}
	errMsg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not enough gasleft") || strings.Contains(errMsg, "not enough gasleft"):
		return &RelayerError{Kind: ErrRelayerGuardGas, Err: err}
	case strings.Contains(msg, "internal transaction failure") ||
		strings.Contains(msg, "captured a revert") ||
		(result != nil && result.Status == 1):
		return &RelayerError{Kind: ErrInnerRelayedCallFailed, Err: err}
	default:
		return err
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

type proxyRelayRequest struct {
	encodedFunction []byte
	relayCallData   []byte
	body            relayerSubmitBody
	report          TxPreflightReport
}

func (oc *OnChainClient) sendProxyTx(ctx context.Context, target common.Address, calldata []byte) (string, error) {
	return oc.sendProxyTxBatch(ctx, []proxyCallArg{{callTypeCall, target, big.NewInt(0), calldata}})
}

func (oc *OnChainClient) sendProxyTxBatch(ctx context.Context, calls []proxyCallArg) (string, error) {
	res, err := oc.sendProxyTxBatchWithReport(ctx, calls)
	if err != nil {
		return "", err
	}
	return res.TxID, nil
}

func (oc *OnChainClient) sendProxyTxBatchWithReport(ctx context.Context, calls []proxyCallArg) (*TxSendResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	req, err := oc.preflightProxyTxBatch(ctx, calls)
	if err != nil {
		return nil, err
	}

	bodyBytes, _ := json.Marshal(req.body)
	log.Debug().RawJSON("body", bodyBytes).Msg("[RELAYER] submit")

	subReq, err := http.NewRequestWithContext(ctx, "POST", relayerURL+"/submit", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create submit request: %w", err)
	}
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Header.Set("RELAYER_API_KEY", oc.relayerAPIKey)
	subReq.Header.Set("RELAYER_API_KEY_ADDRESS", strings.ToLower(oc.fromAddr.Hex()))

	subResp, err := oc.relayerClient.Do(subReq)
	if err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}
	defer subResp.Body.Close()
	respBody, err := io.ReadAll(subResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read submit body: %w", err)
	}

	if subResp.StatusCode == http.StatusTooManyRequests {
		return nil, &RelayerError{Kind: ErrRelayerRateLimited, Report: &req.report, Err: fmt.Errorf("submit %d: %s", subResp.StatusCode, string(respBody))}
	}
	if subResp.StatusCode != 200 && subResp.StatusCode != 201 {
		return nil, fmt.Errorf("submit %d: %s", subResp.StatusCode, string(respBody))
	}

	var sr struct {
		TransactionID string `json:"transactionID"`
		TxHash        string `json:"transactionHash"`
	}
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("parse submit response: %w", err)
	}
	txID := sr.TransactionID
	if txID == "" {
		txID = sr.TxHash
	}
	if txID == "" {
		return nil, fmt.Errorf("no transaction ID: %s", string(respBody))
	}

	log.Debug().Str("txID", txID).Msg("[ONCHAIN] relayer_ok")
	return &TxSendResult{TxID: txID, Preflight: req.report}, nil
}

func (oc *OnChainClient) preflightProxyTxBatch(ctx context.Context, calls []proxyCallArg) (*proxyRelayRequest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	encodedFunction, err := proxyWalletFactoryABI.Pack("proxy", calls)
	if err != nil {
		return nil, fmt.Errorf("pack proxy: %w", err)
	}

	relayAddr, nonce, nonceBig, err := oc.fetchRelayPayload(ctx)
	if err != nil {
		return nil, err
	}

	report := TxPreflightReport{
		Ops:                  len(calls),
		ProxyWallet:          deriveProxyWallet(oc.fromAddr).Hex(),
		Relay:                relayAddr.Hex(),
		Nonce:                nonce,
		OuterGasLimit:        oc.relayerOuterGasLimit,
		EncodedFunctionBytes: len(encodedFunction),
	}

	est, err := oc.estimateProxyGas(ctx, encodedFunction)
	if err != nil {
		return nil, &RelayerError{Kind: ErrEstimateReverted, Report: &report, Err: fmt.Errorf("estimate proxy gas: %w", err)}
	}
	margin := relayerGasMargin(est)
	gasLimit := est + margin
	report.InnerGasEstimate = est
	report.GasMargin = margin
	report.SignedGasLimit = gasLimit

	sigBytes, err := oc.signProxyRelay(encodedFunction, gasLimit, relayAddr, nonceBig)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	relayCallData, err := oc.packRelayCall(encodedFunction, gasLimit, relayAddr, nonceBig, sigBytes)
	if err != nil {
		return nil, err
	}
	report.RelayCallBytes = len(relayCallData)
	maxGas, intrinsic, err := maxGuardRelayGasLimit(relayCallData, oc.relayerOuterGasLimit)
	if err == nil {
		report.RelayCallIntrinsicGas = intrinsic
		report.ApproxMaxSignedGas = maxGas
		report.ApproxGuardHeadroom = int64(maxGas) - int64(gasLimit)
	}
	if err := oc.simulateRelayHubGuard(ctx, relayAddr, relayCallData, &report); err != nil {
		return nil, err
	}

	log.Debug().
		Uint64("estimate", est).
		Uint64("margin", margin).
		Uint64("gasLimit", gasLimit).
		Uint64("outerGasLimit", report.OuterGasLimit).
		Int64("approxGuardHeadroom", report.ApproxGuardHeadroom).
		Int("ops", len(calls)).
		Msg("[ONCHAIN] gas_preflight")

	body := relayerSubmitBody{
		Type:        "PROXY",
		From:        strings.ToLower(oc.fromAddr.Hex()),
		To:          strings.ToLower(oc.factoryAddr.Hex()),
		ProxyWallet: strings.ToLower(report.ProxyWallet),
		Data:        "0x" + hex.EncodeToString(encodedFunction),
		Value:       "0",
		Nonce:       nonce,
		Signature:   "0x" + hex.EncodeToString(sigBytes),
		SignatureParams: relayerSignatureParams{
			GasPrice:   "0",
			RelayerFee: "0",
			GasLimit:   fmt.Sprintf("%d", gasLimit),
			Relay:      strings.ToLower(relayAddr.Hex()),
			RelayHub:   strings.ToLower(oc.relayHub.Hex()),
		},
	}

	return &proxyRelayRequest{
		encodedFunction: encodedFunction,
		relayCallData:   relayCallData,
		body:            body,
		report:          report,
	}, nil
}

func (oc *OnChainClient) fetchRelayPayload(ctx context.Context) (common.Address, string, *big.Int, error) {
	rpReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/relay-payload?address=%s&type=PROXY", relayerURL, oc.fromAddr.Hex()), nil)
	if err != nil {
		return common.Address{}, "", nil, fmt.Errorf("create relay-payload request: %w", err)
	}
	rpResp, err := oc.relayerClient.Do(rpReq)
	if err != nil {
		return common.Address{}, "", nil, fmt.Errorf("relay-payload: %w", err)
	}
	defer rpResp.Body.Close()
	rpBody, err := io.ReadAll(rpResp.Body)
	if err != nil {
		return common.Address{}, "", nil, fmt.Errorf("read relay-payload body: %w", err)
	}
	if rpResp.StatusCode != 200 {
		if rpResp.StatusCode == http.StatusTooManyRequests {
			return common.Address{}, "", nil, &RelayerError{Kind: ErrRelayerRateLimited, Err: fmt.Errorf("relay-payload %d: %s", rpResp.StatusCode, string(rpBody))}
		}
		return common.Address{}, "", nil, fmt.Errorf("relay-payload %d: %s", rpResp.StatusCode, string(rpBody))
	}

	var rp struct {
		Address string `json:"address"`
		Nonce   string `json:"nonce"`
	}
	if err := json.Unmarshal(rpBody, &rp); err != nil {
		return common.Address{}, "", nil, fmt.Errorf("parse relay-payload: %w", err)
	}

	relayAddr := common.HexToAddress(rp.Address)
	nonceBig := new(big.Int)
	if _, ok := nonceBig.SetString(rp.Nonce, 10); !ok {
		return common.Address{}, "", nil, fmt.Errorf("parse relay nonce %q", rp.Nonce)
	}

	log.Debug().Str("relay", relayAddr.Hex()).Str("nonce", rp.Nonce).Msg("[PROXY] relay_payload")
	return relayAddr, rp.Nonce, nonceBig, nil
}

func (oc *OnChainClient) simulateRelayHubGuard(ctx context.Context, relayAddr common.Address, relayCallData []byte, report *TxPreflightReport) error {
	if oc.relayerOuterGasLimit == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := oc.eth.CallContract(ctx, ethereum.CallMsg{
		From:     relayAddr,
		To:       &oc.relayHub,
		Gas:      oc.relayerOuterGasLimit,
		GasPrice: big.NewInt(0),
		Data:     relayCallData,
	}, nil)
	if err == nil {
		report.RelayHubSimulationOK = true
		return nil
	}
	msg := strings.ToLower(err.Error())
	kind := ErrInnerRelayedCallFailed
	if strings.Contains(msg, "not enough gasleft") || strings.Contains(msg, "out of gas") {
		kind = ErrBatchTooLarge
	}
	return &RelayerError{Kind: kind, Report: report, Err: err}
}

func relayerGasMargin(estimate uint64) uint64 {
	pct := estimate * relayerGasMarginBps / 10_000
	if pct < relayerGasMarginMin {
		return relayerGasMarginMin
	}
	return pct
}

func (oc *OnChainClient) signMaxGuardRelay(encodedFunction []byte, relayAddr common.Address, nonceBig *big.Int) (uint64, []byte, uint64, error) {
	outerGasLimit := oc.relayerOuterGasLimit
	if outerGasLimit == 0 {
		outerGasLimit = defaultRelayerOuterGasLimit
	}
	gasLimit := uint64(outerGasLimit - relayHubGuardReserveGas - relayHubPreGuardGas - txBaseGas)
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
		maxGas, relayCallGas, err := maxGuardRelayGasLimit(relayCallData, outerGasLimit)
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

func maxGuardRelayGasLimit(relayCallData []byte, outerGasLimit uint64) (uint64, uint64, error) {
	relayCallGas := relayCallIntrinsicGas(relayCallData)
	requiredBeforeUserCall := relayCallGas + relayHubGuardReserveGas + relayHubPreGuardGas
	if requiredBeforeUserCall >= outerGasLimit {
		return 0, relayCallGas, fmt.Errorf("relay calldata too large: intrinsic=%d guard=%d preGuard=%d outer=%d", relayCallGas, relayHubGuardReserveGas, relayHubPreGuardGas, outerGasLimit)
	}
	return outerGasLimit - requiredBeforeUserCall, relayCallGas, nil
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
