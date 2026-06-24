package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	defaultRPCURL                 = "http://127.0.0.1:8899"
	defaultHTTPTimeout            = 10 * time.Second
	maxRPCBodyBytes               = 1 << 20
	minimumStakeLamports          = 10_000_000
	validatorPairingPayloadPrefix = "posvalpair:"
)

type keystoreFile struct {
	PrivateKeyBase64 string `json:"private_key_base64,omitempty"`
	SecretKeyBase64  string `json:"secret_key_base64,omitempty"`
	PublicKeyBase64  string `json:"public_key_base64,omitempty"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type latestBlockhashResult struct {
	Blockhash string `json:"blockhash"`
	Slot      uint64 `json:"slot"`
}

type balanceResult struct {
	Value uint64 `json:"value"`
}

type transactionSubmitResult struct {
	Signature string `json:"signature"`
}

func (result *transactionSubmitResult) UnmarshalJSON(data []byte) error {
	var signature string
	if err := json.Unmarshal(data, &signature); err == nil {
		result.Signature = signature
		return nil
	}
	type transactionSubmitResultAlias transactionSubmitResult
	var decoded transactionSubmitResultAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*result = transactionSubmitResult(decoded)
	return nil
}

type validatorPairingPayload struct {
	Version           int    `json:"version"`
	RPCURL            string `json:"rpc_url"`
	ChainID           string `json:"chain_id"`
	ChainIdentityHash string `json:"chain_identity_hash"`
	GenesisHash       string `json:"genesis_hash"`
	NodeName          string `json:"node_name"`
	NodePeerID        string `json:"node_peer_id"`
	ValidatorAddress  string `json:"validator_address"`
	ConsensusAddress  string `json:"consensus_address"`
	BLSPublicKey      string `json:"bls_public_key"`
	Token             string `json:"token"`
	ExpiresAtUnixMS   int64  `json:"expires_at_unix_millis"`
}

type validatorPairingCompleteRequest struct {
	Token            string `json:"token"`
	StakerAddress    string `json:"staker_address"`
	ValidatorAddress string `json:"validator_address"`
	ConsensusAddress string `json:"consensus_address"`
	BLSPublicKey     string `json:"bls_public_key"`
	NodePeerID       string `json:"node_peer_id"`
	StakeLamports    uint64 `json:"stake_lamports"`
	Signature        string `json:"signature"`
}

type validatorPairingCompleteResult struct {
	State            string `json:"state,omitempty"`
	StakerAddress    string `json:"staker_address,omitempty"`
	ValidatorAddress string `json:"validator_address,omitempty"`
	ConsensusAddress string `json:"consensus_address,omitempty"`
	BLSPublicKey     string `json:"bls_public_key,omitempty"`
	NodePeerID       string `json:"node_peer_id,omitempty"`
	StakeLamports    uint64 `json:"stake_lamports,omitempty"`
	Signature        string `json:"signature,omitempty"`
	ConfigUpdated    bool   `json:"config_updated"`
	RestartRequired  bool   `json:"restart_required"`
	ConfigPath       string `json:"config_path,omitempty"`
	ActivationNote   string `json:"activation_note,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "wallet: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return fmt.Errorf("command is required")
	}
	command := strings.TrimSpace(args[0])
	switch command {
	case "new-key":
		return runNewKey(args[1:])
	case "validator-keygen":
		return runValidatorKeygen(args[1:])
	case "address":
		return runAddress(args[1:])
	case "balance":
		return runBalance(args[1:])
	case "transfer":
		return runTransfer(args[1:])
	case "deploy-contract":
		return runDeployContract(args[1:])
	case "validator-register":
		return runValidatorRegister(args[1:])
	case "validator-pair":
		return runValidatorPair(args[1:])
	case "stake":
		return runStake(args[1:])
	case "delegate":
		return runDelegate(args[1:])
	case "undelegate":
		return runUndelegate(args[1:])
	case "withdraw-delegation":
		return runWithdrawDelegation(args[1:])
	default:
		printUsage()
		return fmt.Errorf("unsupported command %q", command)
	}
}

