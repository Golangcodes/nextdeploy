package failfast

import (
	"github.com/Golangcodes/nextdeploy/shared"
	"os"
	"runtime"
)

type ErrorLevel int

const (
	Ignore   ErrorLevel = iota // do nothing, just log
	Warn                       // log a Warning
	Error                      // log an Error and exit with code 1
	Critical                   // log a Critical error and exit with code 1
	Panic                      // log a Panic error and Panic
	// Note: Panic will stop the program immediately, so use with caution.

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
