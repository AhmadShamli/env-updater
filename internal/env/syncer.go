package env

import (
	"fmt"
	"regexp"
	"strings"
)

const ExtraKeysHeader = "# Extra keys preserved from .env"

var safeBareRegex = regexp.MustCompile(`^[A-Za-z0-9_.,:@+%=\/-]+$`)

type PromptKey struct {
	Key          string
	DefaultValue string
}

type AnalysisResult struct {
	SourceKeys    []string
	TargetKeys    []string
	AddedKeys     []string
	PreservedKeys []string
	ExtraKeys     []string
	PromptKeys    []PromptKey
	Warnings      []string
}

type OverrideType int

const (
	OverrideVerbatimTemplate OverrideType = iota
	OverrideCustomValue
)

type Override struct {
	Type  OverrideType
	Value string
}

// Analyze analyzes source and target env files, detecting added/extra keys and duplicates.
func Analyze(src *EnvFile, tgt *EnvFile, prune bool) *AnalysisResult {
	res := &AnalysisResult{}

	// Index source records
	srcIdx := make(map[string]int)
	srcComments := make(map[string]bool)

	for i, r := range src.Records {
		if r.Type == RecordComment {
			for _, l := range r.RawLines {
				srcComments[strings.TrimSpace(l)] = true
			}
		}
		if r.Type == RecordMalformed {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: line %d: not a KEY=VALUE line in template (preserved)", src.Path, r.StartLine))
		}
		if r.Type != RecordKey {
			continue
		}
		if _, exists := srcIdx[r.Key]; exists {
			src.Duplicates[i] = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: line %d: duplicate key %s (ignored; first occurrence kept)", src.Path, r.StartLine, r.Key))
		} else {
			srcIdx[r.Key] = i
			res.SourceKeys = append(res.SourceKeys, r.Key)
		}
	}
	src.KeyIndex = srcIdx

	if tgt == nil {
		// All source keys are added
		for _, k := range res.SourceKeys {
			res.AddedKeys = append(res.AddedKeys, k)
			idx := srcIdx[k]
			sDef := src.Records[idx].CleanValue
			if strings.Contains(sDef, "\n") {
				sDef = "<multi-line>"
			}
			res.PromptKeys = append(res.PromptKeys, PromptKey{
				Key:          k,
				DefaultValue: sDef,
			})
		}
		return res
	}

	// Index target records: last occurrence wins
	tgtIdx := make(map[string]int)
	var tgtOrder []string
	attach := make(map[int]int) // maps target record index -> start index of attached comment block
	malformedAttach := make(map[int]int)
	var malformedRecords []int
	cStart := -1

	for i, r := range tgt.Records {
		switch r.Type {
		case RecordComment:
			commentText := strings.TrimSpace(r.RawLines[0])
			if commentText == ExtraKeysHeader || commentText == UnparsedContentHeader {
				cStart = -1
				continue
			}
			if cStart == -1 {
				cStart = i
			}
		case RecordKey:
			if _, exists := tgtIdx[r.Key]; exists {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: line %d: duplicate key %s (last occurrence wins)", tgt.Path, r.StartLine, r.Key))
			} else {
				tgtOrder = append(tgtOrder, r.Key)
			}
			tgtIdx[r.Key] = i
			if cStart != -1 {
				attach[i] = cStart
				cStart = -1
			}
		case RecordMalformed:
			malformedRecords = append(malformedRecords, i)
			if cStart != -1 {
				malformedAttach[i] = cStart
				cStart = -1
			}
		default:
			// Blank or malformed resets comment block attachment
			cStart = -1
		}
	}
	tgt.KeyIndex = tgtIdx
	res.TargetKeys = tgtOrder

	// Find added and preserved keys
	for _, k := range res.SourceKeys {
		sIdx := srcIdx[k]
		sDef := src.Records[sIdx].CleanValue
		if strings.Contains(sDef, "\n") {
			sDef = "<multi-line>"
		}

		if tIdx, exists := tgtIdx[k]; exists {
			res.PreservedKeys = append(res.PreservedKeys, k)
			if tgt.Records[tIdx].CleanValue == "" {
				res.PromptKeys = append(res.PromptKeys, PromptKey{
					Key:          k,
					DefaultValue: sDef,
				})
			}
		} else {
			res.AddedKeys = append(res.AddedKeys, k)
			res.PromptKeys = append(res.PromptKeys, PromptKey{
				Key:          k,
				DefaultValue: sDef,
			})
		}
	}

	// Find extra keys in target and kept comments
	keptComments := make(map[int]bool)
	for _, k := range tgtOrder {
		if _, exists := srcIdx[k]; !exists {
			res.ExtraKeys = append(res.ExtraKeys, k)
			if !prune {
				tIdx := tgtIdx[k]
				if start, ok := attach[tIdx]; ok {
					for j := start; j < tIdx; j++ {
						keptComments[j] = true
					}
				}
			}
		}
	}

	// Warn about dropped malformed lines or unattached comments in target
	for index, start := range malformedAttach {
		for j := start; j < index; j++ {
			keptComments[j] = true
		}
	}
	for i, r := range tgt.Records {
		switch r.Type {
		case RecordMalformed:
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: line %d: not a KEY=VALUE line, will be preserved as unparsed content", tgt.Path, r.StartLine))
		case RecordComment:
			if keptComments[i] {
				continue
			}
			commentText := strings.TrimSpace(r.RawLines[0])
			if commentText == ExtraKeysHeader || commentText == UnparsedContentHeader {
				continue
			}
			if !srcComments[commentText] {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: line %d: comment not in template, will be dropped", tgt.Path, r.StartLine))
			}
		}
	}

	return res
}

