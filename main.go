package main

import (
	"encoding/json"
	"io"
	"log"
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

	// Parse multipart form with configured size limit
	if err := r.ParseMultipartForm(config.MaxUploadSize); err != nil {
		// Log full error internally, return generic message to client
		log.Printf("Failed to parse multipart form: %v", err)
		sendError(w, "Invalid request format")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		log.Printf("No file in request: %v", err)
		sendError(w, "No file provided in request")
		return
	}
	defer file.Close()

	// Sanitize filename for logging (remove control characters, limit length)
	safeFilename := sanitizeFilename(header.Filename)
	log.Printf("Received file: %s (%d bytes)", safeFilename, header.Size)

	tempFile, err := os.CreateTemp("", "clamav-scan-*")
	if err != nil {
		log.Printf("Failed to create temp file: %v", err)
		sendError(w, "Server error during file processing")
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		log.Printf("Failed to write temp file: %v", err)
		sendError(w, "Server error during file processing")
		return
	}
	tempFile.Close()

	result, err := scanner.ScanFile(tempFile.Name())
	if err != nil {
		log.Printf("Scan failed for %s: %v", safeFilename, err)
		sendError(w, "Scan operation failed")
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// sendError sends an error response to the client.
// Note: message should be a generic, sanitized string - do not include internal errors.
func sendError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(ScanResponse{
		Status: "error",
		Error:  message,
	})
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
