package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	appconfig "solana_golang/config"
	"solana_golang/database"
	"solana_golang/p2p"
	"solana_golang/rpc"
	"solana_golang/utils"
)

const shutdownTimeout = 10 * time.Second

// runtimeResources 保存进程级运行资源 + 统一生命周期关闭顺序避免泄漏。
type runtimeResources struct {
	logger       *slog.Logger
	logCloser    io.Closer
	database     database.Database
	p2pHost      *p2p.Host
	rpcServer    *rpc.Server
	p2pCancel    context.CancelFunc
	serverErrors chan error
}

func main() {
	if err := run(); err != nil {
		slog.Error("application exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run 执行应用主流程 + 串联配置加载、资源启动和优雅停机。
func run() error {
	configPath := configPathFromFlag()
	config, err := appconfig.Load(configPath)
	if err != nil {
		return err
	}

	resources, err := startRuntime(config)
	if err != nil {
		return err
	}
	defer resources.closeLog()

	resources.logger.Info("application started",
		slog.String("config_path", configPath),
		slog.String("database_engine", config.Database.Engine),
		slog.String("p2p_protocol", config.P2P.DefaultProtocol),
		slog.String("rpc_address", config.RPC.Address),
	)

	err = waitForStop(resources)
	if closeErr := resources.close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

// configPathFromFlag 解析配置路径 + 按命令行、环境变量、默认值顺序降级。
func configPathFromFlag() string {
	configPath := flag.String("config", "", "config file path")
	flag.Parse()
	if strings.TrimSpace(*configPath) != "" {
		return *configPath
	}
	if path := strings.TrimSpace(os.Getenv("APP_CONFIG")); path != "" {
		return path
	}
	return appconfig.DefaultPath
}

// startRuntime 启动核心运行时资源 + 按日志、数据库、P2P、RPC 顺序建立依赖。
func startRuntime(config appconfig.AppConfig) (*runtimeResources, error) {
	logger, logCloser, err := newConfiguredLogger(config.Log)
	if err != nil {
		return nil, err
	}
	resources := &runtimeResources{
		logger:       logger,
		logCloser:    logCloser,
		serverErrors: make(chan error, 2),
	}

	if err := registerSerializationSchemas(); err != nil {
		resources.closeLog()
		return nil, err
	}
	if err := resources.openDatabase(config.Database); err != nil {
		resources.closeLog()
		return nil, err
	}
	identity, err := resources.ensureNodeIdentity()
	if err != nil {
		_ = resources.close()
		return nil, err
	}
	if err := resources.startP2P(config.P2P, identity); err != nil {
		_ = resources.close()
		return nil, err
	}
	resources.startRPC(config.RPC)
	return resources, nil
}

// registerSerializationSchemas 注册核心序列化 schema + 缺失协议解码器时禁止节点启动。
func registerSerializationSchemas() error {
	if err := p2p.RegisterP2PMessageSchema(nil); err != nil {
		return fmt.Errorf("cmd: register p2p message schema: %w", err)
	}
	return nil
}

// newConfiguredLogger 初始化日志器 + 支持控制台和文件输出并返回文件关闭器。
func newConfiguredLogger(config appconfig.LogConfig) (*slog.Logger, io.Closer, error) {
	var output io.Writer = os.Stdout
	var closer io.Closer
	if strings.EqualFold(strings.TrimSpace(config.Output), utils.LogOutputFile) {
		file, err := utils.OpenLogFile(config.FilePath)
		if err != nil {
			return nil, nil, err
		}
		output = file
		closer = file
	}

	logger, err := utils.InitDefaultLogger(utils.LoggerConfig{
		Level:     config.Level,
		Format:    config.Format,
		AddSource: config.AddSource,
		Output:    output,
	})
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, err
	}
	return logger, closer, nil
}

// openDatabase 打开并健康检查数据库 + 失败时补充上下文并释放半初始化资源。
func (resources *runtimeResources) openDatabase(config appconfig.DatabaseConfig) error {
	databaseInstance, err := database.NewDatabase(config.DatabaseOptions())
	if err != nil {
		return fmt.Errorf("cmd: open database: %w", err)
	}
	if err := databaseInstance.CheckHealth(); err != nil {
		_ = databaseInstance.Close()
		return fmt.Errorf("cmd: check database health: %w", err)
	}
	resources.database = databaseInstance
	resources.logger.Info("database opened",
		slog.String("engine", config.Engine),
		slog.String("path", config.Path),
		slog.Bool("wal", config.WAL),
	)
	return nil
}

// startP2P 启动 P2P 监听 + 使用独立上下文控制网络协程退出。
func (resources *runtimeResources) startP2P(config appconfig.P2PConfig, identity nodeIdentity) error {
	host, err := p2p.NewHost(p2p.HostConfig{
		PeerID: identity.PeerID,
		SecureIdentity: p2p.SecureSessionIdentity{
			PeerID:             identity.PeerID,
			PublicKey:          identity.PublicKey,
			PrivateKey:         identity.PrivateKey,
			NetworkID:          config.NetworkID,
			SoftwareVersion:    config.SoftwareVersion,
			MinProtocolVersion: p2p.MessageProtocolVersion,
			MaxProtocolVersion: p2p.MessageProtocolVersion,
		},
		EnableSecureSession: true,
		PreferredProtocols:  p2pProtocolOrder(config.Protocol()),
		MaxPeers:            config.MaxPeers,
		Logger:              resources.logger,
		PeerStore:           newDatabasePeerStore(resources.database),
		PersistedPeerLimit:  config.MaxPeers,
	})
	if err != nil {
		return fmt.Errorf("cmd: create p2p host: %w", err)
	}
	restoredPeers, err := host.LoadStoredPeers(context.Background(), config.MaxPeers)
	if err != nil {
		_ = host.Close()
		return fmt.Errorf("cmd: load stored p2p peers: %w", err)
	}

	address, err := utils.BuildMultiAddress(config.IPType, config.ListenIP, config.Protocol(), config.ListenPort, identity.PeerID)
	if err != nil {
		_ = host.Close()
		return fmt.Errorf("cmd: build p2p listen address: %w", err)
	}
	bootnodes, err := config.BootstrapAddresses()
	if err != nil {
		_ = host.Close()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	resources.p2pHost = host
	resources.p2pCancel = cancel
	go func() {
		err := host.Listen(ctx, address, resources.handleP2PConnection)
		if err != nil {
			resources.serverErrors <- fmt.Errorf("cmd: p2p listen: %w", err)
		}
	}()
	go host.StartHeartbeat(ctx)
	go resources.bootstrapP2P(ctx, config, bootnodes)
	resources.logger.Info("p2p listener starting",
		slog.String("address", address.String()),
		slog.Int("restored_peers", restoredPeers),
	)
	return nil
}

// bootstrapP2P 执行节点发现启动流程 + 失败只记录日志避免外部 bootnode 影响本地监听。
func (resources *runtimeResources) bootstrapP2P(ctx context.Context, config appconfig.P2PConfig, bootnodes []utils.MultiAddress) {
	if resources.p2pHost == nil || len(bootnodes) == 0 {
		return
	}
	summary, err := resources.p2pHost.Bootstrap(ctx, p2p.BootstrapConfig{
		Bootnodes:            bootnodes,
		MinOutboundPeers:     config.MinOutboundPeers,
		QueryLimit:           20,
		RefreshTargetCount:   8,
		DialTimeout:          time.Duration(config.BootstrapTimeoutMillis) * time.Millisecond,
		StartConnectionLoops: true,
	})
	if err != nil {
		resources.logger.Warn("p2p bootstrap finished with errors",
			slog.Int("bootnodes", summary.BootnodeCount),
			slog.Int("connected_bootnodes", summary.ConnectedBootnodes),
			slog.Int("discovered_peers", summary.DiscoveredPeers),
			slog.Int("connected_peers", summary.ConnectedPeers),
			slog.Any("error", err),
		)
		return
	}
	resources.logger.Info("p2p bootstrap finished",
		slog.Int("bootnodes", summary.BootnodeCount),
		slog.Int("connected_bootnodes", summary.ConnectedBootnodes),
		slog.Int("discovered_peers", summary.DiscoveredPeers),
		slog.Int("connected_peers", summary.ConnectedPeers),
	)
}

// handleP2PConnection 处理入站连接消息 + 持续读取并交给协议注册表分发。
func (resources *runtimeResources) handleP2PConnection(ctx context.Context, connection p2p.Connection) {
	if resources.p2pHost == nil {
		_ = connection.Close()
		resources.logger.Warn("p2p host missing", slog.String("connection_id", connection.ID()))
		return
	}
	resources.p2pHost.HandleConnection(ctx, connection)
}

// startRPC 启动 JSON-RPC 服务 + 将监听错误汇聚到主协程统一处理。
func (resources *runtimeResources) startRPC(config appconfig.RPCConfig) {
	resources.rpcServer = rpc.NewServer(rpc.ServerConfig{
		Address:      config.Address,
		MaxBodyBytes: config.MaxBodyBytes,
		MaxBatchSize: config.MaxBatchSize,
		Logger:       resources.logger,
	}, rpc.NewDefaultRouter(nil))
	go func() {
		if err := resources.rpcServer.ListenAndServe(); err != nil {
			resources.serverErrors <- err
		}
	}()
}

// waitForStop 等待停机事件 + 同时监听系统信号和后台服务错误。
func waitForStop(resources *runtimeResources) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case signalValue := <-signals:
		resources.logger.Info("shutdown signal received", slog.String("signal", signalValue.String()))
		return nil
	case err := <-resources.serverErrors:
		return err
	}
}

// close 按依赖逆序关闭资源 + 聚合错误保证所有资源都有机会释放。
func (resources *runtimeResources) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var closeErrors []error
	if resources.p2pCancel != nil {
		resources.p2pCancel()
	}
	if resources.rpcServer != nil {
		if err := resources.rpcServer.Shutdown(ctx); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	if resources.p2pHost != nil {
		if err := resources.p2pHost.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	if resources.database != nil {
		if err := resources.database.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errorsJoin(closeErrors)
}

// closeLog 关闭日志文件句柄 + 置空引用避免重复关闭。
func (resources *runtimeResources) closeLog() {
	if resources.logCloser != nil {
		_ = resources.logCloser.Close()
		resources.logCloser = nil
	}
}

// errorsJoin 合并关闭错误 + 封装标准库实现便于测试覆盖。
func errorsJoin(errs []error) error {
	return errors.Join(errs...)
}

func p2pProtocolOrder(defaultProtocol utils.MultiAddressProtocol) []utils.MultiAddressProtocol {
	if defaultProtocol == utils.ProtocolQUIC {
		return []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP}
	}
	return []utils.MultiAddressProtocol{utils.ProtocolTCP, utils.ProtocolQUIC}
}
