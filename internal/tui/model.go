package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"coy/internal/config"
	"coy/internal/discovery"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

type saveMsg struct {
	result config.Result
	err    error
}

type discoveryMsg struct {
	provider string
	models   []discovery.Model
	err      error
}

type Model struct {
	store              *config.Store
	doc                *config.Document
	dryRun             bool
	width, height      int
	cursor             int
	modelCursor        int
	modelPane          bool
	collapsed          map[string]bool
	groupCollapsed     map[string]bool
	selection          map[string]bool
	pendingDelete      []*config.Entry
	filter, filterKind string
	searchKind         string
	query              textinput.Model
	cancelDiscovery    context.CancelFunc
	discoveryContext   context.Context
	mode               string
	status             string
	busy               bool
	editing            *config.Entry
	kind               string
	inputs             []textinput.Model
	labels             []string
	focus              int
	preview            string
	previewOffset      int
}

func New(store *config.Store, doc *config.Document, dryRun bool) Model {
	return Model{store: store, doc: doc, dryRun: dryRun, width: 80, height: 24, mode: "list", collapsed: map[string]bool{}, groupCollapsed: map[string]bool{}, selection: map[string]bool{}}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) selected() *config.Entry {
	if m.modelPane {
		return m.currentRow().entry
	}
	return m.provider()
}

func (m *Model) form(kind string, e *config.Entry) tea.Cmd {
	m.kind, m.editing, m.focus, m.mode = kind, e, 0, "form"
	m.inputs = nil
	m.labels = nil
	m.status = ""
	if e == nil {
		input := textinput.New()
		input.CharLimit = 256
		m.labels = append(m.labels, "ID（保存后不可重命名）")
		if kind == "model" {
			if p := m.provider(); p != nil {
				input.SetValue(p.ID + "/")
			}
		}
		m.inputs = append(m.inputs, input)
	}
	for _, f := range config.Fields(kind) {
		input := textinput.New()
		input.CharLimit = 4096
		if f.Kind == "secret" {
			input.EchoMode = textinput.EchoPassword
			input.EchoCharacter = '•'
		}
		if e != nil {
			input.SetValue(e.Value(f.Name))
		} else if f.Name == "type" {
			input.SetValue("openai-compat")
		} else if kind == "provider" && f.Name == "discover-models" {
			// Persist the curated list so Crush does not reintroduce deleted models.
			input.SetValue("false")
		}
		label := f.Label
		if kind == "provider" && f.Name == "discover-models" {
			label = "Crush 启动时继续发现（false 保留模型删选结果）"
		}
		if e != nil && e.Dynamic(f.Name) {
			label += "（保留原表达式；修改后按字面值保存）"
		}
		m.labels = append(m.labels, label)
		m.inputs = append(m.inputs, input)
	}
	return m.inputs[0].Focus()
}

