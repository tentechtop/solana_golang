package main

import (
	"fmt"
	"os"

	"solana_golang/vm/assembler"
)

func main() {
	if err := run(os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: svmasm <input.svmasm> <output.svmbin>")
	}
	source, err := os.ReadFile(args[1])
	if err != nil {
		return fmt.Errorf("read source %s: %w", args[1], err)
	}
	bytecode, err := assembler.Assemble(string(source))
	if err != nil {
		return fmt.Errorf("assemble %s: %w", args[1], err)
	}
	if err := os.WriteFile(args[2], bytecode, 0600); err != nil {
		return fmt.Errorf("write bytecode %s: %w", args[2], err)
	}
	return nil
}
