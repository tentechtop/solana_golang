package utils

import "testing"

// TestNormalizeHex 验证十六进制规范化 + 保证空白和前缀处理稳定。
func TestNormalizeHex(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lower prefix", input: "  0xabc  ", want: "abc"},
		{name: "upper prefix", input: "0XABC", want: "ABC"},
		{name: "without prefix", input: " abc ", want: "abc"},
		{name: "short value", input: "0", want: "0"},
		{name: "empty", input: "   ", want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := NormalizeHex(testCase.input); got != testCase.want {
				t.Fatalf("NormalizeHex(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}
