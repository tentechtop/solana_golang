package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"solana_golang/internal/posnode"
)

func main() {
	configPath := flag.String("config", "", "posnode config json path")
	printPeerSeed := flag.String("print-peer-id", "", "print peer id for seed and exit")
	flag.Parse()

	if strings.TrimSpace(*printPeerSeed) != "" {
		peerID, err := posnode.PeerIDFromSeed(*printPeerSeed)
		if err != nil {
			exitError("posnode: derive peer id: %v", err)
		}
		fmt.Println(peerID)
		return
	}
	if strings.TrimSpace(*configPath) == "" {
		exitError("posnode: -config is required")
	}
	if err := posnode.Run(*configPath); err != nil {
		slog.Error("posnode exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
