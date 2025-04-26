package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/mmynk/dwny/downloader"
	"go.uber.org/zap"
)

var (
	url        = flag.String("u", "", "URL to download")
	outputPath = flag.String("o", "", "Output path")
)

func main() {
	parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	go func() {
		<-sigCh
		cancel()
	}()

	logger := setupLogger()
	defer logger.Sync()

	downloader := downloader.NewDownloader(ctx, *url, *outputPath, logger)
	err := downloader.Download(ctx)
	if err != nil {
		logger.Error("Failed to download file", zap.Error(err))
		os.Exit(1)
	}
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
	cfg.OutputPaths = []string{"/var/log/dwny/dwny.log"}
	cfg.ErrorOutputPaths = []string{"/var/log/dwny/dwny-error.log"}
	logger, err := cfg.Build()
	if err != nil {
		fmt.Println("Failed to build logger:", err)
		os.Exit(1)
	}

	zap.ReplaceGlobals(logger)
	return logger
}

func parseFlags() {
	flag.Parse()

	if *url == "" {
		fmt.Println("URL is required")
		os.Exit(1)
	}
}
