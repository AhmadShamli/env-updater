package env

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSyncerMerge(t *testing.T) {
	srcContent := `# Template Header
APP_NAME=Default
APP_ENV=local
NEW_KEY=default_new
export API_URL="https://example.com"
`
	tgtContent := `APP_NAME="Custom App #1"
export API_URL="https://prod.example.com"
# Note for extra key
CUSTOM_KEY=123
`
	src, _, err := ParseReader(strings.NewReader(srcContent), ".env.example")
	if err != nil {
		t.Fatalf("parse src: %v", err)
	}
	tgt, _, err := ParseReader(strings.NewReader(tgtContent), ".env")
	if err != nil {
		t.Fatalf("parse tgt: %v", err)
	}

	analysis := Analyze(src, tgt, false)
	if len(analysis.AddedKeys) != 2 {
		t.Errorf("expected 2 added keys (APP_ENV, NEW_KEY), got %v", analysis.AddedKeys)
	}
	if len(analysis.ExtraKeys) != 1 || analysis.ExtraKeys[0] != "CUSTOM_KEY" {
		t.Errorf("expected CUSTOM_KEY in extra keys, got %v", analysis.ExtraKeys)
	}

	merged := string(Merge(src, tgt, false, nil))

	if !strings.Contains(merged, "# Template Header") {
		t.Errorf("expected template header preserved")
	}
	if !strings.Contains(merged, `APP_NAME="Custom App #1"`) {
		t.Errorf("expected custom app name preserved")
	}
	if !strings.Contains(merged, "APP_ENV=local") {
		t.Errorf("expected APP_ENV=local added")
	}
	if !strings.Contains(merged, "NEW_KEY=default_new") {
		t.Errorf("expected NEW_KEY=default_new added")
	}
	if !strings.Contains(merged, `export API_URL="https://prod.example.com"`) {
		t.Errorf("expected API_URL preserved from target")
	}
	if !strings.Contains(merged, ExtraKeysHeader) {
		t.Errorf("expected ExtraKeysHeader")
	}
	if !strings.Contains(merged, "# Note for extra key\nCUSTOM_KEY=123") {
		t.Errorf("expected attached comment with extra key:\n%s", merged)
	}
}

func TestFormatValueLiteralRoundTrip(t *testing.T) {
	values := []string{
		"it's $HOME", "it's $(printf EXPANDED)", "it's `printf EXPANDED`",
		`it's "quoted" with \backslashes and $dollars`, "", "line one\nline two's $HOME",
	}
	for _, value := range values {
		assignment := FormatValue("KEY", value, false)
		parsed, warnings, err := ParseReader(strings.NewReader(assignment+"\n"), ".env")
		if err != nil || len(warnings) != 0 || len(parsed.Records) != 1 || parsed.Records[0].CleanValue != value {
			t.Fatalf("dotenv round trip %q: records=%+v warnings=%v err=%v", value, parsed, warnings, err)
		}
		if shell, err := exec.LookPath("sh"); err == nil {
			out, err := exec.Command(shell, "-c", assignment+`; printf '%s' "$KEY"`).CombinedOutput()
			if err != nil || string(out) != value {
				t.Errorf("shell round trip %q: got %q err=%v", value, out, err)
			}
		}
	}
}

func TestMergePreservesMultilineTerminators(t *testing.T) {
	for _, input := range []string{
		"A=\"first\nsecond\"\nB=value\r\n",
		"A='first\r\nsecond\nthird'\r\nB=value\n",
		"A=\"first\nsecond\"\r\nB=value\r\n",
	} {
		src, _, _ := ParseReader(strings.NewReader("A=default\nB=default\n"), "template")
		tgt, _, _ := ParseReader(strings.NewReader(input), "target")
		Analyze(src, tgt, false)
		merged := Merge(src, tgt, false, nil)
		for _, record := range tgt.Records {
			if len(record.RawLines) < 2 {
				continue
			}
			var raw strings.Builder
			for i, line := range record.RawLines {
				raw.WriteString(line)
				if i < len(record.RawLines)-1 {
					raw.WriteString(record.LineEndings[i])
				}
			}
			if !strings.Contains(string(merged), raw.String()) {
				t.Errorf("multiline bytes changed: want %q in %q", raw.String(), merged)
			}
		}
		reparsed, warnings, err := ParseReader(strings.NewReader(string(merged)), "merged")
		if err != nil || len(warnings) != 0 {
			t.Fatalf("parse merged: %v %v", err, warnings)
		}
		Analyze(src, reparsed, false)
		if again := string(Merge(src, reparsed, false, nil)); again != string(merged) {
			t.Errorf("mixed line endings are not idempotent: %q != %q", again, merged)
		}
	}
}

func TestSyncPreservesUnknownTargetContent(t *testing.T) {
	src, _, _ := ParseReader(strings.NewReader("A=default\nB=default\n"), "template")
	targetText := "A=live\n# config for external tool\n${CUSTOM_COMMAND}\nB=live\nUNKNOWN LINE\n"
	tgt, warnings, _ := ParseReader(strings.NewReader(targetText), "target")
	analysis := Analyze(src, tgt, true)
	if len(warnings) != 0 || len(analysis.Warnings) != 2 {
		t.Fatalf("want diagnostics for opaque lines: parse=%v analysis=%v", warnings, analysis.Warnings)
	}
	merged := string(Merge(src, tgt, true, nil))
	if !strings.Contains(merged, "# Unparsed content preserved from .env\n# config for external tool\n${CUSTOM_COMMAND}\nUNKNOWN LINE\n") {
		t.Fatalf("unknown content was dropped: %q", merged)
	}
	Analyze(src, tgt, false)
	if again := string(Merge(src, tgt, true, nil)); again != merged {
		t.Fatalf("opaque section is not idempotent: %q != %q", again, merged)
	}
}

func TestSyncerDuplicates(t *testing.T) {
	srcContent := `A=1
A=2
`
	tgtContent := `A=first
A=last
`
	src, _, _ := ParseReader(strings.NewReader(srcContent), ".env.example")
	tgt, _, _ := ParseReader(strings.NewReader(tgtContent), ".env")

	analysis := Analyze(src, tgt, false)
	if len(analysis.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %v", analysis.Warnings)
	}

	merged := string(Merge(src, tgt, false, nil))
	lines := strings.Split(strings.TrimSpace(merged), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %v", lines)
	}
	if lines[0] != "A=last" {
		t.Errorf("expected A=last, got %s", lines[0])
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		key       string
		val       string
		hasExport bool
		expected  string
	}{
		{"KEY", "bare_val123", false, "KEY=bare_val123"},
		{"KEY", "value with spaces", false, "KEY='value with spaces'"},
		{"KEY", "pa$word", false, "KEY='pa$word'"},
		{"KEY", "it's working", false, `KEY="it's working"`},
		{"KEY", `it's "quoted"`, false, `KEY="it's \"quoted\""`},
		{"EXPORTED", "foo", true, "export EXPORTED=foo"},
	}

	for _, tt := range tests {
		got := FormatValue(tt.key, tt.val, tt.hasExport)
		if got != tt.expected {
			t.Errorf("FormatValue(%q, %q, %v) = %q, expected %q", tt.key, tt.val, tt.hasExport, got, tt.expected)
		}
	}
}
