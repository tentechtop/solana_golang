package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

type manifest struct {
	Validators []validatorManifest `json:"validators"`
	UserSeeds  []string            `json:"user_seeds"`
}

type validatorManifest struct {
	ValidatorAddress string `json:"validator_address"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type latestBlockhashResult struct {
	Blockhash string `json:"blockhash"`
	Slot      uint64 `json:"slot"`
	Height    uint64 `json:"height"`
}

type transactionDetailResult struct {
	Signature   string `json:"signature"`
	Found       bool   `json:"found"`
	Location    string `json:"location"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	BlockHeight uint64 `json:"block_height"`
	Slot        uint64 `json:"slot"`
	Blockhash   string `json:"blockhash"`
	Finalized   bool   `json:"finalized"`
}

type probeReport struct {
	RPCURL             string                  `json:"rpc_url"`
	Operation          string                  `json:"operation"`
	SourceAddress      string                  `json:"source_address,omitempty"`
	DestinationAddress string                  `json:"destination_address,omitempty"`
	ValidatorAddress   string                  `json:"validator_address,omitempty"`
	Signature          string                  `json:"signature"`
	Submitted          bool                    `json:"submitted"`
	Confirmed          bool                    `json:"confirmed"`
	Transaction        transactionDetailResult `json:"transaction"`
}

type probeAccounts struct {
	SourceAddress      string
	DestinationAddress string
	ValidatorAddress   string
}

func main() {
	manifestPath := flag.String("manifest", "deploy/generated-4/manifest.json", "cluster manifest path")
	rpcURL := flag.String("rpc-url", "http://101.35.87.31:8899/", "public rpc url")
	operation := flag.String("operation", "transfer", "probe operation: transfer or delegate")
	lamports := flag.Uint64("lamports", 1_000, "transfer lamports")
	timeout := flag.Duration("timeout", 45*time.Second, "confirmation timeout")
	sourceIndex := flag.Int("source-index", 0, "zero-based user seed index for source account")
	destinationIndex := flag.Int("destination-index", 1, "zero-based user seed index for transfer destination")
	validatorIndex := flag.Int("validator-index", 0, "zero-based validator index for delegation")
	flag.Parse()

	report, err := runProbe(*manifestPath, *rpcURL, *operation, *lamports, *timeout, *sourceIndex, *destinationIndex, *validatorIndex)
	if err != nil {
		if report.Signature != "" {
			printReport(report)
		}
		exitError("public rpc probe: %v", err)
	}
	printReport(report)
}

func printReport(report probeReport) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		exitError("marshal report: %v", err)
	}
	fmt.Println(string(data))
}

func runProbe(manifestPath string, rpcURL string, operation string, lamports uint64, timeout time.Duration, sourceIndex int, destinationIndex int, validatorIndex int) (probeReport, error) {
	rpcURL, err := validateRPCURL(rpcURL)
	if err != nil {
		return probeReport{}, err
	}
	operation, err = validateOperation(operation)
	if err != nil {
		return probeReport{}, err
	}
	if lamports == 0 {
		return probeReport{}, fmt.Errorf("lamports must be positive")
	}
	if operation == "delegate" && lamports < stake.MinimumStakeLamports {
		return probeReport{}, fmt.Errorf("delegate lamports must be at least %d", stake.MinimumStakeLamports)
	}
	if timeout <= 0 {
		return probeReport{}, fmt.Errorf("timeout must be positive")
	}
	plan, err := readManifest(manifestPath)
	if err != nil {
		return probeReport{}, fmt.Errorf("read manifest: %w", err)
	}
	sourceSeed, destinationSeed, err := selectProbeSeeds(plan.UserSeeds, sourceIndex, destinationIndex)
	if err != nil {
		return probeReport{}, err
	}

	contextValue, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	transaction, accounts, signature, err := buildProbeTransaction(contextValue, rpcURL, operation, plan, sourceSeed, destinationSeed, validatorIndex, lamports)
	if err != nil {
		return probeReport{}, err
	}
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return probeReport{}, fmt.Errorf("encode transaction: %w", err)
	}
	var submittedSignature string
	if err := callRPC(contextValue, rpcURL, "sendTransaction", []any{base64.StdEncoding.EncodeToString(encoded)}, &submittedSignature); err != nil {
		return probeReport{}, fmt.Errorf("send transaction: %w", err)
	}
	if strings.TrimSpace(submittedSignature) != signature {
		return probeReport{}, fmt.Errorf("submitted signature mismatch: got %s want %s", submittedSignature, signature)
	}
	detail, err := waitTransactionInBlock(contextValue, rpcURL, signature)
	report := probeReport{
		RPCURL:             rpcURL,
		Operation:          operation,
		SourceAddress:      accounts.SourceAddress,
		DestinationAddress: accounts.DestinationAddress,
		ValidatorAddress:   accounts.ValidatorAddress,
		Signature:          signature,
		Submitted:          true,
		Confirmed:          detail.Found && detail.Location == "block",
		Transaction:        detail,
	}
	if err != nil {
		return report, fmt.Errorf("signature %s not confirmed: %w; last_location=%s last_status=%s last_error=%s", signature, err, detail.Location, detail.Status, detail.Error)
	}
	return report, nil
}

