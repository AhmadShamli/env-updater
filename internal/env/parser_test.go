package env

import (
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	input := `# Comment
DB_HOST=localhost
export DB_PORT=5432

API_URL="https://example.com" # inline comment
SECRET='my-secret#not-a-comment'
`
	envFile, warnings, err := ParseReader(strings.NewReader(input), ".env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if len(envFile.Records) != 6 {
		t.Fatalf("expected 6 records, got %d", len(envFile.Records))
	}

	r0 := envFile.Records[0]
	if r0.Type != RecordComment {
		t.Errorf("expected comment, got %v", r0.Type)
	}

	r1 := envFile.Records[1]
	if r1.Key != "DB_HOST" || r1.CleanValue != "localhost" || r1.HasExport {
		t.Errorf("unexpected record 1: %+v", r1)
	}

	r2 := envFile.Records[2]
	if r2.Key != "DB_PORT" || r2.CleanValue != "5432" || !r2.HasExport {
		t.Errorf("unexpected record 2: %+v", r2)
	}

	r4 := envFile.Records[4]
	if r4.Key != "API_URL" || r4.CleanValue != "https://example.com" {
		t.Errorf("unexpected record 4: %+v", r4)
	}

	r5 := envFile.Records[5]
	if r5.Key != "SECRET" || r5.CleanValue != "my-secret#not-a-comment" {
		t.Errorf("unexpected record 5: %+v", r5)
	}
}

func TestParseMultiline(t *testing.T) {
	input := `PRIVATE_KEY="-----BEGIN KEY-----
line1
line2
-----END KEY-----"
AFTER_KEY=true
JSON='{
  "key": "val"
}'
`
	envFile, warnings, err := ParseReader(strings.NewReader(input), ".env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if len(envFile.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(envFile.Records))
	}

	r0 := envFile.Records[0]
	if r0.Key != "PRIVATE_KEY" {
		t.Errorf("expected PRIVATE_KEY, got %s", r0.Key)
	}
	expectedPK := "-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----"
	if r0.CleanValue != expectedPK {
		t.Errorf("clean value mismatch:\nExpected:\n%s\nActual:\n%s", expectedPK, r0.CleanValue)
	}
	if len(r0.RawLines) != 4 {
		t.Errorf("expected 4 raw lines, got %d", len(r0.RawLines))
	}

	r1 := envFile.Records[1]
	if r1.Key != "AFTER_KEY" || r1.CleanValue != "true" {
		t.Errorf("unexpected r1: %+v", r1)
	}

	r2 := envFile.Records[2]
	if r2.Key != "JSON" {
		t.Errorf("expected JSON, got %s", r2.Key)
	}
	expectedJSON := "{\n  \"key\": \"val\"\n}"
	if r2.CleanValue != expectedJSON {
		t.Errorf("clean value mismatch:\nExpected:\n%s\nActual:\n%s", expectedJSON, r2.CleanValue)
	}
}

func TestParseCRLF(t *testing.T) {
	input := "FOO=bar\r\nBAZ=qux\r\n"
	envFile, _, err := ParseReader(strings.NewReader(input), ".env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !envFile.UseCRLF {
		t.Errorf("expected UseCRLF to be true")
	}
}

func TestParseUnterminatedQuote(t *testing.T) {
	input := "A=\"unterminated\nB=1\nC='unterminated_sq\nD=2\n"
	envFile, warnings, err := ParseReader(strings.NewReader(input), ".env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "unterminated quote") {
		t.Errorf("expected 2 unterminated quote warnings, got %v", warnings)
	}
	if len(envFile.Records) != 4 {
		t.Fatalf("expected 4 records, got %d", len(envFile.Records))
	}
	if len(envFile.Records[0].RawLines) != 1 {
		t.Errorf("expected unterminated double-quote record to have 1 raw line, got %d: %v", len(envFile.Records[0].RawLines), envFile.Records[0].RawLines)
	}
	if len(envFile.Records[2].RawLines) != 1 {
		t.Errorf("expected unterminated single-quote record to have 1 raw line, got %d: %v", len(envFile.Records[2].RawLines), envFile.Records[2].RawLines)
	}
	if envFile.Records[1].Key != "B" || envFile.Records[3].Key != "D" {
		t.Errorf("expected records B and D, got %+v and %+v", envFile.Records[1], envFile.Records[3])
	}
}

