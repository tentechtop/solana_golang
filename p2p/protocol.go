package p2p

import (
	"fmt"
	"strings"
)

// ProtocolID 表示协议编号 + 作为消息头中的固定路由键。
type ProtocolID uint32

const (
	// ProtocolPingV1 表示探活请求 + 验证节点连接是否可用。
	ProtocolPingV1 ProtocolID = 0
	// ProtocolPongV1 表示探活响应 + 与 ping 请求建立请求响应关系。
	ProtocolPongV1 ProtocolID = 1
	// ProtocolHandshakeV1 表示通用握手请求 + 预留给非安全会话的链和能力协商。
	ProtocolHandshakeV1 ProtocolID = 2
	// ProtocolBlockV1 表示区块响应 + 承载按哈希或高度查询到的区块。
	ProtocolBlockV1 ProtocolID = 3
	// ProtocolFindNodeRequestV1 表示查找节点请求 + 支持 DHT 邻近节点发现。
	ProtocolFindNodeRequestV1 ProtocolID = 8
	// ProtocolBroadcastResourceV1 表示资源摘要广播 + 用于扩散区块和交易哈希。
	ProtocolBroadcastResourceV1 ProtocolID = 9
	// ProtocolGetResourceRequestV1 表示资源拉取请求 + 用于按摘要获取完整数据。
	ProtocolGetResourceRequestV1 ProtocolID = 10
	// ProtocolReceiveBlockV1 表示接收区块 + 用于新区块直接传播。
	ProtocolReceiveBlockV1 ProtocolID = 11
	// ProtocolReceiveTransactionV1 表示接收交易 + 用于交易池传播。
	ProtocolReceiveTransactionV1 ProtocolID = 12
	// ProtocolQueryBlockByHashV1 表示按哈希查询区块 + 用于历史区块同步。
	ProtocolQueryBlockByHashV1 ProtocolID = 13
	// ProtocolQueryBlockByHeightV1 表示按高度查询区块 + 用于缺口补齐。
	ProtocolQueryBlockByHeightV1 ProtocolID = 14
	// ProtocolQueryCommonAncestorV1 表示查询共同祖先 + 用于分叉同步定位。
	ProtocolQueryCommonAncestorV1 ProtocolID = 15
	// ProtocolHandshakeSuccessV1 表示握手成功通知 + 用于连接认证完成后的状态更新。
	ProtocolHandshakeSuccessV1 ProtocolID = 16
	// ProtocolQueryBlockHeadersV1 表示查询区块头 + 用于轻量同步和分叉判断。
	ProtocolQueryBlockHeadersV1 ProtocolID = 17
	// ProtocolPeerHintsV1 表示节点提示 + 用于交换已签名的可连接节点地址。
	ProtocolPeerHintsV1 ProtocolID = 18
	// ProtocolNodeStatusV1 表示节点状态 + 用于传播同步高度和能力信息。
	ProtocolNodeStatusV1 ProtocolID = 19
	// ProtocolFindNodeResponseV1 表示查找节点响应 + 返回 DHT 邻近节点集合。
	ProtocolFindNodeResponseV1 ProtocolID = 20
	// ProtocolHotStuffVoteV1 表示 HotStuff 投票 + 用于共识投票传播。
	ProtocolHotStuffVoteV1 ProtocolID = 21
	// ProtocolHotStuffQCV1 表示 HotStuff 证书 + 用于视图推进和提交证明。
	ProtocolHotStuffQCV1 ProtocolID = 22
	// ProtocolSecureSessionV1 表示安全会话握手 + 用于节点认证和会话密钥派生。
	ProtocolSecureSessionV1 ProtocolID = 23
	// ProtocolIdentifyRequestV1 表示节点身份查询 + 用于连接建立后主动获取对端可拨地址。
	ProtocolIdentifyRequestV1 ProtocolID = 24
	// ProtocolIdentifyResponseV1 表示节点身份响应 + 返回对端签名的地址记录。
	ProtocolIdentifyResponseV1 ProtocolID = 25
)

// MessagePriority 表示消息优先级 + 用于后续队列和 QUIC stream 调度。
type MessagePriority uint8

const (
	// MessagePriorityLow 表示低优先级 + 用于历史同步和大体积数据。
	MessagePriorityLow MessagePriority = 0
	// MessagePriorityNormal 表示普通优先级 + 用于交易和节点发现。
	MessagePriorityNormal MessagePriority = 1
	// MessagePriorityHigh 表示高优先级 + 用于共识投票和证书。
	MessagePriorityHigh MessagePriority = 2
)

// ProtocolClass 表示协议流量类别 + 用于把控制面保护和高频数据面保护拆开。
type ProtocolClass uint8

