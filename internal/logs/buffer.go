package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMaxLines = 5000
	DefaultMaxBytes = 2 << 20
)

type Buffer struct {
	maxLines int
	maxBytes int
	lines    []string
	bytes    int
	dropped  int
}

func NewBuffer(maxLines, maxBytes int) *Buffer {
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Buffer{maxLines: maxLines, maxBytes: maxBytes}
}

func (buffer *Buffer) Append(lines ...string) {
	for _, line := range lines {
		if len(line)+1 > buffer.maxBytes {
			line = tailBytes(line, buffer.maxBytes-1)
		}
		buffer.lines = append(buffer.lines, line)
		buffer.bytes += len(line) + 1
		for len(buffer.lines) > buffer.maxLines || buffer.bytes > buffer.maxBytes {
			buffer.bytes -= len(buffer.lines[0]) + 1
			buffer.lines = buffer.lines[1:]
			buffer.dropped++
		}
	}
}

func (buffer *Buffer) Lines() []string {
	return append([]string(nil), buffer.lines...)
}

func (buffer *Buffer) Filtered(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return buffer.Lines()
	}
	result := make([]string, 0)
	for _, line := range buffer.lines {
		if strings.Contains(strings.ToLower(line), query) {
			result = append(result, line)
		}
	}
	return result
}

func (buffer *Buffer) Dropped() int { return buffer.dropped }

func (buffer *Buffer) Len() int { return len(buffer.lines) }

func (buffer *Buffer) Clear() {
	buffer.lines = nil
	buffer.bytes = 0
	buffer.dropped = 0
}

func (buffer *Buffer) Save(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("log save path is required")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	cleaned := filepath.Clean(path)
	file, err := os.OpenFile(cleaned, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()
	for _, line := range buffer.lines {
		if _, err := fmt.Fprintln(file, line); err != nil {
			return fmt.Errorf("write log file: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync log file: %w", err)
	}
	return nil
}

func Wrap(lines []string, width int) []string {
	if width <= 0 {
		return nil
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		for len(runes) > width {
			wrapped = append(wrapped, string(runes[:width]))
			runes = runes[width:]
		}
		wrapped = append(wrapped, string(runes))
	}
	return wrapped
}

func tailBytes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(value) <= maximum {
		return value
	}
	start := len(value) - maximum
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}
