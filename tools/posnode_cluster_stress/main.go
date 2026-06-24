package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"solana_golang/blockchain"
	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

type manifest struct {
	Validators []validatorManifest `json:"validators"`
	UserSeeds  []string            `json:"user_seeds"`
	RPCURLs    []string            `json:"rpc_urls"`
}

type validatorManifest struct {
	Name             string `json:"name"`
	StakerSeed       string `json:"staker_seed"`
	ValidatorAddress string `json:"validator_address"`
	RPCURL           string `json:"rpc_url"`
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

type consensusStatusResult struct {
	EpochID uint64 `json:"epoch_id"`
	Slot    uint64 `json:"slot"`
}

type operationStats struct {
	Submitted    atomic.Uint64
	Succeeded    atomic.Uint64
	Failed       atomic.Uint64
	mutex        sync.Mutex
	Errors       map[string]uint64
	ErrorSamples map[string]string
}

type stressState struct {
	mutex             sync.Mutex
	stakePositions    []stakePosition
	delegatePositions []stakePosition
}

type stakePosition struct {
	Seed             string
	ValidatorAddress string
	Amount           uint64
	CreatedEpoch     uint64
	UnlockEpoch      uint64
	Withdrawable     bool
}

type stressReport struct {
	StartedAt         string                    `json:"started_at"`
	FinishedAt        string                    `json:"finished_at"`
	DurationSeconds   float64                   `json:"duration_seconds"`
	Workers           int                       `json:"workers"`
	TransferSucceeded uint64                    `json:"transfer_succeeded"`
	StakeSucceeded    uint64                    `json:"stake_succeeded"`
	DelegateSucceeded uint64                    `json:"delegate_succeeded"`
	WithdrawSucceeded uint64                    `json:"withdraw_succeeded"`
	Failed            uint64                    `json:"failed"`
	ByOperation       map[string]operationCount `json:"by_operation"`
}

type operationCount struct {
	Submitted    uint64            `json:"submitted"`
	Succeeded    uint64            `json:"succeeded"`
	Failed       uint64            `json:"failed"`
	Errors       map[string]uint64 `json:"errors,omitempty"`
	ErrorSamples map[string]string `json:"error_samples,omitempty"`
}

func main() {
	manifestPath := flag.String("manifest", "deploy/generated-4/manifest.json", "cluster manifest path")
	duration := flag.Duration("duration", 2*time.Minute, "stress duration")
	workers := flag.Int("workers", 6, "concurrent workers")
	transactions := flag.Int("transactions", 0, "exact transfer transaction count; duration becomes timeout")
	startIndex := flag.Uint64("start-index", 0, "first fixed transfer index")
	rpcURL := flag.String("rpc-url", "", "override all submissions to one rpc url")
	outputPath := flag.String("output", "deploy/generated-4/stress-report.json", "summary output path")
	flag.Parse()

	if *workers < 1 {
		exitError("workers must be positive")
	}
	if *transactions < 0 {
		exitError("transactions cannot be negative")
	}
	plan, err := readManifest(*manifestPath)
	if err != nil {
		exitError("read manifest: %v", err)
	}
	plan, err = applyRPCURLOverride(plan, *rpcURL)
	if err != nil {
		exitError("override rpc url: %v", err)
	}
	if len(plan.RPCURLs) == 0 || len(plan.UserSeeds) < 4 || len(plan.Validators) == 0 {
		exitError("manifest does not contain enough rpc urls, users, or validators")
	}
	var report stressReport
	if *transactions > 0 {
		report, err = runFixedTransfers(plan, *duration, *workers, *transactions, *startIndex)
	} else {
		report, err = runStress(plan, *duration, *workers)
	}
	if err != nil {
		exitError("run stress: %v", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		exitError("marshal report: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*outputPath, data, 0o644); err != nil {
		exitError("write report: %v", err)
	}
	fmt.Printf("stress complete: transfer=%d stake=%d delegate=%d withdraw=%d failed=%d\n",
		report.TransferSucceeded,
		report.StakeSucceeded,
		report.DelegateSucceeded,
		report.WithdrawSucceeded,
		report.Failed,
	)
}

func runFixedTransfers(plan manifest, timeout time.Duration, workers int, transactionCount int, startIndex uint64) (stressReport, error) {
	if transactionCount < 1 {
		return stressReport{}, fmt.Errorf("transaction count must be positive")
	}
	if timeout <= 0 {
		return stressReport{}, fmt.Errorf("timeout must be positive")
	}
	if ^uint64(0)-startIndex < uint64(transactionCount) {
		return stressReport{}, fmt.Errorf("transaction index range overflows")
	}
	started := time.Now()
	contextValue, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	stats := &operationStats{}
	var nextIndex atomic.Uint64
	var workerGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		workerGroup.Add(1)
		go func(workerID int) {
			defer workerGroup.Done()
			random := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
			for {
				transactionIndex := int(nextIndex.Add(1)) - 1
				if transactionIndex >= transactionCount {
					return
				}
				err := submitFixedTransfer(contextValue, plan, random, startIndex+uint64(transactionIndex))
				recordResult(stats, err)
			}
		}(index)
	}
	workerGroup.Wait()
	finished := time.Now()
	transferCount := operationCount{
		Submitted:    stats.Submitted.Load(),
		Succeeded:    stats.Succeeded.Load(),
		Failed:       stats.Failed.Load(),
		Errors:       stats.copyErrors(),
		ErrorSamples: stats.copyErrorSamples(),
	}
	return stressReport{
		StartedAt:         started.Format(time.RFC3339Nano),
		FinishedAt:        finished.Format(time.RFC3339Nano),
		DurationSeconds:   finished.Sub(started).Seconds(),
		Workers:           workers,
		TransferSucceeded: transferCount.Succeeded,
		Failed:            transferCount.Failed,
		ByOperation: map[string]operationCount{
			"transfer": transferCount,
		},
	}, nil
}

func runStress(plan manifest, duration time.Duration, workers int) (stressReport, error) {
	started := time.Now()
	contextValue, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	stats := map[string]*operationStats{
		"transfer": {},
		"stake":    {},
		"delegate": {},
		"withdraw": {},
	}
	state := &stressState{}
	var workerGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		workerGroup.Add(1)
		go func(workerID int) {
			defer workerGroup.Done()
			random := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
			for contextValue.Err() == nil {
				operation := workerID % 4
				switch operation {
				case 0:
					submitTransfer(contextValue, plan, random, stats["transfer"])
				case 1:
					submitStakeOrUnstake(contextValue, plan, random, state, stats["stake"], stats["withdraw"])
				case 2:
					submitDelegate(contextValue, plan, random, state, stats["delegate"])
				default:
					submitDelegationWithdraw(contextValue, plan, random, state, stats["withdraw"])
				}
				time.Sleep(120 * time.Millisecond)
			}
		}(index)
	}
	workerGroup.Wait()
	finished := time.Now()
	byOperation := make(map[string]operationCount, len(stats))
	var failed uint64
	for name, item := range stats {
		count := operationCount{
			Submitted:    item.Submitted.Load(),
			Succeeded:    item.Succeeded.Load(),
			Failed:       item.Failed.Load(),
			Errors:       item.copyErrors(),
			ErrorSamples: item.copyErrorSamples(),
		}
		byOperation[name] = count
		failed += count.Failed
	}
	return stressReport{
		StartedAt:         started.Format(time.RFC3339Nano),
		FinishedAt:        finished.Format(time.RFC3339Nano),
		DurationSeconds:   finished.Sub(started).Seconds(),
		Workers:           workers,
		TransferSucceeded: byOperation["transfer"].Succeeded,
		StakeSucceeded:    byOperation["stake"].Succeeded,
		DelegateSucceeded: byOperation["delegate"].Succeeded,
		WithdrawSucceeded: byOperation["withdraw"].Succeeded,
		Failed:            failed,
		ByOperation:       byOperation,
	}, nil
}

func submitTransfer(ctx context.Context, plan manifest, random *rand.Rand, stats *operationStats) {
	sourceSeed := plan.UserSeeds[random.Intn(len(plan.UserSeeds))]
	destinationSeed := plan.UserSeeds[random.Intn(len(plan.UserSeeds))]
	if sourceSeed == destinationSeed {
		return
	}
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		recordFailure(stats)
		return
	}
	destination, err := keyPairFromSeed(destinationSeed)
	if err != nil {
		recordFailure(stats)
		return
	}
	err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
		return blockchain.NewTransferTransaction(source, destination.PublicKey, 1_000, blockhash)
	})
	recordResult(stats, err)
}

