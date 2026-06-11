package logger

import (
	"fmt"
	"log"
	"os"
)

// We create separate loggers for different log levels.
// Each logger prints a prefix like [INFO], [ERROR], [DEBUG].
var (
	infoLogger  *log.Logger
	errorLogger *log.Logger
	debugLogger *log.Logger
)

// init runs automatically when the package is loaded.
// It sets up our three loggers with different prefixes.
func init() {
	infoLogger = log.New(os.Stdout, "[INFO]  ", log.Ldate|log.Ltime)
	errorLogger = log.New(os.Stderr, "[ERROR] ", log.Ldate|log.Ltime)
	debugLogger = log.New(os.Stdout, "[DEBUG] ", log.Ldate|log.Ltime)
}

// LogInfo prints an informational message.
// Usage: logger.LogInfo("Crawling %s", url)
func LogInfo(format string, args ...interface{}) {
	infoLogger.Println(fmt.Sprintf(format, args...))
}

// LogError prints an error message to stderr.
// Usage: logger.LogError("Failed to fetch %s: %v", url, err)
func LogError(format string, args ...interface{}) {
	errorLogger.Println(fmt.Sprintf(format, args...))
}

// LogDebug prints a debug message.
// Usage: logger.LogDebug("Found %d links on %s", count, url)
func LogDebug(format string, args ...interface{}) {
	debugLogger.Println(fmt.Sprintf(format, args...))
}
