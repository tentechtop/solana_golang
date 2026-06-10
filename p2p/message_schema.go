package p2p

import (
	"fmt"

	"solana_golang/schema"
)

const (
	// P2PMessageSchemaType 定义 P2P 消息 schema 类型 + 用于 raw bytes 存储索引。
	P2PMessageSchemaType = "p2p.message"
	// P2PMessageSchemaID 定义 P2P 消息 schema ID + 协议升级时必须新增 ID。
	P2PMessageSchemaID = "p2p.message.borsh.v1"
)

// P2PMessageSchema 返回 P2P 消息 schema + 将 Borsh 解码和业务校验绑定到注册中心。
func P2PMessageSchema() schema.Schema {
	return schema.Schema{
		Key: schema.SchemaKey{
			Type:     P2PMessageSchemaType,
			Version:  MessageProtocolVersion,
			Codec:    schema.CodecBorsh,
			SchemaID: P2PMessageSchemaID,
		},
		Decode: func(payload []byte) (any, error) {
			message, err := UnmarshalBinary(payload, DefaultMaxMessageSize)
			if err != nil {
				return nil, err
			}
			return message, nil
		},
		Validate: func(value any) error {
			message, ok := value.(Message)
			if !ok {
				return fmt.Errorf("%w: decoded value is not p2p message", ErrInvalidMessage)
			}
			return message.Validate(DefaultMaxMessageSize)
		},
		Migrate: func(value any) (any, error) {
			return value, nil
		},
	}
}

// RegisterP2PMessageSchema 注册 P2P 消息 schema + 启动时缺失注册应直接失败。
func RegisterP2PMessageSchema(registry *schema.Registry) error {
	if registry == nil {
		registry = schema.DefaultRegistry
	}
	record := P2PMessageSchema()
	if _, exists := registry.Lookup(record.Key); exists {
		return nil
	}
	return registry.Register(record)
}

// NewP2PMessageEnvelope 创建 P2P raw envelope + 数据库保存 Borsh bytes 时补齐版本元信息。
func NewP2PMessageEnvelope(message Message) (schema.Envelope, error) {
	payload, err := message.MarshalBinary(DefaultMaxMessageSize)
	if err != nil {
		return schema.Envelope{}, err
	}
	return schema.NewEnvelope(P2PMessageSchemaType, MessageProtocolVersion, schema.CodecBorsh, P2PMessageSchemaID, payload)
}
