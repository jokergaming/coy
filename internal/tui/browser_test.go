package tui

import (
	"errors"
	"strings"
	"testing"

	"coy/internal/config"
	"coy/internal/discovery"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func browser(t *testing.T) Model {
	t.Helper()
	d, err := config.Parse("model add mine/vendor/model-a\nprovider add other --type openai-compat --base-url 'https://example.invalid/v1'\nprovider add mine --type openai-compat --base-url 'https://example.invalid/v1'\nmodel add other/model-b\nmodel add mine/model-c\n")
	if err != nil {
		t.Fatal(err)
	}
	return New(&config.Store{Path: "/synthetic/crushrc"}, d, false)
}

func TestBrowserNavigationAndScopedDeletion(t *testing.T) {
	m := browser(t)
	if m.provider().ID != "other" || len(m.models()) != 1 || m.models()[0].ID != "other/model-b" {
		t.Fatal("models not grouped by provider")
	}
	m, _ = update(t, m, key("j"))
	if m.provider().ID != "mine" || len(m.models()) != 2 {
		t.Fatal("provider selection did not change models")
	}
	m, _ = update(t, m, key(" "))
	if !m.collapsed["mine"] || !strings.Contains(m.View(), "目录已折叠") {
		t.Fatal("directory did not collapse")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.modelPane || m.collapsed["mine"] || m.selected().ID != "mine/vendor/model-a" {
		t.Fatal("enter should open model directory")
	}
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("n"))
	if len(m.models()) != 2 {
		t.Fatal("cancel deleted model")
	}
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("y"))
	if len(m.models()) != 1 || m.selected().ID != "mine/model-c" {
		t.Fatal("wrong model removed or cursor lost")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.modelPane || m.selected().ID != "mine" {
		t.Fatal("left should return to parent")
	}
	m, _ = update(t, m, key("k"))
	if len(m.models()) != 1 || m.models()[0].ID != "other/model-b" {
		t.Fatal("deletion affected another provider")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(t, m, key("e"))
	if m.mode != "form" || m.editing.ID != "other/model-b" {
		t.Fatal("model edit did not target active pane")
	}
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 30}} {
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		m, _ = update(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if lipgloss.Width(m.View()) > size[0] {
			t.Fatal("browser exceeded window width")
		}
	}
}

func TestDiscoveryFailureCancellationAndAtomicImport(t *testing.T) {
	m := browser(t)
	m, cmd := update(t, m, key("r"))
	if cmd == nil || !m.busy {
		t.Fatal("discovery not started")
	}
	m, _ = update(t, m, key("d"))
	if m.mode != "list" {
		t.Fatal("draft edited while discovery was pending")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = update(t, m, cmd()) // Cancelled before execution: never contacts the network.
	if m.busy || len(m.doc.Providers()) != 2 || !strings.Contains(m.status, "取消") {
		t.Fatal("cancel should preserve provider and report cancellation")
	}
	m, _ = update(t, m, discoveryMsg{provider: "other", err: errors.New("HTTP 401")})
	if !strings.Contains(m.status, "HTTP 401") || len(m.models()) != 1 {
		t.Fatal("failure lost edits or hid error")
	}
	m, _ = update(t, m, discoveryMsg{provider: "other", models: []discovery.Model{{ID: "valid"}, {ID: "invalid id"}}})
	if len(m.models()) != 1 || !strings.Contains(m.status, "模型未导入") {
		t.Fatal("invalid catalogue partially imported")
	}
	m, _ = update(t, m, discoveryMsg{provider: "other", models: []discovery.Model{{ID: "model-b", Name: "Overwrite"}, {ID: "new"}, {ID: "new"}}})
	if len(m.models()) != 2 || m.models()[0].Value("name") != "" || !m.modelPane {
		t.Fatal("discovery should deduplicate, preserve existing models and open pane")
	}
}

func TestEmptyModelPaneAndParentForm(t *testing.T) {
	m := browser(t)
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("y"))
	if m.selected() != nil {
		t.Fatal("empty model pane must not select provider")
	}
	m, _ = update(t, m, key("d"))
	if m.mode != "list" {
		t.Fatal("delete in empty pane must not delete parent")
	}
	m, _ = update(t, m, key("m"))
	if m.inputs[0].Value() != "other/" {
		t.Fatal("new model should use current directory even when empty")
	}
}

func TestLongIDsKeepBrowserRowsStable(t *testing.T) {
	m := browser(t)
	want := lipgloss.Height(m.browserView())
	longID := strings.Repeat("long-id-", 40)
	model := m.models()[0]
	m.providers()[0].ID = longID
	model.ID = longID + "/" + longID
	if got := lipgloss.Height(m.browserView()); got != want {
		t.Fatalf("long IDs wrapped browser rows: got %d, want %d", got, want)
	}
}

func TestCancelDiscardsAnAlreadyCompletedDiscovery(t *testing.T) {
	m := browser(t)
	m, _ = update(t, m, key("r"))
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	// The HTTP command can finish before Esc, with its result still queued.
	m, _ = update(t, m, discoveryMsg{provider: "other", models: []discovery.Model{{ID: "unwanted"}}})
	if len(m.models()) != 1 || !strings.Contains(m.status, "取消") || m.busy {
		t.Fatal("cancellation should also discard a queued successful response")
	}
}
