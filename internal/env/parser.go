package env

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var keyRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// ParseReader parses .env content from an io.Reader with no line-length limits.
func ParseReader(r io.Reader, filename string) (*EnvFile, []string, error) {
	var warnings []string

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}

	lines, lineEndings := splitLines(data)
	useCRLF := bytes.Contains(data, []byte("\r\n"))

	envFile := &EnvFile{
		Path:        filename,
		UseCRLF:     useCRLF,
		KeyIndex:    make(map[string]int),
		Duplicates:  make(map[int]bool),
		AttachedDoc: make(map[int][]Record),
	}
	for _, ending := range lineEndings {
		if ending != "" {
			envFile.LineEnding = ending
			break
		}
	}

	if len(data) == 0 {
		return envFile, warnings, nil
	}

	totalLines := len(lines)
	i := 0
	for i < totalLines {
		lineNum := i + 1
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if lineNum == 1 {
			trimmed = strings.TrimPrefix(trimmed, "\uFEFF")
		}

		// Blank line
		if trimmed == "" {
			envFile.Records = append(envFile.Records, Record{
				Type:        RecordBlank,
				RawLines:    []string{line},
				LineEndings: lineEndings[lineNum-1 : lineNum],
				StartLine:   lineNum,
			})
			i++
			continue
		}

		// Comment line
		if strings.HasPrefix(trimmed, "#") {
			envFile.Records = append(envFile.Records, Record{
				Type:        RecordComment,
				RawLines:    []string{line},
				LineEndings: lineEndings[lineNum-1 : lineNum],
				StartLine:   lineNum,
			})
			i++
			continue
		}

		// Check for export prefix
		hasExport := false
		if strings.HasPrefix(trimmed, "export") && len(trimmed) > 6 && (trimmed[6] == ' ' || trimmed[6] == '\t') {
			hasExport = true
			trimmed = strings.TrimLeft(trimmed[6:], " \t")
		}

		eqIdx := strings.Index(trimmed, "=")
		if eqIdx == -1 {
			key := strings.TrimSpace(trimmed)
			if keyRegex.MatchString(key) {
				envFile.Records = append(envFile.Records, Record{
					Type: RecordKey, Key: key, RawLines: []string{line},
					LineEndings: lineEndings[lineNum-1 : lineNum], StartLine: lineNum,
				})
				i++
				continue
			}
			envFile.Records = append(envFile.Records, Record{
				Type:        RecordMalformed,
				RawLines:    []string{line},
				LineEndings: lineEndings[lineNum-1 : lineNum],
				StartLine:   lineNum,
			})
			i++
			continue
		}

		key := strings.TrimRight(trimmed[:eqIdx], " \t")
		if !keyRegex.MatchString(key) {
			envFile.Records = append(envFile.Records, Record{
				Type:        RecordMalformed,
				RawLines:    []string{line},
				LineEndings: lineEndings[lineNum-1 : lineNum],
				StartLine:   lineNum,
			})
			i++
			continue
		}

		// Key-value found
		rawVal := strings.TrimLeft(trimmed[eqIdx+1:], " \t")
		rawLines := []string{line}
		cleanVal := ""

		if strings.HasPrefix(rawVal, "\"") {
			// Double-quoted value (can be multi-line)
			closeIdx, _ := findClosingQuoteState(rawVal[1:], '"', false)
			if closeIdx != -1 {
				cleanVal = unescapeDQ(rawVal[1 : 1+closeIdx])
			} else {
				found := false
				var multiBuf strings.Builder
				multiBuf.WriteString(rawVal[1:])
				var candLines []string
				for j := i + 1; j < totalLines; j++ {
					nextLine := lines[j]
					candLines = append(candLines, nextLine)
					cIdx, _ := findClosingQuoteState(nextLine, '"', false)
					if cIdx != -1 {
						multiBuf.WriteString("\n")
						multiBuf.WriteString(nextLine[:cIdx])
						cleanVal = unescapeDQ(multiBuf.String())
						rawLines = append(rawLines, candLines...)
						found = true
						i = j
						break
					}
					multiBuf.WriteString("\n")
					multiBuf.WriteString(nextLine)
				}
				if !found {
					cleanVal = rawVal[1:]
					warnings = append(warnings, fmt.Sprintf("%s: line %d: unterminated quote in %s (treated as single line)", filename, lineNum, key))
				}
			}
		} else if strings.HasPrefix(rawVal, "'") || strings.HasPrefix(rawVal, "`") {
			// Single-quoted value (can be multi-line)
			quoteChar := rawVal[0]
			closeIdx := findQuote(rawVal[1:], quoteChar)
			if closeIdx != -1 {
				cleanVal = decodeQuoted(rawVal[1:1+closeIdx], quoteChar)
			} else {
				found := false
				var multiBuf strings.Builder
				multiBuf.WriteString(rawVal[1:])
				var candLines []string
				for j := i + 1; j < totalLines; j++ {
					nextLine := lines[j]
					candLines = append(candLines, nextLine)
					cIdx := findQuote(nextLine, quoteChar)
					if cIdx != -1 {
						multiBuf.WriteString("\n")
						multiBuf.WriteString(nextLine[:cIdx])
						cleanVal = decodeQuoted(multiBuf.String(), quoteChar)
						rawLines = append(rawLines, candLines...)
						found = true
						i = j
						break
					}
					multiBuf.WriteString("\n")
					multiBuf.WriteString(nextLine)
				}
				if !found {
					cleanVal = rawVal[1:]
					warnings = append(warnings, fmt.Sprintf("%s: line %d: unterminated quote in %s (treated as single line)", filename, lineNum, key))
				}
			}
		} else {
			// Unquoted value: strip trailing inline comments and whitespace
			v := rawVal
			if strings.HasPrefix(v, "#") {
				v = ""
			} else {
				v = stripInlineComment(v)
				v = strings.TrimRight(v, " \t")
			}
			cleanVal = v
		}

		envFile.Records = append(envFile.Records, Record{
			Type:        RecordKey,
			Key:         key,
			CleanValue:  cleanVal,
			HasExport:   hasExport,
			RawLines:    rawLines,
			LineEndings: lineEndings[lineNum-1 : i+1],
			StartLine:   lineNum,
		})
		i++
	}

	return envFile, warnings, nil
}