// FormatValue formats a key and value safely with optional export prefix.
// It uses single quotes where possible to prevent shell / dotenv expansion.
func FormatValue(key, val string, hasExport bool) string {
	prefix := ""
	if hasExport {
		prefix = "export "
	}

	if safeBareRegex.MatchString(val) {
		return fmt.Sprintf("%s%s=%s", prefix, key, val)
	}

	// If no single quote, wrap in single quotes (completely immune to $, `, ", etc.)
	if !strings.Contains(val, "'") {
		return fmt.Sprintf("%s%s='%s'", prefix, key, val)
	}

	// Fallback to double quotes with backslash escaping
	var escaped strings.Builder
	for i := 0; i < len(val); i++ {
		c := val[i]
		if c == '\\' || c == '"' || c == '$' || c == '`' {
			escaped.WriteByte('\\')
		}
		escaped.WriteByte(c)
	}
	return fmt.Sprintf("%s%s=\"%s\"", prefix, key, escaped.String())
}

func writeRecord(out *strings.Builder, r Record, lineEnd string, separator bool) {
	if out.Len() > 0 {
		written := out.String()
		if separator && !strings.HasSuffix(written, "\n") && !strings.HasSuffix(written, "\r") {
			out.WriteString(lineEnd)
		}
	}
	for i, line := range r.RawLines {
		out.WriteString(line)
		ending := ""
		if i < len(r.LineEndings) {
			ending = r.LineEndings[i]
		}
		if ending == "" && i < len(r.RawLines)-1 {
			ending = lineEnd
		}
		out.WriteString(ending)
	}
}

