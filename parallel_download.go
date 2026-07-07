package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	minParallelDownloadSize = 32 * 1024 * 1024
	downloadChunkSize       = 16 * 1024 * 1024
	maxDownloadWorkers      = 16
	maxDownloadRangeRetries = 5
)

var errRangeIncomplete = errors.New("range response ended before requested bytes were read")

type downloadProgressFunc func(downloaded, total int64)

type downloadResult struct {
	DownloadedBytes int64
	TotalBytes      int64
	Parallel        bool
	Workers         int
}

func downloadFileAuto(ctx context.Context, client *http.Client, url string, headers map[string]string, path string, totalHint int64, progress downloadProgressFunc) (downloadResult, error) {
	if client == nil {
		client = http.DefaultClient
	}

	total, ranges := probeDownload(ctx, client, url, headers)
	if total <= 0 {
		total = totalHint
	}

	if ranges && total >= minParallelDownloadSize {
		workers := chooseDownloadWorkers(total)
		result, err := downloadFileParallel(ctx, client, url, headers, path, total, workers, progress)
		if err == nil {
			result.Parallel = true
			result.Workers = workers
			return result, nil
		}
		if errors.Is(err, context.Canceled) {
			return downloadResult{}, err
		}
		log.Warnf("download: parallel download unavailable, falling back to single stream: %v", err)
		_ = os.Remove(path)
	}

	return downloadFileSingle(ctx, client, url, headers, path, total, progress)
}

func probeDownload(ctx context.Context, client *http.Client, url string, headers map[string]string) (int64, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return -1, false
	}
	applyDownloadHeaders(req, headers)

	resp, err := client.Do(req)
	if err != nil {
		return -1, false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return -1, false
	}

	total := resp.ContentLength
	if total <= 0 {
		if value := resp.Header.Get("Content-Length"); value != "" {
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				total = parsed
			}
		}
	}

	acceptRanges := strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes")
	return total, acceptRanges
}

func chooseDownloadWorkers(total int64) int {
	workers := int((total + downloadChunkSize - 1) / downloadChunkSize)
	if workers < 2 {
		workers = 2
	}
	cpuLimit := runtime.NumCPU() * 2
	if cpuLimit < 4 {
		cpuLimit = 4
	}
	if workers > cpuLimit {
		workers = cpuLimit
	}
	if workers > maxDownloadWorkers {
		workers = maxDownloadWorkers
	}
	return workers
}

func downloadFileSingle(ctx context.Context, client *http.Client, url string, headers map[string]string, path string, total int64, progress downloadProgressFunc) (downloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return downloadResult{}, err
	}
	applyDownloadHeaders(req, headers)

	resp, err := client.Do(req)
	if err != nil {
		return downloadResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return downloadResult{}, fmt.Errorf("下载返回 %d: %s", resp.StatusCode, string(body))
	}

	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	out, err := os.Create(path)
	if err != nil {
		return downloadResult{}, err
	}
	defer out.Close()

	var downloaded int64
	buf := make([]byte, 128*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				return downloadResult{}, writeErr
			}
			downloaded += int64(n)
			if progress != nil {
				progress(downloaded, total)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return downloadResult{DownloadedBytes: downloaded, TotalBytes: total}, nil
			}
			return downloadResult{}, readErr
		}
	}
}

func downloadFileParallel(ctx context.Context, client *http.Client, url string, headers map[string]string, path string, total int64, workers int, progress downloadProgressFunc) (downloadResult, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return downloadResult{}, err
	}
	defer out.Close()

	if err := out.Truncate(total); err != nil {
		return downloadResult{}, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type part struct {
		start int64
		end   int64
	}

	parts := make(chan part)
	errCh := make(chan error, 1)
	var downloaded atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 128*1024)
			for p := range parts {
				if err := downloadRange(ctx, client, url, headers, out, p.start, p.end, buf, &downloaded, total, progress); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}

	go func() {
		defer close(parts)
		for start := int64(0); start < total; start += downloadChunkSize {
			end := start + downloadChunkSize - 1
			if end >= total {
				end = total - 1
			}
			select {
			case <-ctx.Done():
				return
			case parts <- part{start: start, end: end}:
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if downloaded.Load() != total {
			return downloadResult{}, fmt.Errorf("并发下载大小不匹配: got %d, want %d", downloaded.Load(), total)
		}
		return downloadResult{DownloadedBytes: downloaded.Load(), TotalBytes: total}, nil
	case err := <-errCh:
		<-done
		return downloadResult{}, err
	case <-ctx.Done():
		<-done
		return downloadResult{}, ctx.Err()
	}
}

func downloadRange(ctx context.Context, client *http.Client, url string, headers map[string]string, out *os.File, start, end int64, buf []byte, downloaded *atomic.Int64, total int64, progress downloadProgressFunc) error {
	offset := start
	attempts := 0

	for offset <= end {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		applyDownloadHeaders(req, headers)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))

		resp, err := client.Do(req)
		if err != nil {
			if isRetryableDownloadError(err) && attempts < maxDownloadRangeRetries {
				attempts++
				sleepBeforeRangeRetry(ctx, attempts)
				continue
			}
			return err
		}

		if resp.StatusCode != http.StatusPartialContent {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close()
			return fmt.Errorf("Range 请求返回 %d: %s", resp.StatusCode, string(body))
		}

		readErr := copyRangeResponse(resp.Body, out, &offset, end, buf, downloaded, total, progress)
		resp.Body.Close()
		if readErr == nil {
			attempts = 0
			continue
		}
		if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
			return readErr
		}
		if attempts >= maxDownloadRangeRetries {
			return fmt.Errorf("Range 下载不完整: %d-%d got %d bytes: %w", start, end, offset-start, readErr)
		}
		attempts++
		log.Warnf("download: range %d-%d interrupted at %d bytes, retrying (%d/%d): %v", start, end, offset-start, attempts, maxDownloadRangeRetries, readErr)
		sleepBeforeRangeRetry(ctx, attempts)
	}

	return nil
}

func copyRangeResponse(body io.Reader, out *os.File, offset *int64, end int64, buf []byte, downloaded *atomic.Int64, total int64, progress downloadProgressFunc) error {
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := out.WriteAt(buf[:n], *offset); writeErr != nil {
				return writeErr
			}
			*offset += int64(n)
			current := downloaded.Add(int64(n))
			if progress != nil {
				progress(current, total)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if *offset <= end {
					return errRangeIncomplete
				}
				return nil
			}
			return readErr
		}
	}
}

func isRetryableDownloadError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, context.DeadlineExceeded)
}

func sleepBeforeRangeRetry(ctx context.Context, attempt int) {
	delay := time.Duration(attempt) * 300 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func applyDownloadHeaders(req *http.Request, headers map[string]string) {
	req.Header.Set("User-Agent", "llamacontrol/1.0")
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
}
