package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Document struct {
	source   string
	entries  []*Entry
	refs     map[string]bool
	ReadOnly string
}

func Parse(source string) (*Document, error) {
	entries, refs, ro, err := parse(source)
	if err != nil {
		return nil, err
	}
	return &Document{source: source, entries: entries, refs: refs, ReadOnly: ro}, nil
}

func (d *Document) customProvider(id string) bool {
	if Builtin(id) {
		return false
	}
	for _, e := range d.entries {
		if e.Kind == "provider" && e.ID == id && !e.deleted {
			return true
		}
	}
	return false
}

func (d *Document) Entries() []*Entry {
	var result []*Entry
	for _, e := range d.entries {
		p := e.ID
		if e.Kind == "model" {
			p, _, _ = strings.Cut(e.ID, "/")
		}
		if !e.deleted && d.customProvider(p) {
			result = append(result, e)
		}
	}
	return result
}

func (d *Document) Providers() []string {
	var ids []string
	for _, e := range d.Entries() {
		if e.Kind == "provider" {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// AddModels imports a complete catalogue transactionally. Existing custom
// models win; invalid IDs leave the entire draft unchanged.
func (d *Document) AddModels(provider string, models map[string]string) (int, error) {
	if err := d.editable(nil); err != nil {
		return 0, err
	}
	if !d.customProvider(provider) {
		return 0, errors.New("请先创建对应的自定义提供商")
	}
	existing := map[string]bool{}
	for _, e := range d.Entries() {
		if e.Kind == "model" {
			existing[e.ID] = true
		}
	}
	next := *d
	next.entries = slices.Clone(d.entries)
	count := 0
	for _, id := range slices.Sorted(maps.Keys(models)) {
		fullID := provider + "/" + id
		if existing[fullID] {
			continue
		}
		if err := next.Put(nil, "model", fullID, map[string]string{"name": models[id]}); err != nil {
			return 0, fmt.Errorf("发现的模型无法安全导入：%w", err)
		}
		count++
	}
	d.entries = next.entries
	return count, nil
}

func (d *Document) editable(e *Entry) error {
	if d.ReadOnly != "" {
		return errors.New(d.ReadOnly)
	}
	if e != nil && !slices.Contains(d.Entries(), e) {
		return errors.New("条目不属于可管理的自定义配置")
	}
	return nil
}

// withModelName shares the name rule between form edits and repairs at save time.
// Copies keep previews and failed saves from changing the editing draft.
func (e *Entry) withModelName() *Entry {
	if e.Kind != "model" || e.deleted || e.Value("name") != "" {
		return e
	}
	_, name, _ := strings.Cut(e.ID, "/")
	next := *e
	next.changes = map[string]string{}
	maps.Copy(next.changes, e.changes)
	next.changes["name"] = name
	return &next
}

func (d *Document) forSave() *Document {
	if d.ReadOnly != "" {
		return d
	}
	next := *d
	next.entries = make([]*Entry, len(d.entries))
	for i, e := range d.entries {
		next.entries[i] = e.withModelName()
	}
	return &next
}

// Put updates only changed form fields. Unchanged shell expressions stay byte exact.
// IDs are immutable to avoid rewriting references in unrelated configuration.
func (d *Document) Put(e *Entry, kind, id string, fields map[string]string) error {
	if err := d.editable(e); err != nil {
		return err
	}
	if kind != "provider" && kind != "model" {
		return errors.New("未知条目类型")
	}
	if err := validateID(kind, id); err != nil {
		return err
	}
	if e != nil && (e.ID != id || e.Kind != kind) {
		return errors.New("已有条目的 ID 不可重命名；请新增独立条目")
	}
	for _, other := range d.entries {
		if other != e && !other.deleted && other.Kind == kind && other.ID == id {
			return errors.New("该 ID 已存在")
		}
	}
	if kind == "model" {
		p, _, _ := strings.Cut(id, "/")
		if !d.customProvider(p) {
			return errors.New("请先创建对应的自定义提供商")
		}
	}
	for _, f := range Fields(kind) {
		v := fields[f.Name]
		if e != nil && v == e.Value(f.Name) {
			continue
		}
		if err := validateField(f, v); err != nil {
			return err
		}
	}
	if kind == "provider" && (fields["type"] == "" || fields["base-url"] == "") {
		return errors.New("自定义提供商必须填写 API 类型和地址")
	}
	if e == nil {
		e = &Entry{Kind: kind, ID: id, options: map[string]value{}, changes: map[string]string{}, fresh: true}
		d.entries = append(d.entries, e)
	}
	for _, f := range Fields(kind) {
		v := fields[f.Name]
		if v != e.Value(f.Name) {
			e.changes[f.Name] = v
		}
	}
	*e = *e.withModelName()
	return nil
}

func (d *Document) Delete(e *Entry) error {
	return d.DeleteEntries([]*Entry{e})
}

// DeleteEntries validates the entire selection before changing the draft.
func (d *Document) DeleteEntries(entries []*Entry) error {
	for _, e := range entries {
		if e == nil {
			return errors.New("未选择条目")
		}
		if err := d.canDelete(e); err != nil {
			return err
		}
	}
	for _, e := range entries {
		e.deleted = true
	}
	return nil
}

func (d *Document) canDelete(e *Entry) error {
	if err := d.editable(e); err != nil {
		return err
	}
	for ref := range d.refs {
		if ref == e.ID || (e.Kind == "provider" && strings.HasPrefix(ref, e.ID+"/")) {
			return errors.New("该条目被 model large/small 引用，请先在 Crush 中更改选择")
		}
	}
	if e.Kind == "provider" {
		for _, child := range d.entries {
			if !child.deleted && child.Kind == "model" && strings.HasPrefix(child.ID, e.ID+"/") {
				return errors.New("请先删除此提供商的自定义模型")
			}
		}
	}
	return nil
}

func validGroup(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 || name != strings.TrimSpace(name) {
		return errors.New("分组名最多 80 字，不能包含控制字符或首尾空白")
	}
	return nil
}

// GroupModels uses inert comments, leaving Crush's model IDs and flags intact.
// An empty name removes membership; names are scoped to each provider.
func (d *Document) GroupModels(entries []*Entry, name string) error {
	if err := validGroup(name); err != nil {
		return err
	}
	for _, e := range entries {
		if e == nil || e.Kind != "model" {
			return errors.New("只能对自定义模型分组")
		}
		if err := d.editable(e); err != nil {
			return err
		}
	}
	for _, e := range entries {
		e.group = name
	}
	return nil
}

type patch struct {
	start, end int
	text       string
}

func (d *Document) Generate() (string, error) {
	var patches []patch
	var added strings.Builder
	for _, e := range d.entries {
		if e.group != e.originalGroup || e.deleted {
			metadata := ""
			if e.group != "" && !e.deleted {
				metadata = "# coy:model-group " + e.ID + " " + strconv.Quote(e.group)
			}
			if e.groupEnd > 0 {
				patches = append(patches, patch{e.groupStart, e.groupEnd, metadata})
			} else if metadata != "" {
				added.WriteString(metadata + "\n")
			}
		}
		if e.deleted {
			if !e.fresh {
				patches = append(patches, patch{e.start, e.deleteEnd, ""})
			}
			continue
		}
		if e.fresh {
			fmt.Fprintf(&added, "%s add %s", e.Kind, quote(e.ID))
			for _, f := range Fields(e.Kind) {
				if v := e.Value(f.Name); v != "" {
					fmt.Fprintf(&added, " --%s %s", f.Name, encode(f, v))
				}
			}
			added.WriteByte('\n')
			continue
		}
		var extra strings.Builder
		for _, f := range Fields(e.Kind) {
			v, changed := e.changes[f.Name]
			if !changed {
				continue
			}
			replacement := ""
			if v != "" {
				replacement = "--" + f.Name + " " + encode(f, v)
			}
			if old, exists := e.options[f.Name]; exists {
				patches = append(patches, patch{old.start, old.end, replacement})
			} else if replacement != "" {
				extra.WriteString(" " + replacement)
			}
		}
		if extra.Len() > 0 {
			patches = append(patches, patch{e.end, e.end, extra.String()})
		}
	}
	slices.SortFunc(patches, func(a, b patch) int { return cmp.Compare(b.start, a.start) })
	out := d.source
	for _, p := range patches {
		out = out[:p.start] + p.text + out[p.end:]
	}
	if added.Len() > 0 {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += added.String()
	}
	if _, err := Parse(out); err != nil {
		return "", err
	}
	return out, nil
}

func (d *Document) Dirty() bool { s, err := d.Generate(); return err != nil || s != d.source }

// Preview reports field-level changes only; arbitrary source lines can contain
// credentials in any position, so a raw unified diff would not be safe.
func (d *Document) Preview() string {
	var b strings.Builder
	for _, e := range d.entries {
		if e.deleted && e.fresh {
			continue
		}
		if e.deleted {
			fmt.Fprintf(&b, "- %s %s\n", e.Kind, e.ID)
			continue
		}
		if !e.fresh && len(e.changes) == 0 && e.group == e.originalGroup {
			continue
		}
		prefix := "~"
		if e.fresh {
			prefix = "+"
		}
		fmt.Fprintf(&b, "%s %s %s\n", prefix, e.Kind, e.ID)
		if e.group != e.originalGroup {
			state := "已设置（值隐藏）"
			if e.group == "" {
				state = "已移出"
			}
			fmt.Fprintf(&b, "    分组: %s\n", state)
		}
		for _, f := range Fields(e.Kind) {
			if _, changed := e.changes[f.Name]; !changed {
				continue
			}
			// URLs and names may also contain secrets; only typed non-secret values are rendered.
			state := "已设置（值隐藏）"
			if e.Value(f.Name) == "" {
				state = "已清除"
			} else if slices.Contains([]string{"type", "bool", "int", "float", "effort"}, f.Kind) {
				state = e.Value(f.Name)
			}
			fmt.Fprintf(&b, "    --%s: %s\n", f.Name, state)
		}
	}
	if b.Len() == 0 {
		return "无变更。"
	}
	return b.String()
}