// Merge merges source and target into output bytes based on analysis and overrides.
func Merge(src *EnvFile, tgt *EnvFile, prune bool, overrides map[string]Override) []byte {
	useCRLF := false
	if tgt != nil {
		useCRLF = tgt.UseCRLF
	} else if src != nil {
		useCRLF = src.UseCRLF
	}

	lineEnd := "\n"
	if tgt != nil && tgt.LineEnding != "" {
		lineEnd = tgt.LineEnding
	} else if src != nil && src.LineEnding != "" {
		lineEnd = src.LineEnding
	} else if useCRLF {
		lineEnd = "\r\n"
	}

	var out strings.Builder
	lastBlank := true

	// Build target index and comment attachment if target exists
	tgtIdx := make(map[string]int)
	var tgtOrder []string
	attach := make(map[int]int)
	malformedAttach := make(map[int]int)
	var malformedRecords []int
	cStart := -1

	if tgt != nil {
		for i, r := range tgt.Records {
			switch r.Type {
			case RecordComment:
				if strings.TrimSpace(r.RawLines[0]) == ExtraKeysHeader || strings.TrimSpace(r.RawLines[0]) == UnparsedContentHeader {
					cStart = -1
					continue
				}
				if cStart == -1 {
					cStart = i
				}
			case RecordKey:
				tgtIdx[r.Key] = i
				tgtOrder = append(tgtOrder, r.Key)
				if cStart != -1 {
					attach[i] = cStart
					cStart = -1
				}
			case RecordMalformed:
				malformedRecords = append(malformedRecords, i)
				if cStart != -1 {
					malformedAttach[i] = cStart
					cStart = -1
				}
			default:
				cStart = -1
			}
		}
	}

	// Iterate through source records
	for i, r := range src.Records {
		if src.Duplicates[i] {
			continue
		}

		if r.Type != RecordKey {
			writeRecord(&out, r, lineEnd, true)
			lastBlank = (r.Type == RecordBlank)
			continue
		}

		k := r.Key
		if ovr, exists := overrides[k]; exists {
			switch ovr.Type {
			case OverrideCustomValue:
				hasExp := r.HasExport
				if tIdx, ok := tgtIdx[k]; ok {
					hasExp = tgt.Records[tIdx].HasExport
				}
				out.WriteString(FormatValue(k, ovr.Value, hasExp))
				out.WriteString(lineEnd)
			case OverrideVerbatimTemplate:
				writeRecord(&out, r, lineEnd, false)
			}
		} else if tIdx, ok := tgtIdx[k]; ok {
			// Key exists in target: preserve target's raw lines exactly
			writeRecord(&out, tgt.Records[tIdx], lineEnd, false)
		} else {
			// New key from template
			writeRecord(&out, r, lineEnd, false)
		}
		lastBlank = false
	}

	// Extra keys
	if !prune && tgt != nil {
		srcIdx := src.KeyIndex
		headerDone := false

		// Unique target order
		seenTgt := make(map[string]bool)
		for _, k := range tgtOrder {
			if seenTgt[k] {
				continue
			}
			seenTgt[k] = true

			if _, inSrc := srcIdx[k]; inSrc {
				continue
			}

			if !headerDone {
				if !lastBlank {
					out.WriteString(lineEnd)
				}
				out.WriteString(ExtraKeysHeader)
				out.WriteString(lineEnd)
				headerDone = true
			}

			tIdx := tgtIdx[k]
			if start, ok := attach[tIdx]; ok {
				for j := start; j < tIdx; j++ {
					writeRecord(&out, tgt.Records[j], lineEnd, true)
				}
			}
			r := tgt.Records[tIdx]
			writeRecord(&out, r, lineEnd, false)
			lastBlank = false
		}
	}

	if tgt != nil && len(malformedRecords) > 0 {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") && !strings.HasSuffix(out.String(), "\r") {
			out.WriteString(lineEnd)
		}
		out.WriteString(UnparsedContentHeader)
		out.WriteString(lineEnd)
		for _, index := range malformedRecords {
			if start, ok := malformedAttach[index]; ok {
				for j := start; j < index; j++ {
					writeRecord(&out, tgt.Records[j], lineEnd, true)
				}
			}
			writeRecord(&out, tgt.Records[index], lineEnd, true)
		}
	}

	return []byte(out.String())
}
