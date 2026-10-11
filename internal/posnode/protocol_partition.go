package posnode

import (
	"fmt"

	"github.com/tentechtop/ordexus-core/p2p"
)

// transactionMessagePartitionKey 按交易 ID 串行同一交易的中继副本，避免重复验签和落盘。
func transactionMessagePartitionKey(message p2p.Message) string {
	transaction, _, err := decodeTransactionRouteMessage(message)
	if err != nil {
		return ""
	}
	transactionID, err := transaction.TxIDString()
	if err != nil {
		return ""
	}
	return transactionID
}

// voteMessagePartitionKey 按原始验证者串行投票，不让同一中继节点阻塞其他验证者的投票。
func voteMessagePartitionKey(message p2p.Message) string {
	envelope, err := decodeVoteMessage(message)
	if err != nil {
		return ""
	}
	return envelope.Vote.VoterID
}

// qcMessagePartitionKey 按共识决定串行证书，不依赖实际转发证书的中继节点。
func qcMessagePartitionKey(message p2p.Message) string {
	envelope, err := unmarshalQCEnvelopeBinary(message.Payload)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d/%d/%d/%s",
		envelope.QC.Type,
		envelope.QC.Slot,
		envelope.QC.BlockHeight,
		envelope.QC.BlockHash.String(),
	)
}
