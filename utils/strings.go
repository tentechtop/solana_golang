package utils

import "strings"

// NormalizeHex 规范化十六进制字符串 + 去除空白和可选前缀。
func NormalizeHex(value string) string {
	normalized := strings.TrimSpace(value)
	if len(normalized) >= 2 && normalized[0] == '0' && (normalized[1] == 'x' || normalized[1] == 'X') {
		return normalized[2:]
	}
	return normalized
}
