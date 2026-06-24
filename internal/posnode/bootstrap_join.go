package posnode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"solana_golang/rpc"
	"solana_golang/utils"
)

const (
	bootstrapRPCVersion          = "2.0"
	maxBootstrapRPCResponseBytes = 8 << 20
	bootstrapRPCCallTimeout      = 5 * time.Second
)

type bootstrapRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type bootstrapRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpc.Error      `json:"error,omitempty"`
}

func prepareBootstrapJoinConfig(ctx context.Context, config nodeConfig, logger *slog.Logger) (nodeConfig, error) {
	if !config.BootstrapJoin.Enabled {
		return config, nil
	}
	registration, err := buildBootstrapRegistration(config)
	if err != nil {
		return nodeConfig{}, err
	}
	deadline := time.Time{}
	if config.BootstrapJoin.TimeoutMillis > 0 {
		deadline = time.Now().Add(time.Duration(config.BootstrapJoin.TimeoutMillis) * time.Millisecond)
	}
	pollInterval := time.Duration(config.BootstrapJoin.PollIntervalMillis) * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nodeConfig{}, err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nodeConfig{}, fmt.Errorf("posnode: bootstrap join timed out")
		}
		registerResult, registerErr := callBootstrapRPC[rpc.BootstrapRegisterValidatorResult](ctx, config.BootstrapJoin.RPCURL, rpc.MethodBootstrapRegisterValidator, []any{registration})
		if registerErr != nil {
			logger.Warn("bootstrap validator register failed", slog.Any("error", registerErr))
			if err := sleepBootstrapJoin(ctx, pollInterval); err != nil {
				return nodeConfig{}, err
			}
			continue
		}
		logger.Info("bootstrap validator registration accepted",
			slog.Bool("ready", registerResult.Ready),
			slog.Int("validator_count", registerResult.ValidatorCount),
			slog.Int("min_validators", registerResult.MinValidators),
		)
		manifest, manifestErr := callBootstrapRPC[rpc.BootstrapManifestResult](ctx, config.BootstrapJoin.RPCURL, rpc.MethodGetBootstrapManifest, []any{})
		if manifestErr != nil {
			logger.Warn("bootstrap manifest fetch failed", slog.Any("error", manifestErr))
			if err := sleepBootstrapJoin(ctx, pollInterval); err != nil {
				return nodeConfig{}, err
			}
			continue
		}
		if !manifest.Ready {
			logger.Info("bootstrap manifest pending",
				slog.Int("validator_count", manifest.ValidatorCount),
				slog.Int("min_validators", manifest.MinValidators),
			)
			if err := sleepBootstrapJoin(ctx, pollInterval); err != nil {
				return nodeConfig{}, err
			}
			continue
		}
		if err := validateBootstrapManifestForLocalNode(manifest, registration.PeerID); err != nil {
			return nodeConfig{}, err
		}
		joinedConfig, err := applyBootstrapManifest(config, manifest)
		if err != nil {
			return nodeConfig{}, err
		}
		logger.Info("bootstrap manifest applied",
			slog.String("chain_id", joinedConfig.ChainID),
			slog.String("chain_identity_hash", joinedConfig.ChainIdentityHash),
			slog.String("genesis_hash", joinedConfig.GenesisHash),
			slog.Int64("genesis_start_unix_millis", joinedConfig.GenesisStartMs),
			slog.Int("validator_count", len(joinedConfig.Genesis.InitialValidators)),
		)
		return joinedConfig, nil
	}
}

