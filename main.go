package main

import (
	"fmt"
	"os"

	"github.com/mmynk/dwny/cmd"
	"go.uber.org/zap"
)

func main() {
	logger := setupLogger()
	defer logger.Sync()

	cmd.Execute(logger)
}

func setupLogger() *zap.Logger {
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "INFO"
	}

	level, err := zap.ParseAtomicLevel(logLevel)
	if err != nil {
		fmt.Println("Invalid log level:", err)
		os.Exit(1)
	}

	cfg := zap.NewDevelopmentConfig()
	cfg.Level = level
	logger, err := cfg.Build()
	if err != nil {
		fmt.Println("Failed to build logger:", err)
		os.Exit(1)
	}

	zap.ReplaceGlobals(logger)
	return logger
}