func submitFixedTransfer(ctx context.Context, plan manifest, random *rand.Rand, transactionIndex uint64) error {
	sourceSeed, destinationSeed, lamports, err := fixedTransferParams(plan.UserSeeds, transactionIndex)
	if err != nil {
		return err
	}
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return err
	}
	destination, err := keyPairFromSeed(destinationSeed)
	if err != nil {
		return err
	}
	return submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
		return blockchain.NewTransferTransaction(source, destination.PublicKey, lamports, blockhash)
	})
}

func fixedTransferParams(userSeeds []string, transactionIndex uint64) (string, string, uint64, error) {
	if len(userSeeds) < 2 {
		return "", "", 0, fmt.Errorf("fixed transfer requires at least two user seeds")
	}
	seedCount := uint64(len(userSeeds))
	sourceIndex := transactionIndex % seedCount
	destinationOffset := transactionIndex/seedCount%(seedCount-1) + 1
	destinationIndex := (sourceIndex + destinationOffset) % seedCount
	lamports := uint64(1_000) + transactionIndex%1_000
	return userSeeds[sourceIndex], userSeeds[destinationIndex], lamports, nil
}

func submitStakeOrUnstake(ctx context.Context, plan manifest, random *rand.Rand, state *stressState, stakeStats *operationStats, withdrawStats *operationStats) {
	currentEpoch, _ := getCurrentEpoch(ctx, randomRPC(plan, random))
	if currentEpoch > 0 {
		position, ok := state.popWithdrawableStake(currentEpoch)
		if ok {
			staker, err := keyPairFromSeed(position.Seed)
			if err != nil {
				recordFailure(withdrawStats)
				return
			}
			validatorAddress, err := structure.PublicKeyFromBase58(position.ValidatorAddress)
			if err != nil {
				recordFailure(withdrawStats)
				return
			}
			err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
				return blockchain.NewWithdrawUnstakedTransaction(staker, validatorAddress, currentEpoch, blockhash)
			})
			recordResult(withdrawStats, err)
			return
		}
		position, ok = state.popMatureStake(currentEpoch)
		if ok {
			staker, err := keyPairFromSeed(position.Seed)
			if err != nil {
				recordFailure(withdrawStats)
				return
			}
			validatorAddress, err := structure.PublicKeyFromBase58(position.ValidatorAddress)
			if err != nil {
				recordFailure(withdrawStats)
				return
			}
			unlockEpoch := currentEpoch + 1
			err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
				return blockchain.NewUnstakeTransaction(staker, validatorAddress, position.Amount, unlockEpoch, blockhash)
			})
			if err == nil {
				position.UnlockEpoch = unlockEpoch
				position.Withdrawable = true
				state.pushStakePosition(position)
			}
			recordResult(withdrawStats, err)
			return
		}
	}
	validator := plan.Validators[random.Intn(len(plan.Validators))]
	staker, err := keyPairFromSeed(validator.StakerSeed)
	if err != nil {
		recordFailure(stakeStats)
		return
	}
	validatorAddress, err := structure.PublicKeyFromBase58(validator.ValidatorAddress)
	if err != nil {
		recordFailure(stakeStats)
		return
	}
	amount := stake.MinimumStakeLamports
	err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
		return blockchain.NewStakeTransaction(staker, validatorAddress, amount, blockhash)
	})
	if err == nil {
		state.pushStakePosition(stakePosition{
			Seed:             validator.StakerSeed,
			ValidatorAddress: validator.ValidatorAddress,
			Amount:           amount,
			CreatedEpoch:     currentEpoch,
		})
	}
	recordResult(stakeStats, err)
}

