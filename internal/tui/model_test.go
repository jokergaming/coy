package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"coy/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func update(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestCreatePreviewSaveAndReload(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "dry-run"}[dry], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crushrc")
			s, d, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			m := New(s, d, dry)
			m, _ = update(t, m, key("n"))
			if m.mode != "form" {
				t.Fatal("form did not open")
			}
			m.inputs[0].SetValue("my-api")
			m.inputs[1].SetValue("My API")
			m.inputs[3].SetValue("https://example.com/v1")
			m.inputs[4].SetValue("super-private-key")
			if strings.Contains(m.View(), "super-private-key") {
				t.Fatal("secret visible")
			}
			m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if len(d.Entries()) != 1 {
				t.Fatal("provider not created", m.status)
			}
			m, _ = update(t, m, key("m"))
			m.inputs[0].SetValue("my-api/model-v1")
			m.inputs[1].SetValue("Model V1")
			m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if len(d.Entries()) != 2 {
				t.Fatal("model not created", m.status)
			}
			m, cmd := update(t, m, key("p"))
			if cmd == nil {
				t.Fatal("preview command missing")
			}
			m, _ = update(t, m, cmd())
			if m.mode != "preview" || strings.Contains(m.View(), "super-private-key") {
				t.Fatal("unsafe preview")
			}
			m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			m, cmd = update(t, m, key("s"))
			if !dry {
				if m.mode != "confirm-save" {
					t.Fatal("confirmation missing")
				}
				m, cmd = update(t, m, key("y"))
			}
			if cmd == nil {
				t.Fatal("save command missing")
			}
			m, _ = update(t, m, cmd())
			if dry {
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("dry-run wrote config")
				}
				if !d.Dirty() {
					t.Fatal("dry-run lost edits")
				}
			} else {
				_, loaded, err := config.Load(path)
				if err != nil || len(loaded.Entries()) != 2 {
					t.Fatal("saved config cannot be loaded", err)
				}
			}
		})
	}
}

func TestCancelDeleteAndDirtyQuit(t *testing.T) {
	s, d, err := config.Load(filepath.Join(t.TempDir(), "crushrc"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(s, d, false)
	m, _ = update(t, m, key("n"))
	m.inputs[0].SetValue("discarded")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if d.Dirty() {
		t.Fatal("cancel changed document")
	}
	if err = d.Put(nil, "provider", "mine", map[string]string{"type": "openai-compat", "base-url": "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	m, _ = update(t, m, key("q"))
	if m.mode != "confirm-quit" {
		t.Fatal("dirty quit not confirmed")
	}
	m, _ = update(t, m, key("n"))
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("n"))
	if len(d.Entries()) != 1 {
		t.Fatal("cancel deleted entry")
	}
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("y"))
	if len(d.Entries()) != 0 {
		t.Fatal("delete did not apply")
	}
}

func TestSaveFailureKeepsDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crushrc")
	s, d, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Put(nil, "provider", "mine", map[string]string{"type": "openai-compat", "base-url": "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("# external change"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(s, d, false)
	m, _ = update(t, m, key("s"))
	m, cmd := update(t, m, key("y"))
	m, _ = update(t, m, cmd())
	if !d.Dirty() || !strings.Contains(m.status, "外部修改") {
		t.Fatal("failed save lost edits or hid error")
	}
}
