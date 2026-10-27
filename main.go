package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

// ScanResponse is the JSON response for scan requests
type ScanResponse struct {
	Status       string   `json:"status"`        // "clean", "infected", "error"
	Threats      []Threat `json:"threats"`       // List of detected threats
	ScannedFiles int      `json:"scanned_files"` // Number of files scanned
	ScanTimeMs   int64    `json:"scan_time_ms"`  // Scan duration in milliseconds
	Error        string   `json:"error,omitempty"`
}

// Threat represents a detected virus/malware
type Threat struct {
	Name     string `json:"name"`                // Virus/malware name
	File     string `json:"file"`                // File path within archive
	FileHash string `json:"file_hash,omitempty"` // SHA256 hash of infected file
	Severity string `json:"severity"`            // Always "critical" for malware
}

// HealthResponse for health check endpoint
type HealthResponse struct {
	Status        string `json:"status"`
	ClamAVVersion string `json:"clamav_version,omitempty"`
	DBVersion     string `json:"db_version,omitempty"`
}

// Global scanner instance
var scanner *Scanner

// Global config instance
var config *Config

func main() {
	// Load configuration from environment variables
	config = LoadConfig()

	// Log configuration on startup
	log.Printf("ClamAV REST server starting...")
	config.LogConfig()

	// Initialize scanner with configuration
	scanner = NewScanner(config)

	// Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/scan", scanHandler)

	// Configure server with timeouts to prevent slow-loris attacks
	// and connection exhaustion
	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mux,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	log.Printf("Listening on port %s", config.Port)

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

// healthHandler returns service health status.
// Returns 503 Service Unavailable if clamd is not running.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	version, dbVersion, err := scanner.GetVersion()

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		// clamd is unavailable - return 503 so Kubernetes knows we're unhealthy
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(HealthResponse{
			Status:        "unhealthy",
			ClamAVVersion: "",
			DBVersion:     "",
		})
		return
	}

	json.NewEncoder(w).Encode(HealthResponse{
		Status:        "ok",
		ClamAVVersion: version,
		DBVersion:     dbVersion,
	})
}

// scanHandler handles file upload and scanning
func scanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()

	tempPath, filename, err := saveUpload(w, r, "")
	if err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			log.Printf("Upload exceeds %d bytes", tooLarge.Limit)
			sendErrorStatus(w, "Upload too large", http.StatusRequestEntityTooLarge)
		case errors.Is(err, errBadForm):
			log.Printf("Failed to parse multipart form: %v", err)
			sendError(w, "Invalid request format")
		case errors.Is(err, errNoFile):
			log.Printf("No file in request: %v", err)
			sendError(w, "No file provided in request")
		default:
			log.Printf("Failed to save upload: %v", err)
			sendError(w, "Server error during file processing")
		}
		return
	}
	defer os.Remove(tempPath)

	// Sanitize filename for logging (remove control characters, limit length)
	safeFilename := sanitizeFilename(filename)
	log.Printf("Received file: %s", safeFilename)

	result, err := scanner.ScanFile(tempPath)
	if err != nil {
		log.Printf("Scan failed for %s: %v", safeFilename, err)

		errorResponse := ScanResponse{
			Status:       "error",
			Threats:      nil,
			ScannedFiles: 0,
			ScanTimeMs:   time.Since(startTime).Milliseconds(),
			Error:        err.Error(),
		}

		sendResponse(w, errorResponse)
		return
	}

	status := "clean"
	if len(result.Threats) > 0 {
		status = "infected"
	}

	response := ScanResponse{
		Status:       status,
		Threats:      result.Threats,
		ScannedFiles: result.ScannedFiles,
		ScanTimeMs:   time.Since(startTime).Milliseconds(),
	}

	log.Printf("Scan completed: %s - %s (%d threats, %d files, %dms)",
		safeFilename, status, len(result.Threats), result.ScannedFiles, response.ScanTimeMs)

	sendResponse(w, response)
}

var (
	errBadForm = errors.New("invalid multipart request")
	errNoFile  = errors.New("no file part in request")
)

// saveUpload streams the "file" part of a multipart request into a new temp
// file in dir (the default temp dir if empty), so memory use stays constant
// regardless of upload size. The caller removes the returned path.
func saveUpload(w http.ResponseWriter, r *http.Request, dir string) (path, filename string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, config.MaxUploadSize)

	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", errBadForm, err)
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return "", "", errNoFile
		}
		if err != nil {
			return "", "", classifyReadError(err)
		}
		if part.FormName() == "file" {
			return copyPart(part, dir)
		}
	}
}

// copyPart writes the part to a new temp file in dir and returns its path.
func copyPart(part *multipart.Part, dir string) (string, string, error) {
	defer part.Close()

	tempFile, err := os.CreateTemp(dir, "clamav-scan-*")
	if err != nil {
		return "", "", err
	}
	if err := copyUpload(tempFile, part); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return "", "", err
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFile.Name())
		return "", "", err
	}
	return tempFile.Name(), part.FileName(), nil
}

// copyUpload copies src to dst. Only src failures are classified as request
// errors; a dst failure (disk full, I/O error) stays a plain server error.
func copyUpload(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, readClassifier{src})
	return err
}

type readClassifier struct{ io.Reader }

func (r readClassifier) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && err != io.EOF {
		err = classifyReadError(err)
	}
	return n, err
}

// classifyReadError keeps size-limit errors intact and marks any other
// body read failure as a malformed request.
func classifyReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return err
	}
	return fmt.Errorf("%w: %v", errBadForm, err)
}

// sendError sends an error response to the client.
// Note: message should be a generic, sanitized string - do not include internal errors.
func sendError(w http.ResponseWriter, message string) {
	sendErrorStatus(w, message, http.StatusInternalServerError)
}

func sendErrorStatus(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ScanResponse{
		Status: "error",
		Error:  message,
	})
}

func sendResponse(w http.ResponseWriter, response ScanResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// sanitizeFilename removes control characters and limits length for safe logging.
func sanitizeFilename(filename string) string {
	// Limit length to prevent log flooding
	const maxLen = 100
	if len(filename) > maxLen {
		filename = filename[:maxLen] + "..."
	}

	var result []rune
	for _, r := range filename {
		if r < 32 || r == 127 {
			result = append(result, '_')
		} else {
			result = append(result, r)
		}
	}

	return string(result)
}