func submitDelegate(ctx context.Context, plan manifest, random *rand.Rand, state *stressState, stats *operationStats) {
	currentEpoch, _ := getCurrentEpoch(ctx, randomRPC(plan, random))
	userSeed := plan.UserSeeds[random.Intn(len(plan.UserSeeds))]
	validator := plan.Validators[random.Intn(len(plan.Validators))]
	delegator, err := keyPairFromSeed(userSeed)
	if err != nil {
		recordFailure(stats)
		return
	}
	validatorAddress, err := structure.PublicKeyFromBase58(validator.ValidatorAddress)
	if err != nil {
		recordFailure(stats)
		return
	}
	amount := stake.MinimumStakeLamports
	err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
		return blockchain.NewDelegateStakeTransaction(delegator, validatorAddress, amount, blockhash)
	})
	if err == nil {
		state.pushDelegatePosition(stakePosition{
			Seed:             userSeed,
			ValidatorAddress: validator.ValidatorAddress,
			Amount:           amount,
			CreatedEpoch:     currentEpoch,
		})
	}
	recordResult(stats, err)
}

func submitDelegationWithdraw(ctx context.Context, plan manifest, random *rand.Rand, state *stressState, stats *operationStats) {
	currentEpoch, err := getCurrentEpoch(ctx, randomRPC(plan, random))
	if err != nil {
		recordFailure(stats)
		return
	}
	position, ok := state.popWithdrawableDelegation(currentEpoch)
	if ok {
		delegator, err := keyPairFromSeed(position.Seed)
		if err != nil {
			recordFailure(stats)
			return
		}
		validatorAddress, err := structure.PublicKeyFromBase58(position.ValidatorAddress)
		if err != nil {
			recordFailure(stats)
			return
		}
		err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
			return blockchain.NewWithdrawDelegationTransaction(delegator, validatorAddress, currentEpoch, blockhash)
		})
		recordResult(stats, err)
		return
	}
	position, ok = state.popMatureDelegation(currentEpoch)
	if !ok {
		time.Sleep(250 * time.Millisecond)
		return
	}
	delegator, err := keyPairFromSeed(position.Seed)
	if err != nil {
		recordFailure(stats)
		return
	}
	validatorAddress, err := structure.PublicKeyFromBase58(position.ValidatorAddress)
	if err != nil {
		recordFailure(stats)
		return
	}
	unlockEpoch := currentEpoch + 1
	err = submitBuiltTransaction(ctx, plan, random, func(blockhash structure.Hash) (structure.Transaction, error) {
		return blockchain.NewUndelegateStakeTransaction(delegator, validatorAddress, position.Amount, unlockEpoch, blockhash)
	})
	if err == nil {
		position.UnlockEpoch = unlockEpoch
		position.Withdrawable = true
		state.pushDelegatePosition(position)
	}
	recordResult(stats, err)
}

