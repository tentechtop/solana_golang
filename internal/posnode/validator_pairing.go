package posnode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"solana_golang/consensus"
	"solana_golang/programs/stake"
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	validatorPairingPayloadPrefix = "posvalpair:"
	validatorPairingVersion       = 1
	validatorPairingTokenBytes    = 32
	validatorPairingModeRegister  = "validator_registration"
	validatorPairingModeBootstrap = "bootstrap_join"
)

type validatorPairingSession struct {
	mutex           sync.Mutex
	payload         validatorPairingPayload
	tokenHash       string
	state           string
	qrText          string
	qrTerminal      string
	validatorPath   string
	consensusPath   string
	blsPath         string
	completedResult rpc.ValidatorPairingCompleteResult
}

type validatorPairingPayload struct {
	Version            int    `json:"version"`
	Mode               string `json:"mode,omitempty"`
	RPCURL             string `json:"rpc_url"`
	BootstrapRPCURL    string `json:"bootstrap_rpc_url,omitempty"`
	ChainID            string `json:"chain_id"`
	ChainIdentityHash  string `json:"chain_identity_hash"`
	GenesisHash        string `json:"genesis_hash"`
	NodeName           string `json:"node_name"`
	NodePeerID         string `json:"node_peer_id"`
	AdvertisedIP       string `json:"advertised_ip,omitempty"`
	AdvertisedPort     int    `json:"advertised_port,omitempty"`
	Network            string `json:"network,omitempty"`
	ValidatorAddress   string `json:"validator_address"`
	ConsensusAddress   string `json:"consensus_address"`
	BLSPublicKey       string `json:"bls_public_key"`
	RegisteredAtUnixMS int64  `json:"registered_at_unix_millis,omitempty"`
	Token              string `json:"token"`
	ExpiresAtUnixMS    int64  `json:"expires_at_unix_millis"`
}

func (node *posNode) startValidatorPairing() error {
	if !node.config.validatorPairingEnabled() {
		return nil
	}
	if !node.config.bootstrapPairingPending() && (node.config.validatorEnabled() || node.config.consensusEnabled() || node.ledger == nil) {
		return nil
	}
	session, err := node.newValidatorPairingSession()
	if err != nil {
		return err
	}
	node.validatorPairing = session
	node.logger.Info("validator wallet pairing ready",
		slog.String("state", session.state),
		slog.String("rpc_url", session.payload.RPCURL),
		slog.String("node_peer_id", session.payload.NodePeerID),
		slog.String("validator_address", session.payload.ValidatorAddress),
		slog.String("consensus_address", session.payload.ConsensusAddress),
		slog.Int64("expires_at_unix_millis", session.payload.ExpiresAtUnixMS),
	)
	printValidatorPairingQR(session)
	return nil
}

