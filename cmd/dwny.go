package cmd

import (
	"context"
	"os"
	"os/signal"

	"github.com/mmynk/dwny/downloader"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	urls    []string
	dir     string
	workers int
	logger  *zap.Logger
	rootCmd = &cobra.Command{
		Use:   "dwny",
		Short: "Download files from the web",
		Long:  `dwny is a tool to download multiple files from the web simultaneously.`,
		Run: func(cmd *cobra.Command, args []string) {
			download(urls, dir, workers)
			cmd.Println("\nDownload completed")
		},
	}
)

func init() {
	rootCmd.Flags().StringSliceVarP(&urls, "urls", "u", []string{}, "URLs to download")
	rootCmd.Flags().StringVarP(&dir, "dir", "d", "", "Output directory")
	rootCmd.Flags().IntVarP(&workers, "workers", "w", 10, "Number of workers to use")
	rootCmd.MarkFlagRequired("urls")
	rootCmd.MarkFlagRequired("dir")
}

func Execute(l *zap.Logger) error {
	logger = l
	return rootCmd.Execute()
}

func download(urls []string, dir string, workers int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	go func() {
		<-sigCh
		cancel()
	}()

	downloader := downloader.NewDownloader(ctx, urls, dir, workers, logger)
	results := downloader.Download(ctx)
	for _, result := range results {
		if result.Err != nil {
			logger.Error("Failed to download file", zap.String("url", result.URL), zap.Error(result.Err))
		}
	}
}
