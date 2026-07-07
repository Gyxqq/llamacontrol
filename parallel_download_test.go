package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadFileAutoUsesParallelRanges(t *testing.T) {
	data := bytes.Repeat([]byte("abcdef0123456789"), (minParallelDownloadSize/16)+1)
	var rangeRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			http.ServeContent(w, r, "model.gguf", dataModTime, bytes.NewReader(data))
			return
		}
		rangeRequests.Add(1)

		start, end, ok := parseTestRange(rangeHeader)
		if !ok || start < 0 || end >= int64(len(data)) || start > end {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "model.part")
	result, err := downloadFileAuto(t.Context(), server.Client(), server.URL, nil, path, int64(len(data)), nil)
	if err != nil {
		t.Fatalf("downloadFileAuto failed: %v", err)
	}
	if !result.Parallel {
		t.Fatal("expected parallel download")
	}
	if rangeRequests.Load() < 2 {
		t.Fatalf("expected multiple range requests, got %d", rangeRequests.Load())
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded file content mismatch")
	}
}

func TestDownloadFileAutoFallsBackToSingleStream(t *testing.T) {
	data := bytes.Repeat([]byte("fallback"), (minParallelDownloadSize/8)+1)
	var fullRequests atomic.Int64
	var rangeRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			return
		}
		if r.Header.Get("Range") != "" {
			rangeRequests.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
		fullRequests.Add(1)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "model.part")
	result, err := downloadFileAuto(t.Context(), server.Client(), server.URL, nil, path, int64(len(data)), nil)
	if err != nil {
		t.Fatalf("downloadFileAuto failed: %v", err)
	}
	if result.Parallel {
		t.Fatal("expected single-stream fallback")
	}
	if rangeRequests.Load() == 0 {
		t.Fatal("expected an attempted range request before fallback")
	}
	if fullRequests.Load() != 1 {
		t.Fatalf("expected one full fallback request, got %d", fullRequests.Load())
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded fallback file content mismatch")
	}
}

func TestDownloadFileAutoRetriesInterruptedRange(t *testing.T) {
	data := bytes.Repeat([]byte("retry-range-"), (minParallelDownloadSize/12)+1)
	var rangeRequests atomic.Int64
	var mu sync.Mutex
	interrupted := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			return
		}

		start, end, ok := parseTestRange(r.Header.Get("Range"))
		if !ok || start < 0 || end >= int64(len(data)) || start > end {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		rangeRequests.Add(1)

		mu.Lock()
		shouldInterrupt := !interrupted && start == 0
		if shouldInterrupt {
			interrupted = true
		}
		mu.Unlock()

		length := end - start + 1
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(http.StatusPartialContent)
		if shouldInterrupt {
			_, _ = w.Write(data[start : start+(length/2)])
			return
		}
		_, _ = w.Write(data[start : end+1])
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "model.part")
	result, err := downloadFileAuto(t.Context(), server.Client(), server.URL, nil, path, int64(len(data)), nil)
	if err != nil {
		t.Fatalf("downloadFileAuto failed: %v", err)
	}
	if !result.Parallel {
		t.Fatal("expected parallel download")
	}
	if rangeRequests.Load() <= int64(chooseDownloadWorkers(int64(len(data)))) {
		t.Fatalf("expected an extra range request for retry, got %d", rangeRequests.Load())
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded file content mismatch")
	}
}

var dataModTime = time.Unix(0, 0)

func parseTestRange(header string) (int64, int64, bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(header, prefix), "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return start, end, true
}