func (node *posNode) newValidatorPairingSession() (*validatorPairingSession, error) {
	keystoreDir := validatorPairingKeystoreDir(node.config)
	if err := os.MkdirAll(keystoreDir, 0o700); err != nil {
		return nil, fmt.Errorf("posnode: create validator pairing keystore dir: %w", err)
	}
	validatorPath := filepath.Join(keystoreDir, "validator.json")
	consensusPath := filepath.Join(keystoreDir, "consensus.json")
	blsPath := filepath.Join(keystoreDir, "bls.json")
	validatorKey, err := loadOrCreatePairingEd25519Key(validatorPath)
	if err != nil {
		return nil, fmt.Errorf("posnode: validator pairing validator key: %w", err)
	}
	consensusKey, err := loadOrCreatePairingEd25519Key(consensusPath)
	if err != nil {
		return nil, fmt.Errorf("posnode: validator pairing consensus key: %w", err)
	}
	blsKey, err := loadOrCreatePairingBLSKey(blsPath)
	if err != nil {
		return nil, fmt.Errorf("posnode: validator pairing bls key: %w", err)
	}
	tokenBytes, err := randomBytes(validatorPairingTokenBytes)
	if err != nil {
		return nil, err
	}
	token := utils.Base64RawEncode(tokenBytes)
	expiresAt := time.Now().Add(time.Duration(node.config.ValidatorPairing.TokenTTLMillis) * time.Millisecond)
	mode := validatorPairingModeRegister
	bootstrapRPCURL := ""
	advertisedIP := ""
	advertisedPort := 0
	network := ""
	registeredAtUnixMS := int64(0)
	chainID := node.config.ChainID
	chainIdentityHash := node.config.ChainIdentityHash
	genesisHash := node.config.GenesisHash
	if node.config.bootstrapPairingPending() {
		mode = validatorPairingModeBootstrap
		if !node.config.ChainIDExplicit {
			chainID = ""
		}
		bootstrapRPCURL = strings.TrimSpace(node.config.BootstrapJoin.RPCURL)
		advertisedIP, advertisedPort = validatorPairingAdvertisedEndpoint(node.config)
		network = string(utils.ProtocolTCP)
		registeredAtUnixMS = time.Now().UnixMilli()
		chainIdentityHash = ""
		genesisHash = ""
	}
	payload := validatorPairingPayload{
		Version:            validatorPairingVersion,
		Mode:               mode,
		RPCURL:             validatorPairingRPCURL(node.config),
		BootstrapRPCURL:    bootstrapRPCURL,
		ChainID:            chainID,
		ChainIdentityHash:  chainIdentityHash,
		GenesisHash:        genesisHash,
		NodeName:           node.config.NodeName,
		NodePeerID:         node.peerKeyPair.peerID,
		AdvertisedIP:       advertisedIP,
		AdvertisedPort:     advertisedPort,
		Network:            network,
		ValidatorAddress:   validatorKey.PublicKey.String(),
		ConsensusAddress:   consensusKey.PublicKey.String(),
		BLSPublicKey:       utils.Base58Encode(blsKey.PublicKey),
		RegisteredAtUnixMS: registeredAtUnixMS,
		Token:              token,
		ExpiresAtUnixMS:    expiresAt.UnixMilli(),
	}
	qrText, err := encodeValidatorPairingPayload(payload)
	if err != nil {
		return nil, err
	}
	qrCode, err := qrcode.New(qrText, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("posnode: build validator pairing qr: %w", err)
	}
	return &validatorPairingSession{
		payload:       payload,
		tokenHash:     utils.Base64RawEncode(utils.SHA256([]byte(token))),
		state:         "pending",
		qrText:        qrText,
		qrTerminal:    qrCode.ToString(false),
		validatorPath: validatorPath,
		consensusPath: consensusPath,
		blsPath:       blsPath,
	}, nil
}

func validatorPairingKeystoreDir(config nodeConfig) string {
	if strings.TrimSpace(config.ValidatorPairing.KeystoreDir) != "" {
		return filepath.Clean(config.ValidatorPairing.KeystoreDir)
	}
	return filepath.Join(config.DataRootPath, "validator-pairing")
}

func validatorPairingRPCURL(config nodeConfig) string {
	host := strings.TrimSpace(config.AdvertisedIP)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = firstPrivateIPv4()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, fmt.Sprintf("%d", config.RPCPort)), Path: "/"}).String()
}

func validatorPairingAdvertisedEndpoint(config nodeConfig) (string, int) {
	host := strings.TrimSpace(config.AdvertisedIP)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = firstPrivateIPv4()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	port := config.AdvertisedPort
	if port == 0 {
		port = config.ListenPort
	}
	return host, port
}

func firstPrivateIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := item.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

func encodeValidatorPairingPayload(payload validatorPairingPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("posnode: encode validator pairing payload: %w", err)
	}
	return validatorPairingPayloadPrefix + utils.Base64RawEncode(data), nil
}

func printValidatorPairingQR(session *validatorPairingSession) {
	var output bytes.Buffer
	output.WriteString("\nPOSNODE 验证者钱包绑定\n")
	output.WriteString("请用同一局域网内的钱包扫码，钱包签名质押后会自动绑定为本节点验证者钱包。\n")
	output.WriteString(session.qrTerminal)
	output.WriteString("\n")
	output.WriteString("如果钱包暂不支持扫码，请复制下面的 payload 执行 wallet validator-pair。\n")
	output.WriteString(session.qrText)
	output.WriteString("\n\n")
	fmt.Fprint(os.Stdout, output.String())
}

