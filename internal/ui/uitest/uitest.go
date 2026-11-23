// Package uitest provides helpers for testing Bubble Tea models: building
// key messages and running commands synchronously.
package uitest

import (
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// specialKeys maps key names, as tea.Key.String reports them, to key codes.
var specialKeys = map[string]rune{
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEscape,
	"tab":       tea.KeyTab,
	"backspace": tea.KeyBackspace,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"pgup":      tea.KeyPgUp,
	"pgdown":    tea.KeyPgDown,
	"space":     tea.KeySpace,
}

// KeyPress returns the key press message for a key name such as "q", "G",
// "?", "enter", "esc", "up" or "ctrl+c". It panics on a name it does not
// know, which is a mistake in the test.
func KeyPress(name string) tea.KeyPressMsg {
	if code, ok := specialKeys[name]; ok {
		k := tea.KeyPressMsg{Code: code}
		if code == tea.KeySpace {
			k.Text = " "
		}
		return k
	}
	if len(name) > len("ctrl+") && name[:len("ctrl+")] == "ctrl+" {
		r, n := utf8.DecodeRuneInString(name[len("ctrl+"):])
		if n == len(name)-len("ctrl+") {
			return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
		}
	}
	if r, n := utf8.DecodeRuneInString(name); n == len(name) && r != utf8.RuneError {
		return tea.KeyPressMsg{Code: r, Text: name}
	}
	panic("uitest: unknown key " + name)
}

// cmdTimeout bounds how long Run waits for a command. Models under test
// must not schedule real timers (inject a fake clock instead), so a command
// that takes this long is blocked and fails the test.
const cmdTimeout = 5 * time.Second

// Run executes cmd and returns the messages it produces, flattening
// batches. A command that does not return within cmdTimeout fails the test.
// Sequences are not supported.
func Run(t testing.TB, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case nil:
			return nil
		case tea.BatchMsg:
			var out []tea.Msg
			for _, c := range msg {
				out = append(out, Run(t, c)...)
			}
			return out
		default:
			return []tea.Msg{msg}
		}
	case <-time.After(cmdTimeout):
		t.Fatalf("command did not return within %v", cmdTimeout)
		return nil
	}
}

// IsQuit reports whether msgs contain tea.QuitMsg.
func IsQuit(msgs []tea.Msg) bool {
	for _, m := range msgs {
		if _, ok := m.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}