func (m Model) save(dry bool) tea.Cmd {
	return func() tea.Msg { r, err := m.store.Save(m.doc, dry); return saveMsg{r, err} }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.query.Width = max(10, m.width-16)
		for i := range m.inputs {
			m.inputs[i].Width = max(10, min(80, m.width-8))
		}
		return m, nil
	case discoveryMsg:
		m.busy = false
		cancelled := m.discoveryContext != nil && m.discoveryContext.Err() != nil
		if m.cancelDiscovery != nil {
			m.cancelDiscovery()
			m.cancelDiscovery = nil
		}
		m.discoveryContext = nil
		if cancelled {
			m.status = "模型发现已取消；提供商草稿已保留。"
			return m, nil
		}
		if msg.err != nil {
			m.status = "模型发现失败：" + msg.err.Error() + "；提供商草稿已保留，可编辑后按 r 重试或 m 手动添加。"
			return m, nil
		}
		models := map[string]string{}
		for _, model := range msg.models {
			models[model.ID] = model.Name
		}
		count, err := m.doc.AddModels(msg.provider, models)
		if err != nil {
			m.status = err.Error() + "；模型未导入，提供商草稿已保留。"
			return m, nil
		}
		m.selectEntry("provider", msg.provider)
		m.collapsed[msg.provider] = false
		m.modelPane = true
		m.modelCursor = 0
		m.status = fmt.Sprintf("模型发现完成：新增 %d 个模型到草稿；d 删除所选模型，s 保存。", count)
		return m, nil
	case saveMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "错误：" + msg.err.Error()
			m.mode = "list"
			return m, nil
		}
		if msg.result.Document != nil {
			m.doc = msg.result.Document
		}
		if msg.result.DryRun {
			m.mode = "preview"
			m.preview = msg.result.Preview
			m.previewOffset = 0
			m.status = "DRY RUN：未写入配置或备份"
		} else if !msg.result.Changed {
			m.status = "无变更，未写入。"
			m.mode = "list"
		} else {
			m.mode = "list"
			m.status = "已保存。备份：" + msg.result.Backup
			if msg.result.Created {
				m.status = "已创建配置（原文件不存在，无原件可备份）。"
			}
		}
		return m, nil
	case tea.KeyMsg:
		if m.busy {
			if m.cancelDiscovery != nil && (msg.String() == "esc" || msg.String() == "ctrl+c") {
				m.cancelDiscovery()
			}
			return m, nil
		}
		key := msg.String()
		if m.mode == "form" {
			return m.updateForm(msg)
		}
		if m.mode == "search" {
			return m.updateSearch(msg)
		}
		if m.mode == "group" {
			return m.updateGroup(msg)
		}
		if m.mode == "preview" {
			switch key {
			case "esc", "q", "ctrl+c":
				m.mode = "list"
			case "down", "j":
				m.previewOffset = min(m.previewOffset+1, max(0, len(strings.Split(m.preview, "\n"))-max(1, m.height-9)))
			case "up", "k":
				m.previewOffset = max(0, m.previewOffset-1)
			}
			return m, nil
		}
		if strings.HasPrefix(m.mode, "confirm-") {
			if key == "esc" || key == "n" || key == "ctrl+c" {
				m.mode = "list"
				m.pendingDelete = nil
				return m, nil
			}
			if m.mode == "confirm-delete" && (key == "up" || key == "down" || key == "j" || key == "k") {
				step := 1
				if key == "up" || key == "k" {
					step = -1
				}
				m.previewOffset = max(0, min(m.previewOffset+step, len(m.pendingDelete)-1))
				return m, nil
			}
			if key != "y" {
				return m, nil
			}
			switch m.mode {
			case "confirm-quit":
				return m, tea.Quit
			case "confirm-save":
				m.busy = true
				return m, m.save(m.dryRun)
			case "confirm-delete":
				if err := m.doc.DeleteEntries(m.pendingDelete); err != nil {
					m.status = err.Error()
				} else {
					m.status = "已从编辑草稿删除，尚未写入。"
					clear(m.selection)
				}
				m.pendingDelete = nil
				m.cursor = min(m.cursor, max(0, len(m.providers())-1))
				m.modelCursor = min(m.modelCursor, max(0, len(m.modelRows())-1))
				m.mode = "list"
			}
			return m, nil
		}
		switch key {
		case "q", "ctrl+c":
			if m.doc.Dirty() {
				m.mode = "confirm-quit"
				return m, nil
			}
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "tab", "shift+tab":
			if m.modelPane {
				m.modelPane = false
			} else {
				m.openProvider()
			}
		case "right", "l":
			if m.modelPane && m.currentRow().entry == nil {
				m.groupCollapsed[m.groupKey(m.currentRow().group)] = false
				return m, nil
			}
			m.openProvider()
		case "left", "h", "esc":
			if m.modelPane {
				if row := m.currentRow(); key != "esc" && row.entry == nil && row.group != "" && !m.groupCollapsed[m.groupKey(row.group)] {
					m.toggleGroup()
					return m, nil
				}
				m.modelPane = false
			} else if p := m.provider(); p != nil {
				m.collapsed[p.ID] = true
			}
		case " ":
			if m.modelPane {
				if e := m.selected(); e != nil {
					if m.selection[e.ID] {
						delete(m.selection, e.ID)
					} else {
						m.selection[e.ID] = true
					}
				} else {
					m.toggleGroup()
				}
			}
			if p := m.provider(); p != nil && !m.modelPane {
				m.collapsed[p.ID] = !m.collapsed[p.ID]
			}
		case "enter":
			if !m.modelPane {
				m.openProvider()
				return m, nil
			}
			if m.currentRow().entry == nil {
				m.toggleGroup()
				return m, nil
			}
			if e := m.selected(); e != nil && m.doc.ReadOnly == "" {
				cmd := m.form(e.Kind, e)
				return m, cmd
			}
		case "/", "?":
			m.openProvider()
			if m.modelPane {
				kind := "fuzzy"
				if key == "?" {
					kind = "regex"
				}
				cmd := m.startSearch(kind)
				return m, cmd
			}
		case "ctrl+l":
			m.filter, m.filterKind, m.modelCursor = "", "", 0
		case "ctrl+a":
			if m.modelPane {
				matches, err := m.matchingModels()
				if err != nil {
					m.status = err.Error()
					return m, nil
				}
				clear(m.selection)
				for _, e := range matches {
					m.selection[e.ID] = true
				}
			}
		case "ctrl+u":
			clear(m.selection)
		case "n", "m", "e", "d", "r", "g", "G":
			if m.doc.ReadOnly != "" {
				m.status = m.doc.ReadOnly
				return m, nil
			}
			switch key {
			case "g", "G":
				if !m.modelPane {
					return m, nil
				}
				if key == "g" {
					cmd := m.startGroup()
					return m, cmd
				}
				targets := m.modelTargets()
				if len(targets) == 0 {
					m.status = "请先选择要移出分组的模型。"
					return m, nil
				}
				if err := m.doc.GroupModels(targets, ""); err != nil {
					m.status = err.Error()
				} else {
					clear(m.selection)
					m.modelCursor, m.status = 0, "已移出分组草稿；s 保存。"
				}
			case "r":
				cmd := m.discoverProvider()
				return m, cmd
			case "n":
				cmd := m.form("provider", nil)
				return m, cmd
			case "m":
				if len(m.doc.Providers()) == 0 {
					m.status = "请先新增自定义 provider。"
					return m, nil
				}
				cmd := m.form("model", nil)
				return m, cmd
			case "e":
				if e := m.selected(); e != nil {
					cmd := m.form(e.Kind, e)
					return m, cmd
				}
			case "d":
				m.requestDelete()
			}
		case "p":
			m.busy = true
			return m, m.save(true)
		case "s", "ctrl+s":
			if m.dryRun {
				m.busy = true
				return m, m.save(true)
			}
			m.mode = "confirm-save"
		}
	}
	if m.mode == "form" && len(m.inputs) > 0 {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	if m.mode == "search" || m.mode == "group" {
		var cmd tea.Cmd
		m.query, cmd = m.query.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = "list"
		m.inputs = nil
		return m, nil
	case "tab", "down", "shift+tab", "up":
		m.inputs[m.focus].Blur()
		step := 1
		if msg.String() == "shift+tab" || msg.String() == "up" {
			step = -1
		}
		m.focus = (m.focus + step + len(m.inputs)) % len(m.inputs)
		return m, m.inputs[m.focus].Focus()
	case "ctrl+s", "enter":
		fields := map[string]string{}
		offset := 0
		id := ""
		if m.editing == nil {
			id = m.inputs[0].Value()
			offset = 1
		} else {
			id = m.editing.ID
		}
		for i, f := range config.Fields(m.kind) {
			fields[f.Name] = m.inputs[i+offset].Value()
		}
		if err := m.doc.Put(m.editing, m.kind, id, fields); err != nil {
			m.status = err.Error()
			return m, nil
		}
		createdProvider := m.editing == nil && m.kind == "provider"
		m.selectEntry(m.kind, id)
		m.mode = "list"
		m.inputs = nil
		m.status = "已更新编辑草稿；p 预览，s 保存。"
		if createdProvider && fields["disable"] != "true" {
			cmd := m.discoverProvider()
			return m, cmd
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (m Model) View() string {
	if m.width < 40 || m.height < 12 {
		return "窗口过小，请调整至至少 40 × 12。\nq 退出（未保存时需确认）。"
	}
	var b strings.Builder
	compact := m.height < 18
	header := "coy · crush-on-you"
	if m.dryRun {
		header += "  [DRY RUN]"
	}
	if m.doc.Dirty() {
		header += "  ● 未保存"
	}
	b.WriteString(accent.Render(header) + "\n" + muted.Render(clean(m.store.Path)) + "\n")
	if !compact {
		b.WriteByte('\n')
	}
	switch m.mode {
	case "form":
		b.WriteString(accent.Render("编辑 "+m.kind) + "\n")
		rows := max(1, (m.height-10)/3)
		start := max(0, m.focus-rows+1)
		for i := start; i < min(len(m.inputs), start+rows); i++ {
			b.WriteString(clean(m.labels[i]) + "\n" + m.inputs[i].View() + "\n\n")
		}
		b.WriteString(muted.Render("Tab/↑↓ 切换字段 · Enter/Ctrl+S 确认草稿 · Esc 取消"))
	case "preview":
		b.WriteString(accent.Render("变更预览（凭据与自由文本值隐藏）") + "\n")
		lines := strings.Split(m.preview, "\n")
		start := min(m.previewOffset, len(lines))
		b.WriteString(strings.Join(lines[start:min(len(lines), start+max(1, m.height-9))], "\n") + "\n")
		b.WriteString(muted.Render("↑↓ 滚动 · Esc 返回"))
	case "confirm-delete":
		b.WriteString(fmt.Sprintf("从草稿删除以下 %d 个条目？y 确认 / n 取消\n", len(m.pendingDelete)))
		start := min(m.previewOffset, len(m.pendingDelete))
		for _, e := range m.pendingDelete[start:min(len(m.pendingDelete), start+max(1, m.height-10))] {
			b.WriteString("  " + clean(e.ID) + "\n")
		}
		b.WriteString(muted.Render("↑↓ 查看全部待删条目；任何引用冲突会阻止整批删除。"))
	case "group":
		b.WriteString(fmt.Sprintf("将 %d 个模型加入分组（同名则合并）\n", len(m.modelTargets())))
		b.WriteString(m.query.View() + "\nEnter 确认草稿 · Esc 取消")
	case "confirm-quit":
		b.WriteString("存在未保存的修改。丢弃并退出？y 确认 / n 取消")
	case "confirm-save":
		b.WriteString("备份现有全局 crushrc 后写入修改？y 确认 / n 取消")
	default:
		if m.doc.ReadOnly != "" {
			b.WriteString("只读：" + m.doc.ReadOnly + "\n")
		}
		if m.mode != "search" && m.filter != "" {
			label := "模糊搜索"
			if m.filterKind == "regex" {
				label = "正则筛选"
			}
			b.WriteString(muted.Render(label+"："+clean(m.filter)) + "\n")
		}
		b.WriteString(m.browserView())
		if m.mode == "search" {
			label := "模糊搜索"
			if m.searchKind == "regex" {
				label = "正则筛选"
			}
			b.WriteString(label + " " + m.query.View() + "\nTab 切换匹配方式 · Enter 应用 · Esc 取消")
			if m.searchKind == "regex" {
				b.WriteString("\n" + muted.Render(regexHelp))
			}
		} else if compact {
			b.WriteString(muted.Render("/ 搜索 · ? 正则 · g 分组 · d 删除\n空格 多选/折叠 · s 保存 · q 退出"))
		} else {
			b.WriteString("\n" + muted.Render("↑↓/jk 移动 · Tab 切栏 · 空格 多选/折叠 · Enter 编辑/展开\n/ 模糊 · ? 正则 · Ctrl+L 清筛选 · Ctrl+A 全选匹配 · Ctrl+U 清选\ng 分组 · G 移出 · d 删除 · n/m 新增 · e 编辑 · r 发现\np 预览 · s 保存 · q 退出"))
		}
	}
	if m.busy {
		b.WriteString("\n处理中…")
		if m.cancelDiscovery != nil {
			b.WriteString(" Esc 取消模型发现")
		}
	}
	if m.status != "" {
		if !compact {
			b.WriteByte('\n')
		}
		b.WriteString("\n" + clean(m.status))
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String()) + "\n"
}
