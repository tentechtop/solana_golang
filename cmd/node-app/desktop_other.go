//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func openURL(address string)   { _ = exec.Command("xdg-open", address).Start() }
func showError(message string) { fmt.Fprintln(os.Stderr, message) }
