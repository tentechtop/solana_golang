package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"solana_golang/database"
	"solana_golang/utils"
)

const (
	DefaultPath = "config/local/config.yaml"

	defaultRPCAddress      = ":8899"
	defaultRPCMaxBodyBytes = int64(1 << 20)
	defaultRPCMaxBatchSize = 32
	defaultLogLevel        = "info"
	defaultLogFormat       = utils.LogFormatJSON
	defaultLogOutput       = utils.LogOutputConsole
	defaultDatabaseEngine  = database.EnginePebble
	defaultDatabasePath    = "./data/pebble"
	defaultP2PIPType       = utils.MultiAddressIP4
	defaultP2PListenIP     = "0.0.0.0"
	defaultP2PListenPort   = 5002
	defaultP2PProtocol     = utils.ProtocolTCP
	defaultP2PNetworkID    = "solana_golang:localnet:00000000000000000000000000000000"
	defaultP2PSoftware     = "solana_golang/0.1.0"
)

// AppConfig 保存启动配置 + 集中声明外部可调参数避免散落读取。
type AppConfig struct {
	RPC      RPCConfig      `yaml:"rpc"`
	Log      LogConfig      `yaml:"log"`
	Database DatabaseConfig `yaml:"database"`
	P2P      P2PConfig      `yaml:"p2p"`
}

// RPCConfig 保存 JSON-RPC 配置 + 限制入口资源防止请求放大。
type RPCConfig struct {
	Address      string `yaml:"address"`
	MaxBodyBytes int64  `yaml:"max_body_bytes"`
	MaxBatchSize int    `yaml:"max_batch_size"`
}

// LogConfig 保存日志配置 + 支持控制台和文件两种生产常用输出。
type LogConfig struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	AddSource bool   `yaml:"add_source"`
	Output    string `yaml:"output"`
	FilePath  string `yaml:"file_path"`
}

// DatabaseConfig 保存数据库配置 + 支持启动时选择底层存储引擎。
type DatabaseConfig struct {
	Engine string `yaml:"engine"`
	Path   string `yaml:"path"`
	WAL    bool   `yaml:"wal"`
}

// P2PConfig 保存 P2P 配置 + 用显式字段构造安全 multi-address。
type P2PConfig struct {
	IPType                 string   `yaml:"ip_type"`
	ListenIP               string   `yaml:"listen_ip"`
	ListenPort             int      `yaml:"listen_port"`
	DefaultProtocol        string   `yaml:"default_protocol"`
	MaxPeers               int      `yaml:"max_peers"`
	NetworkID              string   `yaml:"network_id"`
	SoftwareVersion        string   `yaml:"software_version"`
	Bootstrap              []string `yaml:"bootstrap"`
	MinOutboundPeers       int      `yaml:"min_outbound_peers"`
	BootstrapTimeoutMillis int      `yaml:"bootstrap_timeout_millis"`
}

// Load 读取配置文件 + 使用严格 YAML 解析提前发现拼写错误。
func Load(path string) (AppConfig, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath
	}
	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return AppConfig{}, err
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return AppConfig{}, fmt.Errorf("config: open %s: %w", resolvedPath, err)
	}
	defer file.Close()

	config := Default()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return AppConfig{}, fmt.Errorf("config: decode %s: %w", resolvedPath, err)
	}
	if err := config.Validate(); err != nil {
		return AppConfig{}, fmt.Errorf("config: validate %s: %w", resolvedPath, err)
	}
	return config, nil
}

// Default 构造默认配置 + 保证脚本直接运行时有完整启动参数。
func Default() AppConfig {
	return AppConfig{
		RPC: RPCConfig{
			Address:      defaultRPCAddress,
			MaxBodyBytes: defaultRPCMaxBodyBytes,
			MaxBatchSize: defaultRPCMaxBatchSize,
		},
		Log: LogConfig{
			Level:    defaultLogLevel,
			Format:   defaultLogFormat,
			Output:   defaultLogOutput,
			FilePath: "./logs/solana_golang.log",
		},
		Database: DatabaseConfig{
			Engine: string(defaultDatabaseEngine),
			Path:   defaultDatabasePath,
			WAL:    true,
		},
		P2P: P2PConfig{
			IPType:                 defaultP2PIPType,
			ListenIP:               defaultP2PListenIP,
			ListenPort:             defaultP2PListenPort,
			DefaultProtocol:        string(defaultP2PProtocol),
			MaxPeers:               64,
			NetworkID:              defaultP2PNetworkID,
			SoftwareVersion:        defaultP2PSoftware,
			MinOutboundPeers:       8,
			BootstrapTimeoutMillis: 5_000,
		},
	}
}

// Validate 校验配置边界 + 阻断无效配置进入启动路径。
func (config AppConfig) Validate() error {
	if err := config.RPC.Validate(); err != nil {
		return err
	}
	if err := config.Log.Validate(); err != nil {
		return err
	}
	if err := config.Database.Validate(); err != nil {
		return err
	}
	return config.P2P.Validate()
}

