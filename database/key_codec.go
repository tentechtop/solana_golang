package database

import (
	"bytes"
	"encoding/binary"
)

const tableKeyPrefixSize = 2

func tableBounds(table Table) ([]byte, []byte) {
	return tablePrefix(table), tableUpperBound(table)
}
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
func prefixBounds(table Table, prefix []byte) ([]byte, []byte) {
	lower := encodeKey(table, prefix)
	upper := keySuccessor(lower)
	tableUpper := tableUpperBound(table)
	if upper == nil || (tableUpper != nil && bytes.Compare(upper, tableUpper) > 0) {
		upper = tableUpper
	}
	return lower, upper
}
func tablePrefix(table Table) []byte {
	prefix := make([]byte, tableKeyPrefixSize)
	binary.BigEndian.PutUint16(prefix, table.Code())
	return prefix
}
func tableUpperBound(table Table) []byte {
	code := table.Code()
	if code == ^uint16(0) {
		return nil
	}
	upper := make([]byte, tableKeyPrefixSize)
	binary.BigEndian.PutUint16(upper, code+1)
	return upper
}
func encodeKey(table Table, key []byte) []byte {
	prefix := tablePrefix(table)
	encoded := make([]byte, len(prefix)+len(key))
	copy(encoded, prefix)
	copy(encoded[len(prefix):], key)
	return encoded
}
func stripTablePrefix(key []byte) []byte {
	if len(key) <= tableKeyPrefixSize {
		return []byte{}
	}
	return cloneBytes(key[tableKeyPrefixSize:])
}
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
func cloneOperation(op DBOperation) DBOperation {
	op.Key = cloneBytes(op.Key)
	op.Value = cloneBytes(op.Value)
	return op
}
func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