const (
	// ProtocolClassAuto 表示自动分类 + 保持旧注册代码兼容并按内置协议表兜底。
	ProtocolClassAuto ProtocolClass = 0
	// ProtocolClassControl 表示控制面协议 + 用于握手、发现、身份和心跳等低频关键流量。
	ProtocolClassControl ProtocolClass = 1
	// ProtocolClassData 表示数据面协议 + 用于区块、交易、共识和业务消息等高频流量。
	ProtocolClassData ProtocolClass = 2
)

// ProtocolConcurrencyMode 表示协议并发模型 + 显式区分保序状态流和可并行流量。
type ProtocolConcurrencyMode uint8

const (
	// ProtocolConcurrencyOrdered 表示有状态保序 + 默认保护同 peer 同协议的处理顺序。
	ProtocolConcurrencyOrdered ProtocolConcurrencyMode = 0
	// ProtocolConcurrencyStateless 表示无状态并行 + 每条消息可独立分区处理。
	ProtocolConcurrencyStateless ProtocolConcurrencyMode = 1
	// ProtocolConcurrencyStateKey 表示按状态键并行 + 同一状态键保序且不同状态键并发。
	ProtocolConcurrencyStateKey ProtocolConcurrencyMode = 2
)

// ProtocolPartitionKey 提取协议状态分片键 + 让有状态协议按业务对象隔离并行。
type ProtocolPartitionKey func(Message) string

// ProtocolSpec 保存协议元数据 + 供注册表校验路由和响应语义。
type ProtocolSpec struct {
	ID           ProtocolID
	Name         string
	HasResponse  bool
	Priority     MessagePriority
	Class        ProtocolClass
	Concurrency  ProtocolConcurrencyMode
	PartitionKey ProtocolPartitionKey
}

// Validate 校验协议定义 + 防止空名称和非法优先级进入注册表。
func (spec ProtocolSpec) Validate() error {
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidProtocol)
	}
	if spec.Priority > MessagePriorityHigh {
		return fmt.Errorf("%w: invalid priority", ErrInvalidProtocol)
	}
	if spec.Class > ProtocolClassData {
		return fmt.Errorf("%w: invalid protocol class", ErrInvalidProtocol)
	}
	if spec.Concurrency > ProtocolConcurrencyStateKey {
		return fmt.Errorf("%w: invalid protocol concurrency", ErrInvalidProtocol)
	}
	if spec.Concurrency == ProtocolConcurrencyStateKey && spec.PartitionKey == nil {
		return fmt.Errorf("%w: missing protocol partition key", ErrInvalidProtocol)
	}
	return nil
}

// EffectiveClass 返回协议最终分类 + 兼容未显式声明分类的旧协议注册代码。
func (spec ProtocolSpec) EffectiveClass() ProtocolClass {
	if spec.Class != ProtocolClassAuto {
		return spec.Class
	}
	return defaultProtocolClass(spec.ID)
}

// AllowsParallelHandling 返回协议并行处理能力 + 仅无状态或按状态键隔离的协议允许跨分区执行。
func (spec ProtocolSpec) AllowsParallelHandling() bool {
	return spec.Concurrency == ProtocolConcurrencyStateless || spec.Concurrency == ProtocolConcurrencyStateKey
}

func normalizeProtocolConcurrency(concurrency ProtocolConcurrencyMode) ProtocolConcurrencyMode {
	if concurrency > ProtocolConcurrencyStateKey {
		return ProtocolConcurrencyOrdered
	}
	return concurrency
}

// defaultProtocolClass 返回内置协议默认分类 + 防止高频业务协议被控制面限速误判。
func defaultProtocolClass(protocolID ProtocolID) ProtocolClass {
	switch protocolID {
	case ProtocolPingV1,
		ProtocolPongV1,
		ProtocolHandshakeV1,
		ProtocolFindNodeRequestV1,
		ProtocolHandshakeSuccessV1,
		ProtocolPeerHintsV1,
		ProtocolNodeStatusV1,
		ProtocolFindNodeResponseV1,
		ProtocolSecureSessionV1,
		ProtocolIdentifyRequestV1,
		ProtocolIdentifyResponseV1:
		return ProtocolClassControl
	default:
		return ProtocolClassData
	}
}

// defaultProtocolPriority 返回内置协议默认优先级 + 让队列和 QUIC stream 调度使用同一套分类。
func defaultProtocolPriority(protocolID ProtocolID) MessagePriority {
	switch protocolID {
	case ProtocolPingV1,
		ProtocolPongV1,
		ProtocolHandshakeV1,
		ProtocolHandshakeSuccessV1,
		ProtocolHotStuffVoteV1,
		ProtocolHotStuffQCV1,
		ProtocolSecureSessionV1,
		ProtocolIdentifyRequestV1,
		ProtocolIdentifyResponseV1:
		return MessagePriorityHigh
	case ProtocolBlockV1,
		ProtocolGetResourceRequestV1,
		ProtocolQueryBlockByHashV1,
		ProtocolQueryBlockByHeightV1,
		ProtocolQueryCommonAncestorV1,
		ProtocolQueryBlockHeadersV1:
		return MessagePriorityLow
	default:
		return MessagePriorityNormal
	}
}