func buildBootstrapRegistration(config nodeConfig) (rpc.BootstrapValidatorRegistrationRequest, error) {
	peerKeyPair, err := loadRawKeyPair(config.PeerSeed, config.PeerKeyPath, config, "peer")
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, err
	}
	localKeys, err := loadLocalValidatorKeyPairs(config)
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, err
	}
	advertisedIP := strings.TrimSpace(config.AdvertisedIP)
	if advertisedIP == "" {
		advertisedIP = strings.TrimSpace(config.ListenIP)
	}
	advertisedPort := config.AdvertisedPort
	if advertisedPort == 0 {
		advertisedPort = config.ListenPort
	}
	registeredAtUnixMilli := config.BootstrapJoin.RegisteredAtUnixMilli
	if registeredAtUnixMilli == 0 {
		registeredAtUnixMilli = time.Now().UnixMilli()
	}
	// 功能目的：省略发现模式链 ID；实现原因：由引导节点返回权威链身份，避免用户预配置错误链。
	chainID := strings.TrimSpace(config.ChainID)
	if config.BootstrapJoin.Enabled && !config.ChainIDExplicit {
		chainID = ""
	}
	request := rpc.BootstrapValidatorRegistrationRequest{
		ChainID:               chainID,
		NodeName:              config.NodeName,
		PeerID:                peerKeyPair.peerID,
		AdvertisedIP:          advertisedIP,
		AdvertisedPort:        advertisedPort,
		Network:               string(utils.ProtocolTCP),
		StakerAddress:         localKeys.stakerAddress.String(),
		ValidatorAddress:      localKeys.validator.PublicKey.String(),
		ConsensusPublicKey:    localKeys.consensus.PublicKey.String(),
		BLSPublicKeyBase64:    utils.Base64Encode(localKeys.bls.PublicKey),
		StakeLamports:         config.StakeLamports,
		RegisteredAtUnixMilli: registeredAtUnixMilli,
	}
	stakerSignature := strings.TrimSpace(config.BootstrapJoin.StakerSignature)
	if stakerSignature == "" {
		if len(localKeys.staker.PrivateKey) == 0 {
			return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap join requires staker_signature when staker private key is not local")
		}
		signature, err := localKeys.staker.Sign(bootstrapRegistrationSignBytes(request))
		if err != nil {
			return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: sign bootstrap staker authorization: %w", err)
		}
		stakerSignature = signature.String()
	}
	request.StakerSignature = stakerSignature
	signature, err := localKeys.consensus.Sign(bootstrapRegistrationSignBytes(request))
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: sign bootstrap registration: %w", err)
	}
	request.Signature = signature.String()
	return request, nil
}

func validateBootstrapManifestForLocalNode(manifest rpc.BootstrapManifestResult, localPeerID string) error {
	if strings.TrimSpace(manifest.ChainID) == "" {
		return fmt.Errorf("posnode: bootstrap manifest chain id is empty")
	}
	if strings.TrimSpace(manifest.ChainIdentityHash) == "" || strings.TrimSpace(manifest.GenesisHash) == "" {
		return fmt.Errorf("posnode: bootstrap manifest chain identity is empty")
	}
	if manifest.GenesisStartUnixMilli <= 0 {
		return fmt.Errorf("posnode: bootstrap manifest genesis start is invalid")
	}
	if manifest.ValidatorCount < manifest.MinValidators {
		return fmt.Errorf("posnode: bootstrap manifest validator count below threshold")
	}
	for _, validator := range manifest.Genesis.InitialValidators {
		if validator.PeerID == localPeerID {
			return nil
		}
	}
	return fmt.Errorf("posnode: bootstrap manifest does not include local validator")
}

func callBootstrapRPC[T any](ctx context.Context, rpcURL string, method string, params any) (T, error) {
	var zero T
	callContext, cancel := context.WithTimeout(ctx, bootstrapRPCCallTimeout)
	defer cancel()
	body, err := json.Marshal(bootstrapRPCRequest{
		JSONRPC: bootstrapRPCVersion,
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return zero, fmt.Errorf("posnode: encode bootstrap rpc request: %w", err)
	}
	request, err := http.NewRequestWithContext(callContext, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return zero, fmt.Errorf("posnode: create bootstrap rpc request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return zero, fmt.Errorf("posnode: call bootstrap rpc: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBootstrapRPCResponseBytes))
	if err != nil {
		return zero, fmt.Errorf("posnode: read bootstrap rpc response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return zero, fmt.Errorf("posnode: bootstrap rpc status %d: %s", response.StatusCode, string(responseBody))
	}
	decoded := bootstrapRPCResponse{}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return zero, fmt.Errorf("posnode: decode bootstrap rpc response: %w", err)
	}
	if decoded.Error != nil {
		return zero, fmt.Errorf("posnode: bootstrap rpc error %d %s: %v", decoded.Error.Code, decoded.Error.Message, decoded.Error.Data)
	}
	result := zero
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		return zero, fmt.Errorf("posnode: decode bootstrap rpc result: %w", err)
	}
	return result, nil
}

func sleepBootstrapJoin(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
