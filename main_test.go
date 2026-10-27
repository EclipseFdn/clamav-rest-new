package main

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal filename unchanged",
			input:    "document.pdf",
			expected: "document.pdf",
		},
		{
			name:     "filename with spaces",
			input:    "my document.pdf",
			expected: "my document.pdf",
		},
		{
			name:     "newline replaced",
			input:    "file\nname.txt",
			expected: "file_name.txt",
		},
		{
			name:     "carriage return replaced",
			input:    "file\rname.txt",
			expected: "file_name.txt",
		},
		{
			name:     "tab replaced",
			input:    "file\tname.txt",
			expected: "file_name.txt",
		},
		{
			name:     "null byte replaced",
			input:    "file\x00name.txt",
			expected: "file_name.txt",
		},
		{
			name:     "DEL character replaced",
			input:    "file\x7fname.txt",
			expected: "file_name.txt",
		},
		{
			name:     "long filename truncated",
			input:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.txt",
			expected: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...",
		},
		{
			name:     "exactly 100 chars not truncated",
			input:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			expected: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:     "unicode preserved",
			input:    "文档.pdf",
			expected: "文档.pdf",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSendError(t *testing.T) {
	recorder := httptest.NewRecorder()

	sendError(recorder, "Test error message")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	contentType := recorder.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var response ScanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response.Status != "error" {
		t.Errorf("response.Status = %q, want error", response.Status)
	}
	if response.Error != "Test error message" {
		t.Errorf("response.Error = %q, want 'Test error message'", response.Error)
	}
}

func TestScanHandlerMethodNotAllowed(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/scan", nil)
			recorder := httptest.NewRecorder()

			scanHandler(recorder, req)

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestScanHandlerNoFile(t *testing.T) {
	// Initialize config for the handler
	config = &Config{
		MaxUploadSize: 10 << 20,
	}

	req := httptest.NewRequest(http.MethodPost, "/scan", nil)
	req.Header.Set("Content-Type", "multipart/form-data")
	recorder := httptest.NewRecorder()

	scanHandler(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestHealthHandlerRequiresScanner(t *testing.T) {
	// Skip if scanner not initialized (requires clamd)
	if scanner == nil {
		t.Skip("scanner not initialized (requires clamd)")
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	healthHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	contentType := recorder.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
}

func setMaxUploadSize(t *testing.T, size int64) {
	t.Helper()
	old := config
	config = &Config{MaxUploadSize: size}
	t.Cleanup(func() { config = old })
}

func multipartRequest(t *testing.T, fields map[string]string, content io.Reader) *http.Request {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { pr.Close() })
	writer := multipart.NewWriter(pw)
	go func() {
		for k, v := range fields {
			writer.WriteField(k, v)
		}
		part, _ := writer.CreateFormFile("file", "big.vsix")
		_, err := io.Copy(part, content)
		writer.Close()
		pw.CloseWithError(err)
	}()
	req := httptest.NewRequest(http.MethodPost, "/scan", pr)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestSaveUploadStreamsToDisk(t *testing.T) {
	const size = 64 << 20
	setMaxUploadSize(t, 2*size)

	dir := t.TempDir()
	req := multipartRequest(t, map[string]string{"note": "x"}, io.LimitReader(zeroReader{}, size))

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	path, filename, err := saveUpload(httptest.NewRecorder(), req, dir)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}

	if filename != "big.vsix" {
		t.Errorf("filename = %q, want big.vsix", filename)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != size {
		t.Errorf("saved file: info=%v err=%v, want %d bytes", info, err, size)
	}
	// Buffering the upload would allocate at least its full size.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > size/4 {
		t.Errorf("allocated %d bytes for a %d byte upload", alloc, size)
	}
}

func TestSaveUploadTooLarge(t *testing.T) {
	setMaxUploadSize(t, 10<<20)
	dir := t.TempDir()
	req := multipartRequest(t, nil, io.LimitReader(zeroReader{}, config.MaxUploadSize+1))

	_, _, err := saveUpload(httptest.NewRecorder(), req, dir)

	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want *http.MaxBytesError", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestSaveUploadNoFilePart(t *testing.T) {
	setMaxUploadSize(t, 10<<20)
	body := strings.NewReader("--b\r\nContent-Disposition: form-data; name=\"other\"\r\n\r\nv\r\n--b--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/scan", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")

	_, _, err := saveUpload(httptest.NewRecorder(), req, t.TempDir())

	if !errors.Is(err, errNoFile) {
		t.Errorf("err = %v, want errNoFile", err)
	}
}

func TestScanHandlerTooLarge(t *testing.T) {
	setMaxUploadSize(t, 10<<20)
	t.Setenv("TMPDIR", t.TempDir())
	req := multipartRequest(t, nil, io.LimitReader(zeroReader{}, config.MaxUploadSize+1))
	recorder := httptest.NewRecorder()

	scanHandler(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	var response ScanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if response.Status != "error" {
		t.Errorf("response.Status = %q, want error", response.Status)
	}
}

func TestCopyUploadWriteErrorIsNotBadRequest(t *testing.T) {
	err := copyUpload(errWriter{syscall.ENOSPC}, strings.NewReader("data"))

	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("err = %v, want ENOSPC", err)
	}
	if errors.Is(err, errBadForm) {
		t.Errorf("write failure classified as bad request: %v", err)
	}
}

func TestCopyUploadReadErrorIsBadRequest(t *testing.T) {
	err := copyUpload(io.Discard, errReader{io.ErrUnexpectedEOF})

	if !errors.Is(err, errBadForm) {
		t.Errorf("err = %v, want errBadForm", err)
	}
}

func TestCopyUploadKeepsSizeLimitError(t *testing.T) {
	err := copyUpload(io.Discard, errReader{&http.MaxBytesError{Limit: 1}})

	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) || errors.Is(err, errBadForm) {
		t.Errorf("err = %v, want unwrapped *http.MaxBytesError", err)
	}
}
