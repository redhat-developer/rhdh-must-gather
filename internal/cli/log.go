package cli

import (
	"fmt"
	"os"
	"time"
)

func logInfo(format string, args ...any) {
	logMsg("INFO", format, args...)
}

func logWarn(format string, args ...any) {
	logMsg("WARN", format, args...)
}

func logError(format string, args ...any) {
	logMsg("ERROR", format, args...)
}

func logMsg(level, format string, args ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[%s] [%s] %s\n", ts, level, msg)
}
