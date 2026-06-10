package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"solana_golang/database"
	"solana_golang/utils"
)

var (
	nodeIdentityPublicKey  = []byte("node_identity/public_key")
	nodeIdentityPrivateKey = []byte("node_identity/private_key")
)

type nodeIdentity struct {
	PeerID     string
	PublicKey  []byte
	PrivateKey []byte
}

// ensureNodeIdentity 加载或创建节点身份 + 以数据库记录作为唯一可信来源。
func (resources *runtimeResources) ensureNodeIdentity(configuredPeerID string) (nodeIdentity, error) {
	identity, found, err := resources.loadNodeIdentity()
	if err != nil {
		return nodeIdentity{}, err
	}
	if !found {
		identity, err = resources.createNodeIdentity()
		if err != nil {
			return nodeIdentity{}, err
		}
		resources.logger.Info("p2p node identity created", slog.String("peer_id", identity.PeerID))
		return identity, nil
	}

	if shouldWarnConfiguredPeerID(configuredPeerID, identity.PeerID) {
		resources.logger.Warn("p2p config peer_id ignored",
			slog.String("configured_peer_id", configuredPeerID),
			slog.String("database_peer_id", identity.PeerID),
		)
	}
	resources.logger.Info("p2p node identity loaded", slog.String("peer_id", identity.PeerID))
	return identity, nil
}

// loadNodeIdentity 一致性读取节点身份 + 避免公钥和私钥跨版本读取。
func (resources *runtimeResources) loadNodeIdentity() (nodeIdentity, bool, error) {
	if resources.database == nil {
		return nodeIdentity{}, false, errors.New("cmd: database is nil")
	}

	transaction, err := resources.database.BeginReadTransaction()
	if err != nil {
		return nodeIdentity{}, false, fmt.Errorf("cmd: begin node identity read: %w", err)
	}
	defer transaction.Close()

	publicKey, err := transaction.Get(database.TablePeer, nodeIdentityPublicKey)
	if err != nil {
		return nodeIdentity{}, false, fmt.Errorf("cmd: read node public key: %w", err)
	}
	privateKey, err := transaction.Get(database.TablePeer, nodeIdentityPrivateKey)
	if err != nil {
		return nodeIdentity{}, false, fmt.Errorf("cmd: read node private key: %w", err)
	}
	if publicKey == nil && privateKey == nil {
		return nodeIdentity{}, false, nil
	}
	if publicKey == nil || privateKey == nil {
		return nodeIdentity{}, false, errors.New("cmd: incomplete node identity in database")
	}

	identity := nodeIdentity{
		PeerID:     utils.Base58Encode(publicKey),
		PublicKey:  utils.CloneBytes(publicKey),
		PrivateKey: utils.CloneBytes(privateKey),
	}
	if err := validateNodeIdentity(identity); err != nil {
		return nodeIdentity{}, false, err
	}
	return identity, true, nil
}

// createNodeIdentity 创建节点身份 + 通过事务保证公钥和私钥原子落库。
func (resources *runtimeResources) createNodeIdentity() (nodeIdentity, error) {
	publicKey, privateKey, err := utils.GenerateEd25519KeyPairBytes()
	if err != nil {
		return nodeIdentity{}, fmt.Errorf("cmd: generate node identity: %w", err)
	}
	identity := nodeIdentity{
		PeerID:     utils.Base58Encode(publicKey),
		PublicKey:  utils.CloneBytes(publicKey),
		PrivateKey: utils.CloneBytes(privateKey),
	}
	if err := validateNodeIdentity(identity); err != nil {
		return nodeIdentity{}, err
	}

	operations := []database.DBOperation{
		database.NewUpdateOperation(database.TablePeer, nodeIdentityPublicKey, identity.PublicKey),
		database.NewUpdateOperation(database.TablePeer, nodeIdentityPrivateKey, identity.PrivateKey),
	}
	if err := resources.database.DataTransaction(operations); err != nil {
		return nodeIdentity{}, fmt.Errorf("cmd: save node identity: %w", err)
	}
	if err := resources.database.Flush(); err != nil {
		return nodeIdentity{}, fmt.Errorf("cmd: flush node identity: %w", err)
	}
	return identity, nil
}

// validateNodeIdentity 校验节点身份 + 防止损坏密钥进入 P2P 启动路径。
func validateNodeIdentity(identity nodeIdentity) error {
	if len(identity.PublicKey) != utils.Ed25519KeySize {
		return fmt.Errorf("cmd: node public key requires %d bytes", utils.Ed25519KeySize)
	}
	if len(identity.PrivateKey) != utils.Ed25519KeySize {
		return fmt.Errorf("cmd: node private key requires %d bytes", utils.Ed25519KeySize)
	}
	derivedPublicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("cmd: derive node public key: %w", err)
	}
	if !bytes.Equal(identity.PublicKey, derivedPublicKey) {
		return errors.New("cmd: node public key does not match private key")
	}
	if identity.PeerID != utils.Base58Encode(identity.PublicKey) {
		return errors.New("cmd: node peer id does not match public key")
	}
	return nil
}

// shouldWarnConfiguredPeerID 判断配置 ID 是否冲突 + 占位值不触发噪声日志。
func shouldWarnConfiguredPeerID(configuredPeerID string, databasePeerID string) bool {
	configuredPeerID = strings.TrimSpace(configuredPeerID)
	if configuredPeerID == "" || configuredPeerID == databasePeerID {
		return false
	}
	return configuredPeerID != utils.Base58Encode(make([]byte, utils.Ed25519KeySize))
}
