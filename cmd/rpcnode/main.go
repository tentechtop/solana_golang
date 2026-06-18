package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"solana_golang/internal/rpcnode"
)

func main() {
	configPath := flag.String("config", "", "rpcnode config json path")
	printPeerSeed := flag.String("print-peer-id", "", "print peer id for seed and exit")
	flag.Parse()

	if strings.TrimSpace(*printPeerSeed) != "" {
		peerID, err := rpcnode.PeerIDFromSeed(*printPeerSeed)
		if err != nil {
			exitError("rpcnode: derive peer id: %v", err)
		}
		fmt.Println(peerID)
		return
	}
	if strings.TrimSpace(*configPath) == "" {
		exitError("rpcnode: -config is required")
	}
	if err := rpcnode.Run(*configPath); err != nil {
		slog.Error("rpcnode exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