func submitBuiltTransaction(ctx context.Context, plan manifest, random *rand.Rand, build func(structure.Hash) (structure.Transaction, error)) error {
	rpcURL := randomRPC(plan, random)
	latest, err := getLatestBlockhash(ctx, rpcURL)
	if err != nil {
		return err
	}
	blockhash, err := structure.HashFromBase58(latest.Blockhash)
	if err != nil {
		return err
	}
	transaction, err := build(blockhash)
	if err != nil {
		return err
	}
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return err
	}
	var signature string
	return callRPC(ctx, rpcURL, "sendTransaction", []any{base64.StdEncoding.EncodeToString(encoded)}, &signature)
}

func (state *stressState) pushStakePosition(position stakePosition) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.stakePositions = append(state.stakePositions, position)
}

func (state *stressState) pushDelegatePosition(position stakePosition) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.delegatePositions = append(state.delegatePositions, position)
}

func (state *stressState) popMatureStake(currentEpoch uint64) (stakePosition, bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	for index, position := range state.stakePositions {
		if position.Withdrawable || currentEpoch <= position.CreatedEpoch {
			continue
		}
		state.stakePositions = append(state.stakePositions[:index], state.stakePositions[index+1:]...)
		return position, true
	}
	return stakePosition{}, false
}

func (state *stressState) popWithdrawableStake(currentEpoch uint64) (stakePosition, bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	for index, position := range state.stakePositions {
		if !position.Withdrawable || currentEpoch < position.UnlockEpoch {
			continue
		}
		state.stakePositions = append(state.stakePositions[:index], state.stakePositions[index+1:]...)
		return position, true
	}
	return stakePosition{}, false
}

func (state *stressState) popMatureDelegation(currentEpoch uint64) (stakePosition, bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	for index, position := range state.delegatePositions {
		if position.Withdrawable || currentEpoch <= position.CreatedEpoch {
			continue
		}
		state.delegatePositions = append(state.delegatePositions[:index], state.delegatePositions[index+1:]...)
		return position, true
	}
	return stakePosition{}, false
}

func (state *stressState) popWithdrawableDelegation(currentEpoch uint64) (stakePosition, bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	for index, position := range state.delegatePositions {
		if !position.Withdrawable || currentEpoch < position.UnlockEpoch {
			continue
		}
		state.delegatePositions = append(state.delegatePositions[:index], state.delegatePositions[index+1:]...)
		return position, true
	}
	return stakePosition{}, false
}