// Validate 校验 RPC 配置 + 防止无监听地址和无界请求体。
func (config RPCConfig) Validate() error {
	if strings.TrimSpace(config.Address) == "" {
		return errors.New("rpc address cannot be empty")
	}
	if config.MaxBodyBytes <= 0 {
		return errors.New("rpc max_body_bytes must be positive")
	}
	if config.MaxBatchSize <= 0 {
		return errors.New("rpc max_batch_size must be positive")
	}
	return nil
}

// Validate 校验日志配置 + 拒绝未知输出目标和格式。
func (config LogConfig) Validate() error {
	if _, err := utils.ParseLogLevel(config.Level); err != nil {
		return err
	}
	if _, err := utils.ParseLogFormat(config.Format); err != nil {
		return err
	}
	output := strings.ToLower(strings.TrimSpace(config.Output))
	if output == "" || output == utils.LogOutputConsole {
		return nil
	}
	if output != utils.LogOutputFile {
		return fmt.Errorf("log output must be console or file, got %q", config.Output)
	}
	if strings.TrimSpace(config.FilePath) == "" {
		return errors.New("log file_path cannot be empty when output is file")
	}
	return nil
}

// Validate 校验数据库配置 + 限制存储引擎白名单。
func (config DatabaseConfig) Validate() error {
	if strings.TrimSpace(config.Path) == "" {
		return errors.New("database path cannot be empty")
	}
	switch database.EngineType(strings.ToLower(strings.TrimSpace(config.Engine))) {
	case database.EnginePebble, database.EngineLevelDB:
		return nil
	default:
		return fmt.Errorf("database engine must be pebble or leveldb, got %q", config.Engine)
	}
}

// Validate 校验 P2P 配置 + 保证协议、端口和节点身份可用。
func (config P2PConfig) Validate() error {
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return fmt.Errorf("p2p listen_port out of range 1-65535: %d", config.ListenPort)
	}
	if config.MaxPeers <= 0 {
		return errors.New("p2p max_peers must be positive")
	}
	if _, err := utils.ParseMultiAddressProtocol(config.DefaultProtocol); err != nil {
		return err
	}
	if err := validateP2PListenAddress(config.IPType, config.ListenIP); err != nil {
		return err
	}
	if strings.TrimSpace(config.NetworkID) == "" {
		return errors.New("p2p network_id cannot be empty")
	}
	if strings.TrimSpace(config.SoftwareVersion) == "" {
		return errors.New("p2p software_version cannot be empty")
	}
	if config.MinOutboundPeers < 0 {
		return errors.New("p2p min_outbound_peers cannot be negative")
	}
	if config.MinOutboundPeers > config.MaxPeers {
		return errors.New("p2p min_outbound_peers cannot exceed max_peers")
	}
	if config.BootstrapTimeoutMillis <= 0 {
		return errors.New("p2p bootstrap_timeout_millis must be positive")
	}
	for _, rawAddress := range config.Bootstrap {
		if _, err := utils.ParseMultiAddress(rawAddress); err != nil {
			return fmt.Errorf("p2p bootstrap address %q: %w", rawAddress, err)
		}
	}
	return nil
}

func (config P2PConfig) Protocol() utils.MultiAddressProtocol {
	protocol, err := utils.ParseMultiAddressProtocol(config.DefaultProtocol)
	if err != nil {
		return defaultP2PProtocol
	}
	return protocol
}

func (config P2PConfig) BootstrapAddresses() ([]utils.MultiAddress, error) {
	addresses := make([]utils.MultiAddress, 0, len(config.Bootstrap))
	for _, rawAddress := range config.Bootstrap {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return nil, fmt.Errorf("p2p bootstrap address %q: %w", rawAddress, err)
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

// DatabaseOptions 转换数据库配置 + 隔离 YAML 字段和数据库包类型。
func (config DatabaseConfig) DatabaseOptions() database.DatabaseConfig {
	return database.DatabaseConfig{
		Engine: database.EngineType(strings.ToLower(strings.TrimSpace(config.Engine))),
		Path:   config.Path,
		WAL:    config.WAL,
	}
}
func resolveConfigPath(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) || fileExists(cleanPath) {
		return cleanPath, nil
	}

	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("config: get working directory: %w", err)
	}
	for {
		candidate := filepath.Join(directory, cleanPath)
		if fileExists(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return cleanPath, nil
}
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func validateP2PListenAddress(ipType string, listenIP string) error {
	switch strings.ToLower(strings.TrimSpace(ipType)) {
	case utils.MultiAddressIP4:
		if ip := net.ParseIP(listenIP); ip == nil || ip.To4() == nil {
			return fmt.Errorf("p2p listen_ip must be valid IPv4: %q", listenIP)
		}
		return nil
	case utils.MultiAddressIP6:
		if ip := net.ParseIP(listenIP); ip == nil || ip.To4() != nil || ip.To16() == nil {
			return fmt.Errorf("p2p listen_ip must be valid IPv6: %q", listenIP)
		}
		return nil
	default:
		return fmt.Errorf("p2p ip_type must be ip4 or ip6, got %q", ipType)
	}
}
