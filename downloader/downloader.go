package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
)

var (
	MaxSimultaneousDownloads = 10
	currentLine              = 0
)

type Download struct {
	filepath       string
	downloadedSize int64
	totalSize      int64
	line           int
}

func NewDownload(filepath string, totalSize int64, line int) *Download {
	return &Download{
		filepath:  filepath,
		totalSize: totalSize,
		line:      line,
	}
}

type Downloader struct {
	urls       []string
	outputDir  string
	numWorkers int
	client     *http.Client
	results    []*DownloadResult
	mu         sync.Mutex
	logger     *zap.Logger
}

type DownloadResult struct {
	URL      string
	Filename string
	Err      error
}

func NewDownloader(ctx context.Context, urls []string, outputDir string, numWorkers int, logger *zap.Logger) *Downloader {
	if numWorkers <= 0 {
		numWorkers = 1
	} else if numWorkers > MaxSimultaneousDownloads {
		numWorkers = MaxSimultaneousDownloads
	}

	return &Downloader{
		urls:       urls,
		outputDir:  outputDir,
		numWorkers: numWorkers,
		client:     &http.Client{},
		results:    []*DownloadResult{},
		logger:     logger,
	}
}

func (d *Downloader) Download(ctx context.Context) []*DownloadResult {
	jobs := make(chan string, len(d.urls))

	wg := sync.WaitGroup{}
	for i := range d.numWorkers {
		wg.Add(1)
		go d.worker(ctx, jobs, &wg, i)
	}

jobLoop:
	for _, url := range d.urls {
		select {
		case <-ctx.Done():
			break jobLoop
		case jobs <- url:
		}
	}
	close(jobs)

	wg.Wait()

	return d.results
}

func (d *Downloader) worker(ctx context.Context, jobs <-chan string, wg *sync.WaitGroup, line int) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case url, ok := <-jobs:
			if !ok {
				return
			}
			result := d.downloadFile(ctx, url, line)

			d.mu.Lock()
			d.results = append(d.results, result)
			d.mu.Unlock()
		}
	}
}
func (d *Downloader) downloadFile(ctx context.Context, url string, line int) *DownloadResult {
	result := &DownloadResult{
		URL: url,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		result.Err = fmt.Errorf("failed to create request: %w", err)
		return result
	}

	// Add browser-like headers to avoid 403 errors
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")

	// Get the file information
	resp, err := d.client.Do(req)
	if err != nil {
		result.Err = fmt.Errorf("failed to make request: %w", err)
		return result
	}
	defer resp.Body.Close()

	d.logger.Debug("Response headers", zap.String("url", url), zap.Any("headers", resp.Header))

	if resp.StatusCode != http.StatusOK {
		result.Err = fmt.Errorf("non-OK status code: %d got %s", resp.StatusCode, resp.Status)
		return result
	}

	size := getFileSize(resp)
	if size == 0 {
		result.Err = errors.New("file size is 0")
		return result
	}

	filename := filepath.Base(url)
	filepath := filepath.Join(d.outputDir, filename)
	result.Filename = filepath
	download := NewDownload(filepath, size, line)
	// Check if the file already exists
	info, err := os.Stat(download.filepath)
	if err != nil {
		err = d.startDownload(ctx, resp, download)
		if err != nil {
			result.Err = err
			return result
		}
		return result
	}

	if info.Size() > size || info.Size() == 0 {
		d.logger.Debug("File is incomplete or corrupted, downloading again",
			zap.String("url", url),
			zap.String("outputPath", download.filepath),
		)
		err = d.startDownload(ctx, resp, download)
		if err != nil {
			result.Err = err
			return result
		}
		return result
	}

	if info.Size() == size {
		d.logger.Debug("File already exists",
			zap.String("url", url),
			zap.String("outputPath", download.filepath),
		)
		return result
	}

	download.downloadedSize = info.Size()
	d.logger.Debug("Continuing download", zap.String("url", url), zap.String("path", download.filepath))
	err = d.continueDownload(ctx, resp, download)
	if err != nil {
		result.Err = err
		return result
	}
	return result
}

func (d *Downloader) startDownload(ctx context.Context, resp *http.Response, download *Download) error {
	file, err := os.Create(download.filepath)
	if err != nil {
		return err
	}
	defer file.Close()
	defer resp.Body.Close()

	d.logger.Debug("Downloading file", zap.String("filename", download.filepath), zap.String("size", prettySize(download.totalSize)))
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
	file, err := os.OpenFile(download.filepath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	defer resp.Body.Close()

	d.logger.Debug("Downloading file",
		zap.String("filename", download.filepath),
		zap.String("remaining", prettySize(download.totalSize-download.downloadedSize)),
		zap.String("size", prettySize(download.totalSize)),
	)
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
	filename := filepath.Base(download.filepath)
	// move the cursor up to the line number
	if currentLine > download.line {
		moveUp(currentLine - download.line)
		currentLine = download.line
	} else if currentLine < download.line {
		moveDown(download.line - currentLine)
		currentLine = download.line
	}
	moveToStart()

	if download.totalSize <= 0 {
		fmt.Printf("%s: %s / unknown size", filename, prettySize(download.downloadedSize))
		return
	}

	progress := float64(download.downloadedSize) / float64(download.totalSize) * 100
	progress = min(progress, 100)
	barWidth := 30
	filledWidth := int(progress / 100 * float64(barWidth))
	emptyWidth := barWidth - filledWidth

	fmt.Printf("%s: [%s%s] %.2f%%", filename, strings.Repeat("█", filledWidth), strings.Repeat(" ", emptyWidth), progress)
	os.Stdout.Sync()
}

func moveUp(n int) {
	fmt.Printf("\033[%dA", n)
}

func moveDown(n int) {
	fmt.Printf("\033[%dB", n)
}

func moveToStart() {
	fmt.Print("\r")
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

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
