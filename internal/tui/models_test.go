package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"coy/internal/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const modelSample = "provider add mine --type openai-compat --base-url https://example.invalid\n" +
	"model add mine/gpt-4 --name 'GPT Four'\nmodel add mine/gpt-5\nmodel add mine/claude-sonnet\nmodel add mine/gpt-5-mini\n" +
	"provider add other --type openai-compat --base-url https://example.invalid\nmodel add other/gpt-4\n"

func modelBrowser(t *testing.T, suffix string) Model {
	t.Helper()
	d, err := config.Parse(modelSample + suffix)
	if err != nil {
		t.Fatal(err)
	}
	m := New(&config.Store{Path: "/synthetic/crushrc"}, d, false)
	m.openProvider()
	return m
}

func search(t *testing.T, m Model, kind, query string) Model {
	t.Helper()
	m, _ = update(t, m, key(kind))
	m.query.SetValue(query)
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

func TestFuzzyAndRegexFiltering(t *testing.T) {
	m := modelBrowser(t, "")
	m = search(t, m, "/", "GPT5")
	matches, err := m.matchingModels()
	if err != nil || len(matches) != 2 || matches[0].ID != "mine/gpt-5" {
		t.Fatal("fuzzy subsequence filtering or ranking failed", err)
	}
	if m.doc.Dirty() {
		t.Fatal("filter marked config dirty")
	}
	m = search(t, m, "/", "four")
	if m.selected() == nil || m.selected().ID != "mine/gpt-4" {
		t.Fatal("model name search failed")
	}
	m = search(t, m, "?", `gpt-(4|5)$`)
	matches, err = m.matchingModels()
	if err != nil || len(matches) != 2 || matches[1].ID != "mine/gpt-5" {
		t.Fatal("regex filtering failed", err)
	}
	m, _ = update(t, m, key("?"))
	m.query.SetValue("[")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != "search" || !strings.Contains(m.View(), "正则表达式无效") {
		t.Fatal("invalid regex not exposed")
	}
	if matches, err := m.matchingModels(); err == nil || len(matches) != 0 {
		t.Fatal("invalid regex silently fell back to another selection")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter != `gpt-(4|5)$` || m.mode != "list" {
		t.Fatal("cancel did not restore prior filter")
	}
	m = search(t, m, "?", "no-matches")
	if m.selected() != nil || !strings.Contains(m.View(), "无匹配模型") {
		t.Fatal("empty result selected an unrelated model")
	}
	m, _ = update(t, m, key("d"))
	if m.mode != "list" {
		t.Fatal("empty search allowed deletion")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})
	if matches, err := m.matchingModels(); err != nil || len(matches) != 4 {
		t.Fatal("clear filter failed", err)
	}
}

func TestRegexSelectBatchDeleteAndCancellation(t *testing.T) {
	m := modelBrowser(t, "")
	m = search(t, m, "?", `gpt-(4|5)$`)
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if len(m.chosenModels()) != 2 {
		t.Fatal("select matches failed")
	}
	// A changed filter does not silently discard selection; confirmation must
	// still list the selected IDs, including those hidden by the new filter.
	m = search(t, m, "?", "sonnet$")
	m, _ = update(t, m, key("d"))
	if len(m.pendingDelete) != 2 || !strings.Contains(m.View(), "mine/gpt-4") || !strings.Contains(m.View(), "mine/gpt-5") {
		t.Fatal("confirmation did not identify all selected models")
	}
	m, _ = update(t, m, key("n"))
	if len(m.models()) != 4 || len(m.chosenModels()) != 2 {
		t.Fatal("cancel changed draft or selection")
	}
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("y"))
	if len(m.models()) != 2 || len(m.chosenModels()) != 0 || !m.doc.Dirty() {
		t.Fatal("batch delete failed")
	}
	all := m.doc.Entries()
	if all[len(all)-1].ID != "other/gpt-4" {
		t.Fatal("batch affected another provider")
	}
}

func TestBatchDeleteReferenceErrorIsAtomic(t *testing.T) {
	m := modelBrowser(t, "model large mine/gpt-5\n")
	m = search(t, m, "?", `gpt-(4|5)$`)
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(t, m, key("d"))
	m, _ = update(t, m, key("y"))
	if len(m.models()) != 4 || m.doc.Dirty() || len(m.chosenModels()) != 2 || !strings.Contains(m.status, "引用") {
		t.Fatal("reference error partially deleted models or lost selection")
	}
}

