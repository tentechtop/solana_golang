package main

import (
	"fmt"
	"os"
	"path/filepath"

	"solana_golang/zk"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: generate_shielded_parameters <development-output-file>")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "WARNING: generating single-party development parameters; do not use them for real assets")
	parameters, err := zk.GenerateShieldedCircuitParameters()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate shielded parameters: %v\n", err)
		os.Exit(1)
	}
	encoded, err := parameters.MarshalBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode shielded parameters: %v\n", err)
		os.Exit(1)
	}
	outputPath := filepath.Clean(os.Args[1])
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputPath, encoded, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write shielded parameters: %v\n", err)
		os.Exit(1)
	}
	verifyingKeyHash, err := parameters.VerifyingKeyHash()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash shielded verifying key: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d bytes, verifying_key_hash=%x\n", len(encoded), verifyingKeyHash)
}
