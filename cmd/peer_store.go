package main

import (
	"context"
	"fmt"

	"solana_golang/database"
	"solana_golang/p2p"
)

var databasePeerStorePrefix = []byte("peer_store/peer/")

type databasePeerStore struct {
	database database.Database
}

// newDatabasePeerStore 创建数据库 PeerStore + 由 cmd 组合层连接 P2P 接口和本地 KV。
func newDatabasePeerStore(databaseInstance database.Database) p2p.PeerStore {
	if databaseInstance == nil {
		return nil
	}
	return &databasePeerStore{database: databaseInstance}
}

// LoadPeers 加载持久化节点 + 使用读事务保证启动恢复视图一致。
func (store *databasePeerStore) LoadPeers(ctx context.Context, limit int) ([]p2p.Peer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1024
	}

	transaction, err := store.database.BeginReadTransaction()
	if err != nil {
		return nil, fmt.Errorf("cmd: begin peer store read: %w", err)
	}
	defer transaction.Close()

	pairs, err := transaction.PrefixQueryWithLimit(database.TablePeer, databasePeerStorePrefix, limit)
	if err != nil {
		return nil, fmt.Errorf("cmd: read peer store prefix: %w", err)
	}
	peers := make([]p2p.Peer, 0, len(pairs))
	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		peer, err := p2p.UnmarshalPeerBinary(pair.Value)
		if err != nil {
			return nil, fmt.Errorf("cmd: decode stored peer %q: %w", string(pair.Key), err)
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

// SavePeer 保存节点快照 + 使用单条事务保持写入语义和后续扩展一致。
func (store *databasePeerStore) SavePeer(ctx context.Context, peer p2p.Peer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := peer.MarshalBinary()
	if err != nil {
		return fmt.Errorf("cmd: encode peer %s: %w", peer.ID, err)
	}
	key := databasePeerStoreKey(peer.ID)
	operation := database.NewUpdateOperation(database.TablePeer, key, encoded)
	if err := store.database.DataTransaction([]database.DBOperation{operation}); err != nil {
		return fmt.Errorf("cmd: save peer %s: %w", peer.ID, err)
	}
	return ctx.Err()
}

// DeletePeer 删除节点快照 + 用于屏蔽节点或运维清理。
func (store *databasePeerStore) DeletePeer(ctx context.Context, peerID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := p2p.NewPeer(peerID, nil); err != nil {
		return fmt.Errorf("cmd: validate delete peer %s: %w", peerID, err)
	}
	if err := store.database.Delete(database.TablePeer, databasePeerStoreKey(peerID)); err != nil {
		return fmt.Errorf("cmd: delete peer %s: %w", peerID, err)
	}
	return ctx.Err()
}

func databasePeerStoreKey(peerID string) []byte {
	key := make([]byte, 0, len(databasePeerStorePrefix)+len(peerID))
	key = append(key, databasePeerStorePrefix...)
	key = append(key, peerID...)
	return key
}