func loadOrCreatePairingEd25519Key(path string) (structure.SolanaKeyPair, error) {
	if _, err := os.Stat(path); err == nil {
		return loadStructureKeyPair("", path, nodeConfig{}, "validator pairing ed25519")
	} else if !os.IsNotExist(err) {
		return structure.SolanaKeyPair{}, err
	}
	seed, err := randomBytes(structure.SolanaPrivateKeySeedSize)
	if err != nil {
		return structure.SolanaKeyPair{}, err
	}
	keyPair, err := structure.KeyPairFromSeed(seed)
	if err != nil {
		return structure.SolanaKeyPair{}, err
	}
	keyFile := nodeKeystoreFile{
		PrivateKeyBase64: utils.Base64Encode(seed),
		PublicKeyBase64:  utils.Base64Encode(keyPair.PublicKey[:]),
	}
	if err := writeNodeKeystoreFile(path, keyFile); err != nil {
		return structure.SolanaKeyPair{}, err
	}
	return keyPair, nil
}

func loadOrCreatePairingBLSKey(path string) (consensus.BLSKeyPair, error) {
	if _, err := os.Stat(path); err == nil {
		return loadBLSKeyPair("", path, nodeConfig{})
	} else if !os.IsNotExist(err) {
		return consensus.BLSKeyPair{}, err
	}
	seed, err := randomBytes(32)
	if err != nil {
		return consensus.BLSKeyPair{}, err
	}
	keyPair, err := consensus.BLSKeyPairFromSeed(seed)
	if err != nil {
		return consensus.BLSKeyPair{}, err
	}
	keyFile := nodeKeystoreFile{
		PrivateKeyBase64: utils.Base64Encode(keyPair.PrivateKey),
		PublicKeyBase64:  utils.Base64Encode(keyPair.PublicKey),
	}
	if err := writeNodeKeystoreFile(path, keyFile); err != nil {
		return consensus.BLSKeyPair{}, err
	}
	return keyPair, nil
}

func writeNodeKeystoreFile(path string, keyFile nodeKeystoreFile) error {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "" || cleanPath == "." {
		return fmt.Errorf("posnode: node keystore path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return fmt.Errorf("posnode: create node keystore dir: %w", err)
	}
	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("posnode: encode node keystore: %w", err)
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return fmt.Errorf("posnode: write node keystore: %w", err)
	}
	return nil
}

func randomBytes(size int) ([]byte, error) {
	if size <= 0 || size > 4096 {
		return nil, fmt.Errorf("posnode: invalid random size %d", size)
	}
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("posnode: read random bytes: %w", err)
	}
	return value, nil
}

func (node *posNode) GetValidatorPairing(ctx context.Context) (rpc.ValidatorPairingResult, error) {
	_ = ctx
	if node.validatorPairing == nil {
		return rpc.ValidatorPairingResult{Enabled: false, State: "disabled"}, nil
	}
	session := node.validatorPairing
	session.mutex.Lock()
	defer session.mutex.Unlock()
	state := session.state
	if state == "pending" && time.Now().UnixMilli() > session.payload.ExpiresAtUnixMS {
		state = "expired"
		session.state = state
	}
	return rpc.ValidatorPairingResult{
		Enabled:            true,
		State:              state,
		Mode:               payloadMode(session.payload),
		RPCURL:             session.payload.RPCURL,
		BootstrapRPCURL:    session.payload.BootstrapRPCURL,
		ChainID:            session.payload.ChainID,
		ChainIdentityHash:  session.payload.ChainIdentityHash,
		GenesisHash:        session.payload.GenesisHash,
		NodeName:           session.payload.NodeName,
		NodePeerID:         session.payload.NodePeerID,
		AdvertisedIP:       session.payload.AdvertisedIP,
		AdvertisedPort:     session.payload.AdvertisedPort,
		Network:            session.payload.Network,
		ValidatorAddress:   session.payload.ValidatorAddress,
		ConsensusAddress:   session.payload.ConsensusAddress,
		BLSPublicKey:       session.payload.BLSPublicKey,
		RegisteredAtUnixMS: session.payload.RegisteredAtUnixMS,
		ExpiresAtUnixMS:    session.payload.ExpiresAtUnixMS,
		Completed:          session.completedResult,
	}, nil
}

