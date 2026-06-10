package database

import (
	"bytes"
	"encoding/binary"
)

const tableKeyPrefixSize = 2

// tableBounds 执行对应逻辑 + 保持函数职责清晰可维护。
func tableBounds(table Table) ([]byte, []byte) {
	return tablePrefix(table), tableUpperBound(table)
}

// rangeBounds 执行对应逻辑 + 保持函数职责清晰可维护。
func rangeBounds(table Table, startKey []byte, endKey []byte) ([]byte, []byte) {
	lower, upper := tableBounds(table)
	if startKey != nil {
		lower = encodeKey(table, startKey)
	}
	if endKey != nil {
		upper = encodeKey(table, endKey)
	}
	return lower, upper
}

// prefixBounds 执行对应逻辑 + 保持函数职责清晰可维护。
func prefixBounds(table Table, prefix []byte) ([]byte, []byte) {
	lower := encodeKey(table, prefix)
	upper := keySuccessor(lower)
	tableUpper := tableUpperBound(table)
	if upper == nil || (tableUpper != nil && bytes.Compare(upper, tableUpper) > 0) {
		upper = tableUpper
	}
	return lower, upper
}

// tablePrefix 执行对应逻辑 + 保持函数职责清晰可维护。
func tablePrefix(table Table) []byte {
	prefix := make([]byte, tableKeyPrefixSize)
	binary.BigEndian.PutUint16(prefix, table.Code())
	return prefix
}

// tableUpperBound 执行对应逻辑 + 保持函数职责清晰可维护。
func tableUpperBound(table Table) []byte {
	code := table.Code()
	if code == ^uint16(0) {
		return nil
	}
	upper := make([]byte, tableKeyPrefixSize)
	binary.BigEndian.PutUint16(upper, code+1)
	return upper
}

// encodeKey 执行对应逻辑 + 保持函数职责清晰可维护。
func encodeKey(table Table, key []byte) []byte {
	prefix := tablePrefix(table)
	encoded := make([]byte, len(prefix)+len(key))
	copy(encoded, prefix)
	copy(encoded[len(prefix):], key)
	return encoded
}

// stripTablePrefix 执行对应逻辑 + 保持函数职责清晰可维护。
func stripTablePrefix(key []byte) []byte {
	if len(key) <= tableKeyPrefixSize {
		return []byte{}
	}
	return cloneBytes(key[tableKeyPrefixSize:])
}

// keySuccessor 执行对应逻辑 + 保持函数职责清晰可维护。
func keySuccessor(key []byte) []byte {
	successor := cloneBytes(key)
	for i := len(successor) - 1; i >= 0; i-- {
		if successor[i] != 0xff {
			successor[i]++
			return successor[:i+1]
		}
	}
	return nil
}

// cloneOperation 执行对应逻辑 + 保持函数职责清晰可维护。
func cloneOperation(op DBOperation) DBOperation {
	op.Key = cloneBytes(op.Key)
	op.Value = cloneBytes(op.Value)
	return op
}

// cloneBytes 执行对应逻辑 + 保持函数职责清晰可维护。
func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
