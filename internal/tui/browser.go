package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"coy/internal/config"
	"coy/internal/discovery"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) providers() []*config.Entry {
	var entries []*config.Entry
	for _, e := range m.doc.Entries() {
		if e.Kind == "provider" {
			entries = append(entries, e)
		}
	}
	return entries
}

func (m Model) provider() *config.Entry {
	providers := m.providers()
	if len(providers) == 0 {
		return nil
	}
	return providers[min(m.cursor, len(providers)-1)]
}

func (m Model) models() []*config.Entry {
	p := m.provider()
	if p == nil {
		return nil
	}
	var models []*config.Entry
	for _, e := range m.doc.Entries() {
		if e.Kind == "model" && strings.HasPrefix(e.ID, p.ID+"/") {
			models = append(models, e)
		}
	}
	return models
}

func (m *Model) move(step int) {
	if m.modelPane {
		m.modelCursor = max(0, min(m.modelCursor+step, len(m.modelRows())-1))
		return
	}
	next := max(0, min(m.cursor+step, len(m.providers())-1))
	if next != m.cursor {
		clear(m.selection)
	}
	m.cursor = next
	m.modelCursor = 0
}

func (m *Model) openProvider() {
	if p := m.provider(); p != nil {
		m.collapsed[p.ID] = false
		m.modelPane = true
	}
}

func (m *Model) selectEntry(kind, id string) {
	clear(m.selection)
	m.filter, m.filterKind = "", ""
	provider := id
	if kind == "model" {
		provider, _, _ = strings.Cut(id, "/")
	}
	for i, e := range m.providers() {
		if e.ID == provider {
			m.cursor = i
			break
		}
	}
	m.modelPane = kind == "model"
	if kind == "model" {
		m.collapsed[provider] = false
		for _, e := range m.models() {
			if e.ID == id {
				m.groupCollapsed[m.groupKey(e.Group())] = false
			}
		}
		for i, row := range m.modelRows() {
			if row.entry != nil && row.entry.ID == id {
				m.modelCursor = i
				break
			}
		}
	}
}

func (m *Model) discoverProvider() tea.Cmd {
	p := m.provider()
	if p == nil {
		m.status = "请先新增自定义提供商。"
		return nil
	}
	baseURL, err := p.RequestValue("base-url")
	if err != nil {
		m.status = err.Error() + "；提供商草稿已保留。"
		return nil
	}
	apiKey, err := p.RequestValue("api-key")
	if err != nil {
		m.status = err.Error() + "；提供商草稿已保留。"
		return nil
	}
	id := p.ID
	ctx, cancel := context.WithCancel(context.Background())
	m.discoveryContext = ctx
	m.cancelDiscovery = cancel
	m.busy = true
	m.status = "提供商草稿已保留，正在获取 /models 列表…"
	return func() tea.Msg {
		models, err := discovery.Fetch(ctx, baseURL, apiKey)
		if err == nil && len(models) == 0 {
			err = errors.New("服务返回了空模型列表")
		}
		return discoveryMsg{provider: id, models: models, err: err}
	}
}

func (m Model) browserView() string {
	rows := max(1, m.height-16)
	if m.height < 18 {
		rows = max(1, m.height-11)
	}
	if m.mode != "search" && m.filter != "" {
		rows = max(1, rows-1)
	}
	if m.mode == "search" && m.searchKind == "regex" {
		rows = max(1, rows-strings.Count(regexHelp, "\n")-1)
	}
	left, right := []string{"提供商 /"}, []string{"模型"}
	providers, models := m.providers(), m.modelRows()
	if len(providers) == 0 {
		left = append(left, "按 n 新增目录")
	}
	start := max(0, m.cursor-rows+1)
	for i := start; i < min(len(providers), start+rows); i++ {
		p := providers[i]
		branch := "▾ "
		if m.collapsed[p.ID] {
			branch = "▸ "
		}
		line := "  " + branch + clean(p.ID) + "/"
		if i == m.cursor {
			line = "›" + line[1:]
			if !m.modelPane {
				line = accent.Render(line)
			}
		}
		left = append(left, line)
	}
	if p := m.provider(); p != nil {
		matches, err := m.matchingModels()
		right[0] = fmt.Sprintf("模型 /%s/ %d/%d · 选%d", clean(p.ID), len(matches), len(m.models()), len(m.chosenModels()))
		query, kind := m.activeFilter()
		if query != "" {
			label := "模糊"
			if kind == "regex" {
				label = "正则"
			}
			right[0] += " [" + label + "]"
		}
		if m.collapsed[p.ID] {
			right = append(right, "目录已折叠，→ 展开")
		} else {
			if err != nil {
				right = append(right, err.Error())
			} else if len(models) == 0 {
				message := "空目录：m 添加 / r 发现"
				if query != "" {
					message = "无匹配模型；Ctrl+L 清除筛选"
				}
				right = append(right, message)
			}
			start := max(0, m.modelCursor-rows+1)
			for i := start; i < min(len(models), start+rows); i++ {
				row := models[i]
				line := "  "
				if row.entry == nil {
					branch := "▾ "
					if query == "" && m.groupCollapsed[m.groupKey(row.group)] {
						branch = "▸ "
					}
					line += branch + clean(row.group) + fmt.Sprintf("/ (%d)", row.count)
				} else {
					if row.group != "" {
						line += "  "
					}
					mark := "[ ] "
					if m.selection[row.entry.ID] {
						mark = "[x] "
					}
					line += mark + clean(strings.TrimPrefix(row.entry.ID, p.ID+"/"))
				}
				if i == m.modelCursor && m.modelPane {
					line = accent.Render("›" + line[1:])
				}
				right = append(right, line)
			}
		}
	}
	leftWidth := max(12, (m.width-5)/3)
	rightWidth := m.width - 5 - leftWidth
	pane := func(lines []string, width int, active bool) string {
		for i := range lines {
			// Truncate before applying pane width, which otherwise wraps long IDs
			// and moves the selected row outside the visible scroll area.
			lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(lines[i])
		}
		style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(width).MaxWidth(width + 2).Height(rows + 1)
		if active {
			style = style.BorderForeground(lipgloss.Color("212"))
		}
		return style.Render(strings.Join(lines, "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, pane(left, leftWidth, !m.modelPane), " ", pane(right, rightWidth, m.modelPane)) + "\n"
}