func (node *posNode) CompleteValidatorPairing(ctx context.Context, request rpc.ValidatorPairingCompleteRequest) (rpc.ValidatorPairingCompleteResult, error) {
	_ = ctx
	if node.validatorPairing == nil {
		return rpc.ValidatorPairingCompleteResult{}, fmt.Errorf("posnode: validator pairing is disabled")
	}
	session := node.validatorPairing
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.state == "completed" {
		return session.completedResult, nil
	}
	if time.Now().UnixMilli() > session.payload.ExpiresAtUnixMS {
		session.state = "expired"
		return rpc.ValidatorPairingCompleteResult{}, fmt.Errorf("posnode: validator pairing token expired")
	}
	if !validValidatorPairingToken(session, request.Token) {
		return rpc.ValidatorPairingCompleteResult{}, fmt.Errorf("posnode: validator pairing token is invalid")
	}
	stakerAddress, err := validateValidatorPairingCompleteRequest(session.payload, request)
	if err != nil {
		return rpc.ValidatorPairingCompleteResult{}, err
	}
	request.StakerAddress = stakerAddress.String()
	if isBootstrapPairingPayload(session.payload) {
		if err := validateBootstrapPairingStakerSignature(session.payload, request); err != nil {
			return rpc.ValidatorPairingCompleteResult{}, err
		}
	} else {
		if err := node.validateValidatorPairingRegistration(request); err != nil {
			return rpc.ValidatorPairingCompleteResult{}, err
		}
	}
	configUpdated := false
	if node.config.validatorPairingAutoWriteConfig() {
		if err := node.writeValidatorPairingConfig(request, session); err != nil {
			return rpc.ValidatorPairingCompleteResult{}, err
		}
		configUpdated = true
	}
	result := rpc.ValidatorPairingCompleteResult{
		State:                    "completed",
		StakerAddress:            strings.TrimSpace(request.StakerAddress),
		ValidatorAddress:         session.payload.ValidatorAddress,
		ConsensusAddress:         session.payload.ConsensusAddress,
		BLSPublicKey:             session.payload.BLSPublicKey,
		NodePeerID:               session.payload.NodePeerID,
		StakeLamports:            request.StakeLamports,
		Signature:                strings.TrimSpace(request.Signature),
		BootstrapStakerSignature: strings.TrimSpace(request.BootstrapStakerSignature),
		ConfigUpdated:            configUpdated,
		RestartRequired:          true,
		ConfigPath:               node.config.ConfigPath,
		ActivationNote:           "validator activates at the next epoch after the registration transaction is finalized",
	}
	if isBootstrapPairingPayload(session.payload) {
		result.ActivationNote = "validator joins bootstrap after restart; block production starts after the bootstrap threshold is reached"
	}
	session.state = "completed"
	session.completedResult = result
	node.logger.Info("validator wallet pairing completed",
		slog.String("staker", result.StakerAddress),
		slog.String("validator_address", result.ValidatorAddress),
		slog.String("signature", result.Signature),
		slog.Bool("config_updated", result.ConfigUpdated),
	)
	return result, nil
}

func validValidatorPairingToken(session *validatorPairingSession, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return utils.SecureEqual([]byte(session.tokenHash), []byte(utils.Base64RawEncode(utils.SHA256([]byte(token)))))
}

