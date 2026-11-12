package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func TestFitText(t *testing.T) {
	short := "hello"
	if FitText(short, 10) != "hello" {
		t.Errorf("expected hello, got %s", FitText(short, 10))
	}

	long := "this/is/a/very/long/path/to/a/file/that/exceeds/the/limit"
	fitted := FitText(long, 20)
	if len(fitted) != 20 {
		t.Errorf("expected length 20, got %d: %q", len(fitted), fitted)
	}
	if fitted[:3] != "..." {
		t.Errorf("expected prefix ..., got %s", fitted)
	}

	// UTF-8 Cyrillic + Korean CJK wide characters
	utf8Path := "Source:  /tmp/папка_с_длинным_именем_для_проверки_заголовка_유니코드_경로/.env.example"
	fittedUTF8 := FitText(utf8Path, HeaderWidth)
	if !utf8.ValidString(fittedUTF8) {
		t.Errorf("FitText produced invalid UTF-8: %q", fittedUTF8)
	}
	padded := runewidth.FillRight(fittedUTF8, HeaderWidth)
	if runewidth.StringWidth(padded) != HeaderWidth {
		t.Errorf("expected display width %d, got %d (%q)", HeaderWidth, runewidth.StringWidth(padded), padded)
	}
}

func TestIsSecretKey(t *testing.T) {
	if !IsSecretKey("DB_PASSWORD") || !IsSecretKey("API_KEY") || !IsSecretKey("JWT_SECRET") || !IsSecretKey("AUTH_TOKEN") {
		t.Errorf("expected secret keys to be detected")
	}
	if IsSecretKey("MONKEY_COUNT") || IsSecretKey("KEYBOARD_LAYOUT") || IsSecretKey("AUTHOR_NAME") {
		t.Errorf("expected non-secret keys not to trigger false positive")
	}
}

func TestPromptEOF(t *testing.T) {
	u := NewUI(true, true, true)
	val, eof, err := u.Prompt("KEY", "def", strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !eof || val != "" {
		t.Errorf("expected eof=true, val='', got eof=%v, val=%q", eof, val)
	}

	val2, eof2, err := u.Prompt("KEY", "def", strings.NewReader("\n"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if eof2 || val2 != "" {
		t.Errorf("expected eof=false, val='', got eof=%v, val=%q", eof2, val2)
	}
}
