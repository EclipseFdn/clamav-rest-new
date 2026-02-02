package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all service configuration.
// All settings can be overridden via environment variables.
type Config struct {
	// Server settings
	Port      string
	DebugMode bool

	// HTTP server timeouts (prevent slow-loris and connection exhaustion)
	ReadTimeout  time.Duration // Max time to read request headers + body
	WriteTimeout time.Duration // Max time to write response
	IdleTimeout  time.Duration // Max time for keep-alive connections

	// Upload limits
	MaxUploadSize int64 // Maximum size of uploaded file (bytes)

	// Zip bomb protection limits
	MaxExtractedSize  int64  // Maximum total size of extracted files (bytes)
	MaxFileCount      int    // Maximum number of files in archive
	MaxSingleFileSize uint64 // Maximum size of single file (bytes)

	// Scan settings
	ScanTimeout time.Duration // Maximum time for scan operation
	MaxThreads  int           // ClamAV MaxThreads (for conditional multiscan)
}

// Environment variable names
const (
	EnvPort             = "PORT"
	EnvLogLevel         = "LOG_LEVEL"
	EnvReadTimeout      = "READ_TIMEOUT_SECONDS"
	EnvWriteTimeout     = "WRITE_TIMEOUT_SECONDS"
	EnvIdleTimeout      = "IDLE_TIMEOUT_SECONDS"
	EnvMaxUploadSize    = "MAX_UPLOAD_SIZE_MB"
	EnvMaxExtractedSize = "MAX_EXTRACTED_SIZE_MB"
	EnvMaxFileCount     = "MAX_FILE_COUNT"
	EnvMaxSingleFile    = "MAX_SINGLE_FILE_MB"
	EnvScanTimeout      = "SCAN_TIMEOUT_MINUTES"
	EnvMaxThreads       = "MAX_THREADS"
)

// Default values
const (
	DefaultPort             = "9000"
	DefaultReadTimeoutSecs  = 30     // 30 seconds
	DefaultWriteTimeoutSecs = 300    // 5 minutes (scanning can take time)
	DefaultIdleTimeoutSecs  = 60     // 60 seconds
	DefaultMaxUploadMB      = 512    // 512MB max upload
	DefaultMaxExtractedMB   = 1024   // 1GB
	DefaultMaxFileCount     = 100000 // 100k files
	DefaultMaxSingleFileMB  = 256    // 256MB
	DefaultScanTimeoutMins  = 5      // 5 minutes
	DefaultMaxThreads       = 10     // ClamAV default
)

// LoadConfig loads configuration from environment variables.
// Uses sensible defaults if not specified.
func LoadConfig() *Config {
	config := &Config{
		// Server settings
		Port:      getEnvStr(EnvPort, DefaultPort),
		DebugMode: strings.ToLower(os.Getenv(EnvLogLevel)) == "debug",

		// HTTP timeouts
		ReadTimeout:  time.Duration(getEnvInt(EnvReadTimeout, DefaultReadTimeoutSecs)) * time.Second,
		WriteTimeout: time.Duration(getEnvInt(EnvWriteTimeout, DefaultWriteTimeoutSecs)) * time.Second,
		IdleTimeout:  time.Duration(getEnvInt(EnvIdleTimeout, DefaultIdleTimeoutSecs)) * time.Second,

		// Upload and extraction limits
		MaxUploadSize:     int64(getEnvInt(EnvMaxUploadSize, DefaultMaxUploadMB)) << 20,
		MaxExtractedSize:  int64(getEnvInt(EnvMaxExtractedSize, DefaultMaxExtractedMB)) << 20,
		MaxFileCount:      getEnvInt(EnvMaxFileCount, DefaultMaxFileCount),
		MaxSingleFileSize: uint64(getEnvInt(EnvMaxSingleFile, DefaultMaxSingleFileMB)) << 20,

		// Scan settings
		ScanTimeout: time.Duration(getEnvInt(EnvScanTimeout, DefaultScanTimeoutMins)) * time.Minute,
		MaxThreads:  getEnvInt(EnvMaxThreads, DefaultMaxThreads),
	}

	return config
}

// LogConfig logs the current configuration (useful for debugging)
func (c *Config) LogConfig() {
	log.Printf("Configuration:")
	log.Printf("  Port: %s", c.Port)
	log.Printf("  Debug mode: %v", c.DebugMode)
	log.Printf("  Read timeout: %v", c.ReadTimeout)
	log.Printf("  Write timeout: %v", c.WriteTimeout)
	log.Printf("  Idle timeout: %v", c.IdleTimeout)
	log.Printf("  Max upload size: %d MB", c.MaxUploadSize>>20)
	log.Printf("  Max extracted size: %d MB", c.MaxExtractedSize>>20)
	log.Printf("  Max file count: %d", c.MaxFileCount)
	log.Printf("  Max single file: %d MB", c.MaxSingleFileSize>>20)
	log.Printf("  Scan timeout: %v", c.ScanTimeout)
	log.Printf("  Max threads: %d (multiscan: %v)", c.MaxThreads, c.MaxThreads >= 2)
}

// getEnvStr returns environment variable value or default
func getEnvStr(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt returns environment variable as int or default
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
		log.Printf("Warning: invalid value for %s, using default %d", key, defaultValue)
	}
	return defaultValue
}