func validateValidatorPairingCompleteRequest(payload validatorPairingPayload, request rpc.ValidatorPairingCompleteRequest) (structure.PublicKey, error) {
	if strings.TrimSpace(request.StakerAddress) == "" {
		return structure.PublicKey{}, fmt.Errorf("posnode: staker address is empty")
	}
	stakerAddress, err := validatorPairingStakerAddress(strings.TrimSpace(request.StakerAddress))
	if err != nil {
		return structure.PublicKey{}, err
	}
	if strings.TrimSpace(request.ValidatorAddress) != payload.ValidatorAddress {
		return structure.PublicKey{}, fmt.Errorf("posnode: validator address mismatch")
	}
	if strings.TrimSpace(request.ConsensusAddress) != payload.ConsensusAddress {
		return structure.PublicKey{}, fmt.Errorf("posnode: consensus address mismatch")
	}
	if strings.TrimSpace(request.BLSPublicKey) != payload.BLSPublicKey {
		return structure.PublicKey{}, fmt.Errorf("posnode: bls public key mismatch")
	}
	if strings.TrimSpace(request.NodePeerID) != payload.NodePeerID {
		return structure.PublicKey{}, fmt.Errorf("posnode: node peer id mismatch")
	}
	if request.StakeLamports < stake.MinimumStakeLamports {
		return structure.PublicKey{}, fmt.Errorf("posnode: stake lamports below minimum: got %d want >= %d", request.StakeLamports, stake.MinimumStakeLamports)
	}
	if isBootstrapPairingPayload(payload) {
		if strings.TrimSpace(request.BootstrapStakerSignature) == "" {
			return structure.PublicKey{}, fmt.Errorf("posnode: bootstrap staker signature is empty")
		}
		return stakerAddress, nil
	}
	if strings.TrimSpace(request.Signature) == "" {
		return structure.PublicKey{}, fmt.Errorf("posnode: registration signature is empty")
	}
	if _, err := structure.SignatureFromBase58(strings.TrimSpace(request.Signature)); err != nil {
		return structure.PublicKey{}, fmt.Errorf("posnode: registration signature: %w", err)
	}
	return stakerAddress, nil
}

func payloadMode(payload validatorPairingPayload) string {
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		return validatorPairingModeRegister
	}
	return mode
}

func isBootstrapPairingPayload(payload validatorPairingPayload) bool {
	return payloadMode(payload) == validatorPairingModeBootstrap
}

func validateBootstrapPairingStakerSignature(payload validatorPairingPayload, request rpc.ValidatorPairingCompleteRequest) error {
	bootstrapRequest, err := bootstrapRegistrationFromPairing(payload, request)
	if err != nil {
		return err
	}
	stakerKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(bootstrapRequest.StakerAddress))
	if err != nil {
		return fmt.Errorf("posnode: bootstrap staker address: %w", err)
	}
	signature, err := structure.SignatureFromBase58(strings.TrimSpace(request.BootstrapStakerSignature))
	if err != nil {
		return fmt.Errorf("posnode: bootstrap staker signature: %w", err)
	}
	if !structure.VerifyMessageSignature(stakerKey, bootstrapRegistrationSignBytes(bootstrapRequest), signature) {
		return fmt.Errorf("posnode: bootstrap staker signature invalid")
	}
	return nil
}

func bootstrapRegistrationFromPairing(payload validatorPairingPayload, request rpc.ValidatorPairingCompleteRequest) (rpc.BootstrapValidatorRegistrationRequest, error) {
	blsPublicKey, err := utils.Base58Decode(strings.TrimSpace(payload.BLSPublicKey))
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: decode bootstrap pairing bls public key: %w", err)
	}
	network := strings.TrimSpace(payload.Network)
	if network == "" {
		network = string(utils.ProtocolTCP)
	}
	return rpc.BootstrapValidatorRegistrationRequest{
		ChainID:               strings.TrimSpace(payload.ChainID),
		NodeName:              strings.TrimSpace(payload.NodeName),
		PeerID:                strings.TrimSpace(payload.NodePeerID),
		AdvertisedIP:          strings.TrimSpace(payload.AdvertisedIP),
		AdvertisedPort:        payload.AdvertisedPort,
		Network:               network,
		StakerAddress:         strings.TrimSpace(request.StakerAddress),
		ValidatorAddress:      strings.TrimSpace(payload.ValidatorAddress),
		ConsensusPublicKey:    strings.TrimSpace(payload.ConsensusAddress),
		BLSPublicKeyBase64:    utils.Base64Encode(blsPublicKey),
		StakeLamports:         request.StakeLamports,
		RegisteredAtUnixMilli: payload.RegisteredAtUnixMS,
		StakerSignature:       strings.TrimSpace(request.BootstrapStakerSignature),
	}, nil
}

