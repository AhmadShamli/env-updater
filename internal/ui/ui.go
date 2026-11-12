package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

const HeaderWidth = 60

type UI struct {
	Quiet       bool
	NoHeader    bool
	NoColor     bool
	headerShown bool
	stdinReader *bufio.Reader

	termMu    sync.Mutex
	termFd    int
	termState *term.State

	cReset  string
	cRed    string
	cGreen  string
	cYellow string
	cBlue   string
	cBold   string
}

func NewUI(quiet, noHeader, noColor bool) *UI {
	u := &UI{
		Quiet:       quiet,
		NoHeader:    noHeader,
		NoColor:     noColor,
		stdinReader: bufio.NewReader(os.Stdin),
	}
	u.setupColors()
	return u
}

func (u *UI) setupColors() {
	isTerm := term.IsTerminal(int(os.Stderr.Fd()))
	if !u.NoColor && isTerm && os.Getenv("NO_COLOR") == "" {
		u.cReset = "\033[0m"
		u.cRed = "\033[31m"
		u.cGreen = "\033[32m"
		u.cYellow = "\033[33m"
		u.cBlue = "\033[34m"
		u.cBold = "\033[1m"
	}
}

// RestoreTerminal restores terminal echo if interrupted during a password prompt.
func (u *UI) RestoreTerminal() {
	u.termMu.Lock()
	defer u.termMu.Unlock()
	if u.termState != nil {
		_ = term.Restore(u.termFd, u.termState)
		u.termState = nil
		fmt.Fprintf(os.Stderr, "\n")
	}
}

func (u *UI) LogInfo(format string, a ...interface{}) {
	if !u.Quiet {
		fmt.Fprintf(os.Stdout, format+"\n", a...)
	}
}

func (u *UI) LogSuccess(format string, a ...interface{}) {
	if !u.Quiet {
		fmt.Fprintf(os.Stdout, "%s"+format+"%s\n", append(append([]interface{}{u.cGreen}, a...), u.cReset)...)
	}
}

func (u *UI) LogWarn(format string, a ...interface{}) {
	if !u.Quiet {
		fmt.Fprintf(os.Stderr, "%sWarning: "+format+"%s\n", append(append([]interface{}{u.cYellow}, a...), u.cReset)...)
	}
}

func (u *UI) LogFail(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s"+format+"%s\n", append(append([]interface{}{u.cYellow}, a...), u.cReset)...)
}

func (u *UI) LogError(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "%sError: "+format+"%s\n", append(append([]interface{}{u.cRed}, a...), u.cReset)...)
}

func (u *UI) Bold(text string) string {
	return u.cBold + text + u.cReset
}

// FitText truncates s from the left by terminal display width so it never slices mid-rune
// and never exceeds width display columns.
func FitText(s string, width int) string {
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	runes := []rune(s)
	target := width - 3
	acc := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		rw := runewidth.RuneWidth(runes[i])
		if acc+rw > target {
			break
		}
		acc += rw
		start = i
	}
	return "..." + string(runes[start:])
}

func (u *UI) headerRow(text string) {
	padded := runewidth.FillRight(FitText(text, HeaderWidth), HeaderWidth)
	fmt.Fprintf(os.Stderr, "%s|%s %s %s|%s\n", u.cBlue, u.cReset, padded, u.cBlue, u.cReset)
}

func (u *UI) headerRule() {
	fmt.Fprintf(os.Stderr, "%s+--------------------------------------------------------------+%s\n", u.cBlue, u.cReset)
}

func (u *UI) ShowHeader(version, license, repo, command, source, target string, dryRun, interactive, prune bool) {
	if u.headerShown || u.Quiet || u.NoHeader {
		return
	}
	u.headerShown = true

	modeStr := command
	if modeStr == "" {
		modeStr = "sync"
	}

	var flags []string
	if dryRun {
		flags = append(flags, "dry-run")
	}
	if interactive {
		flags = append(flags, "interactive")
	}
	if prune {
		flags = append(flags, "prune")
	}
	if len(flags) > 0 {
		modeStr = fmt.Sprintf("%s (%s)", modeStr, strings.Join(flags, ", "))
	}

	u.headerRule()
	u.headerRow("env-updater")
	u.headerRow(".env synchronization and management utility")
	u.headerRow("Version: " + version)
	u.headerRow("License: " + license)
	u.headerRow("Repo:    " + repo)
	u.headerRule()
	u.headerRow("Mode:    " + modeStr)

	switch command {
	case "sync", "check", "status", "":
		u.headerRow("Source:  " + source)
		u.headerRow("Target:  " + target)
	case "restore", "clean-backups":
		u.headerRow("Target:  " + target)
	}

	u.headerRule()
	fmt.Fprintf(os.Stderr, "\n")
}

// IsSecretKey determines whether a variable name likely holds a secret.
func IsSecretKey(key string) bool {
	tokens := strings.Split(strings.ToUpper(key), "_")
	for _, tok := range tokens {
		switch tok {
		case "PASS", "PASSWD", "PASSWORD", "SECRET", "SECRETS", "TOKEN", "TOKENS",
			"KEY", "KEYS", "PRIVATE", "CREDENTIAL", "CREDENTIALS", "AUTH", "SALT", "BEARER":
			return true
		}
	}
	upper := strings.ToUpper(key)
	for _, sub := range []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "PRIVATE", "CREDENTIAL"} {
		if strings.Contains(upper, sub) {
			return true
		}
	}
	return false
}

// Prompt reads interactive input for a key, masking secrets if reading from a terminal.
// It returns (value, eof, error), where eof is true when EOF was reached with no input.
func (u *UI) Prompt(key, defVal string, in io.Reader) (string, bool, error) {
	promptStr := fmt.Sprintf("Enter value for %s", key)
	if defVal != "" {
		promptStr = fmt.Sprintf("Enter value for %s [%s]", key, defVal)
	}
	fmt.Fprintf(os.Stderr, "%s: ", promptStr)

	useStdin := (in == nil || in == os.Stdin)
	if useStdin && IsSecretKey(key) && term.IsTerminal(int(os.Stdin.Fd())) {
		fd := int(os.Stdin.Fd())
		u.termMu.Lock()
		if state, err := term.GetState(fd); err == nil {
			u.termFd = fd
			u.termState = state
		}
		u.termMu.Unlock()

		byteVal, err := term.ReadPassword(fd)

		u.termMu.Lock()
		u.termState = nil
		u.termMu.Unlock()

		fmt.Fprintf(os.Stderr, "\n")
		if err != nil {
			return "", false, err
		}
		val := strings.TrimRight(string(byteVal), "\r\n")
		return val, false, nil
	}

	reader := u.stdinReader
	if !useStdin {
		reader = bufio.NewReader(in)
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				return "", true, nil
			}
			return line, false, nil
		}
		return "", false, err
	}
	line = strings.TrimRight(line, "\r\n")
	return line, false, nil
}
