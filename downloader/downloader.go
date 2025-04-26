package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

type Download struct {
	filename       string
	outputPath     string
	downloadedSize int64
	totalSize      int64
}

func NewDownload(filename string, outputPath string, totalSize int64) *Download {
	return &Download{
		filename:   filename,
		outputPath: outputPath,
		totalSize:  totalSize,
	}
}

type Downloader struct {
	url        string
	outputPath string
	client     *http.Client
	logger     *zap.Logger
}

func NewDownloader(ctx context.Context, url string, outputPath string, logger *zap.Logger) *Downloader {
	return &Downloader{
		url:        url,
		outputPath: outputPath,
		client:     &http.Client{},
		logger:     logger,
	}
}

func (d *Downloader) Download(ctx context.Context) error {
	return d.downloadFile(ctx)
}

func (d *Downloader) downloadFile(ctx context.Context) error {
	// Get the file information
	resp, err := d.client.Get(d.url)
	if err != nil {
		return err
	}
	d.logger.Debug("Response headers", zap.Any("headers", resp.Header))

	size := getFileSize(resp)
	if size == 0 {
		return errors.New("file size is 0")
	}

	download := NewDownload(d.url, d.outputPath, size)
	// Check if the file already exists
	info, err := os.Stat(d.outputPath)
	if err != nil {
		return d.startDownload(ctx, resp, download)
	}

	if info.Size() == size {
		d.logger.Debug("File already exists", zap.String("url", d.url), zap.String("outputPath", d.outputPath))
		return nil
	}

	if info.Size() > size || info.Size() == 0 {
		d.logger.Debug("File is incomplete or corrupted, downloading again", zap.String("url", d.url), zap.String("outputPath", d.outputPath))
		return d.startDownload(ctx, resp, download)
	}

	download.downloadedSize = info.Size()
	d.logger.Debug("Continuing download", zap.String("url", d.url), zap.String("path", d.outputPath))
	return d.continueDownload(ctx, resp, download)
}

func (d *Downloader) startDownload(ctx context.Context, resp *http.Response, download *Download) error {
	file, err := os.Create(download.outputPath)
	if err != nil {
		return err
	}
	defer file.Close()
	defer resp.Body.Close()

	d.logger.Debug("Downloading file", zap.String("filename", download.filename), zap.String("size", prettySize(download.totalSize)))
	buffer := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Download cancelled by user")
			return errors.New("download cancelled")
		default:
			n, err := resp.Body.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}

			_, err = file.Write(buffer[:n])
			if err != nil {
				return err
			}

			download.downloadedSize += int64(n)
			updateProgress(download)
		}
	}
}

func (d *Downloader) continueDownload(ctx context.Context, resp *http.Response, download *Download) error {
	file, err := os.OpenFile(download.outputPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	defer resp.Body.Close()

	d.logger.Debug("Downloading file", zap.String("filename", download.filename), zap.String("remaining", prettySize(download.totalSize-download.downloadedSize)), zap.String("size", prettySize(download.totalSize)))
	buffer := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Download cancelled by user")
			return errors.New("download cancelled")
		default:
			n, err := resp.Body.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}

			_, err = file.Write(buffer[:n])
			if err != nil {
				return err
			}

			download.downloadedSize += int64(n)
			updateProgress(download)
		}
	}
}

func getFileSize(resp *http.Response) int64 {
	sizeFromHeader := resp.Header.Get("Content-Length")
	if sizeFromHeader == "" {
		return 0
	}

	size, err := strconv.ParseInt(sizeFromHeader, 10, 64)
	if err != nil {
		return 0
	}

	return size
}

func updateProgress(download *Download) {
	if download.totalSize == 0 {
		fmt.Printf("\r%s: %s / unknown size", download.filename, prettySize(download.downloadedSize))
		return
	}

	progress := float64(download.downloadedSize) / float64(download.totalSize) * 100
	barWidth := 30
	filledWidth := int(progress / 100 * float64(barWidth))
	emptyWidth := barWidth - filledWidth

	fmt.Printf("\r%s: [%s%s] %.2f%%", download.filename, strings.Repeat("█", filledWidth), strings.Repeat(" ", emptyWidth), progress)
	os.Stdout.Sync()
}

func prettySize(size int64) string {
	suffixes := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}

	i := 0
	for size > 1024 && i < len(suffixes)-1 {
		size /= 1024
		i++
	}

	return fmt.Sprintf("%d %s", size, suffixes[i])
}