func TestLogicalGroupSelectionCollapseAndFiltering(t *testing.T) {
	m := modelBrowser(t, "")
	m = search(t, m, "?", "gpt-")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(t, m, key("g"))
	m.query.SetValue("常用")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.doc.Dirty() || len(m.chosenModels()) != 3 {
		t.Fatal("cancel group changed draft or selection")
	}
	m, _ = update(t, m, key("g"))
	m.query.SetValue("常用")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.chosenModels()) != 0 || m.models()[0].Group() != "常用" || m.models()[2].Group() != "" {
		t.Fatal("group membership incorrect")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})
	if len(m.modelRows()) != 5 || m.selected() != nil {
		t.Fatal("group header or expanded children missing")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.modelRows()) != 2 || !m.groupCollapsed["mine/常用"] {
		t.Fatal("group not folded")
	}
	m = search(t, m, "?", "gpt-5-mini$")
	if len(m.modelRows()) != 2 || m.modelRows()[1].entry.ID != "mine/gpt-5-mini" {
		t.Fatal("filter did not reveal matching folded child")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(t, m, key("G"))
	if m.models()[3].Group() != "" || len(m.chosenModels()) != 0 {
		t.Fatal("ungroup failed")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})
	if !m.groupCollapsed["mine/常用"] || len(m.modelRows()) != 3 {
		t.Fatal("filter changed saved folding state")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if len(m.chosenModels()) != 4 {
		t.Fatal("select all skipped collapsed members")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if len(m.chosenModels()) != 0 {
		t.Fatal("clear selection failed")
	}
}

func TestManualSelectionProviderScopeAndReplacement(t *testing.T) {
	m := modelBrowser(t, "")
	m, _ = update(t, m, key(" "))
	m, _ = update(t, m, key("j"))
	m, _ = update(t, m, key(" "))
	if len(m.chosenModels()) != 2 {
		t.Fatal("manual selection failed")
	}
	m = search(t, m, "?", "sonnet$")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if chosen := m.chosenModels(); len(chosen) != 1 || chosen[0].ID != "mine/claude-sonnet" {
		t.Fatal("select matches did not replace previous selection")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = update(t, m, key("j"))
	if len(m.selection) != 0 || m.provider().ID != "other" {
		t.Fatal("selection leaked between providers")
	}
}

func TestGroupTUISaveAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "dry-run"}[dry], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "crushrc")
			if err := os.WriteFile(path, []byte(modelSample), 0600); err != nil {
				t.Fatal(err)
			}
			s, d, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			m := New(s, d, dry)
			m = search(t, m, "?", `gpt-(4|5)$`)
			m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
			m, _ = update(t, m, key("g"))
			m.query.SetValue("常用")
			m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			m, cmd := update(t, m, key("s"))
			if !dry {
				m, cmd = update(t, m, key("y"))
			}
			if cmd == nil {
				t.Fatal("save command missing")
			}
			m, _ = update(t, m, cmd())
			_, loaded, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if dry {
				names, err := os.ReadDir(dir)
				if err != nil || len(names) != 1 || loaded.Entries()[1].Group() != "" || !m.doc.Dirty() || m.mode != "preview" {
					t.Fatal("dry-run wrote group data or lost edits", err)
				}
			} else if loaded.Entries()[1].Group() != "常用" || loaded.Entries()[2].Group() != "常用" || m.doc.Dirty() {
				t.Fatal("TUI did not save and reload group membership")
			}
		})
	}
}

func TestModelBrowserFitsSmallWindow(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {40, 16}, {80, 24}} {
		m := modelBrowser(t, "")
		m, _ = update(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, state := range []string{"list", "filter", "status", "readonly", "search"} {
			switch state {
			case "filter":
				m = search(t, m, "?", "gpt-")
				m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
			case "status":
				m.status = "已更新分组草稿；s 保存。"
			case "readonly":
				m.doc.ReadOnly = "配置只读"
			case "search":
				m, _ = update(t, m, key("?"))
			}
			view := strings.TrimSuffix(m.View(), "\n")
			if lipgloss.Height(view) > size[1] || lipgloss.Width(view) > size[0] {
				t.Fatalf("%s exceeds %dx%d: %dx%d", state, size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
			}
		}
	}
}
