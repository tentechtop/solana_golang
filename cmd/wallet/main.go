package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

const (
	defaultRPCURL                 = "http://127.0.0.1:8899"
	defaultHTTPTimeout            = 10 * time.Second
	defaultConfirmationTimeout    = 60 * time.Second
	confirmationPollInterval      = 250 * time.Millisecond
	maxRPCBodyBytes               = 4 << 20
	minimumStakeLamports          = 10_000_000
	validatorPairingPayloadPrefix = "posvalpair:"
	validatorPairingModeRegister  = "validator_registration"
	validatorPairingModeBootstrap = "bootstrap_join"
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
	Height    uint64 `json:"height"`
}

type balanceResult struct {
	Value uint64 `json:"value"`
}

type bootstrapManifestStatusResult struct {
	Ready bool `json:"ready"`
}

type transactionSubmitResult struct {
	Signature string `json:"signature"`
}

type transactionDetailResult struct {
	Found  bool   `json:"found"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type stringListFlag []string

func (values *stringListFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
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

type validatorPairingCompleteRequest struct {
	Token                    string `json:"token"`
	StakerAddress            string `json:"staker_address"`
	ValidatorAddress         string `json:"validator_address"`
	ConsensusAddress         string `json:"consensus_address"`
	BLSPublicKey             string `json:"bls_public_key"`
	NodePeerID               string `json:"node_peer_id"`
	StakeLamports            uint64 `json:"stake_lamports"`
	Signature                string `json:"signature"`
	BootstrapStakerSignature string `json:"bootstrap_staker_signature,omitempty"`
}

type validatorPairingCompleteResult struct {
	State                    string `json:"state,omitempty"`
	StakerAddress            string `json:"staker_address,omitempty"`
	ValidatorAddress         string `json:"validator_address,omitempty"`
	ConsensusAddress         string `json:"consensus_address,omitempty"`
	BLSPublicKey             string `json:"bls_public_key,omitempty"`
	NodePeerID               string `json:"node_peer_id,omitempty"`
	StakeLamports            uint64 `json:"stake_lamports,omitempty"`
	Signature                string `json:"signature,omitempty"`
	BootstrapStakerSignature string `json:"bootstrap_staker_signature,omitempty"`
	ConfigUpdated            bool   `json:"config_updated"`
	RestartRequired          bool   `json:"restart_required"`
	ConfigPath               string `json:"config_path,omitempty"`
	ActivationNote           string `json:"activation_note,omitempty"`
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
	case "upgrade-contract":
		return runUpgradeContract(args[1:])
	case "create-contract-account":
		return runCreateContractAccount(args[1:])
	case "contract-call":
		return runContractCall(args[1:])
	case "account-info":
		return runAccountInfo(args[1:])
	case "asset-bootstrap-fungible":
		return runAssetBootstrapFungible(args[1:])
	case "rwa-bootstrap":
		return runRWABootstrap(args[1:])
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
	case "emergency-recovery-submit":
		return runEmergencyRecoverySubmit(args[1:])
	case "emergency-recovery-create":
		return runEmergencyRecoveryCreate(args[1:])
	case "emergency-recovery-sign":
		return runEmergencyRecoverySign(args[1:])
	case "multi-validator-activation-submit":
		return runMultiValidatorActivationSubmit(args[1:])
	case "multi-validator-activation-create":
		return runMultiValidatorActivationCreate(args[1:])
	case "multi-validator-activation-sign":
		return runMultiValidatorActivationSign(args[1:])
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
	authorityKeyPath := flags.String("authority-key", "", "optional manifest upgrade authority keystore path")
	bytecodePath := flags.String("bytecode", "", "compiled .ovmbin bytecode path")
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
	additionalSigners := make([]structure.OrdexusKeyPair, 0, 1)
	if strings.TrimSpace(*authorityKeyPath) != "" {
		authority, err := loadEd25519KeyPair(*authorityKeyPath)
		if err != nil {
			return fmt.Errorf("load deployment authority key: %w", err)
		}
		additionalSigners = append(additionalSigners, authority)
	}
	transactions, err := blockchain.NewDeployContractTransactionsWithSigners(payer, program, programData, *depositLamports, additionalSigners, blockhash)
	if err != nil {
		return fmt.Errorf("build deploy contract transaction: %w", err)
	}
	signatures := make([]string, len(transactions))
	for index := range transactions {
		transactions[index].SubmitTime = time.Now().UnixMilli()
		signatures[index], err = submitTransaction(*rpcURL, transactions[index])
		if err != nil {
			return fmt.Errorf("submit deploy transaction %d/%d: %w", index+1, len(transactions), err)
		}
		if index < len(transactions)-1 {
			if err := waitTransactionConfirmed(*rpcURL, signatures[index], defaultConfirmationTimeout); err != nil {
				return fmt.Errorf("confirm deploy transaction %d/%d: %w", index+1, len(transactions), err)
			}
		}
	}
	result := map[string]any{
		"signature":         signatures[len(signatures)-1],
		"signatures":        signatures,
		"transaction_count": len(signatures),
		"program":           program.PublicKey.String(),
		"bytecode":          filepath.Clean(strings.TrimSpace(*bytecodePath)),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode deploy result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

func runUpgradeContract(args []string) error {
	flags := flag.NewFlagSet("upgrade-contract", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	payerKeyPath := flags.String("payer-key", "", "payer ed25519 keystore path")
	authorityKeyPath := flags.String("authority-key", "", "manifest upgrade authority keystore path")
	programAddressText := flags.String("program", "", "deployed program address")
	bytecodePath := flags.String("bytecode", "", "compiled .ovmbin bytecode path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	payer, err := loadEd25519KeyPair(*payerKeyPath)
	if err != nil {
		return fmt.Errorf("load payer key: %w", err)
	}
	authority, err := loadEd25519KeyPair(*authorityKeyPath)
	if err != nil {
		return fmt.Errorf("load upgrade authority key: %w", err)
	}
	programAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*programAddressText))
	if err != nil {
		return fmt.Errorf("decode program address: %w", err)
	}
	programData, err := readBytecodeFile(*bytecodePath)
	if err != nil {
		return err
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewUpgradeContractTransaction(payer, authority, programAddress, programData, blockhash)
	if err != nil {
		return fmt.Errorf("build upgrade contract transaction: %w", err)
	}
	signature, err := submitTransaction(*rpcURL, transaction)
	if err != nil {
		return err
	}
	return printJSON(map[string]string{
		"signature": signature,
		"program":   programAddress.String(),
		"bytecode":  filepath.Clean(strings.TrimSpace(*bytecodePath)),
	})
}

func runCreateContractAccount(args []string) error {
	flags := flag.NewFlagSet("create-contract-account", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	payerKeyPath := flags.String("payer-key", "", "payer ed25519 keystore path")
	accountKeyPath := flags.String("account-key", "", "new contract account ed25519 keystore path")
	programAddressText := flags.String("program", "", "owner contract program address")
	space := flags.Uint64("space", 0, "account data size in bytes")
	depositLamports := flags.Uint64("deposit-lamports", 0, "extra account deposit lamports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	payer, err := loadEd25519KeyPair(*payerKeyPath)
	if err != nil {
		return fmt.Errorf("load payer key: %w", err)
	}
	account, err := loadEd25519KeyPair(*accountKeyPath)
	if err != nil {
		return fmt.Errorf("load contract account key: %w", err)
	}
	programAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*programAddressText))
	if err != nil {
		return fmt.Errorf("decode program address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewCreateContractAccountTransaction(
		payer,
		account,
		programAddress,
		*space,
		*depositLamports,
		blockhash,
	)
	if err != nil {
		return fmt.Errorf("build contract account transaction: %w", err)
	}
	signature, err := submitTransaction(*rpcURL, transaction)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"signature": signature,
		"account":   account.PublicKey.String(),
		"program":   programAddress.String(),
		"space":     *space,
	})
}

func runContractCall(args []string) error {
	flags := flag.NewFlagSet("contract-call", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	payerKeyPath := flags.String("payer-key", "", "payer ed25519 keystore path")
	programAddressText := flags.String("program", "", "deployed program address")
	dataHex := flags.String("data-hex", "", "instruction data encoded as hex")
	dataBase64 := flags.String("data-base64", "", "instruction data encoded as base64")
	dataFile := flags.String("data-file", "", "instruction data file path")
	simulate := flags.Bool("simulate", false, "simulate without submitting")
	accountSpecs := stringListFlag{}
	signerKeyPaths := stringListFlag{}
	flags.Var(&accountSpecs, "account", "instruction account ADDRESS:r|w|s|sw; may be repeated")
	flags.Var(&signerKeyPaths, "signer-key", "additional signer keystore path; may be repeated")
	if err := flags.Parse(args); err != nil {
		return err
	}
	payer, err := loadEd25519KeyPair(*payerKeyPath)
	if err != nil {
		return fmt.Errorf("load payer key: %w", err)
	}
	programAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*programAddressText))
	if err != nil {
		return fmt.Errorf("decode program address: %w", err)
	}
	accounts, err := parseContractAccounts(accountSpecs)
	if err != nil {
		return err
	}
	signers, err := loadContractSigners(signerKeyPaths)
	if err != nil {
		return err
	}
	instructionData, err := contractInstructionData(*dataHex, *dataBase64, *dataFile)
	if err != nil {
		return err
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewInvokeContractTransaction(payer, programAddress, accounts, instructionData, signers, blockhash)
	if err != nil {
		return fmt.Errorf("build contract call transaction: %w", err)
	}
	if *simulate {
		return simulateAndPrint(*rpcURL, transaction)
	}
	signature, err := submitTransaction(*rpcURL, transaction)
	if err != nil {
		return err
	}
	return printJSON(map[string]string{"signature": signature, "program": programAddress.String()})
}

func runAccountInfo(args []string) error {
	flags := flag.NewFlagSet("account-info", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	address := flags.String("address", "", "account address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*address) == "" {
		return fmt.Errorf("account-info requires -address")
	}
	var result json.RawMessage
	if err := rpcCall(*rpcURL, "getAccountInfo", []any{strings.TrimSpace(*address)}, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

func parseContractAccounts(values []string) ([]structure.AccountMeta, error) {
	accounts := make([]structure.AccountMeta, 0, len(values))
	seen := make(map[structure.PublicKey]struct{}, len(values))
	for index, value := range values {
		parts := strings.Split(strings.TrimSpace(value), ":")
		if len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("contract account %d must use ADDRESS:r|w|s|sw", index)
		}
		address, err := structure.PublicKeyFromBase58(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("decode contract account %d: %w", index, err)
		}
		if _, exists := seen[address]; exists {
			return nil, fmt.Errorf("duplicate contract account %s", address.String())
		}
		seen[address] = struct{}{}
		permission := "r"
		if len(parts) == 2 {
			permission = strings.ToLower(strings.TrimSpace(parts[1]))
		}
		account := structure.AccountMeta{PublicKey: address}
		switch permission {
		case "r":
		case "w":
			account.IsWritable = true
		case "s":
			account.IsSigner = true
		case "sw", "ws":
			account.IsSigner = true
			account.IsWritable = true
		default:
			return nil, fmt.Errorf("contract account %d has invalid permission %q", index, permission)
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func loadContractSigners(paths []string) ([]structure.OrdexusKeyPair, error) {
	signers := make([]structure.OrdexusKeyPair, 0, len(paths))
	for index, path := range paths {
		signer, err := loadEd25519KeyPair(path)
		if err != nil {
			return nil, fmt.Errorf("load contract signer %d: %w", index, err)
		}
		signers = append(signers, signer)
	}
	return signers, nil
}

func contractInstructionData(dataHex string, dataBase64 string, dataFile string) ([]byte, error) {
	provided := 0
	for _, value := range []string{dataHex, dataBase64, dataFile} {
		if strings.TrimSpace(value) != "" {
			provided++
		}
	}
	if provided > 1 {
		return nil, fmt.Errorf("use only one of -data-hex, -data-base64, or -data-file")
	}
	if strings.TrimSpace(dataHex) != "" {
		data, err := hex.DecodeString(strings.TrimSpace(dataHex))
		if err != nil {
			return nil, fmt.Errorf("decode contract data hex: %w", err)
		}
		return data, nil
	}
	if strings.TrimSpace(dataBase64) != "" {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
		if err != nil {
			return nil, fmt.Errorf("decode contract data base64: %w", err)
		}
		return data, nil
	}
	if strings.TrimSpace(dataFile) != "" {
		data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(dataFile)))
		if err != nil {
			return nil, fmt.Errorf("read contract data file: %w", err)
		}
		return data, nil
	}
	return nil, nil
}

func simulateAndPrint(rpcURL string, transaction structure.Transaction) error {
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal simulated transaction: %w", err)
	}
	var result json.RawMessage
	if err := rpcCall(rpcURL, "simulateTransaction", []any{base64.StdEncoding.EncodeToString(encoded)}, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

func printJSON(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

func printRawJSON(value json.RawMessage) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, value, "", "  "); err != nil {
		return fmt.Errorf("format rpc result: %w", err)
	}
	fmt.Println(formatted.String())
	return nil
}

func runAssetBootstrapFungible(args []string) error {
	flags := flag.NewFlagSet("asset-bootstrap-fungible", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	payerKeyPath := flags.String("payer-key", "", "payer and mint authority ed25519 keystore path")
	programAddressText := flags.String("program", "", "deployed ERC20-like program address")
	name := flags.String("name", "", "asset display name")
	symbol := flags.String("symbol", "", "asset symbol")
	decimals := flags.Uint("decimals", 0, "asset decimals")
	initialSupply := flags.Uint64("initial-supply", 0, "initial supply in smallest units")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *decimals > 255 {
		return fmt.Errorf("decimals must be <= 255")
	}
	payer, err := loadEd25519KeyPair(*payerKeyPath)
	if err != nil {
		return fmt.Errorf("load payer key: %w", err)
	}
	programAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*programAddressText))
	if err != nil {
		return fmt.Errorf("decode program address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	transaction, err := blockchain.NewBootstrapFungibleAssetTransaction(blockchain.FungibleAssetBootstrapParams{
		Payer:           payer,
		Program:         programAddress,
		Name:            strings.TrimSpace(*name),
		Symbol:          strings.TrimSpace(*symbol),
		Decimals:        uint8(*decimals),
		InitialSupply:   *initialSupply,
		RecentBlockhash: blockhash,
	})
	if err != nil {
		return fmt.Errorf("build asset bootstrap transaction: %w", err)
	}
	signature, err := submitTransaction(*rpcURL, transaction)
	if err != nil {
		return err
	}
	assetAccounts, err := blockchain.DeriveFungibleAssetBootstrapAccounts(programAddress, payer.PublicKey)
	if err != nil {
		return err
	}
	result := map[string]any{
		"signature":      signature,
		"program":        programAddress.String(),
		"mint":           assetAccounts.Mint.PublicKey.String(),
		"owner":          payer.PublicKey.String(),
		"balance":        assetAccounts.Balance.PublicKey.String(),
		"name":           strings.TrimSpace(*name),
		"symbol":         strings.TrimSpace(*symbol),
		"decimals":       uint8(*decimals),
		"initial_supply": *initialSupply,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode asset bootstrap result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

func runRWABootstrap(args []string) error {
	flags := flag.NewFlagSet("rwa-bootstrap", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "rpc url")
	payerKeyPath := flags.String("payer-key", "", "issuer ed25519 keystore path")
	programAddressText := flags.String("program", "", "deployed immutable RWA program address")
	assetTypeText := flags.String("type", "share", "share or nft")
	name := flags.String("name", "", "regulated asset display name")
	symbol := flags.String("symbol", "", "regulated asset symbol")
	maxSupply := flags.Uint64("max-supply", 0, "maximum whole shares; must be one for NFT")
	initialSupply := flags.Uint64("initial-supply", 0, "initial whole shares issued to issuer")
	documentHash := flags.String("document-hash", "", "lower-case SHA-256 of controlling legal documents")
	identityHash := flags.String("identity-hash", "", "lower-case SHA-256 commitment for issuer identity record")
	if err := flags.Parse(args); err != nil {
		return err
	}
	assetType := uint8(vmprogram.RWAAssetShare)
	switch strings.ToLower(strings.TrimSpace(*assetTypeText)) {
	case "share":
	case "nft":
		assetType = vmprogram.RWAAssetNFT
	default:
		return fmt.Errorf("RWA type must be share or nft")
	}
	payer, err := loadEd25519KeyPair(*payerKeyPath)
	if err != nil {
		return fmt.Errorf("load RWA issuer key: %w", err)
	}
	programAddress, err := structure.PublicKeyFromBase58(strings.TrimSpace(*programAddressText))
	if err != nil {
		return fmt.Errorf("decode RWA program address: %w", err)
	}
	blockhash, err := latestBlockhash(*rpcURL)
	if err != nil {
		return err
	}
	params := blockchain.RWABootstrapParams{
		Payer:           payer,
		Program:         programAddress,
		AssetType:       assetType,
		Name:            strings.TrimSpace(*name),
		Symbol:          strings.TrimSpace(*symbol),
		MaxSupply:       *maxSupply,
		InitialSupply:   *initialSupply,
		DocumentHash:    strings.TrimSpace(*documentHash),
		IdentityHash:    strings.TrimSpace(*identityHash),
		RecentBlockhash: blockhash,
	}
	transaction, err := blockchain.NewBootstrapRWAAssetTransaction(params)
	if err != nil {
		return fmt.Errorf("build RWA bootstrap transaction: %w", err)
	}
	signature, err := submitTransaction(*rpcURL, transaction)
	if err != nil {
		return err
	}
	accounts, err := blockchain.DeriveRWABootstrapAccounts(programAddress, payer.PublicKey, params.Symbol)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"signature":      signature,
		"program":        programAddress.String(),
		"mint":           accounts.Mint.PublicKey.String(),
		"issuer":         payer.PublicKey.String(),
		"issuer_holding": accounts.Holding.PublicKey.String(),
		"type":           strings.ToLower(strings.TrimSpace(*assetTypeText)),
		"name":           params.Name,
		"symbol":         params.Symbol,
		"max_supply":     strconv.FormatUint(params.MaxSupply, 10),
		"initial_supply": strconv.FormatUint(params.InitialSupply, 10),
		"document_hash":  params.DocumentHash,
	})
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
	bootstrapStakerSignature := ""
	if validatorPairingPayloadMode(payload) == validatorPairingModeBootstrap {
		bootstrapStakerSignature = signature
		if bootstrapStakerSignature == "" {
			signatureValue, err := signBootstrapPairingAuthorization(payload, staker, *lamports)
			if err != nil {
				return err
			}
			bootstrapStakerSignature = signatureValue
		}
		signature = ""
		bootstrapRPCURL := strings.TrimSpace(payload.BootstrapRPCURL)
		if err := validateWalletRPCURL(bootstrapRPCURL); err != nil {
			return fmt.Errorf("bootstrap rpc url: %w", err)
		}
		manifestStatus := bootstrapManifestStatusResult{}
		if err := rpcCall(bootstrapRPCURL, "getBootstrapManifest", []any{}, &manifestStatus); err != nil {
			return fmt.Errorf("get bootstrap manifest: %w", err)
		}
		if manifestStatus.Ready {
			signature, err = submitPairedValidatorRegistration(
				bootstrapRPCURL,
				staker,
				validatorAddress,
				consensusAddress,
				blsPublicKey,
				payload.NodePeerID,
				*lamports,
			)
			if err != nil {
				return err
			}
		}
	} else if signature == "" {
		signature, err = submitPairedValidatorRegistration(
			rpcURL,
			staker,
			validatorAddress,
			consensusAddress,
			blsPublicKey,
			payload.NodePeerID,
			*lamports,
		)
		if err != nil {
			return err
		}
	}
	completeRequest := validatorPairingCompleteRequest{
		Token:                    payload.Token,
		StakerAddress:            staker.PublicKey.String(),
		ValidatorAddress:         payload.ValidatorAddress,
		ConsensusAddress:         payload.ConsensusAddress,
		BLSPublicKey:             payload.BLSPublicKey,
		NodePeerID:               payload.NodePeerID,
		StakeLamports:            *lamports,
		Signature:                signature,
		BootstrapStakerSignature: bootstrapStakerSignature,
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

// submitPairedValidatorRegistration 提交配对验证者的链上注册交易 + 创世后加入和普通配对复用同一签名流程。
func submitPairedValidatorRegistration(
	rpcURL string,
	staker structure.OrdexusKeyPair,
	validatorAddress structure.PublicKey,
	consensusAddress structure.PublicKey,
	blsPublicKey []byte,
	peerID string,
	lamports uint64,
) (string, error) {
	blockhash, err := latestBlockhash(rpcURL)
	if err != nil {
		return "", err
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(
		staker,
		validatorAddress,
		consensusAddress,
		blsPublicKey,
		strings.TrimSpace(peerID),
		lamports,
		blockhash,
	)
	if err != nil {
		return "", fmt.Errorf("build paired validator register transaction: %w", err)
	}
	signature, err := submitTransaction(rpcURL, transaction)
	if err != nil {
		return "", fmt.Errorf("submit paired validator register transaction: %w", err)
	}
	if strings.TrimSpace(signature) == "" {
		return "", fmt.Errorf("paired validator register transaction returned empty signature")
	}
	return signature, nil
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

func runEmergencyRecoverySubmit(args []string) error {
	flags := flag.NewFlagSet("emergency-recovery-submit", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "validator management rpc url")
	certificatePath := flags.String("certificate-file", "", "base64 or json emergency recovery certificate file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*certificatePath) == "" {
		return fmt.Errorf("emergency-recovery-submit requires -certificate-file")
	}
	data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(*certificatePath)))
	if err != nil {
		return fmt.Errorf("read emergency recovery certificate: %w", err)
	}
	payload := strings.TrimSpace(string(data))
	if !json.Valid(data) {
		if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
			return fmt.Errorf("emergency recovery certificate must contain json or base64 json")
		}
	} else {
		payload = base64.StdEncoding.EncodeToString(data)
	}
	var transactionID string
	if err := rpcCall(*rpcURL, "submitEmergencyRecovery", []any{payload}, &transactionID); err != nil {
		return err
	}
	if strings.TrimSpace(transactionID) == "" {
		return fmt.Errorf("emergency recovery returned empty transaction id")
	}
	fmt.Println(transactionID)
	return nil
}

func runEmergencyRecoveryCreate(args []string) error {
	flags := flag.NewFlagSet("emergency-recovery-create", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "validator management rpc url")
	sequence := flags.Uint64("sequence", 0, "monotonically increasing recovery sequence")
	expiresIn := flags.Duration("expires-in", 10*time.Minute, "certificate validity duration")
	elderValidatorID := flags.String("elder-validator-id", "", "selected elder validator id")
	excludedValidatorIDs := flags.String("exclude-validator-ids", "", "comma separated unavailable validator ids")
	consensusKeyPath := flags.String("consensus-key", "", "safety committee validator consensus keystore path")
	outPath := flags.String("out", "", "certificate output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sequence == 0 || *expiresIn <= 0 || strings.TrimSpace(*elderValidatorID) == "" || strings.TrimSpace(*outPath) == "" {
		return fmt.Errorf("emergency-recovery-create requires positive -sequence/-expires-in, -elder-validator-id, and -out")
	}
	consensusKey, err := loadEd25519KeyPair(*consensusKeyPath)
	if err != nil {
		return err
	}
	var status struct {
		ChainID   string `json:"chain_id"`
		Consensus struct {
			SafetyCheckpoint struct {
				FinalizedHeight  uint64 `json:"finalized_height"`
				FinalizedHash    string `json:"finalized_hash"`
				EpochID          uint64 `json:"epoch_id"`
				ValidatorSetRoot string `json:"validator_set_root"`
			} `json:"safety_checkpoint"`
		} `json:"consensus"`
	}
	if err := rpcCall(*rpcURL, "getNodeStatus", []any{}, &status); err != nil {
		return err
	}
	certificate := consensus.EmergencyRecoveryCertificate{
		Version:                consensus.EmergencyRecoveryCertificateVersion,
		ChainID:                strings.TrimSpace(status.ChainID),
		Sequence:               *sequence,
		FinalizedHeight:        status.Consensus.SafetyCheckpoint.FinalizedHeight,
		FinalizedHash:          status.Consensus.SafetyCheckpoint.FinalizedHash,
		SafetyEpochID:          status.Consensus.SafetyCheckpoint.EpochID,
		SafetyValidatorSetRoot: status.Consensus.SafetyCheckpoint.ValidatorSetRoot,
		ElderValidatorID:       strings.TrimSpace(*elderValidatorID),
		ExcludedValidatorIDs:   splitCommaSeparated(*excludedValidatorIDs),
		ExpiresAtUnixMilli:     time.Now().Add(*expiresIn).UnixMilli(),
	}
	if err := signEmergencyRecoveryCertificate(&certificate, consensusKey); err != nil {
		return err
	}
	return writeEmergencyRecoveryCertificate(*outPath, certificate)
}

func runEmergencyRecoverySign(args []string) error {
	flags := flag.NewFlagSet("emergency-recovery-sign", flag.ContinueOnError)
	certificatePath := flags.String("certificate-file", "", "input certificate json file")
	consensusKeyPath := flags.String("consensus-key", "", "safety committee validator consensus keystore path")
	outPath := flags.String("out", "", "signed certificate output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*certificatePath) == "" || strings.TrimSpace(*outPath) == "" {
		return fmt.Errorf("emergency-recovery-sign requires -certificate-file and -out")
	}
	data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(*certificatePath)))
	if err != nil {
		return fmt.Errorf("read emergency recovery certificate: %w", err)
	}
	certificate := consensus.EmergencyRecoveryCertificate{}
	if err := json.Unmarshal(data, &certificate); err != nil {
		return fmt.Errorf("parse emergency recovery certificate: %w", err)
	}
	consensusKey, err := loadEd25519KeyPair(*consensusKeyPath)
	if err != nil {
		return err
	}
	if err := signEmergencyRecoveryCertificate(&certificate, consensusKey); err != nil {
		return err
	}
	return writeEmergencyRecoveryCertificate(*outPath, certificate)
}

func signEmergencyRecoveryCertificate(certificate *consensus.EmergencyRecoveryCertificate, signer structure.OrdexusKeyPair) error {
	if certificate == nil || len(signer.PrivateKey) == 0 || signer.PublicKey.IsZero() {
		return fmt.Errorf("emergency recovery certificate or validator signer is empty")
	}
	validatorID := string(consensus.NewValidatorID(signer.PublicKey))
	for _, item := range certificate.Signatures {
		if strings.TrimSpace(item.ValidatorID) == validatorID {
			return fmt.Errorf("validator already signed emergency recovery certificate")
		}
	}
	signBytes, err := certificate.SignBytes()
	if err != nil {
		return err
	}
	signature, err := structure.SignMessage(signer.PrivateKey, signBytes)
	if err != nil {
		return fmt.Errorf("sign emergency recovery certificate: %w", err)
	}
	certificate.Signatures = append(certificate.Signatures, consensus.EmergencyRecoverySignature{
		ValidatorID: validatorID,
		PublicKey:   signer.PublicKey.String(),
		Signature:   signature.String(),
	})
	return nil
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func writeEmergencyRecoveryCertificate(path string, certificate consensus.EmergencyRecoveryCertificate) error {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return fmt.Errorf("emergency recovery certificate output path is empty")
	}
	data, err := json.MarshalIndent(certificate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode emergency recovery certificate: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return fmt.Errorf("create emergency recovery certificate directory: %w", err)
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return fmt.Errorf("write emergency recovery certificate: %w", err)
	}
	return nil
}

func runMultiValidatorActivationSubmit(args []string) error {
	flags := flag.NewFlagSet("multi-validator-activation-submit", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "validator management rpc url")
	certificatePath := flags.String("certificate-file", "", "base64 or json activation certificate file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*certificatePath) == "" {
		return fmt.Errorf("multi-validator-activation-submit requires -certificate-file")
	}
	data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(*certificatePath)))
	if err != nil {
		return fmt.Errorf("read multi-validator activation certificate: %w", err)
	}
	payload := strings.TrimSpace(string(data))
	if !json.Valid(data) {
		if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
			return fmt.Errorf("multi-validator activation certificate must contain json or base64 json")
		}
	} else {
		payload = base64.StdEncoding.EncodeToString(data)
	}
	var checkpointHash string
	if err := rpcCall(*rpcURL, "submitMultiValidatorActivation", []any{payload}, &checkpointHash); err != nil {
		return err
	}
	if strings.TrimSpace(checkpointHash) == "" {
		return fmt.Errorf("multi-validator activation returned empty checkpoint hash")
	}
	fmt.Println(checkpointHash)
	return nil
}

func runMultiValidatorActivationCreate(args []string) error {
	flags := flag.NewFlagSet("multi-validator-activation-create", flag.ContinueOnError)
	rpcURL := flags.String("rpc", defaultRPCURL, "validator management rpc url")
	sequence := flags.Uint64("sequence", 0, "monotonically increasing recovery sequence")
	expiresIn := flags.Duration("expires-in", 10*time.Minute, "certificate validity duration")
	consensusKeyPath := flags.String("consensus-key", "", "safety committee validator consensus keystore path")
	outPath := flags.String("out", "", "certificate output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sequence == 0 || *expiresIn <= 0 || strings.TrimSpace(*outPath) == "" {
		return fmt.Errorf("multi-validator-activation-create requires positive -sequence/-expires-in and -out")
	}
	signer, err := loadEd25519KeyPair(*consensusKeyPath)
	if err != nil {
		return err
	}
	var status struct {
		ChainID    string `json:"chain_id"`
		HeadHeight uint64 `json:"head_height"`
		HeadHash   string `json:"head_hash"`
		Consensus  struct {
			CandidateEpochID          uint64 `json:"candidate_epoch_id"`
			CandidateValidatorSetRoot string `json:"candidate_validator_set_root"`
			SafetyCheckpoint          struct {
				FinalizedHeight  uint64 `json:"finalized_height"`
				FinalizedHash    string `json:"finalized_hash"`
				EpochID          uint64 `json:"epoch_id"`
				ValidatorSetRoot string `json:"validator_set_root"`
			} `json:"safety_checkpoint"`
		} `json:"consensus"`
	}
	if err := rpcCall(*rpcURL, "getNodeStatus", []any{}, &status); err != nil {
		return err
	}
	certificate := consensus.MultiValidatorActivationCertificate{
		Version:                consensus.MultiValidatorActivationCertificateVersion,
		ChainID:                strings.TrimSpace(status.ChainID),
		Sequence:               *sequence,
		SafetyFinalizedHeight:  status.Consensus.SafetyCheckpoint.FinalizedHeight,
		SafetyFinalizedHash:    status.Consensus.SafetyCheckpoint.FinalizedHash,
		SafetyEpochID:          status.Consensus.SafetyCheckpoint.EpochID,
		SafetyValidatorSetRoot: status.Consensus.SafetyCheckpoint.ValidatorSetRoot,
		TargetHeight:           status.HeadHeight,
		TargetHash:             status.HeadHash,
		TargetEpochID:          status.Consensus.CandidateEpochID,
		TargetValidatorSetRoot: status.Consensus.CandidateValidatorSetRoot,
		ExpiresAtUnixMilli:     time.Now().Add(*expiresIn).UnixMilli(),
	}
	if err := signMultiValidatorActivationCertificate(&certificate, signer); err != nil {
		return err
	}
	return writeMultiValidatorActivationCertificate(*outPath, certificate)
}

func runMultiValidatorActivationSign(args []string) error {
	flags := flag.NewFlagSet("multi-validator-activation-sign", flag.ContinueOnError)
	certificatePath := flags.String("certificate-file", "", "input activation certificate json file")
	consensusKeyPath := flags.String("consensus-key", "", "safety committee validator consensus keystore path")
	outPath := flags.String("out", "", "signed certificate output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*certificatePath) == "" || strings.TrimSpace(*outPath) == "" {
		return fmt.Errorf("multi-validator-activation-sign requires -certificate-file and -out")
	}
	data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(*certificatePath)))
	if err != nil {
		return fmt.Errorf("read multi-validator activation certificate: %w", err)
	}
	var certificate consensus.MultiValidatorActivationCertificate
	if err := json.Unmarshal(data, &certificate); err != nil {
		return fmt.Errorf("parse multi-validator activation certificate: %w", err)
	}
	signer, err := loadEd25519KeyPair(*consensusKeyPath)
	if err != nil {
		return err
	}
	if err := signMultiValidatorActivationCertificate(&certificate, signer); err != nil {
		return err
	}
	return writeMultiValidatorActivationCertificate(*outPath, certificate)
}

func signMultiValidatorActivationCertificate(certificate *consensus.MultiValidatorActivationCertificate, signer structure.OrdexusKeyPair) error {
	if certificate == nil || len(signer.PrivateKey) == 0 || signer.PublicKey.IsZero() {
		return fmt.Errorf("multi-validator activation certificate or validator signer is empty")
	}
	validatorID := string(consensus.NewValidatorID(signer.PublicKey))
	for _, item := range certificate.Signatures {
		if strings.TrimSpace(item.ValidatorID) == validatorID {
			return fmt.Errorf("validator already signed multi-validator activation certificate")
		}
	}
	signBytes, err := certificate.SignBytes()
	if err != nil {
		return err
	}
	signature, err := structure.SignMessage(signer.PrivateKey, signBytes)
	if err != nil {
		return fmt.Errorf("sign multi-validator activation certificate: %w", err)
	}
	certificate.Signatures = append(certificate.Signatures, consensus.EmergencyRecoverySignature{
		ValidatorID: validatorID,
		PublicKey:   signer.PublicKey.String(),
		Signature:   signature.String(),
	})
	return nil
}

func writeMultiValidatorActivationCertificate(path string, certificate consensus.MultiValidatorActivationCertificate) error {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return fmt.Errorf("multi-validator activation certificate output path is empty")
	}
	data, err := json.MarshalIndent(certificate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode multi-validator activation certificate: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return fmt.Errorf("create multi-validator activation certificate directory: %w", err)
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return fmt.Errorf("write multi-validator activation certificate: %w", err)
	}
	return nil
}

// writeEd25519Keystore 写入 Ed25519 密钥 + 节点和钱包复用同一种 keystore 格式。
func writeEd25519Keystore(path string) error {
	seed, err := randomBytes(structure.OrdexusPrivateKeySeedSize)
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

func loadEd25519KeyPair(path string) (structure.OrdexusKeyPair, error) {
	keyFile, err := readKeystore(path)
	if err != nil {
		return structure.OrdexusKeyPair{}, err
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) != "" {
		return keyPairFromBase64Seed(keyFile.PrivateKeyBase64)
	}
	if strings.TrimSpace(keyFile.SecretKeyBase64) != "" {
		return keyPairFromBase64Secret(keyFile.SecretKeyBase64)
	}
	return structure.OrdexusKeyPair{}, fmt.Errorf("ed25519 keystore has no private key material")
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
		"rpc_url":           payload.RPCURL,
		"node_peer_id":      payload.NodePeerID,
		"validator_address": payload.ValidatorAddress,
		"consensus_address": payload.ConsensusAddress,
		"bls_public_key":    payload.BLSPublicKey,
		"token":             payload.Token,
	}
	if validatorPairingPayloadMode(payload) == validatorPairingModeBootstrap {
		required["bootstrap_rpc_url"] = payload.BootstrapRPCURL
		required["advertised_ip"] = payload.AdvertisedIP
		required["network"] = payload.Network
		if payload.AdvertisedPort <= 0 {
			return fmt.Errorf("validator pairing payload has invalid advertised port")
		}
		if payload.RegisteredAtUnixMS <= 0 {
			return fmt.Errorf("validator pairing payload has invalid bootstrap registration time")
		}
	} else {
		required["chain_id"] = payload.ChainID
		required["chain_identity_hash"] = payload.ChainIdentityHash
		required["genesis_hash"] = payload.GenesisHash
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

func validatorPairingPayloadMode(payload validatorPairingPayload) string {
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		return validatorPairingModeRegister
	}
	return mode
}

type bootstrapPairingRegistration struct {
	ChainID               string
	NodeName              string
	PeerID                string
	AdvertisedIP          string
	AdvertisedPort        int
	Network               string
	StakerAddress         string
	ValidatorAddress      string
	ConsensusPublicKey    string
	BLSPublicKeyBase64    string
	StakeLamports         uint64
	CommissionBps         uint16
	RegisteredAtUnixMilli int64
}

func signBootstrapPairingAuthorization(payload validatorPairingPayload, staker structure.OrdexusKeyPair, lamports uint64) (string, error) {
	blsPublicKey, err := utils.Base58Decode(strings.TrimSpace(payload.BLSPublicKey))
	if err != nil {
		return "", fmt.Errorf("decode bootstrap bls public key: %w", err)
	}
	registration := bootstrapPairingRegistration{
		ChainID:               strings.TrimSpace(payload.ChainID),
		NodeName:              strings.TrimSpace(payload.NodeName),
		PeerID:                strings.TrimSpace(payload.NodePeerID),
		AdvertisedIP:          strings.TrimSpace(payload.AdvertisedIP),
		AdvertisedPort:        payload.AdvertisedPort,
		Network:               strings.TrimSpace(payload.Network),
		StakerAddress:         staker.PublicKey.String(),
		ValidatorAddress:      strings.TrimSpace(payload.ValidatorAddress),
		ConsensusPublicKey:    strings.TrimSpace(payload.ConsensusAddress),
		BLSPublicKeyBase64:    utils.Base64Encode(blsPublicKey),
		StakeLamports:         lamports,
		RegisteredAtUnixMilli: payload.RegisteredAtUnixMS,
	}
	signature, err := staker.Sign(bootstrapPairingSignBytes(registration))
	if err != nil {
		return "", fmt.Errorf("sign bootstrap authorization: %w", err)
	}
	return signature.String(), nil
}

func bootstrapPairingSignBytes(registration bootstrapPairingRegistration) []byte {
	fields := []string{
		"pos-bootstrap-register-v1",
		strings.TrimSpace(registration.ChainID),
		strings.TrimSpace(registration.NodeName),
		strings.TrimSpace(registration.PeerID),
		strings.TrimSpace(registration.AdvertisedIP),
		strconv.Itoa(registration.AdvertisedPort),
		strings.TrimSpace(registration.Network),
		strings.TrimSpace(registration.StakerAddress),
		strings.TrimSpace(registration.ValidatorAddress),
		strings.TrimSpace(registration.ConsensusPublicKey),
		strings.TrimSpace(registration.BLSPublicKeyBase64),
		strconv.FormatUint(registration.StakeLamports, 10),
		strconv.FormatUint(uint64(registration.CommissionBps), 10),
		strconv.FormatInt(registration.RegisteredAtUnixMilli, 10),
	}
	return []byte(strings.Join(fields, "\n"))
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

func keyPairFromBase64Seed(encodedSeed string) (structure.OrdexusKeyPair, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSeed))
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("decode private key seed: %w", err)
	}
	return structure.KeyPairFromSeed(seed)
}

func keyPairFromBase64Secret(encodedSecret string) (structure.OrdexusKeyPair, error) {
	secretKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSecret))
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("decode secret key: %w", err)
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
	if header := string(data[:4]); header != "OVM1" && header != "SVM1" {
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

func waitTransactionConfirmed(rpcURL string, signature string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var detail transactionDetailResult
		if err := rpcCall(rpcURL, "getTransaction", []any{signature}, &detail); err != nil {
			return err
		}
		if detail.Found {
			switch strings.ToLower(strings.TrimSpace(detail.Status)) {
			case "confirmed":
				return nil
			case "failed", "expired":
				if strings.TrimSpace(detail.Error) != "" {
					return fmt.Errorf("transaction %s: %s", detail.Status, detail.Error)
				}
				return fmt.Errorf("transaction %s", detail.Status)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("confirmation timeout after %s", timeout)
		}
		time.Sleep(confirmationPollInterval)
	}
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
		"  wallet deploy-contract -rpc http://127.0.0.1:8899 -payer-key keys/user.json -program-key keys/program.json [-authority-key keys/authority.json] -bytecode dist/pop.ovmbin -deposit-lamports 0",
		"  wallet upgrade-contract -rpc http://127.0.0.1:8899 -payer-key keys/user.json -authority-key keys/authority.json -program PROGRAM -bytecode dist/pop.ovmbin",
		"  wallet create-contract-account -rpc http://127.0.0.1:8899 -payer-key keys/user.json -account-key keys/state.json -program PROGRAM -space 64 -deposit-lamports 0",
		"  wallet contract-call -rpc http://127.0.0.1:8899 -payer-key keys/user.json -program PROGRAM -account ADDRESS:w -data-hex 0102 [-simulate]",
		"  wallet account-info -rpc http://127.0.0.1:8899 -address ADDRESS",
		"  wallet asset-bootstrap-fungible -rpc http://127.0.0.1:8899 -payer-key keys/user.json -program PROGRAM -name POP -symbol POP -decimals 6 -initial-supply 1000000000000000",
		"  wallet rwa-bootstrap -rpc http://127.0.0.1:8899 -payer-key keys/issuer.json -program PROGRAM -type share|nft -name NAME -symbol SYMBOL -max-supply 1000 -initial-supply 100 -document-hash SHA256 -identity-hash SHA256",
		"  wallet validator-register -rpc http://127.0.0.1:8899 -staker-key keys/staker.json -validator-address ADDRESS -consensus-address ADDRESS -bls-public-key KEY -peer-id PEER -lamports 10000000",
		"  wallet validator-pair -payload PAYLOAD -staker-key keys/staker.json -lamports 10000000",
		"  wallet validator-pair -payload PAYLOAD -staker-key keys/staker.json -signature SIGNATURE",
		"  wallet stake -rpc http://127.0.0.1:8899 -staker-key keys/staker.json -validator-address ADDRESS -lamports 10000000",
		"  wallet delegate -rpc http://127.0.0.1:8899 -key keys/user.json -validator-address ADDRESS -lamports 10000000",
		"  wallet undelegate -rpc http://127.0.0.1:8899 -key keys/user.json -validator-address ADDRESS -lamports 10000000 -unlock-epoch 10",
		"  wallet withdraw-delegation -rpc http://127.0.0.1:8899 -key keys/user.json -validator-address ADDRESS -current-epoch 10",
		"  wallet emergency-recovery-create -rpc http://127.0.0.1:8899 -sequence 1 -elder-validator-id ID -exclude-validator-ids ID1,ID2 -consensus-key keys/consensus-1.json -out recovery.json",
		"  wallet emergency-recovery-sign -certificate-file recovery.json -consensus-key keys/consensus-2.json -out recovery-signed.json",
		"  wallet emergency-recovery-submit -rpc http://127.0.0.1:8899 -certificate-file recovery-signed.json",
		"  wallet multi-validator-activation-create -rpc http://127.0.0.1:8899 -sequence 2 -consensus-key keys/consensus-1.json -out activation.json",
		"  wallet multi-validator-activation-sign -certificate-file activation.json -consensus-key keys/consensus-2.json -out activation-signed.json",
		"  wallet multi-validator-activation-submit -rpc http://127.0.0.1:8899 -certificate-file activation-signed.json",
		"",
		"minimum stake lamports: " + strconv.FormatUint(minimumStakeLamports, 10),
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}
