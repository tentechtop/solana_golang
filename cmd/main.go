package main

import (
	"log/slog"

	"solana_golang/utils"
)

func main() {
	logger, err := utils.LoggerFromEnv()
	if err != nil {
		panic(err)
	}
	slog.SetDefault(logger)
	logger.Info("application started", slog.String("module", "solana_golang"))
}
