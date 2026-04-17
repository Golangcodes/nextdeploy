package shared

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Golangcodes/nextdeploy/shared/sensitive"
)

type LogLevel int

const (
	LevelTrace LogLevel = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelSuccess
	LevelError
	LevelFatal
)

var levelNames = map[LogLevel]string{
	LevelTrace:   "TRACE",
	LevelDebug:   "DEBUG",
	LevelInfo:    "INFO ",
	LevelWarn:    "WARN ",
	LevelError:   "ERROR",
	LevelFatal:   "FATAL",
	LevelSuccess: "GOOD ",
}

var levelColors = map[LogLevel]string{
	LevelTrace:   "\033[38;5;245m",
	LevelDebug:   "\033[38;5;14m",
	LevelInfo:    "\033[38;5;12m",
	LevelWarn:    "\033[38;5;11m",
	LevelError:   "\033[38;5;9m",
	LevelFatal:   "\033[48;5;9m",
	LevelSuccess: "\033[38;5;10m",
}

var levelEmojis = map[LogLevel]string{
	LevelTrace:   "🔍",
	LevelDebug:   "🐞",
	LevelInfo:    "ℹ️ ",
	LevelWarn:    "⚠️ ",
	LevelError:   "💥",
	LevelFatal:   "☠️ ",
	LevelSuccess: "✨",
}

var levelBanners = map[LogLevel]string{
	LevelTrace:   "▁▁▁▁▁",
	LevelDebug:   "▂▂▂▂▂",
	LevelInfo:    "▃▃▃▃▃",
	LevelWarn:    "▅▅▅▅▅",
	LevelError:   "▆▆▆▆▆",
	LevelFatal:   "▇▇▇▇▇",
	LevelSuccess: "▔▔▔▔▔",
}

type Logger struct {
	mu            sync.Mutex
	minLevel      LogLevel
	logger        *log.Logger
	showCaller    bool
	showTimestamp bool
	showBanner    bool
	packageMap    map[string]string
	colorEnabled  bool
	timeFormat    string
	indentLevel   int
}

func New(out io.Writer, prefix string, flag int, minLevel LogLevel) *Logger {
	return &Logger{
		minLevel:      minLevel,
		logger:        log.New(out, prefix, 0),
		showCaller:    true,
		showTimestamp: true,
		showBanner:    true,
		packageMap:    make(map[string]string),
		colorEnabled:  true,
		timeFormat:    "2006-01-02 15:04:05.000",
	}
}

func DefaultLogger() *Logger {
	return New(os.Stdout, "", 0, LevelDebug)
}

func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLevel = level
}

func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.SetOutput(w)
}

func (l *Logger) EnableCallerInfo(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

func (l *Logger) EnableTimestamp(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showTimestamp = enable
}

func (l *Logger) EnableBanner(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showBanner = enable
}

func (l *Logger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.colorEnabled = enable
}

func (l *Logger) SetTimeFormat(format string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.timeFormat = format
}

func (l *Logger) RegisterPackage(pkg string, displayName string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.packageMap[pkg] = displayName
}

func (l *Logger) Indent() *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &Logger{
		minLevel:      l.minLevel,
		logger:        l.logger,
		showCaller:    l.showCaller,
		showTimestamp: l.showTimestamp,
		showBanner:    l.showBanner,
		packageMap:    l.packageMap,
		colorEnabled:  l.colorEnabled,
		timeFormat:    l.timeFormat,
		indentLevel:   l.indentLevel + 1,
	}
}

func (l *Logger) Log(level LogLevel, msg string, args ...interface{}) {
	if level < l.minLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var callerInfo string
	if l.showCaller {
		_, file, line, ok := runtime.Caller(2)
		if ok {
			parts := strings.Split(file, "/")
			if len(parts) > 3 {
				file = strings.Join(parts[len(parts)-3:], "/")
			}
			callerInfo = fmt.Sprintf("%s:%d", file, line)
		}
	}

	var pkgDisplay string
	if l.logger.Prefix() != "" {
		if display, exists := l.packageMap[l.logger.Prefix()]; exists {
			pkgDisplay = display + " "
		}
	}

	levelName := levelNames[level]
	levelColor := levelColors[level]
	levelEmoji := levelEmojis[level]
	levelBanner := levelBanners[level]
	resetColor := "\033[0m"

	if !l.colorEnabled {
		levelColor = ""
		resetColor = ""
	}

	formattedMsg := fmt.Sprintf(msg, args...)
	formattedMsg = sensitive.Scrub(formattedMsg)

	indent := strings.Repeat("  ", l.indentLevel)
	formattedMsg = indent + strings.Replace(formattedMsg, "\n", "\n"+indent, -1)

	var logLine strings.Builder

	if l.showTimestamp {
		logLine.WriteString(fmt.Sprintf("\033[90m%s\033[0m ", time.Now().Format(l.timeFormat)))
	}

	if l.showBanner {
		logLine.WriteString(fmt.Sprintf("%s%s%s ", levelColor, levelBanner, resetColor))
	}

	logLine.WriteString(fmt.Sprintf("%s%s%s %s ", levelColor, levelName, resetColor, levelEmoji))

	if pkgDisplay != "" {
		logLine.WriteString(fmt.Sprintf("%s", pkgDisplay))
	}

	logLine.WriteString(formattedMsg)

	if callerInfo != "" {
		logLine.WriteString(fmt.Sprintf(" \033[90m(%s)\033[0m", callerInfo))
	}

	l.logger.Println(logLine.String())
}