func validatorPairingStakerAddress(address string) (structure.PublicKey, error) {
	trimmedAddress := strings.TrimSpace(address)
	if strings.HasPrefix(trimmedAddress, "T") {
		key, err := structure.PublicKeyFromBase58(strings.TrimPrefix(trimmedAddress, "T"))
		if err != nil {
			return structure.PublicKey{}, fmt.Errorf("posnode: staker address: %w", err)
		}
		return key, nil
	}
	if strings.HasPrefix(trimmedAddress, "Z") {
		return structure.PublicKey{}, fmt.Errorf("posnode: staker address must be transparent")
	}
	key, err := decodeTransparentPublicKey(trimmedAddress, "staker address")
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("posnode: staker address: %w", err)
	}
	return key, nil
}

func (node *posNode) validateValidatorPairingRegistration(request rpc.ValidatorPairingCompleteRequest) error {
	transaction, location, err := node.findValidatorPairingRegistrationTransaction(strings.TrimSpace(request.Signature))
	if err != nil {
		return err
	}
	if err := validateValidatorPairingRegistrationTransaction(transaction, request); err != nil {
		return fmt.Errorf("posnode: validator pairing registration transaction %s: %w", location, err)
	}
	return nil
}

func (node *posNode) findValidatorPairingRegistrationTransaction(signature string) (structure.Transaction, string, error) {
	if transaction, exists := node.mempoolTransactionByID(signature); exists {
		return transaction, "mempool", nil
	}
	if node.ledger == nil {
		return structure.Transaction{}, "", fmt.Errorf("posnode: registration transaction %s not found", signature)
	}
	proposal, _, found, err := node.ledger.TransactionByID(signature)
	if err != nil {
		return structure.Transaction{}, "", fmt.Errorf("posnode: lookup registration transaction: %w", err)
	}
	if !found {
		return structure.Transaction{}, "", fmt.Errorf("posnode: registration transaction %s not found", signature)
	}
	for _, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		if transactionID == signature {
			return transaction, "committed", nil
		}
	}
	return structure.Transaction{}, "", fmt.Errorf("posnode: registration transaction index mismatch")
}

func validateValidatorPairingRegistrationTransaction(transaction structure.Transaction, request rpc.ValidatorPairingCompleteRequest) error {
	signatureValid, err := transaction.HasValidSignatures()
	if err != nil {
		return fmt.Errorf("verify signatures: %w", err)
	}
	if !signatureValid {
		return fmt.Errorf("signature verification failed")
	}
	message, err := transaction.SolanaMessage()
	if err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	if len(message.Instructions) != 1 {
		return fmt.Errorf("must contain exactly one instruction")
	}
	instruction := message.Instructions[0]
	if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) {
		return fmt.Errorf("program index out of range")
	}
	if message.AccountKeys[instruction.ProgramIDIndex] != structure.DefaultBuiltinProgramIDs.Stake {
		return fmt.Errorf("program is not stake")
	}
	if len(instruction.AccountIndexes) != 2 {
		return fmt.Errorf("stake account index count = %d, want 2", len(instruction.AccountIndexes))
	}
	stakerIndex := int(instruction.AccountIndexes[0])
	validatorIndex := int(instruction.AccountIndexes[1])
	if stakerIndex >= len(message.AccountKeys) || validatorIndex >= len(message.AccountKeys) {
		return fmt.Errorf("stake account index out of range")
	}
	if message.AccountKeys[stakerIndex].String() != strings.TrimSpace(request.StakerAddress) {
		return fmt.Errorf("staker account mismatch")
	}
	if message.AccountKeys[validatorIndex].String() != strings.TrimSpace(request.ValidatorAddress) {
		return fmt.Errorf("validator account mismatch")
	}
	accounts := message.StaticAccountMetas()
	if stakerIndex >= len(accounts) || !accounts[stakerIndex].IsSigner || !accounts[stakerIndex].IsWritable {
		return fmt.Errorf("staker account must be signer and writable")
	}
	if validatorIndex >= len(accounts) || !accounts[validatorIndex].IsWritable {
		return fmt.Errorf("validator account must be writable")
	}
	stakeInstruction, err := stake.UnmarshalInstructionBinary(instruction.Data)
	if err != nil {
		return fmt.Errorf("decode stake instruction: %w", err)
	}
	if stakeInstruction.Type != stake.InstructionRegisterValidator {
		return fmt.Errorf("stake instruction is not register validator")
	}
	if stakeInstruction.Amount != request.StakeLamports {
		return fmt.Errorf("stake amount mismatch")
	}
	if stakeInstruction.Amount < stake.MinimumStakeLamports {
		return fmt.Errorf("stake amount below minimum")
	}
	if stakeInstruction.ConsensusPublicKey.String() != strings.TrimSpace(request.ConsensusAddress) {
		return fmt.Errorf("consensus public key mismatch")
	}
	if stakeInstruction.P2PPeerID != strings.TrimSpace(request.NodePeerID) {
		return fmt.Errorf("node peer id mismatch")
	}
	if utils.Base58Encode(stakeInstruction.BLSPublicKey) != strings.TrimSpace(request.BLSPublicKey) {
		return fmt.Errorf("bls public key mismatch")
	}
	return nil
}

