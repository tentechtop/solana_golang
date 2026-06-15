package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStructureProductionImportsStayLowLevel(t *testing.T) {
	structureDirectory := filepath.Join("..", "structure")
	allowedImports := map[string]struct{}{
		"solana_golang/codec/borsh": {},
		"solana_golang/utils":       {},
	}
	legacyImportsByFile := map[string]map[string]struct{}{
		"privacy_instruction.go": {"solana_golang/zk": {}},
	}
	forbiddenPrefixes := []string{
		"solana_golang/runtime",
		"solana_golang/programs",
		"solana_golang/consensus",
		"solana_golang/p2p",
		"solana_golang/database",
		"solana_golang/rpc",
		"solana_golang/cmd",
	}

	err := filepath.WalkDir(structureDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		return assertStructureFileImports(t, path, entry.Name(), allowedImports, legacyImportsByFile, forbiddenPrefixes)
	})
	if err != nil {
		t.Fatalf("walk structure directory: %v", err)
	}
}

func TestRuntimeDoesNotImportConcretePrograms(t *testing.T) {
	runtimeDirectory := filepath.Join("..", "runtime")
	forbiddenPrefixes := []string{
		"solana_golang/programs",
		"solana_golang/consensus",
		"solana_golang/p2p",
		"solana_golang/cmd",
	}
	assertDirectoryDoesNotImport(t, runtimeDirectory, forbiddenPrefixes)
}

func TestProgramsDoNotImportConsensusOrNetwork(t *testing.T) {
	programsDirectory := filepath.Join("..", "programs")
	forbiddenPrefixes := []string{
		"solana_golang/consensus",
		"solana_golang/p2p",
		"solana_golang/cmd",
	}
	assertDirectoryDoesNotImport(t, programsDirectory, forbiddenPrefixes)
}

func assertStructureFileImports(
	t *testing.T,
	path string,
	fileName string,
	allowedImports map[string]struct{},
	legacyImportsByFile map[string]map[string]struct{},
	forbiddenPrefixes []string,
) error {
	t.Helper()

	fileSet := token.NewFileSet()
	parsedFile, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, importSpec := range parsedFile.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(importPath, "solana_golang/") {
			continue
		}
		for _, forbiddenPrefix := range forbiddenPrefixes {
			if strings.HasPrefix(importPath, forbiddenPrefix) {
				t.Fatalf("%s imports forbidden high-level package %s", fileName, importPath)
			}
		}
		if _, allowed := allowedImports[importPath]; allowed {
			continue
		}
		if legacyImports, exists := legacyImportsByFile[fileName]; exists {
			if _, allowed := legacyImports[importPath]; allowed {
				continue
			}
		}
		t.Fatalf("%s imports %s; move business/runtime dependencies behind runtime or programs", fileName, importPath)
	}
	return nil
}

func assertDirectoryDoesNotImport(t *testing.T, directory string, forbiddenPrefixes []string) {
	t.Helper()

	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		parsedFile, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, importSpec := range parsedFile.Imports {
			importPath, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return err
			}
			for _, forbiddenPrefix := range forbiddenPrefixes {
				if strings.HasPrefix(importPath, forbiddenPrefix) {
					t.Fatalf("%s imports forbidden package %s", filepath.Base(path), importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", directory, err)
	}
}
