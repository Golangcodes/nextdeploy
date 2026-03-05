package logs

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

type LogSource string

const (
	SourceApp    LogSource = "APP"
	SourceAudit  LogSource = "AUDIT"
	SourceWAF    LogSource = "WAF"
	SourceDaemon LogSource = "DAEMON"
)

var sourceColors = map[LogSource]string{
	SourceApp:    "\033[38;5;12m", // Blue
	SourceAudit:  "\033[38;5;13m", // Magenta
	SourceWAF:    "\033[38;5;9m",  // Red
	SourceDaemon: "\033[38;5;14m", // Cyan
}

type LogAggregator struct {
	Out           io.Writer
	AppName       string
	ShowTimestamp bool
}

func NewAggregator(out io.Writer, appName string) *LogAggregator {
	return &LogAggregator{
		Out:           out,
		AppName:       appName,
		ShowTimestamp: true,
	}
}

func (a *LogAggregator) WriteSource(source LogSource, p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		a.printLine(source, line)
	}
	return len(p), nil
}

func (a *LogAggregator) printLine(source LogSource, content string) {
	timestamp := ""
	if a.ShowTimestamp {
		timestamp = fmt.Sprintf("\033[90m%s\033[0m ", time.Now().Format("15:04:05"))
	}

	sourceColor := sourceColors[source]
	reset := "\033[0m"
	sourceLabel := fmt.Sprintf("%s% -6s%s ", sourceColor, source, reset)

	// Special case for journalctl metadata (Mar 05 19:39:47 ...)
	content = stripJournalMetadata(content)

	// Apply Next.js specific colorization
	content = colorizeNextJS(content)

	// Apply Audit log labels if it's JSON
	if source == SourceAudit && strings.HasPrefix(content, "{") {
		content = formatAuditJSON(content)
	}

	fmt.Fprintf(a.Out, "%s%s %s\n", timestamp, sourceLabel, content)
}

func stripJournalMetadata(line string) string {
	// Matches "Mar 05 19:39:47 hostname process[pid]: "
	re := regexp.MustCompile(`^[A-Z][a-z]{2}\s+\d+\s+\d{2}:\d{2}:\d{2}\s+[^\s]+\s+[^\s\[]+(?:\[\d+\])?: `)
	return re.ReplaceAllString(line, "")
}

func colorizeNextJS(line string) string {
	// Levels
	line = strings.ReplaceAll(line, "✓ Starting...", "\033[32m✓ Starting...\033[0m")
	line = strings.ReplaceAll(line, "✓ Ready", "\033[32m✓ Ready\033[0m")
	line = strings.ReplaceAll(line, "▲ Next.js", "\033[35m▲ Next.js\033[0m")
	line = strings.ReplaceAll(line, "⨯", "\033[31m⨯\033[0m")
	line = strings.ReplaceAll(line, "- Local:", "\033[90m- Local:\033[0m")
	line = strings.ReplaceAll(line, "- Network:", "\033[90m- Network:\033[0m")

	// URLs
	urlRe := regexp.MustCompile(`https?://[^\s]+`)
	line = urlRe.ReplaceAllStringFunc(line, func(match string) string {
		return fmt.Sprintf("\033[4;34m%s\033[0m", match)
	})

	return line
}

func formatAuditJSON(line string) string {
	// Minimal JSON extract for audit logs
	if strings.Contains(line, `"CommandType"`) {
		re := regexp.MustCompile(`"CommandType":"([^"]+)"`)
		match := re.FindStringSubmatch(line)
		if len(match) > 1 {
			cmdType := match[1]
			res := "SUCCESS"
			if strings.Contains(line, `"Result":"false"`) {
				res = "\033[31mFAILED\033[0m"
			} else {
				res = "\033[32mSUCCESS\033[0m"
			}
			return fmt.Sprintf("Command: \033[1m%s\033[0m -> %s", cmdType, res)
		}
	}
	return line
}

// SourceWriter implements io.Writer for a specific source
type SourceWriter struct {
	agg    *LogAggregator
	source LogSource
}

func (a *LogAggregator) GetWriter(source LogSource) io.Writer {
	return &SourceWriter{agg: a, source: source}
}

func (sw *SourceWriter) Write(p []byte) (n int, err error) {
	return sw.agg.WriteSource(sw.source, p)
}