func (node *posNode) writeValidatorPairingConfig(request rpc.ValidatorPairingCompleteRequest, session *validatorPairingSession) error {
	configPath := strings.TrimSpace(node.config.ConfigPath)
	if configPath == "" {
		return fmt.Errorf("posnode: config path is empty")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("posnode: read config for validator pairing: %w", err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(data, &configMap); err != nil {
		return fmt.Errorf("posnode: decode config for validator pairing: %w", err)
	}
	configMap["node_role"] = "validator"
	configMap["node_roles"] = []string{"validator", "full"}
	configMap["node_capabilities"] = []string{"validator", "relay", "state_sync", "dht"}
	configMap["validator_enabled"] = true
	configMap["consensus_enabled"] = true
	configMap["staker_address"] = strings.TrimSpace(request.StakerAddress)
	configMap["validator_key_path"] = filepath.Clean(session.validatorPath)
	configMap["consensus_key_path"] = filepath.Clean(session.consensusPath)
	configMap["bls_key_path"] = filepath.Clean(session.blsPath)
	configMap["stake_lamports"] = request.StakeLamports
	configMap["auto_register"] = false
	configMap["validator_pairing"] = map[string]any{"enabled": false}
	if isBootstrapPairingPayload(session.payload) {
		configMap["advertised_ip"] = strings.TrimSpace(session.payload.AdvertisedIP)
		configMap["advertised_port"] = session.payload.AdvertisedPort
		configMap["network"] = strings.TrimSpace(session.payload.Network)
		configMap["bootstrap_join"] = map[string]any{
			"enabled":                  true,
			"rpc_url":                  strings.TrimSpace(session.payload.BootstrapRPCURL),
			"poll_interval_millis":     node.config.BootstrapJoin.PollIntervalMillis,
			"timeout_millis":           node.config.BootstrapJoin.TimeoutMillis,
			"registered_at_unix_milli": session.payload.RegisteredAtUnixMS,
			"staker_signature":         strings.TrimSpace(request.BootstrapStakerSignature),
		}
	}
	delete(configMap, "staker_seed")
	delete(configMap, "staker_key_path")
	delete(configMap, "validator_seed")
	delete(configMap, "consensus_seed")
	encoded, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return fmt.Errorf("posnode: encode paired validator config: %w", err)
	}
	tempPath := configPath + ".validator-pairing.tmp"
	if err := os.WriteFile(tempPath, encoded, 0o600); err != nil {
		return fmt.Errorf("posnode: write paired validator config: %w", err)
	}
	if err := os.Rename(tempPath, configPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("posnode: replace paired validator config: %w", err)
	}
	return nil
}