// defaultProtocolConcurrency 返回内置协议并发模型 + 默认业务写入和共识流量保持串行。
func defaultProtocolConcurrency(protocolID ProtocolID) ProtocolConcurrencyMode {
	switch protocolID {
	case ProtocolPingV1,
		ProtocolPongV1,
		ProtocolFindNodeRequestV1,
		ProtocolFindNodeResponseV1,
		ProtocolQueryBlockByHashV1,
		ProtocolQueryBlockByHeightV1,
		ProtocolQueryCommonAncestorV1,
		ProtocolQueryBlockHeadersV1,
		ProtocolIdentifyRequestV1,
		ProtocolIdentifyResponseV1:
		return ProtocolConcurrencyStateless
	default:
		return ProtocolConcurrencyOrdered
	}
}

// NormalizedName 返回规范协议名 + 消除大小写和多余分隔符差异。
func (spec ProtocolSpec) NormalizedName() string {
	return NormalizeProtocolName(spec.Name)
}

// NormalizeProtocolName 规范协议名 + 保持注册和查询使用同一格式。
func NormalizeProtocolName(name string) string {
	normalized := strings.TrimSpace(strings.ToLower(name))
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	return strings.TrimRight(normalized, "/")
}

// DefaultProtocolSpecs 返回内置协议定义 + 覆盖发现、同步、交易和共识消息。
func DefaultProtocolSpecs() []ProtocolSpec {
	return []ProtocolSpec{
		defaultProtocolSpec(ProtocolPingV1, "/p2p/ping/1.0.0", true),
		defaultProtocolSpec(ProtocolPongV1, "/p2p/pong/1.0.0", false),
		defaultProtocolSpec(ProtocolHandshakeV1, "/p2p/handshake/1.0.0", true),
		defaultProtocolSpec(ProtocolBlockV1, "/p2p/block/1.0.0", false),
		defaultProtocolSpec(ProtocolFindNodeRequestV1, "/p2p/find-node/request/1.0.0", true),
		defaultProtocolSpec(ProtocolBroadcastResourceV1, "/p2p/resource/broadcast/1.0.0", false),
		defaultProtocolSpec(ProtocolGetResourceRequestV1, "/p2p/resource/get/1.0.0", true),
		defaultProtocolSpec(ProtocolReceiveBlockV1, "/p2p/block/receive/1.0.0", false),
		defaultProtocolSpec(ProtocolReceiveTransactionV1, "/p2p/transaction/receive/1.0.0", false),
		defaultProtocolSpec(ProtocolQueryBlockByHashV1, "/p2p/block/query-by-hash/1.0.0", true),
		defaultProtocolSpec(ProtocolQueryBlockByHeightV1, "/p2p/block/query-by-height/1.0.0", true),
		defaultProtocolSpec(ProtocolQueryCommonAncestorV1, "/p2p/common-ancestor/query/1.0.0", true),
		defaultProtocolSpec(ProtocolHandshakeSuccessV1, "/p2p/handshake-success/1.0.0", false),
		defaultProtocolSpec(ProtocolQueryBlockHeadersV1, "/p2p/block-headers/query/1.0.0", true),
		defaultProtocolSpec(ProtocolPeerHintsV1, "/p2p/peer-hints/1.0.0", false),
		defaultProtocolSpec(ProtocolNodeStatusV1, "/p2p/node-status/1.0.0", false),
		defaultProtocolSpec(ProtocolFindNodeResponseV1, "/p2p/find-node/response/1.0.0", false),
		defaultProtocolSpec(ProtocolHotStuffVoteV1, "/p2p/hotstuff/vote/1.0.0", false),
		defaultProtocolSpec(ProtocolHotStuffQCV1, "/p2p/hotstuff/qc/1.0.0", false),
		defaultProtocolSpec(ProtocolSecureSessionV1, "/p2p/secure-session/1.0.0", true),
		defaultProtocolSpec(ProtocolIdentifyRequestV1, "/p2p/identify/request/1.0.0", true),
		defaultProtocolSpec(ProtocolIdentifyResponseV1, "/p2p/identify/response/1.0.0", false),
	}
}

func defaultProtocolSpec(protocolID ProtocolID, name string, hasResponse bool) ProtocolSpec {
	return ProtocolSpec{
		ID:          protocolID,
		Name:        name,
		HasResponse: hasResponse,
		Priority:    defaultProtocolPriority(protocolID),
		Concurrency: defaultProtocolConcurrency(protocolID),
	}
}