func buildProbeTransaction(ctx context.Context, rpcURL string, operation string, plan manifest, sourceSeed string, destinationSeed string, validatorIndex int, lamports uint64) (structure.Transaction, probeAccounts, string, error) {
	latest, err := getLatestBlockhash(ctx, rpcURL)
	if err != nil {
		return structure.Transaction{}, probeAccounts{}, "", err
	}
	blockhash, err := structure.HashFromBase58(latest.Blockhash)
	if err != nil {
		return structure.Transaction{}, probeAccounts{}, "", fmt.Errorf("decode blockhash: %w", err)
	}
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return structure.Transaction{}, probeAccounts{}, "", fmt.Errorf("derive source key: %w", err)
	}
	transaction, accounts, err := buildOperationTransaction(operation, plan, source, destinationSeed, validatorIndex, lamports, blockhash)
	if err != nil {
		return structure.Transaction{}, probeAccounts{}, "", err
	}
	accounts.SourceAddress = source.PublicKey.String()
	signature, err := transaction.TxIDString()
	if err != nil {
		return structure.Transaction{}, probeAccounts{}, "", fmt.Errorf("transaction id: %w", err)
	}
	return transaction, accounts, signature, nil
}

func buildOperationTransaction(operation string, plan manifest, source structure.SolanaKeyPair, destinationSeed string, validatorIndex int, lamports uint64, blockhash structure.Hash) (structure.Transaction, probeAccounts, error) {
	switch operation {
	case "transfer":
		destination, err := keyPairFromSeed(destinationSeed)
		if err != nil {
			return structure.Transaction{}, probeAccounts{}, fmt.Errorf("derive destination key: %w", err)
		}
		transaction, err := blockchain.NewTransferTransaction(source, destination.PublicKey, lamports, blockhash)
		return transaction, probeAccounts{DestinationAddress: destination.PublicKey.String()}, err
	case "delegate":
		validatorAddress, err := selectValidatorAddress(plan, validatorIndex)
		if err != nil {
			return structure.Transaction{}, probeAccounts{}, err
		}
		transaction, err := blockchain.NewDelegateStakeTransaction(source, validatorAddress, lamports, blockhash)
		return transaction, probeAccounts{ValidatorAddress: validatorAddress.String()}, err
	default:
		return structure.Transaction{}, probeAccounts{}, fmt.Errorf("unsupported operation %s", operation)
	}
}

func waitTransactionInBlock(ctx context.Context, rpcURL string, signature string) (transactionDetailResult, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastDetail transactionDetailResult
	for {
		var detail transactionDetailResult
		err := callRPC(ctx, rpcURL, "getTransaction", []any{signature}, &detail)
		if err == nil {
			lastDetail = detail
			if detail.Found && detail.Location == "block" {
				return detail, nil
			}
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return lastDetail, fmt.Errorf("wait transaction block: %w", err)
			}
			return lastDetail, fmt.Errorf("wait transaction block: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func getLatestBlockhash(ctx context.Context, rpcURL string) (latestBlockhashResult, error) {
	var result latestBlockhashResult
	if err := callRPC(ctx, rpcURL, "getLatestBlockhash", []any{}, &result); err != nil {
		return latestBlockhashResult{}, fmt.Errorf("get latest blockhash: %w", err)
	}
	return result, nil
}

func callRPC(ctx context.Context, rpcURL string, method string, params []any, result any) error {
	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("post request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("http status %s", response.Status)
	}
	var envelope rpcResponse
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("rpc %s: %s", method, envelope.Error.detail())
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}

func readManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var value manifest
	if err := json.Unmarshal(data, &value); err != nil {
		return manifest{}, err
	}
	return value, nil
}

func selectProbeSeeds(seeds []string, sourceIndex int, destinationIndex int) (string, string, error) {
	if len(seeds) < 2 {
		return "", "", fmt.Errorf("manifest needs at least two user seeds")
	}
	if sourceIndex < 0 || sourceIndex >= len(seeds) {
		return "", "", fmt.Errorf("source index %d out of range", sourceIndex)
	}
	if destinationIndex < 0 || destinationIndex >= len(seeds) {
		return "", "", fmt.Errorf("destination index %d out of range", destinationIndex)
	}
	sourceSeed := strings.TrimSpace(seeds[sourceIndex])
	destinationSeed := strings.TrimSpace(seeds[destinationIndex])
	if sourceSeed == "" || destinationSeed == "" {
		return "", "", fmt.Errorf("probe seeds must not be empty")
	}
	if sourceSeed == destinationSeed {
		return "", "", fmt.Errorf("probe source and destination seeds must differ")
	}
	return sourceSeed, destinationSeed, nil
}

func selectValidatorAddress(plan manifest, validatorIndex int) (structure.PublicKey, error) {
	if validatorIndex < 0 || validatorIndex >= len(plan.Validators) {
		return structure.PublicKey{}, fmt.Errorf("validator index %d out of range", validatorIndex)
	}
	addressText := strings.TrimSpace(plan.Validators[validatorIndex].ValidatorAddress)
	if addressText == "" {
		return structure.PublicKey{}, fmt.Errorf("validator address at index %d is empty", validatorIndex)
	}
	address, err := structure.PublicKeyFromBase58(addressText)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("decode validator address: %w", err)
	}
	return address, nil
}

func validateOperation(value string) (string, error) {
	operation := strings.ToLower(strings.TrimSpace(value))
	switch operation {
	case "transfer", "delegate":
		return operation, nil
	default:
		return "", fmt.Errorf("operation must be transfer or delegate")
	}
}

func validateRPCURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("rpc url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse rpc url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("rpc url must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("rpc url host is empty")
	}
	return trimmed, nil
}

func (rpcError *rpcError) detail() string {
	if rpcError == nil {
		return ""
	}
	if len(rpcError.Data) == 0 {
		return rpcError.Message
	}
	return fmt.Sprintf("%s data=%s", rpcError.Message, string(rpcError.Data))
}

func keyPairFromSeed(seed string) (structure.SolanaKeyPair, error) {
	return structure.KeyPairFromSeed(utils.SHA256([]byte(strings.TrimSpace(seed))))
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
