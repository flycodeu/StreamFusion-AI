package serviceinstall

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNativeDownloadResumesAndRechecksCompleteCache(t *testing.T) {
	data := []byte("verified package bytes")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Write(data[:5]) // EOF while the declared response is incomplete.
			return
		}
		if r.Header.Get("Range") != "bytes=5-" {
			t.Errorf("expected preserved prefix, range=%q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[5:])
	}))
	defer server.Close()
	part := NativePart{Name: "package", URL: server.URL, SHA256: rawHash(data), ByteSize: int64(len(data))}
	path := filepath.Join(t.TempDir(), part.Name)
	var lastProgress int64
	progress := func(_ string, size, _ int64) { lastProgress = size }
	if _, err := downloadNativePart(context.Background(), server.Client(), part, path, ArchiveLimit, progress); err == nil || err.Error() != "NATIVE_DOWNLOAD_INTERRUPTED" {
		t.Fatalf("interrupted response must remain resumable: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data[:5]) {
		t.Fatalf("lost partial file: %q %v", got, err)
	}
	for range 2 {
		if size, err := downloadNativePart(context.Background(), server.Client(), part, path, ArchiveLimit, progress); err != nil || size != int64(len(data)) {
			t.Fatalf("resume/reuse: size=%d err=%v", size, err)
		}
	}
	if calls != 2 || lastProgress != int64(len(data)) {
		t.Fatalf("cached bytes were downloaded again or progress missing: calls=%d progress=%d", calls, lastProgress)
	}
	if err := os.WriteFile(path, make([]byte, len(data)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := downloadNativePart(context.Background(), server.Client(), part, path, ArchiveLimit, nil); err == nil || calls != 2 {
		t.Fatal("corrupt complete cache was trusted or sent to network", err, calls)
	}
}

func TestNativeDownloadRangeIgnoredRestartsAndWrongRangeRejected(t *testing.T) {
	data := []byte("complete package")
	for _, wrongRange := range []bool{false, true} {
		t.Run(strconv.FormatBool(wrongRange), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=3-" {
					t.Error("missing range request")
				}
				if wrongRange {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 2-%d/%d", len(data)-1, len(data)))
					w.WriteHeader(http.StatusPartialContent)
				}
				w.Write(data)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "package")
			if err := os.WriteFile(path, data[:3], 0600); err != nil {
				t.Fatal(err)
			}
			part := NativePart{Name: "package", URL: server.URL, SHA256: rawHash(data), ByteSize: int64(len(data))}
			_, err := downloadNativePart(context.Background(), server.Client(), part, path, ArchiveLimit, nil)
			got, readErr := os.ReadFile(path)
			if wrongRange {
				if err == nil || err.Error() != "NATIVE_SOURCE_RANGE_INVALID" || string(got) != string(data[:3]) {
					t.Fatal("invalid response modified the verified prefix boundary", err, string(got))
				}
			} else if err != nil || readErr != nil || string(got) != string(data) {
				t.Fatal("server without Range support did not restart cleanly", err, string(got))
			}
		})
	}
}

func TestNativeDownloadRejectsDigestMismatchAndCacheLink(t *testing.T) {
	data := []byte("untrusted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	part := NativePart{Name: "package", URL: server.URL, SHA256: rawHash([]byte("trusted!!")), ByteSize: int64(len(data))}
	path := filepath.Join(t.TempDir(), "package")
	if _, err := downloadNativePart(context.Background(), server.Client(), part, path, ArchiveLimit, nil); err == nil || err.Error() != "NATIVE_SOURCE_HASH_OR_SIZE_MISMATCH" {
		t.Fatal("downloaded bytes were not verified", err)
	}
	link := filepath.Join(t.TempDir(), "package-link")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := downloadNativePart(context.Background(), server.Client(), part, link, ArchiveLimit, nil); err == nil || err.Error() != "NATIVE_DOWNLOAD_CACHE_INVALID" {
		t.Fatal("cache symlink accepted", err)
	}
}