func getLatestBlockhash(ctx context.Context, rpcURL string) (latestBlockhashResult, error) {
	var result latestBlockhashResult
	err := callRPC(ctx, rpcURL, "getLatestBlockhash", []any{}, &result)
	return result, err
}

func getCurrentEpoch(ctx context.Context, rpcURL string) (uint64, error) {
	var result consensusStatusResult
	if err := callRPC(ctx, rpcURL, "getConsensusStatus", []any{}, &result); err != nil {
		return 0, err
	}
	return result.EpochID, nil
}

func callRPC(ctx context.Context, rpcURL string, method string, params []any, result any) error {
	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var envelope rpcResponse
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return fmt.Errorf("rpc %s: %s", method, envelope.Error.detail())
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, result)
}

func recordResult(stats *operationStats, err error) {
	stats.Submitted.Add(1)
	if err != nil {
		stats.Failed.Add(1)
		stats.recordError(bucketStressError(err), err.Error())
		return
	}
	stats.Succeeded.Add(1)
}

func recordFailure(stats *operationStats) {
	stats.Submitted.Add(1)
	stats.Failed.Add(1)
	stats.recordError("local_validation", "local validation failed")
}

func (stats *operationStats) recordError(bucket string, sample string) {
	if bucket == "" {
		bucket = "unknown"
	}
	stats.mutex.Lock()
	defer stats.mutex.Unlock()
	if stats.Errors == nil {
		stats.Errors = make(map[string]uint64, 1)
	}
	if stats.ErrorSamples == nil {
		stats.ErrorSamples = make(map[string]string, 1)
	}
	stats.Errors[bucket]++
	if _, exists := stats.ErrorSamples[bucket]; !exists {
		stats.ErrorSamples[bucket] = strings.TrimSpace(sample)
	}
}

func (stats *operationStats) copyErrors() map[string]uint64 {
	stats.mutex.Lock()
	defer stats.mutex.Unlock()
	if len(stats.Errors) == 0 {
		return nil
	}
	result := make(map[string]uint64, len(stats.Errors))
	for bucket, count := range stats.Errors {
		result[bucket] = count
	}
	return result
}

func (stats *operationStats) copyErrorSamples() map[string]string {
	stats.mutex.Lock()
	defer stats.mutex.Unlock()
	if len(stats.ErrorSamples) == 0 {
		return nil
	}
	result := make(map[string]string, len(stats.ErrorSamples))
	for bucket, sample := range stats.ErrorSamples {
		result[bucket] = sample
	}
	return result
}

func bucketStressError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	errorText := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errorText, "recent blockhash"):
		return "recent_blockhash_invalid"
	case strings.Contains(errorText, "mempool"):
		return "mempool_rejected"
	case strings.Contains(errorText, "insufficient") || strings.Contains(errorText, "balance"):
		return "insufficient_balance"
	case strings.Contains(errorText, "stake") || strings.Contains(errorText, "delegate") || strings.Contains(errorText, "delegation"):
		return "stake_state_invalid"
	case strings.Contains(errorText, "http") || strings.Contains(errorText, "rpc") || strings.Contains(errorText, "post "):
		return "rpc_or_transport"
	default:
		return "other"
	}
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

func randomRPC(plan manifest, random *rand.Rand) string {
	return plan.RPCURLs[random.Intn(len(plan.RPCURLs))]
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
	value.RPCURLs = filterValidatorRPCs(value)
	return value, nil
}

func filterValidatorRPCs(value manifest) []string {
	urls := make([]string, 0, len(value.Validators))
	for _, validator := range value.Validators {
		if strings.TrimSpace(validator.RPCURL) == "" {
			continue
		}
		urls = append(urls, validator.RPCURL)
	}
	if len(urls) == 0 {
		return value.RPCURLs
	}
	return urls
}

func applyRPCURLOverride(value manifest, rpcURL string) (manifest, error) {
	rpcURL = strings.TrimSpace(rpcURL)
	if rpcURL == "" {
		return value, nil
	}
	if !strings.HasPrefix(rpcURL, "http://") && !strings.HasPrefix(rpcURL, "https://") {
		return manifest{}, fmt.Errorf("rpc url must start with http:// or https://")
	}
	value.RPCURLs = []string{rpcURL}
	return value, nil
}

func keyPairFromSeed(seed string) (structure.SolanaKeyPair, error) {
	return structure.KeyPairFromSeed(utils.SHA256([]byte(strings.TrimSpace(seed))))
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
