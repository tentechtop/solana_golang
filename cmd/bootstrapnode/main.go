package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"solana_golang/internal/bootstrapnode"
)

func main() {
	configPath := flag.String("config", "", "bootstrapnode config json path")
	printPeerSeed := flag.String("print-peer-id", "", "print peer id for seed and exit")
	flag.Parse()

	if strings.TrimSpace(*printPeerSeed) != "" {
		peerID, err := bootstrapnode.PeerIDFromSeed(*printPeerSeed)
		if err != nil {
			exitError("bootstrapnode: derive peer id: %v", err)
		}
		fmt.Println(peerID)
		return
	}
	if strings.TrimSpace(*configPath) == "" {
		exitError("bootstrapnode: -config is required")
	}
	if err := bootstrapnode.Run(*configPath); err != nil {
		slog.Error("bootstrapnode exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
