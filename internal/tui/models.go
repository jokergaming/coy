package tui

import (
	"cmp"
	"errors"
	"regexp"
	"slices"
	"strings"

	"coy/internal/config"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

const regexHelp = "(?i) 忽略大小写 · (?m) ^$ 按行匹配\n" +
	"(?s) .匹配换行 · (?U) 反转贪婪\n" +
	"(?im) 组合 · (?-i) 关闭 · (?i:…) 局部"

type modelRow struct {
	entry *config.Entry
	group string
	count int
}

func (m Model) activeFilter() (string, string) {
	if m.mode == "search" {
		return m.query.Value(), m.searchKind
	}
	return m.filter, m.filterKind
}

func (m Model) matchingModels() ([]*config.Entry, error) {
	query, kind := m.activeFilter()
	models := m.models()
	if query == "" {
		return models, nil
	}
	var re *regexp.Regexp
	if kind == "regex" {
		var err error
		re, err = regexp.Compile(query)
		if err != nil {
			// regexp errors echo input, which may contain pasted credentials.
			return nil, errors.New("正则表达式无效，请检查括号、转义和重复运算符")
		}
	}
	type match struct {
		entry *config.Entry
		score int
	}
	var matches []match
	for _, e := range models {
		name := ""
		if !e.Dynamic("name") {
			name = e.Value("name")
		}
		if re != nil {
			if re.MatchString(e.ID) || (name != "" && re.MatchString(name)) {
				matches = append(matches, match{entry: e})
			}
			continue
		}
		score, ok := fuzzyScore(query, e.ID+" "+name)
		if ok {
			matches = append(matches, match{e, score})
		}
	}
	if re == nil {
		slices.SortStableFunc(matches, func(a, b match) int { return cmp.Compare(b.score, a.score) })
	}
	result := make([]*config.Entry, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.entry)
	}
	return result, nil
}

// Match ordered subsequences, rewarding consecutive characters and word starts.
// Space-separated terms can match independently, as in an interactive fuzzy finder.
func fuzzyScore(query, text string) (int, bool) {
	textRunes := []rune(strings.ToLower(text))
	score := 0
	for term := range strings.FieldsSeq(strings.ToLower(query)) {
		position, previous := 0, -2
		for _, want := range term {
			found := false
			for position < len(textRunes) {
				i, r := position, textRunes[position]
				position++
				if r != want {
					continue
				}
				score += 10
				if i == previous+1 {
					score += 12
				}
				if i == 0 || strings.ContainsRune("/._- :", textRunes[i-1]) {
					score += 8
				}
				score -= i - previous - 1
				previous, found = i, true
				break
			}
			if !found {
				return 0, false
			}
		}
	}
	return score, true
}

func (m Model) modelRows() []modelRow {
	models, _ := m.matchingModels() // The browser displays the validation error separately.
	groups := map[string][]*config.Entry{}
	for _, e := range models {
		if e.Group() != "" {
			groups[e.Group()] = append(groups[e.Group()], e)
		}
	}
	query, _ := m.activeFilter()
	var rows []modelRow
	seen := map[string]bool{}
	for _, e := range models {
		group := e.Group()
		if group == "" {
			rows = append(rows, modelRow{entry: e})
			continue
		}
		if seen[group] {
			continue
		}
		seen[group] = true
		rows = append(rows, modelRow{group: group, count: len(groups[group])})
		if query != "" || !m.groupCollapsed[m.groupKey(group)] {
			for _, child := range groups[group] {
				rows = append(rows, modelRow{entry: child, group: group})
			}
		}
	}
	return rows
}

func (m Model) groupKey(group string) string {
	if p := m.provider(); p != nil {
		return p.ID + "/" + group
	}
	return group
}

func (m Model) currentRow() modelRow {
	rows := m.modelRows()
	if len(rows) == 0 {
		return modelRow{}
	}
	return rows[min(m.modelCursor, len(rows)-1)]
}

func (m *Model) toggleGroup() {
	row := m.currentRow()
	if row.entry == nil && row.group != "" {
		query, _ := m.activeFilter()
		if query != "" {
			m.status = "筛选期间匹配分组保持展开；Ctrl+L 清除筛选后可折叠。"
			return
		}
		key := m.groupKey(row.group)
		m.groupCollapsed[key] = !m.groupCollapsed[key]
	}
}

func (m Model) chosenModels() []*config.Entry {
	var entries []*config.Entry
	for _, e := range m.models() {
		if m.selection[e.ID] {
			entries = append(entries, e)
		}
	}
	return entries
}

func (m Model) modelTargets() []*config.Entry {
	if selected := m.chosenModels(); len(selected) > 0 {
		return selected
	}
	if e := m.selected(); e != nil && e.Kind == "model" {
		return []*config.Entry{e}
	}
	return nil
}

func (m *Model) startSearch(kind string) tea.Cmd {
	m.mode, m.searchKind = "search", kind
	m.query = textinput.New()
	m.query.CharLimit = 512
	m.query.Width = max(10, m.width-16)
	m.query.SetValue(m.filter)
	m.modelCursor = 0
	m.status = ""
	return m.query.Focus()
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode, m.modelCursor = "list", 0
		return m, nil
	case "enter":
		if _, err := m.matchingModels(); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.filter, m.filterKind = m.query.Value(), m.searchKind
		m.mode, m.modelCursor, m.status = "list", 0, ""
		return m, nil
	case "tab":
		if m.searchKind == "regex" {
			m.searchKind = "fuzzy"
		} else {
			m.searchKind = "regex"
		}
	case "up", "down":
		step := 1
		if msg.String() == "up" {
			step = -1
		}
		m.move(step)
		return m, nil
	default:
		var cmd tea.Cmd
		m.query, cmd = m.query.Update(msg)
		m.modelCursor, m.status = 0, ""
		return m, cmd
	}
	m.modelCursor, m.status = 0, ""
	return m, nil
}

func (m *Model) startGroup() tea.Cmd {
	if len(m.modelTargets()) == 0 {
		m.status = "请先用空格或 Ctrl+A 选择模型。"
		return nil
	}
	m.mode, m.status = "group", ""
	m.query = textinput.New()
	m.query.CharLimit = 80
	m.query.Width = max(10, m.width-8)
	return m.query.Focus()
}

func (m Model) updateGroup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = "list"
		return m, nil
	case "enter":
		name := m.query.Value()
		if name == "" {
			m.status = "请输入有效分组名；G 可移出分组。"
			return m, nil
		}
		if err := m.doc.GroupModels(m.modelTargets(), name); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.groupCollapsed[m.groupKey(name)] = false
		clear(m.selection)
		m.mode, m.modelCursor = "list", 0
		m.status = "已更新分组草稿；s 保存。"
		return m, nil
	}
	var cmd tea.Cmd
	m.query, cmd = m.query.Update(msg)
	return m, cmd
}

func (m *Model) requestDelete() {
	m.pendingDelete = nil
	if m.modelPane {
		m.pendingDelete = m.modelTargets()
	} else if e := m.selected(); e != nil {
		m.pendingDelete = []*config.Entry{e}
	}
	if len(m.pendingDelete) > 0 {
		m.mode, m.previewOffset = "confirm-delete", 0
	}
}
