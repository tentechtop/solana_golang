package posnode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
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
	validatorPairingQRImageSize   = 512
	validatorPairingModeRegister  = "validator_registration"
	validatorPairingModeBootstrap = "bootstrap_join"
)

type validatorPairingSession struct {
	mutex           sync.Mutex
	payload         validatorPairingPayload
	tokenHash       string
	state           string
	qrText          string
	qrHTMLPath      string
	qrImagePath     string
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
		bootstrapRPCURL = strings.TrimSpace(node.config.BootstrapJoin.RPCURL)
		advertisedIP, advertisedPort = validatorPairingAdvertisedEndpoint(node.config)
		network = string(utils.ProtocolTCP)
		registeredAtUnixMS = time.Now().UnixMilli()
		chainID = ""
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
	qrImagePath := validatorPairingQRImagePath(node.config)
	qrImageBytes, err := writeValidatorPairingQRImage(qrCode, qrImagePath)
	if err != nil {
		return nil, err
	}
	qrHTMLPath := validatorPairingQRHTMLPath(node.config)
	if err := writeValidatorPairingQRHTML(qrHTMLPath, qrImageBytes, qrText, payload); err != nil {
		return nil, err
	}
	return &validatorPairingSession{
		payload:       payload,
		tokenHash:     utils.Base64RawEncode(utils.SHA256([]byte(token))),
		state:         "pending",
		qrText:        qrText,
		qrHTMLPath:    qrHTMLPath,
		qrImagePath:   qrImagePath,
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

func validatorPairingQRImagePath(config nodeConfig) string {
	return filepath.Join(validatorPairingKeystoreDir(config), "pairing-qr.png")
}

func validatorPairingQRHTMLPath(config nodeConfig) string {
	return filepath.Join(validatorPairingKeystoreDir(config), "pairing-qr.html")
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

func writeValidatorPairingQRImage(qrCode *qrcode.QRCode, imagePath string) ([]byte, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(imagePath))
	if cleanPath == "" || cleanPath == "." {
		return nil, fmt.Errorf("posnode: validator pairing qr image path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return nil, fmt.Errorf("posnode: create validator pairing qr dir: %w", err)
	}
	pngBytes, err := qrCode.PNG(validatorPairingQRImageSize)
	if err != nil {
		return nil, fmt.Errorf("posnode: encode validator pairing qr image: %w", err)
	}
	if err := os.WriteFile(cleanPath, pngBytes, 0o600); err != nil {
		return nil, fmt.Errorf("posnode: write validator pairing qr image: %w", err)
	}
	return pngBytes, nil
}

func writeValidatorPairingQRHTML(htmlPath string, qrImageBytes []byte, qrText string, payload validatorPairingPayload) error {
	cleanPath := filepath.Clean(strings.TrimSpace(htmlPath))
	if cleanPath == "" || cleanPath == "." {
		return fmt.Errorf("posnode: validator pairing qr html path is empty")
	}
	if len(qrImageBytes) == 0 {
		return fmt.Errorf("posnode: validator pairing qr image is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return fmt.Errorf("posnode: create validator pairing qr html dir: %w", err)
	}
	htmlText := buildValidatorPairingQRHTML(qrImageBytes, qrText, payload)
	if err := os.WriteFile(cleanPath, []byte(htmlText), 0o600); err != nil {
		return fmt.Errorf("posnode: write validator pairing qr html: %w", err)
	}
	return nil
}

func buildValidatorPairingQRHTML(qrImageBytes []byte, qrText string, payload validatorPairingPayload) string {
	// 功能目的：生成本地扫码页面；实现原因：浏览器展示比终端字符二维码更稳定。
	imageBase64 := base64.StdEncoding.EncodeToString(qrImageBytes)
	expiresAt := time.UnixMilli(payload.ExpiresAtUnixMS).Format(time.RFC3339)
	var builder strings.Builder
	builder.WriteString("<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	builder.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	builder.WriteString("<title>POSNODE 验证者钱包绑定</title>")
	builder.WriteString("<style>")
	builder.WriteString("body{margin:0;font-family:Arial,'Microsoft YaHei',sans-serif;background:#f5f7fb;color:#111827;}")
	builder.WriteString(".page{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:32px;box-sizing:border-box;}")
	builder.WriteString(".panel{width:min(760px,100%);background:#fff;border:1px solid #d9e1ee;border-radius:8px;padding:28px;box-shadow:0 10px 30px rgba(15,23,42,.10);}")
	builder.WriteString("h1{margin:0 0 8px;font-size:26px;line-height:1.25;}p{margin:0 0 18px;color:#4b5563;line-height:1.6;}")
	builder.WriteString(".qr{display:flex;justify-content:center;margin:20px 0;}.qr img{width:min(520px,86vw);height:auto;image-rendering:pixelated;}")
	builder.WriteString(".meta{display:grid;grid-template-columns:140px 1fr;gap:10px 14px;margin:18px 0;font-size:14px;}.label{color:#6b7280;}.value{word-break:break-all;}")
	builder.WriteString("textarea{width:100%;min-height:96px;box-sizing:border-box;border:1px solid #cbd5e1;border-radius:6px;padding:10px;font-size:12px;line-height:1.5;color:#1f2937;}")
	builder.WriteString(".warning{margin-top:14px;color:#92400e;background:#fffbeb;border:1px solid #fde68a;border-radius:6px;padding:10px;font-size:14px;}")
	builder.WriteString("</style></head><body><main class=\"page\"><section class=\"panel\">")
	builder.WriteString("<h1>POSNODE 验证者钱包绑定</h1>")
	builder.WriteString("<p>请用 APP 扫描二维码，自动创建或选择验证者质押钱包并完成节点绑定。</p>")
	builder.WriteString("<div class=\"qr\"><img alt=\"验证者钱包绑定二维码\" src=\"data:image/png;base64,")
	builder.WriteString(imageBase64)
	builder.WriteString("\"></div><div class=\"meta\">")
	builder.WriteString("<div class=\"label\">节点名称</div><div class=\"value\">")
	builder.WriteString(html.EscapeString(payload.NodeName))
	builder.WriteString("</div><div class=\"label\">RPC 地址</div><div class=\"value\">")
	builder.WriteString(html.EscapeString(payload.RPCURL))
	builder.WriteString("</div><div class=\"label\">节点身份</div><div class=\"value\">")
	builder.WriteString(html.EscapeString(payload.NodePeerID))
	builder.WriteString("</div><div class=\"label\">有效期</div><div class=\"value\">")
	builder.WriteString(html.EscapeString(expiresAt))
	builder.WriteString("</div></div>")
	builder.WriteString("<textarea readonly>")
	builder.WriteString(html.EscapeString(qrText))
	builder.WriteString("</textarea>")
	builder.WriteString("<div class=\"warning\">二维码和 payload 只用于本次节点绑定，不要发送给陌生人。</div>")
	builder.WriteString("</section></main></body></html>")
	return builder.String()
}

func printValidatorPairingQR(session *validatorPairingSession) {
	var output bytes.Buffer
	qrHTMLURI := localFileURI(session.qrHTMLPath)
	output.WriteString("\nPOSNODE 验证者钱包绑定\n")
	output.WriteString("浏览器扫码页面已生成，请用 APP 扫码绑定验证者钱包。\n")
	if qrHTMLURI != "" {
		output.WriteString("扫码地址: ")
		output.WriteString(qrHTMLURI)
		output.WriteString("\n")
	}
	output.WriteString("扫码页面文件: ")
	output.WriteString(session.qrHTMLPath)
	output.WriteString("\n")
	output.WriteString("PowerShell 打开页面: Invoke-Item -LiteralPath \"")
	output.WriteString(session.qrHTMLPath)
	output.WriteString("\"\n")
	output.WriteString("二维码文件: ")
	output.WriteString(session.qrImagePath)
	output.WriteString("\n")
	output.WriteString("如果 APP 暂时无法扫码，请复制下面的 payload 执行 wallet validator-pair。\n")
	output.WriteString(session.qrText)
	output.WriteString("\n\n")
	fmt.Fprint(os.Stdout, output.String())
}

func localFileURI(path string) string {
	// 功能目的：把本地扫码页面转成浏览器地址；实现原因：控制台需要直接给用户可打开的扫码入口。
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "" || cleanPath == "." {
		return ""
	}
	absolutePath, err := filepath.Abs(cleanPath)
	if err == nil {
		cleanPath = absolutePath
	}
	slashPath := filepath.ToSlash(cleanPath)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
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
	activationStarted := node.shouldAutoActivatePairedValidator(configUpdated, session)
	state := "completed"
	restartRequired := true
	activationNote := "validator activates at the next epoch after the registration transaction is finalized"
	if activationStarted {
		state = "activating"
		restartRequired = false
		activationNote = "validator wallet paired; node activation starts automatically"
	}
	result := rpc.ValidatorPairingCompleteResult{
		State:                    state,
		StakerAddress:            strings.TrimSpace(request.StakerAddress),
		ValidatorAddress:         session.payload.ValidatorAddress,
		ConsensusAddress:         session.payload.ConsensusAddress,
		BLSPublicKey:             session.payload.BLSPublicKey,
		NodePeerID:               session.payload.NodePeerID,
		StakeLamports:            request.StakeLamports,
		Signature:                strings.TrimSpace(request.Signature),
		BootstrapStakerSignature: strings.TrimSpace(request.BootstrapStakerSignature),
		ConfigUpdated:            configUpdated,
		RestartRequired:          restartRequired,
		ActivationStarted:        activationStarted,
		ConfigPath:               node.config.ConfigPath,
		ActivationNote:           activationNote,
	}
	if isBootstrapPairingPayload(session.payload) && !activationStarted {
		result.ActivationNote = "validator joins bootstrap after restart; block production starts after the bootstrap threshold is reached"
	}
	session.state = state
	session.completedResult = result
	node.logger.Info("validator wallet pairing completed",
		slog.String("staker", result.StakerAddress),
		slog.String("validator_address", result.ValidatorAddress),
		slog.String("signature", result.Signature),
		slog.Bool("config_updated", result.ConfigUpdated),
		slog.Bool("activation_started", result.ActivationStarted),
	)
	if activationStarted {
		node.startPairedValidatorActivation(session)
	}
	return result, nil
}

// shouldAutoActivatePairedValidator 判断是否可热启动 + 只有初次 bootstrap 扫码进程具备安全的运行中激活边界。
func (node *posNode) shouldAutoActivatePairedValidator(configUpdated bool, session *validatorPairingSession) bool {
	if !configUpdated || session == nil {
		return false
	}
	if node.runtimeContext == nil || !node.config.bootstrapPairingPending() {
		return false
	}
	return isBootstrapPairingPayload(session.payload)
}

// startPairedValidatorActivation 后台激活验证者 + 避免 APP 请求等待 bootstrap 和账本初始化。
func (node *posNode) startPairedValidatorActivation(session *validatorPairingSession) {
	node.mutex.Lock()
	if node.pairingActivationRunning || node.ledger != nil {
		node.mutex.Unlock()
		return
	}
	node.pairingActivationRunning = true
	ctx := node.runtimeContext
	node.mutex.Unlock()

	go func() {
		err := node.activatePairedValidatorRuntime(ctx)
		node.finishPairedValidatorActivation(session, err)
	}()
}

// finishPairedValidatorActivation 写入激活结果 + APP 轮询时能看到明确状态和错误原因。
func (node *posNode) finishPairedValidatorActivation(session *validatorPairingSession, activationError error) {
	node.mutex.Lock()
	node.pairingActivationRunning = false
	node.mutex.Unlock()

	session.mutex.Lock()
	defer session.mutex.Unlock()
	result := session.completedResult
	if activationError != nil {
		session.state = "activation_failed"
		result.State = session.state
		result.RestartRequired = true
		result.ActivationError = activationError.Error()
		result.ActivationNote = "validator activation failed; fix the reported error and restart the node"
		session.completedResult = result
		node.logger.Error("validator wallet pairing activation failed", slog.Any("error", activationError))
		return
	}
	session.state = "completed"
	result.State = session.state
	result.RestartRequired = false
	result.ActivationError = ""
	result.ActivationNote = "validator node started automatically after wallet pairing"
	session.completedResult = result
	node.logger.Info("validator wallet pairing activation completed",
		slog.String("staker", result.StakerAddress),
		slog.String("validator_address", result.ValidatorAddress),
		slog.String("node_peer_id", result.NodePeerID),
	)
}

// activatePairedValidatorRuntime 加载已绑定配置并启动节点 + 扫码进程转为真实验证者进程。
func (node *posNode) activatePairedValidatorRuntime(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := loadNodeConfig(node.config.ConfigPath)
	if err != nil {
		return fmt.Errorf("posnode: load paired validator config: %w", err)
	}
	config, err = prepareBootstrapJoinConfig(ctx, config, node.logger)
	if err != nil {
		return fmt.Errorf("posnode: prepare paired validator bootstrap: %w", err)
	}
	activatedNode, err := newPosNode(config, node.logger)
	if err != nil {
		return fmt.Errorf("posnode: initialize paired validator runtime: %w", err)
	}
	if err := node.adoptPairedValidatorRuntime(activatedNode); err != nil {
		activatedNode.closeRuntimeResources()
		return err
	}
	if err := node.startP2P(ctx); err != nil {
		return fmt.Errorf("posnode: start paired validator p2p: %w", err)
	}
	if node.config.AutoRegister && node.config.validatorEnabled() {
		go node.autoRegisterLoop(ctx)
	}
	node.startLedgerWorkers(ctx)
	node.logger.Info("paired validator runtime started",
		slog.String("chain_id", node.config.ChainID),
		slog.String("chain_identity_hash", node.config.ChainIdentityHash),
		slog.String("genesis_hash", node.config.GenesisHash),
		slog.String("peer_id", node.peerKeyPair.peerID),
		slog.String("staker", node.stakerAddress.String()),
		slog.String("validator", node.validatorKeyPair.PublicKey.String()),
	)
	return nil
}

// adoptPairedValidatorRuntime 接管初始化资源 + 保留原 RPC 服务和配对会话不中断 APP 轮询。
func (node *posNode) adoptPairedValidatorRuntime(activatedNode *posNode) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.ledger != nil || node.host != nil {
		return fmt.Errorf("posnode: validator runtime is already started")
	}
	node.config = activatedNode.config
	node.startedAt = time.Now()
	node.bootstrapCoordinator = activatedNode.bootstrapCoordinator
	node.db = activatedNode.db
	node.ledger = activatedNode.ledger
	node.executor = activatedNode.executor
	node.peerKeyPair = activatedNode.peerKeyPair
	node.stakerAddress = activatedNode.stakerAddress
	node.stakerKeyPair = activatedNode.stakerKeyPair
	node.validatorKeyPair = activatedNode.validatorKeyPair
	node.consensusKeyPair = activatedNode.consensusKeyPair
	node.blsKeyPair = activatedNode.blsKeyPair
	node.blockhashQueue = activatedNode.blockhashQueue
	node.mempool = activatedNode.mempool
	node.seenTransactions = activatedNode.seenTransactions
	node.rejectedTransactions = activatedNode.rejectedTransactions
	node.seenProposals = activatedNode.seenProposals
	node.seenQCs = activatedNode.seenQCs
	node.pendingEvidence = activatedNode.pendingEvidence
	node.seenEvidence = activatedNode.seenEvidence
	node.proposalChoices = activatedNode.proposalChoices
	node.signedVoteChoices = activatedNode.signedVoteChoices
	node.voteEnvelopeChoices = activatedNode.voteEnvelopeChoices
	node.orphanProposals = activatedNode.orphanProposals
	node.epochSnapshot = activatedNode.epochSnapshot
	node.leaderSchedule = activatedNode.leaderSchedule
	node.voteCollector = activatedNode.voteCollector
	node.voteCollectors = activatedNode.voteCollectors
	node.epochSnapshots = activatedNode.epochSnapshots
	node.leaderSchedules = activatedNode.leaderSchedules
	node.metrics = activatedNode.metrics
	node.lastProducedSlot = activatedNode.lastProducedSlot
	node.lastVotedSlot = activatedNode.lastVotedSlot
	node.livenessGate = activatedNode.livenessGate
	node.registeredSelf = activatedNode.registeredSelf
	node.knownPeerIDs = activatedNode.knownPeerIDs
	node.bootstrapManifestApplied = activatedNode.bootstrapManifestApplied
	node.rpcForwardFanoutLimiter = activatedNode.rpcForwardFanoutLimiter
	node.doubleVoteInjected = activatedNode.doubleVoteInjected
	node.doubleProposalInjected = activatedNode.doubleProposalInjected
	return nil
}

// closeRuntimeResources 释放未接管资源 + 激活失败时防止临时账本和网络句柄泄漏。
func (node *posNode) closeRuntimeResources() {
	if node.host != nil {
		_ = node.host.Close()
	}
	if node.db != nil {
		_ = node.db.Close()
	}
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
		delete(configMap, "chain_id")
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
