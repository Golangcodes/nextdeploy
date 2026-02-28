package failfast

import (
	"os"
	"runtime"

	"github.com/Golangcodes/nextdeploy/shared"
)

type ErrorLevel int

const (
	Ignore ErrorLevel = iota
	Warn
	Error
	Critical
	Panic
)

var (
	failfastLogger = shared.PackageLogger("FailFast::", "🚨 FailFast::")
)

func Failfast(err error, level ErrorLevel, message string) {
	if err == nil {
		return
	}
	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		failfastLogger.Error("Failed to retrieve caller information: %v", err)
		return
	}
	funcname := runtime.FuncForPC(pc).Name()
	logMsg := `
  🔧 ERROR: %s
📄 FILE: %s
📌 LINE: %d
⚙️  FUNC: %s
💥 MSG: %s

	`
	failfastLogger.Error(logMsg, err, file, line, funcname, message)
	switch level {
	case Ignore:
		failfastLogger.Info("Ignoring error as per configuration.")
	case Warn:
		failfastLogger.Warn("Warning: %s", err)
	case Error:
		failfastLogger.Error("Error: %s", err)
		os.Exit(1)
	case Critical:
		failfastLogger.Error("Critical error: %s", err)
		os.Exit(1)
	case Panic:
		panic(err)
	}

}
