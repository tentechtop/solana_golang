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
	// ProtocolHandshakeV1 表示握手请求 + 交换链 ID、节点身份和能力。
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
	// ProtocolPeerHintsV1 表示节点提示 + 用于交换可连接节点地址。
	ProtocolPeerHintsV1 ProtocolID = 18
	// ProtocolNodeStatusV1 表示节点状态 + 用于传播同步高度和能力信息。
	ProtocolNodeStatusV1 ProtocolID = 19
	// ProtocolFindNodeResponseV1 表示查找节点响应 + 返回 DHT 邻近节点集合。
	ProtocolFindNodeResponseV1 ProtocolID = 20
	// ProtocolHotStuffVoteV1 表示 HotStuff 投票 + 用于共识投票传播。
	ProtocolHotStuffVoteV1 ProtocolID = 21
	// ProtocolHotStuffQCV1 表示 HotStuff 证书 + 用于视图推进和提交证明。
	ProtocolHotStuffQCV1 ProtocolID = 22
	// ProtocolSecureSessionV1 表示安全会话握手 + 用于节点认证、临时密钥交换和会话密钥派生。
	ProtocolSecureSessionV1 ProtocolID = 23
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

// ProtocolSpec 保存协议元数据 + 供注册表校验路由和响应语义。
type ProtocolSpec struct {
	ID          ProtocolID
	Name        string
	HasResponse bool
	Priority    MessagePriority
}

// Validate 校验协议定义 + 防止空名称和非法优先级进入注册表。
func (spec ProtocolSpec) Validate() error {
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidProtocol)
	}
	if spec.Priority > MessagePriorityHigh {
		return fmt.Errorf("%w: invalid priority", ErrInvalidProtocol)
	}
	return nil
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
		{ID: ProtocolPingV1, Name: "/p2p/ping/1.0.0", HasResponse: true, Priority: MessagePriorityHigh},
		{ID: ProtocolPongV1, Name: "/p2p/pong/1.0.0", HasResponse: false, Priority: MessagePriorityHigh},
		{ID: ProtocolHandshakeV1, Name: "/p2p/handshake/1.0.0", HasResponse: true, Priority: MessagePriorityHigh},
		{ID: ProtocolBlockV1, Name: "/p2p/block/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolFindNodeRequestV1, Name: "/p2p/find-node/request/1.0.0", HasResponse: true, Priority: MessagePriorityNormal},
		{ID: ProtocolBroadcastResourceV1, Name: "/p2p/resource/broadcast/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolGetResourceRequestV1, Name: "/p2p/resource/get/1.0.0", HasResponse: true, Priority: MessagePriorityNormal},
		{ID: ProtocolReceiveBlockV1, Name: "/p2p/block/receive/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolReceiveTransactionV1, Name: "/p2p/transaction/receive/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolQueryBlockByHashV1, Name: "/p2p/block/query-by-hash/1.0.0", HasResponse: true, Priority: MessagePriorityNormal},
		{ID: ProtocolQueryBlockByHeightV1, Name: "/p2p/block/query-by-height/1.0.0", HasResponse: true, Priority: MessagePriorityNormal},
		{ID: ProtocolQueryCommonAncestorV1, Name: "/p2p/common-ancestor/query/1.0.0", HasResponse: true, Priority: MessagePriorityNormal},
		{ID: ProtocolHandshakeSuccessV1, Name: "/p2p/handshake-success/1.0.0", HasResponse: false, Priority: MessagePriorityHigh},
		{ID: ProtocolQueryBlockHeadersV1, Name: "/p2p/block-headers/query/1.0.0", HasResponse: true, Priority: MessagePriorityNormal},
		{ID: ProtocolPeerHintsV1, Name: "/p2p/peer-hints/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolNodeStatusV1, Name: "/p2p/node-status/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolFindNodeResponseV1, Name: "/p2p/find-node/response/1.0.0", HasResponse: false, Priority: MessagePriorityNormal},
		{ID: ProtocolHotStuffVoteV1, Name: "/p2p/hotstuff/vote/1.0.0", HasResponse: false, Priority: MessagePriorityHigh},
		{ID: ProtocolHotStuffQCV1, Name: "/p2p/hotstuff/qc/1.0.0", HasResponse: false, Priority: MessagePriorityHigh},
		{ID: ProtocolSecureSessionV1, Name: "/p2p/secure-session/1.0.0", HasResponse: true, Priority: MessagePriorityHigh},
	}
}