func TestParseLargeLineOver64KB(t *testing.T) {
	bigVal := strings.Repeat("X", 100000)
	input := "BIG_KEY=\"" + bigVal + "\"\nSMALL_KEY=ok\n"
	envFile, warnings, err := ParseReader(strings.NewReader(input), ".env")
	if err != nil {
		t.Fatalf("unexpected error on >64KB line: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if len(envFile.Records) != 2 || envFile.Records[0].CleanValue != bigVal {
		t.Errorf("large line value was not parsed accurately")
	}
}

func TestUnescapeDQPreservesBackslashes(t *testing.T) {
	input := `WIN_PATH="C:\Users\admin\file.txt"
ESCAPED_QUOTE="say \"hello\" \\ world\n"
`
	envFile, _, err := ParseReader(strings.NewReader(input), ".env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if envFile.Records[0].CleanValue != `C:\Users\admin\file.txt` {
		t.Errorf("expected Windows path preserved, got %q", envFile.Records[0].CleanValue)
	}
	if envFile.Records[1].CleanValue != `say "hello" \ world\n` {
		t.Errorf("expected only quotes and double-backslashes unescaped, got %q", envFile.Records[1].CleanValue)
	}
}

func TestMultilineQuoteAfterBackslashNewline(t *testing.T) {
	for _, input := range []string{
		"A=\"first\\\n\"\nB=existing\n",
		"A=\"first\nsecond\\\n\"\nB=existing\n",
		"A=\"first\\\\\n\"\nB=existing\n",
	} {
		file, warnings, err := ParseReader(strings.NewReader(input), ".env")
		if err != nil || len(warnings) != 0 {
			t.Fatalf("parse %q: err=%v warnings=%v", input, err, warnings)
		}
		if len(file.Records) != 2 || file.Records[1].Key != "B" {
			t.Fatalf("following key lost: %+v", file.Records)
		}
		Analyze(file, nil, false)
		if got := string(Merge(file, nil, false, nil)); got != input {
			t.Errorf("round trip: got %q, want %q", got, input)
		}
	}
}

func TestDotenvFormatMatrix(t *testing.T) {
	input := "\uFEFF# UTF-8 BOM\r\n" +
		"_LOWER_key=value\n" +
		"NUMBER_2=\n" +
		"export  SPACED = \"value # with hash\" # trailing note\r\n" +
		"INLINE=abc#literal\n" +
		"INLINE_COMMENT=abc # removed\n" +
		"HASH='a#b'\n" +
		"DOLLAR=\"\\$HOME\"\n" +
		"BACKTICK=\"\\`command`\"\n" +
		"SINGLE_ESCAPED='it\\'s literal'\r\n" +
		"BACKTICK_QUOTED=`literal $HOME`\n" +
		"DECLARED\n" +
		"EMPTY\n" +
		"MULTI=\"one\r\ntwo\nthree\"\r\n" +
		"NOT_A_KEY=lowercase-key\n" +
		"bad syntax to keep\n" +
		"LAST=value"
	file, warnings, err := ParseReader(strings.NewReader(input), "matrix")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: err=%v warnings=%v", err, warnings)
	}
	want := map[string]string{
		"_LOWER_key": "value", "NUMBER_2": "", "SPACED": "value # with hash",
		"INLINE": "abc#literal", "INLINE_COMMENT": "abc", "HASH": "a#b",
		"DOLLAR": "$HOME", "BACKTICK": "`command`", "SINGLE_ESCAPED": "it's literal",
		"BACKTICK_QUOTED": "literal $HOME", "DECLARED": "", "EMPTY": "", "MULTI": "one\ntwo\nthree",
		"NOT_A_KEY": "lowercase-key", "LAST": "value",
	}
	got := make(map[string]string)
	for _, r := range file.Records {
		if r.Type == RecordKey {
			got[r.Key] = r.CleanValue
		}
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s: got %q, want %q", key, got[key], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected keys: got %#v", got)
	}
	var malformed int
	for _, record := range file.Records {
		if record.Type == RecordMalformed {
			malformed++
		}
	}
	if malformed != 1 {
		t.Errorf("want one preserved non-assignment line, got %d", malformed)
	}
}

func TestLineEndingVariantsAndEOF(t *testing.T) {
	for _, input := range []string{"", "A=1\n", "A=1\r", "A=1\r\n", "A=1\n\n", "A=1\rB=2\r"} {
		file, _, err := ParseReader(strings.NewReader(input), "line-endings")
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		Analyze(file, nil, false)
		got := string(Merge(file, nil, false, nil))
		if input != "" && got != input {
			t.Errorf("round-trip %q -> %q", input, got)
		}
	}
}