func runNewKey(args []string) error {
	flags := flag.NewFlagSet("new-key", flag.ContinueOnError)
	keyType := flags.String("type", "ed25519", "ed25519 or bls")
	outPath := flags.String("out", "", "keystore output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) == "" {
		return fmt.Errorf("new-key requires -out")
	}
	switch strings.ToLower(strings.TrimSpace(*keyType)) {
	case "ed25519":
		return writeEd25519Keystore(*outPath)
	case "bls":
		return writeBLSKeystore(*outPath)
	default:
		return fmt.Errorf("unsupported key type %q", *keyType)
	}
}

func runValidatorKeygen(args []string) error {
	flags := flag.NewFlagSet("validator-keygen", flag.ContinueOnError)
	outDir := flags.String("out-dir", "", "validator key output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*outDir) == "" {
		return fmt.Errorf("validator-keygen requires -out-dir")
	}
	names := []string{"peer", "staker", "validator", "consensus"}
	for _, name := range names {
		if err := writeEd25519Keystore(filepath.Join(*outDir, name+".json")); err != nil {
			return err
		}
	}
	if err := writeBLSKeystore(filepath.Join(*outDir, "bls.json")); err != nil {
		return err
	}
	return printValidatorKeySummary(*outDir)
}

func runAddress(args []string) error {
	flags := flag.NewFlagSet("address", flag.ContinueOnError)
	keyPath := flags.String("key", "", "ed25519 keystore path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	keyPair, err := loadEd25519KeyPair(*keyPath)
	if err != nil {
		return err
	}
	fmt.Println(keyPair.PublicKey.String())
	return nil
}

func runBalance(args []string) error {
	flags := flag.NewFlagSet("balance", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	address := flags.String("address", "", "account address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*address) == "" {
		return fmt.Errorf("balance requires -address")
	}
	var result balanceResult
	if err := rpcCall(*rpcURL, "getBalance", []any{strings.TrimSpace(*address)}, &result); err != nil {
		return err
	}
	fmt.Println(result.Value)
	return nil
}

func runTransfer(args []string) error {
	flags := flag.NewFlagSet("transfer", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	keyPath := flags.String("key", "", "source keystore path")
	destinationText := flags.String("to", "", "destination address")
	lamports := flags.Uint64("lamports", 0, "transfer lamports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	source, err := loadEd25519KeyPair(*keyPath)
	if err != nil {
		return err
	}
	destination, err := structure.PublicKeyFromBase58(strings.TrimSpace(*destinationText))
	if err != nil {
		return fmt.Errorf("decode destination address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewTransferTransaction(source, destination, *lamports, blockhash)
	if err != nil {
		return fmt.Errorf("build transfer transaction: %w", err)
	}
	return submitAndPrint(*rpcURL, transaction)
}

func runDeployContract(args []string) error {
	flags := flag.NewFlagSet("deploy-contract", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	payerKeyPath := flags.String("payer-key", "", "payer ed25519 keystore path")
	programKeyPath := flags.String("program-key", "", "program ed25519 keystore path")
	bytecodePath := flags.String("bytecode", "", "compiled .svmbin bytecode path")
	depositLamports := flags.Uint64("deposit-lamports", 0, "extra deployment deposit lamports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	payer, err := loadEd25519KeyPair(*payerKeyPath)
	if err != nil {
		return fmt.Errorf("load payer key: %w", err)
	}
	program, err := loadEd25519KeyPair(*programKeyPath)
	if err != nil {
		return fmt.Errorf("load program key: %w", err)
	}
	programData, err := readBytecodeFile(*bytecodePath)
	if err != nil {
		return err
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewDeployContractTransaction(payer, program, programData, *depositLamports, blockhash)
	if err != nil {
		return fmt.Errorf("build deploy contract transaction: %w", err)
	}
	signature, err := submitTransaction(*rpcURL, transaction)
	if err != nil {
		return err
	}
	result := map[string]string{
		"signature": signature,
		"program":   program.PublicKey.String(),
		"bytecode":  filepath.Clean(strings.TrimSpace(*bytecodePath)),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode deploy result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

func runValidatorRegister(args []string) error {
	flags := flag.NewFlagSet("validator-register", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	stakerKeyPath := flags.String("staker-key", "", "staker keystore path")
	validatorAddressText := flags.String("validator-address", "", "validator account address")
	consensusAddressText := flags.String("consensus-address", "", "consensus public key")
	blsPublicKeyText := flags.String("bls-public-key", "", "bls public key base58")
	peerID := flags.String("peer-id", "", "p2p peer id")
	lamports := flags.Uint64("lamports", 0, "initial stake lamports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	staker, err := loadEd25519KeyPair(*stakerKeyPath)
	if err != nil {
		return err
	}
	validatorAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*validatorAddressText))
	if err != nil {
		return fmt.Errorf("decode validator address: %w", err)
	}
	consensusAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*consensusAddressText))
	if err != nil {
		return fmt.Errorf("decode consensus address: %w", err)
	}
	blsPublicKey, err := utils.Base58Decode(strings.TrimSpace(*blsPublicKeyText))
	if err != nil {
		return fmt.Errorf("decode bls public key: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(staker, validatorAddress, consensusAddress, blsPublicKey, *peerID, *lamports, blockhash)
	if err != nil {
		return fmt.Errorf("build validator register transaction: %w", err)
	}
	return submitAndPrint(*rpcURL, transaction)
}

func runValidatorPair(args []string) error {
	flags := flag.NewFlagSet("validator-pair", flag.ContinueOnError)
	payloadText := flags.String("payload", "", "validator pairing qr payload")
	payloadFile := flags.String("payload-file", "", "file containing validator pairing qr payload")
	rpcOverride := flags.String("rpc", "", "optional rpc url override")
	stakerKeyPath := flags.String("staker-key", "", "staker wallet keystore path")
	lamports := flags.Uint64("lamports", minimumStakeLamports, "initial validator stake lamports")
	registrationSignature := flags.String("signature", "", "existing validator registration signature")
	if err := flags.Parse(args); err != nil {
		return err
	}
	payload, err := loadValidatorPairingPayload(*payloadText, *payloadFile)
	if err != nil {
		return err
	}
	rpcURL := strings.TrimSpace(payload.RPCURL)
	if strings.TrimSpace(*rpcOverride) != "" {
		rpcURL = strings.TrimSpace(*rpcOverride)
	}
	if err := validateWalletRPCURL(rpcURL); err != nil {
		return err
	}
	if payload.ExpiresAtUnixMS <= time.Now().UnixMilli() {
		return fmt.Errorf("validator pairing payload expired")
	}
	if *lamports == 0 {
		return fmt.Errorf("validator-pair requires positive -lamports")
	}
	staker, err := loadEd25519KeyPair(*stakerKeyPath)
	if err != nil {
		return err
	}
	validatorAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(payload.ValidatorAddress))
	if err != nil {
		return fmt.Errorf("decode pairing validator address: %w", err)
	}
	consensusAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(payload.ConsensusAddress))
	if err != nil {
		return fmt.Errorf("decode pairing consensus address: %w", err)
	}
	blsPublicKey, err := utils.Base58Decode(strings.TrimSpace(payload.BLSPublicKey))
	if err != nil {
		return fmt.Errorf("decode pairing bls public key: %w", err)
	}
	if err := consensus.ValidateBLSPublicKey(blsPublicKey); err != nil {
		return err
	}
	signature := strings.TrimSpace(*registrationSignature)
	if signature == "" {
		blockhash, err := latestBlockhash(rpcURL)
		if err != nil {
			return err
		}
		transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(
			staker,
			validatorAddress,
			consensusAddress,
			blsPublicKey,
			strings.TrimSpace(payload.NodePeerID),
			*lamports,
			blockhash,
		)
		if err != nil {
			return fmt.Errorf("build paired validator register transaction: %w", err)
		}
		signature, err = submitTransaction(rpcURL, transaction)
		if err != nil {
			return err
		}
	}
	completeRequest := validatorPairingCompleteRequest{
		Token:            payload.Token,
		StakerAddress:    staker.PublicKey.String(),
		ValidatorAddress: payload.ValidatorAddress,
		ConsensusAddress: payload.ConsensusAddress,
		BLSPublicKey:     payload.BLSPublicKey,
		NodePeerID:       payload.NodePeerID,
		StakeLamports:    *lamports,
		Signature:        signature,
	}
	var result validatorPairingCompleteResult
	if err := rpcCall(rpcURL, "completeValidatorPairing", []any{completeRequest}, &result); err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode validator pairing result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

func runStake(args []string) error {
	flags := flag.NewFlagSet("stake", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	stakerKeyPath := flags.String("staker-key", "", "staker keystore path")
	validatorAddressText := flags.String("validator-address", "", "validator account address")
	lamports := flags.Uint64("lamports", 0, "stake lamports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	staker, err := loadEd25519KeyPair(*stakerKeyPath)
	if err != nil {
		return err
	}
	validatorAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*validatorAddressText))
	if err != nil {
		return fmt.Errorf("decode validator address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewStakeTransaction(staker, validatorAddress, *lamports, blockhash)
	if err != nil {
		return fmt.Errorf("build stake transaction: %w", err)
	}
	return submitAndPrint(*rpcURL, transaction)
}

func runDelegate(args []string) error {
	flags := flag.NewFlagSet("delegate", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	delegatorKeyPath := flags.String("key", "", "delegator keystore path")
	validatorAddressText := flags.String("validator-address", "", "validator account address")
	lamports := flags.Uint64("lamports", 0, "delegate lamports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	delegator, err := loadEd25519KeyPair(*delegatorKeyPath)
	if err != nil {
		return err
	}
	validatorAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*validatorAddressText))
	if err != nil {
		return fmt.Errorf("decode validator address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewDelegateStakeTransaction(delegator, validatorAddress, *lamports, blockhash)
	if err != nil {
		return fmt.Errorf("build delegate transaction: %w", err)
	}
	return submitAndPrint(*rpcURL, transaction)
}

func runUndelegate(args []string) error {
	flags := flag.NewFlagSet("undelegate", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	delegatorKeyPath := flags.String("key", "", "delegator keystore path")
	validatorAddressText := flags.String("validator-address", "", "validator account address")
	lamports := flags.Uint64("lamports", 0, "undelegate lamports")
	unlockEpoch := flags.Uint64("unlock-epoch", 0, "unlock epoch")
	if err := flags.Parse(args); err != nil {
		return err
	}
	delegator, err := loadEd25519KeyPair(*delegatorKeyPath)
	if err != nil {
		return err
	}
	validatorAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*validatorAddressText))
	if err != nil {
		return fmt.Errorf("decode validator address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewUndelegateStakeTransaction(delegator, validatorAddress, *lamports, *unlockEpoch, blockhash)
	if err != nil {
		return fmt.Errorf("build undelegate transaction: %w", err)
	}
	return submitAndPrint(*rpcURL, transaction)
}

func runWithdrawDelegation(args []string) error {
	flags := flag.NewFlagSet("withdraw-delegation", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	delegatorKeyPath := flags.String("key", "", "delegator keystore path")
	validatorAddressText := flags.String("validator-address", "", "validator account address")
	currentEpoch := flags.Uint64("current-epoch", 0, "current epoch")
	if err := flags.Parse(args); err != nil {
		return err
	}
	delegator, err := loadEd25519KeyPair(*delegatorKeyPath)
	if err != nil {
		return err
	}
	validatorAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*validatorAddressText))
	if err != nil {
		return fmt.Errorf("decode validator address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewWithdrawDelegationTransaction(delegator, validatorAddress, *currentEpoch, blockhash)
	if err != nil {
		return fmt.Errorf("build withdraw delegation transaction: %w", err)
	}
	return submitAndPrint(*rpcURL, transaction)
}

// writeEd25519Keystore 写入 Ed25519 密钥 + 节点和钱包复用同一种 keystore 格式。
func writeEd25519Keystore(path string) error {
	seed, err := randomBytes(structure.SolanaPrivateKeySeedSize)
	if err != nil {
		return err
	}
	keyPair, err := structure.KeyPairFromSeed(seed)
	if err != nil {
		return err
	}
	keyFile := keystoreFile{
		PrivateKeyBase64: base64.StdEncoding.EncodeToString(keyPair.PrivateKey),
		PublicKeyBase64:  base64.StdEncoding.EncodeToString(keyPair.PublicKey[:]),
	}
	if err := writeKeystore(path, keyFile); err != nil {
		return err
	}
	fmt.Printf("%s %s\n", path, keyPair.PublicKey.String())
	return nil
}

// writeBLSKeystore 写入 BLS 密钥 + 验证者注册需要公开 BLS 公钥。
func writeBLSKeystore(path string) error {
	seed, err := randomBytes(32)
	if err != nil {
		return err
	}
	keyPair, err := consensus.BLSKeyPairFromSeed(seed)
	if err != nil {
		return err
	}
	keyFile := keystoreFile{
		PrivateKeyBase64: base64.StdEncoding.EncodeToString(keyPair.PrivateKey),
		PublicKeyBase64:  base64.StdEncoding.EncodeToString(keyPair.PublicKey),
	}
	if err := writeKeystore(path, keyFile); err != nil {
		return err
	}
	fmt.Printf("%s %s\n", path, utils.Base58Encode(keyPair.PublicKey))
	return nil
}

func writeKeystore(path string, keyFile keystoreFile) error {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return fmt.Errorf("keystore path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return fmt.Errorf("create keystore directory: %w", err)
	}
	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("encode keystore: %w", err)
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return fmt.Errorf("write keystore: %w", err)
	}
	return nil
}

func printValidatorKeySummary(outDir string) error {
	peerKey, err := loadEd25519KeyPair(filepath.Join(outDir, "peer.json"))
	if err != nil {
		return err
	}
	validatorKey, err := loadEd25519KeyPair(filepath.Join(outDir, "validator.json"))
	if err != nil {
		return err
	}
	consensusKey, err := loadEd25519KeyPair(filepath.Join(outDir, "consensus.json"))
	if err != nil {
		return err
	}
	blsPublicKey, err := loadBLSPublicKey(filepath.Join(outDir, "bls.json"))
	if err != nil {
		return err
	}
	fmt.Println("peer_id=" + peerKey.PublicKey.String())
	fmt.Println("validator_address=" + validatorKey.PublicKey.String())
	fmt.Println("consensus_address=" + consensusKey.PublicKey.String())
	fmt.Println("bls_public_key=" + utils.Base58Encode(blsPublicKey))
	return nil
}

func loadEd25519KeyPair(path string) (structure.SolanaKeyPair, error) {
	keyFile, err := readKeystore(path)
	if err != nil {
		return structure.SolanaKeyPair{}, err
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) != "" {
		return keyPairFromBase64Seed(keyFile.PrivateKeyBase64)
	}
	if strings.TrimSpace(keyFile.SecretKeyBase64) != "" {
		return keyPairFromBase64Secret(keyFile.SecretKeyBase64)
	}
	return structure.SolanaKeyPair{}, fmt.Errorf("ed25519 keystore has no private key material")
}

func loadBLSPublicKey(path string) ([]byte, error) {
	keyFile, err := readKeystore(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(keyFile.PublicKeyBase64) == "" {
		return nil, fmt.Errorf("bls keystore has no public key")
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyFile.PublicKeyBase64))
	if err != nil {
		return nil, fmt.Errorf("decode bls public key: %w", err)
	}
	return value, nil
}

func loadValidatorPairingPayload(payloadText string, payloadFile string) (validatorPairingPayload, error) {
	text := strings.TrimSpace(payloadText)
	if strings.TrimSpace(payloadFile) != "" {
		data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(payloadFile)))
		if err != nil {
			return validatorPairingPayload{}, fmt.Errorf("read validator pairing payload file: %w", err)
		}
		text = strings.TrimSpace(string(data))
	}
	if text == "" {
		return validatorPairingPayload{}, fmt.Errorf("validator-pair requires -payload or -payload-file")
	}
	if !strings.HasPrefix(text, validatorPairingPayloadPrefix) {
		return validatorPairingPayload{}, fmt.Errorf("validator pairing payload prefix is invalid")
	}
	encoded := strings.TrimPrefix(text, validatorPairingPayloadPrefix)
	data, err := utils.Base64RawDecode(encoded)
	if err != nil {
		return validatorPairingPayload{}, fmt.Errorf("decode validator pairing payload: %w", err)
	}
	payload := validatorPairingPayload{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return validatorPairingPayload{}, fmt.Errorf("parse validator pairing payload: %w", err)
	}
	if err := validateValidatorPairingPayload(payload); err != nil {
		return validatorPairingPayload{}, err
	}
	return payload, nil
}

func validateValidatorPairingPayload(payload validatorPairingPayload) error {
	if payload.Version != 1 {
		return fmt.Errorf("unsupported validator pairing payload version %d", payload.Version)
	}
	required := map[string]string{
		"rpc_url":             payload.RPCURL,
		"chain_id":            payload.ChainID,
		"chain_identity_hash": payload.ChainIdentityHash,
		"genesis_hash":        payload.GenesisHash,
		"node_peer_id":        payload.NodePeerID,
		"validator_address":   payload.ValidatorAddress,
		"consensus_address":   payload.ConsensusAddress,
		"bls_public_key":      payload.BLSPublicKey,
		"token":               payload.Token,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("validator pairing payload missing %s", name)
		}
	}
	if payload.ExpiresAtUnixMS <= 0 {
		return fmt.Errorf("validator pairing payload has invalid expiry")
	}
	return validateWalletRPCURL(payload.RPCURL)
}

func validateWalletRPCURL(rawURL string) error {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse rpc url: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("rpc url scheme must be http or https")
	}
	if parsedURL.User != nil {
		return fmt.Errorf("rpc url must not contain user info")
	}
	if strings.TrimSpace(parsedURL.Host) == "" {
		return fmt.Errorf("rpc url host is empty")
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return fmt.Errorf("rpc url must not contain query or fragment")
	}
	return nil
}

func readKeystore(path string) (keystoreFile, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return keystoreFile{}, fmt.Errorf("keystore path is empty")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return keystoreFile{}, fmt.Errorf("read keystore: %w", err)
	}
	keyFile := keystoreFile{}
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return keystoreFile{}, fmt.Errorf("decode keystore: %w", err)
	}
	return keyFile, nil
}

func keyPairFromBase64Seed(encodedSeed string) (structure.SolanaKeyPair, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSeed))
	if err != nil {
		return structure.SolanaKeyPair{}, fmt.Errorf("decode private key seed: %w", err)
	}
	return structure.KeyPairFromSeed(seed)
}

func keyPairFromBase64Secret(encodedSecret string) (structure.SolanaKeyPair, error) {
	secretKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSecret))
	if err != nil {
		return structure.SolanaKeyPair{}, fmt.Errorf("decode secret key: %w", err)
	}
	return structure.KeyPairFromSecretKey64(secretKey)
}

func randomBytes(size int) ([]byte, error) {
	if size <= 0 || size > 4096 {
		return nil, fmt.Errorf("invalid random size %d", size)
	}
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return value, nil
}

func readBytecodeFile(path string) ([]byte, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return nil, fmt.Errorf("bytecode path is empty")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read bytecode: %w", err)
	}
	if len(data) < 10 {
		return nil, fmt.Errorf("bytecode is too short")
	}
	if string(data[:4]) != "SVM1" {
		return nil, fmt.Errorf("bytecode magic is invalid")
	}
	return data, nil
}

func latestBlockhash(rpcURL string) (structure.Hash, error) {
	var result latestBlockhashResult
	if err := rpcCall(rpcURL, "getLatestBlockhash", []any{}, &result); err != nil {
		return structure.Hash{}, err
	}
	blockhash, err := structure.HashFromBase58(result.Blockhash)
	if err != nil {
		return structure.Hash{}, fmt.Errorf("decode latest blockhash: %w", err)
	}
	return blockhash, nil
}

func submitAndPrint(rpcURL string, transaction structure.Transaction) error {
	signature, err := submitTransaction(rpcURL, transaction)
	if err != nil {
		return err
	}
	fmt.Println(signature)
	return nil
}

func submitTransaction(rpcURL string, transaction structure.Transaction) (string, error) {
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal transaction: %w", err)
	}
	var result transactionSubmitResult
	payload := base64.StdEncoding.EncodeToString(encoded)
	if err := rpcCall(rpcURL, "sendTransaction", []any{payload}, &result); err != nil {
		return "", err
	}
	return result.Signature, nil
}

func rpcCall(rpcURL string, method string, params []any, result any) error {
	request := rpcRequest{JSONRPC: "2.0", ID: uint64(time.Now().UnixNano()), Method: method, Params: params}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode rpc request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultHTTPTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(rpcURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create rpc request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("call rpc: %w", err)
	}
	defer httpResponse.Body.Close()
	limitedBody := io.LimitReader(httpResponse.Body, maxRPCBodyBytes)
	responseBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		return fmt.Errorf("read rpc response: %w", err)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return fmt.Errorf("rpc http status %d: %s", httpResponse.StatusCode, string(responseBytes))
	}
	response := rpcResponse{}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return fmt.Errorf("decode rpc response: %w", err)
	}
	if response.Error != nil {
		return fmt.Errorf("rpc error %d: %s", response.Error.Code, response.Error.Message)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode rpc result: %w", err)
	}
	return nil
}

func printUsage() {
	lines := []string{
		"usage:",
		"  wallet new-key -type ed25519 -out keys/staker.json",
		"  wallet validator-keygen -out-dir keys/validator-node",
		"  wallet address -key keys/staker.json",
		"  wallet balance -rpc http://127.0.0.1:8899 -address ADDRESS",
		"  wallet transfer -rpc http://127.0.0.1:8899 -key keys/staker.json -to ADDRESS -lamports 1000",
		"  wallet deploy-contract -rpc http://127.0.0.1:8899 -payer-key keys/user.json -program-key keys/program.json -bytecode dist/pop.svmbin -deposit-lamports 0",
		"  wallet validator-register -rpc http://127.0.0.1:8899 -staker-key keys/staker.json -validator-address ADDRESS -consensus-address ADDRESS -bls-public-key KEY -peer-id PEER -lamports 10000000",
		"  wallet validator-pair -payload PAYLOAD -staker-key keys/staker.json -lamports 10000000",
		"  wallet validator-pair -payload PAYLOAD -staker-key keys/staker.json -signature SIGNATURE",
		"  wallet stake -rpc http://127.0.0.1:8899 -staker-key keys/staker.json -validator-address ADDRESS -lamports 10000000",
		"  wallet delegate -rpc http://127.0.0.1:8899 -key keys/user.json -validator-address ADDRESS -lamports 10000000",
		"  wallet undelegate -rpc http://127.0.0.1:8899 -key keys/user.json -validator-address ADDRESS -lamports 10000000 -unlock-epoch 10",
		"  wallet withdraw-delegation -rpc http://127.0.0.1:8899 -key keys/user.json -validator-address ADDRESS -current-epoch 10",
		"",
		"minimum stake lamports: " + strconv.FormatUint(minimumStakeLamports, 10),
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}