func (l *Logger) Trace(msg string, args ...interface{}) {
	l.Log(LevelTrace, msg, args...)
}

func (l *Logger) Debug(msg string, args ...interface{}) {
	l.Log(LevelDebug, msg, args...)
}

func (l *Logger) Info(msg string, args ...interface{}) {
	l.Log(LevelInfo, msg, args...)
}

func (l *Logger) Warn(msg string, args ...interface{}) {
	l.Log(LevelWarn, msg, args...)
}

func (l *Logger) Error(msg string, args ...interface{}) {
	l.Log(LevelError, msg, args...)
}

func (l *Logger) Fatal(msg string, args ...interface{}) {
	l.Log(LevelFatal, msg, args...)
	os.Exit(1)
}

func (l *Logger) Success(msg string, args ...interface{}) {
	l.Log(LevelSuccess, msg, args...)
}

func (l *Logger) WithPrefix(prefix string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := &Logger{
		minLevel:      l.minLevel,
		logger:        log.New(l.logger.Writer(), prefix, 0),
		showCaller:    l.showCaller,
		showTimestamp: l.showTimestamp,
		showBanner:    l.showBanner,
		packageMap:    make(map[string]string),
		colorEnabled:  l.colorEnabled,
		timeFormat:    l.timeFormat,
	}

	for k, v := range l.packageMap {
		newLogger.packageMap[k] = v
	}

	return newLogger
}

func PackageLogger(pkgName string, displayName string) *Logger {
	logger := DefaultLogger()
	logger.RegisterPackage(pkgName, displayName)
	return logger.WithPrefix(pkgName)
}

func (l *Logger) Timed(label string, fn func()) {
	start := time.Now()
	done := make(chan bool)

	go func() {
		spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				l.mu.Lock()
				levelColor := levelColors[LevelInfo]
				resetColor := "\033[0m"
				if !l.colorEnabled {
					levelColor = ""
					resetColor = ""
				}
				msg := fmt.Sprintf("%s%s%s %s %s...", levelColor, levelNames[LevelInfo], resetColor, spinner[i], label)
				if l.showTimestamp {
					msg = fmt.Sprintf("\033[90m%s\033[0m %s", time.Now().Format(l.timeFormat), msg)
				}
				l.logger.Print("\r" + msg)
				l.mu.Unlock()
				i = (i + 1) % len(spinner)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	fn()
	close(done)

	l.logger.Print("\r\033[K")

	duration := time.Since(start)
	l.Info("%s completed in %s", label, duration)
}

func (l *Logger) JSON(level LogLevel, data interface{}) {
	if level < l.minLevel {
		return
	}

	jsonData, err := json.MarshalIndent(data, strings.Repeat("  ", l.indentLevel), "  ")
	if err != nil {
		l.Error("Failed to marshal JSON: %v", err)
		return
	}

	fmtattedJSON := string(jsonData)
	fmt.Println("" + fmtattedJSON)
}

func (l *Logger) Table(level LogLevel, headers []string, rows [][]string) {
	if level < l.minLevel {
		return
	}

	colWidths := make([]int, len(headers))
	for i, h := range headers {
		colWidths[i] = len(h)
	}

	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}

	var table strings.Builder

	table.WriteString("\n")
	for i, h := range headers {
		table.WriteString(fmt.Sprintf(" %-*s ", colWidths[i], h))
		if i < len(headers)-1 {
			table.WriteString("│")
		}
	}

	table.WriteString("\n")
	for i, w := range colWidths {
		table.WriteString(strings.Repeat("─", w+2))
		if i < len(colWidths)-1 {
			table.WriteString("┼")
		}
	}
	table.WriteString("\n")

	for _, row := range rows {
		for i, cell := range row {
			table.WriteString(fmt.Sprintf(" %-*s ", colWidths[i], cell))
			if i < len(row)-1 {
				table.WriteString("│")
			}
		}
		table.WriteString("\n")
	}

	l.Info("%s", table.String())
}

func (l *Logger) Progress(level LogLevel, current, total int, label string) {
	if level < l.minLevel {
		return
	}

	const barWidth = 30
	progress := float64(current) / float64(total)
	filled := int(barWidth * progress)

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	levelColor := levelColors[level]
	resetColor := "\033[0m"
	if !l.colorEnabled {
		levelColor = ""
		resetColor = ""
	}

	msg := fmt.Sprintf("%s [%s] %3.0f%% %s", label, bar, progress*100, levelEmojis[level])

	l.mu.Lock()
	defer l.mu.Unlock()

	var logLine strings.Builder

	if l.showTimestamp {
		logLine.WriteString(fmt.Sprintf("\033[90m%s\033[0m ", time.Now().Format(l.timeFormat)))
	}

	logLine.WriteString(fmt.Sprintf("%s%s%s %s %s",
		levelColor, levelNames[level], resetColor,
		levelEmojis[level], msg))

	l.logger.Print("\r" + logLine.String())
	if current >= total {
		l.logger.Println()
	}
}
