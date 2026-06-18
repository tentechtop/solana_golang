package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"solana_golang/structure"
	"solana_golang/vm"
	"solana_golang/vm/assembler"
)

type manifestFile struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	ComputeUnitLimit uint64   `json:"compute_unit_limit"`
	RequiredSyscalls []string `json:"required_syscalls"`
	UpgradeAuthority string   `json:"upgrade_authority"`
}

func main() {
	if err := run(os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("svmasm", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "auto", "manifest path, auto, or none")
	noManifest := flags.Bool("no-manifest", false, "disable automatic manifest lookup")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return fmt.Errorf("usage: svmasm [-manifest <path|auto|none>] [-no-manifest] <input.svmasm> <output.svmbin>")
	}

	inputPath := flags.Arg(0)
	outputPath := flags.Arg(1)
	source, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read source %s: %w", inputPath, err)
	}
	manifest, usedManifestPath, hasManifest, err := resolveManifest(inputPath, *manifestPath, *noManifest)
	if err != nil {
		return err
	}

	var bytecode []byte
	if hasManifest {
		bytecode, err = assembler.AssembleWithManifest(string(source), manifest)
	} else {
		bytecode, err = assembler.Assemble(string(source))
	}
	if err != nil {
		return fmt.Errorf("assemble %s: %w", inputPath, err)
	}
	if err := writeBytecode(outputPath, bytecode); err != nil {
		return err
	}
	printBuildResult(outputPath, usedManifestPath, hasManifest)
	return nil
}

func resolveManifest(inputPath string, manifestOption string, noManifest bool) (vm.ProgramManifest, string, bool, error) {
	option := strings.TrimSpace(manifestOption)
	if noManifest {
		if option != "" && !strings.EqualFold(option, "auto") && !strings.EqualFold(option, "none") {
			return vm.ProgramManifest{}, "", false, fmt.Errorf("-manifest and -no-manifest cannot be used together")
		}
		return vm.ProgramManifest{}, "", false, nil
	}
	if option == "" || strings.EqualFold(option, "auto") {
		return resolveAutomaticManifest(inputPath)
	}
	if strings.EqualFold(option, "none") {
		return vm.ProgramManifest{}, "", false, nil
	}
	manifest, err := readManifest(option)
	if err != nil {
		return vm.ProgramManifest{}, "", false, err
	}
	return manifest, filepath.Clean(option), true, nil
}

func resolveAutomaticManifest(inputPath string) (vm.ProgramManifest, string, bool, error) {
	candidatePath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".svm.json"
	manifest, err := readManifest(candidatePath)
	if err == nil {
		return manifest, filepath.Clean(candidatePath), true, nil
	}
	if os.IsNotExist(err) {
		return vm.ProgramManifest{}, "", false, nil
	}
	return vm.ProgramManifest{}, "", false, err
}

func readManifest(path string) (vm.ProgramManifest, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return vm.ProgramManifest{}, fmt.Errorf("manifest path is empty")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return vm.ProgramManifest{}, fmt.Errorf("read manifest %s: %w", cleanPath, err)
	}
	decoded := manifestFile{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return vm.ProgramManifest{}, fmt.Errorf("decode manifest %s: %w", cleanPath, err)
	}
	manifest, err := buildProgramManifest(decoded)
	if err != nil {
		return vm.ProgramManifest{}, fmt.Errorf("validate manifest %s: %w", cleanPath, err)
	}
	return manifest, nil
}

func buildProgramManifest(decoded manifestFile) (vm.ProgramManifest, error) {
	if strings.TrimSpace(decoded.Name) == "" {
		return vm.ProgramManifest{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(decoded.Version) == "" {
		return vm.ProgramManifest{}, fmt.Errorf("version is required")
	}
	if decoded.ComputeUnitLimit == 0 {
		return vm.ProgramManifest{}, fmt.Errorf("compute_unit_limit is required")
	}
	syscalls, err := manifestSyscalls(decoded.RequiredSyscalls)
	if err != nil {
		return vm.ProgramManifest{}, err
	}
	authority, err := manifestAuthority(decoded.UpgradeAuthority)
	if err != nil {
		return vm.ProgramManifest{}, err
	}
	return vm.ProgramManifest{
		ComputeUnitLimit: decoded.ComputeUnitLimit,
		UpgradeAuthority: authority,
		RequiredSyscalls: syscalls,
	}, nil
}

func manifestSyscalls(names []string) ([]vm.SyscallID, error) {
	syscalls := make([]vm.SyscallID, 0, len(names))
	for _, name := range names {
		syscallID, err := manifestSyscallByName(name)
		if err != nil {
			return nil, err
		}
		syscalls = append(syscalls, syscallID)
	}
	return syscalls, nil
}

func manifestSyscallByName(name string) (vm.SyscallID, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "log", "sol_log":
		return vm.SyscallLog, nil
	case "sha256", "sol_sha256":
		return vm.SyscallSHA256, nil
	case "set_return_data", "sol_set_return_data":
		return vm.SyscallSetReturnData, nil
	case "get_return_data", "sol_get_return_data":
		return vm.SyscallGetReturnData, nil
	case "get_clock", "sol_get_clock":
		return vm.SyscallGetClock, nil
	case "get_rent", "sol_get_rent":
		return vm.SyscallGetRent, nil
	case "create_program_address", "sol_create_program_address":
		return vm.SyscallCreateProgramAddress, nil
	case "invoke", "sol_invoke":
		return vm.SyscallInvoke, nil
	case "invoke_signed", "sol_invoke_signed":
		return vm.SyscallInvokeSigned, nil
	case "verify_schnorr", "zk_verify_schnorr":
		return vm.SyscallVerifySchnorr, nil
	case "get_account_data", "sol_get_account_data":
		return vm.SyscallGetAccountData, nil
	case "set_account_data", "sol_set_account_data":
		return vm.SyscallSetAccountData, nil
	case "privacy_execute", "sol_privacy_execute":
		return vm.SyscallPrivacyExecute, nil
	case "stake_pool_execute", "sol_stake_pool_execute":
		return vm.SyscallStakePoolExecute, nil
	case "asset_execute", "sol_asset_execute":
		return vm.SyscallAssetExecute, nil
	default:
		return 0, fmt.Errorf("unsupported syscall %q", name)
	}
}

func manifestAuthority(value string) (vm.Address, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return vm.Address{}, nil
	}
	publicKey, err := structure.PublicKeyFromBase58(text)
	if err != nil {
		return vm.Address{}, fmt.Errorf("decode upgrade_authority: %w", err)
	}
	var authority vm.Address
	copy(authority[:], publicKey[:])
	return authority, nil
}

func writeBytecode(outputPath string, bytecode []byte) error {
	cleanPath := filepath.Clean(strings.TrimSpace(outputPath))
	if cleanPath == "." || cleanPath == "" {
		return fmt.Errorf("output path is empty")
	}
	if directory := filepath.Dir(cleanPath); directory != "." {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}
	if err := os.WriteFile(cleanPath, bytecode, 0600); err != nil {
		return fmt.Errorf("write bytecode %s: %w", cleanPath, err)
	}
	return nil
}

func printBuildResult(outputPath string, manifestPath string, hasManifest bool) {
	if hasManifest {
		fmt.Printf("wrote %s with manifest %s\n", filepath.Clean(outputPath), manifestPath)
		return
	}
	fmt.Printf("wrote %s without manifest\n", filepath.Clean(outputPath))
}
