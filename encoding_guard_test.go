package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var textFileExtensions = map[string]struct{}{
	".go":   {},
	".md":   {},
	".txt":  {},
	".yml":  {},
	".yaml": {},
	".json": {},
	".toml": {},
}

var mojibakePatterns = []string{
	string(rune(0xfffd)),
	string([]rune{0x951f, 0x65a4}),
	string([]rune{0x6dc7, 0x6fee}),
	string([]rune{0x7019, 0x6c2b, 0x7b9f}),
	string([]rune{0x741b, 0x3127, 0x305a}),
	string([]rune{0x95c3, 0x53c9}),
	string([]rune{0x9286, 0x3006}),
	string([]rune{0x20ac, 0x003f}),
	string([]rune{0x934a, 0x521b}),
	string([]rune{0x7ee0, 0xff04}),
	string([]rune{0x6434, 0x5fd3}),
	string([]rune{0x6751, 0x6594}),
	string([]rune{0x9432, 0x7193}),
}

func TestRepositoryTextFilesAreValidUTF8(t *testing.T) {
	root := "."
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if shouldSkipTextGuardDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isGuardedTextFile(path) {
			return nil
		}
		return assertValidRepositoryTextFile(t, path)
	})
	if err != nil {
		t.Fatalf("walk repository text files: %v", err)
	}
}

func shouldSkipTextGuardDir(name string) bool {
	switch name {
	case ".git", ".idea", "vendor", "node_modules":
		return true
	default:
		return false
	}
}

func isGuardedTextFile(path string) bool {
	_, ok := textFileExtensions[strings.ToLower(filepath.Ext(path))]
	return ok
}

func assertValidRepositoryTextFile(t *testing.T, path string) error {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Contains(data, []byte{0}) {
		return nil
	}
	if !utf8.Valid(data) {
		t.Fatalf("%s contains invalid UTF-8 bytes", path)
	}

	content := string(data)
	for _, pattern := range mojibakePatterns {
		if strings.Contains(content, pattern) {
			t.Fatalf("%s contains mojibake pattern %q", path, pattern)
		}
	}
	return nil
}
