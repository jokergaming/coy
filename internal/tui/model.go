package tui

import (
	"fmt"
	"strings"
	"unicode"

	"coy/internal/config"
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

type Model struct {
	store         *config.Store
	doc           *config.Document
	dryRun        bool
	width, height int
	cursor        int
	mode          string
	status        string
	busy          bool
	editing       *config.Entry
	kind          string
	inputs        []textinput.Model
	labels        []string
	focus         int
	preview       string
	previewOffset int
}

func New(store *config.Store, doc *config.Document, dryRun bool) Model {
	return Model{store: store, doc: doc, dryRun: dryRun, width: 80, height: 24, mode: "list"}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) selected() *config.Entry {
	entries := m.doc.Entries()
	if len(entries) == 0 {
		return nil
	}
	return entries[min(m.cursor, len(entries)-1)]
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
			if selected := m.selected(); selected != nil {
				p := selected.ID
				if selected.Kind == "model" {
					p, _, _ = strings.Cut(p, "/")
				}
				input.SetValue(p + "/")
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
		}
		label := f.Label
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
		for i := range m.inputs {
			m.inputs[i].Width = max(10, min(80, m.width-8))
		}
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
			return m, nil
		}
		key := msg.String()
		if m.mode == "form" {
			return m.updateForm(msg)
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
				if err := m.doc.Delete(m.selected()); err != nil {
					m.status = err.Error()
				} else {
					m.status = "已从编辑草稿删除，尚未写入。"
				}
				m.cursor = min(m.cursor, max(0, len(m.doc.Entries())-1))
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
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(max(0, len(m.doc.Entries())-1), m.cursor+1)
		case "n", "m", "e", "enter", "d":
			if m.doc.ReadOnly != "" {
				m.status = m.doc.ReadOnly
				return m, nil
			}
			switch key {
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
			case "e", "enter":
				if e := m.selected(); e != nil {
					cmd := m.form(e.Kind, e)
					return m, cmd
				}
			case "d":
				if m.selected() != nil {
					m.mode = "confirm-delete"
				}
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
		m.mode = "list"
		m.inputs = nil
		m.status = "已更新编辑草稿；p 预览，s 保存。"
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
	header := "coy · crush-on-you"
	if m.dryRun {
		header += "  [DRY RUN]"
	}
	if m.doc.Dirty() {
		header += "  ● 未保存"
	}
	b.WriteString(accent.Render(header) + "\n" + muted.Render(clean(m.store.Path)) + "\n\n")
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
		b.WriteString("删除当前条目的编辑草稿？y 确认 / n 取消")
	case "confirm-quit":
		b.WriteString("存在未保存的修改。丢弃并退出？y 确认 / n 取消")
	case "confirm-save":
		b.WriteString("备份现有全局 crushrc 后写入修改？y 确认 / n 取消")
	default:
		if m.doc.ReadOnly != "" {
			b.WriteString("只读：" + m.doc.ReadOnly + "\n")
		}
		entries := m.doc.Entries()
		rows := max(1, m.height-11)
		start := max(0, m.cursor-rows+1)
		if len(entries) == 0 {
			b.WriteString("暂无可管理的自定义条目。按 n 添加 provider。\n")
		}
		for i := start; i < min(len(entries), start+rows); i++ {
			e := entries[i]
			line := fmt.Sprintf("  %-8s %s", e.Kind, clean(e.ID))
			if i == m.cursor {
				line = accent.Render("›" + line[1:])
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + muted.Render("n 新增 provider · m 新增 model · e 编辑 · d 删除\np 预览 · s 保存 · ↑↓ 选择 · q 退出"))
	}
	if m.busy {
		b.WriteString("\n处理中…")
	}
	if m.status != "" {
		b.WriteString("\n\n" + clean(m.status))
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String()) + "\n"
}
