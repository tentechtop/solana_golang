package p2p

const (
	// ProtocolPoSBlockByHashV1 定义按哈希拉区块协议 + 缺父块时需要从 peer 补齐历史区块。
	ProtocolPoSBlockByHashV1 ProtocolID = 44
	// ProtocolPoSBlockByHeightV1 定义按高度拉区块协议 + 落后节点需要顺序补齐主链缺口。
	ProtocolPoSBlockByHeightV1 ProtocolID = 45
	// ProtocolPoSStateSnapshotV1 定义状态快照协议 + 新节点需要验证 state root 后恢复执行。
	ProtocolPoSStateSnapshotV1 ProtocolID = 46
	// ProtocolPoSStatusV1 定义节点状态协议 + 运维和同步前置探测需要统一状态入口。
	ProtocolPoSStatusV1 ProtocolID = 47
	// ProtocolPoSEvidenceV1 定义作恶证据协议 + slashing 需要先传播可验证证据。
	ProtocolPoSEvidenceV1 ProtocolID = 48
	// ProtocolPoSBlockLocatorV1 定义主链定位点协议 + 分叉同步前先定位共同祖先。
	ProtocolPoSBlockLocatorV1 ProtocolID = 49
	// ProtocolPoSCommonAncestorV1 定义共同祖先查询协议 + proposal 和 block sync 统一分支起点。
	ProtocolPoSCommonAncestorV1 ProtocolID = 50
)