func splitLines(data []byte) ([]string, []string) {
	if len(data) == 0 {
		return nil, nil
	}
	lines := make([]string, 0, bytes.Count(data, []byte{'\n'})+1)
	endings := make([]string, 0, cap(lines))
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' && data[i] != '\r' {
			continue
		}
		lines = append(lines, string(data[start:i]))
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			endings = append(endings, "\r\n")
			i++
		} else {
			endings = append(endings, string(data[i]))
		}
		start = i + 1
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
		endings = append(endings, "")
	}
	return lines, endings
}

// ParseFile parses a .env file from disk.
func ParseFile(filename string) (*EnvFile, []string, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, nil, err
	}
	if fi.IsDir() {
		return nil, nil, fmt.Errorf("%s is a directory", filename)
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	return ParseReader(f, filename)
}

// findClosingQuoteState finds the index of quoteChar in one physical line, tracking backslash escapes when quoteChar == '"'.
func findClosingQuoteState(s string, quoteChar byte, _ bool) (int, bool) {
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quoteChar == '"' {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && i+1 < len(s) && strings.ContainsRune("\\\"$`", rune(s[i+1])) {
				escaped = true
				continue
			}
		}
		if c == quoteChar {
			return i, false
		}
	}
	return -1, escaped
}

func findQuote(s string, quote byte) int {
	for i := 0; i < len(s); i++ {
		if quote == '\'' && s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '\'') {
			i++
			continue
		}
		if s[i] == quote {
			return i
		}
	}
	return -1
}

func decodeQuoted(s string, quote byte) string {
	if quote == '\'' {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '\'') {
				i++
			}
			b.WriteByte(s[i])
		}
		return b.String()
	}
	return unescapeDQ(s)
}

// unescapeDQ handles double-quoted shell escapes while preserving other backslash sequences (e.g. \n, \t, C:\path).
func unescapeDQ(s string) string {
	var buf strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\' || s[i+1] == '$' || s[i+1] == '`' || s[i+1] == '\n') {
			if s[i+1] == '\n' {
				i++
				continue
			}
			buf.WriteByte(s[i+1])
			i++
			continue
		}
		buf.WriteByte(s[i])
	}
	return buf.String()
}

func stripInlineComment(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j < len(s) && s[j] == '#' {
				return s[:i]
			}
		}
	}
	return s
}
