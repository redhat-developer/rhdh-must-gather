package log

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Level int

const (
	LevelInfo Level = iota
	LevelDebug
	LevelTrace
)

var currentLevel = LevelInfo

func Init() {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		currentLevel = LevelDebug
	case "trace":
		currentLevel = LevelTrace
	default:
		currentLevel = LevelInfo
	}
}

func Info(format string, args ...any) {
	logMsg("INFO", format, args...)
}

func Warn(format string, args ...any) {
	logMsg("WARN", format, args...)
}

func Error(format string, args ...any) {
	logMsg("ERROR", format, args...)
}

func Success(format string, args ...any) {
	logMsg("SUCCESS", format, args...)
}

func Debug(format string, args ...any) {
	if currentLevel >= LevelDebug {
		logMsg("DEBUG", format, args...)
	}
}

func IsDebug() bool {
	return currentLevel >= LevelDebug
}

func logMsg(level, format string, args ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[%s] [%s] %s\n", ts, level, msg)
}
