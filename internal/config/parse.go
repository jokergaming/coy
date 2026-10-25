package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type value struct {
	text       string
	raw        string
	dynamic    bool
	start, end int
}

type Entry struct {
	Kind                 string
	ID                   string
	start, end           int
	deleteEnd            int
	options              map[string]value
	changes              map[string]string
	deleted              bool
	fresh                bool
	group                string
	originalGroup        string
	groupStart, groupEnd int
}

func (e Entry) Group() string { return e.group }

func (e Entry) Value(name string) string {
	if v, ok := e.changes[name]; ok {
		return v
	}
	v := e.options[name]
	if v.dynamic {
		return v.raw
	}
	return v.text
}

func (e Entry) Dynamic(name string) bool {
	if _, changed := e.changes[name]; changed {
		return false
	}
	return e.options[name].dynamic
}

// RequestValue resolves only a single environment variable reference. Shell
// evaluation, defaults, concatenation and commands are deliberately unsupported.
func (e Entry) RequestValue(name string) (string, error) {
	v := e.Value(name)
	if !e.Dynamic(name) {
		if variable, ok := strings.CutPrefix(v, "env:"); ok && name == "api-key" {
			resolved := os.Getenv(variable)
			if !envName.MatchString(variable) || resolved == "" {
				return "", errors.New("API Key 引用的环境变量未设置或为空")
			}
			return resolved, nil
		}
		return v, nil
	}
	if strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		v = v[1 : len(v)-1]
	}
	variable, ok := strings.CutPrefix(v, "$")
	if strings.HasPrefix(variable, "{") && strings.HasSuffix(variable, "}") {
		variable = variable[1 : len(variable)-1]
	}
	if !ok || !envName.MatchString(variable) {
		return "", errors.New("模型发现只支持字面值或单个环境变量引用，不执行动态 Shell 表达式")
	}
	resolved := os.Getenv(variable)
	if resolved == "" {
		return "", errors.New("模型发现需要的环境变量未设置或为空")
	}
	return resolved, nil
}

func wordValue(w *syntax.Word, src string) value {
	v := value{raw: src[w.Pos().Offset():w.End().Offset()], start: int(w.Pos().Offset()), end: int(w.End().Offset())}
	var read func([]syntax.WordPart, bool)
	read = func(parts []syntax.WordPart, quoted bool) {
		for _, part := range parts {
			switch p := part.(type) {
			case *syntax.Lit:
				v.text += unescape(p.Value, quoted)
			case *syntax.SglQuoted:
				if p.Dollar {
					v.dynamic = true
				}
				v.text += p.Value
			case *syntax.DblQuoted:
				read(p.Parts, true)
			default:
				v.dynamic = true
			}
		}
	}
	read(w.Parts, false)
	return v
}

func unescape(s string, quoted bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (!quoted || strings.ContainsRune("$`\"\\\n", rune(s[i+1]))) {
			i++
			if s[i] == '\n' {
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parse(src string) ([]*Entry, map[string]bool, string, error) {
	f, err := syntax.NewParser(syntax.KeepComments(true)).Parse(strings.NewReader(src), "crushrc")
	if err != nil {
		return nil, nil, "", fmt.Errorf("crushrc Bash 语法无效；请在本地检查配置（错误详情已隐藏，避免泄漏凭据）")
	}
	var entries []*Entry
	refs := map[string]bool{}
	readOnly := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			readOnly = "配置含命令替换或进程替换，可能动态修改配置；当前仅可查看"
		}
		return true
	})
	for _, stmt := range f.Stmts {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || stmt.Background || stmt.Negated || stmt.Coprocess || len(stmt.Redirs) > 0 || len(call.Assigns) > 0 {
			readOnly = "配置含控制流、赋值或重定向，无法安全确定自定义条目；当前仅可查看"
			continue
		}
		args := make([]value, len(call.Args))
		for i, w := range call.Args {
			args[i] = wordValue(w, src)
		}
		if len(args) == 0 {
			continue
		}
		cmd := args[0].text
		if args[0].dynamic || !slices.Contains([]string{"provider", "model", "mcp", "lsp", "hook", "permissions", "option"}, cmd) {
			readOnly = "配置含 source 或其他任意 Shell 命令；不执行脚本，当前仅可查看"
			continue
		}
		if cmd != "provider" && cmd != "model" {
			continue
		}
		if len(args) < 3 || args[1].dynamic || args[2].dynamic {
			readOnly = "provider/model 命令含动态标识符或缺少参数；当前仅可查看"
			continue
		}
		if cmd == "model" && (args[1].text == "large" || args[1].text == "small") {
			refs[args[2].text] = true
			continue
		}
		if args[1].text != "add" {
			readOnly = "配置含 provider/model remove 等顺序操作；当前仅可查看"
			continue
		}
		e := &Entry{Kind: cmd, ID: args[2].text, start: int(call.Pos().Offset()), end: int(call.End().Offset()), deleteEnd: int(stmt.End().Offset()), options: map[string]value{}, changes: map[string]string{}}
		p, m, hasSlash := strings.Cut(e.ID, "/")
		if (cmd == "provider" && !providerID.MatchString(e.ID)) || (cmd == "model" && (!hasSlash || !providerID.MatchString(p) || !modelID.MatchString(m))) {
			readOnly = "配置含不支持的条目标识符；当前仅可查看"
		}
		for i := 3; i < len(args); {
			name, flag := strings.CutPrefix(args[i].text, "--")
			arity := 1
			if name == "extra-header" {
				arity = 2
			}
			if args[i].dynamic || !flag || strings.Contains(name, "=") || i+arity >= len(args) {
				readOnly = "配置含暂不支持的参数语法（需 --flag value）；当前仅可查看"
				break
			}
			if _, exists := e.options[name]; exists && name != "extra-header" {
				readOnly = "同一命令含重复参数；当前仅可查看"
			}
			e.options[name] = args[i+arity]
			// Include the flag in the span so clearing a field removes both words.
			v := e.options[name]
			v.start = args[i].start
			e.options[name] = v
			i += 1 + arity
		}
		entries = append(entries, e)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		key := e.Kind + "/" + e.ID
		if seen[key] {
			readOnly = "同一条目存在多次定义；当前仅可查看"
		}
		seen[key] = true
	}
	// Only comments are metadata; matching text inside quoted values is inert.
	groups := map[string]bool{}
	syntax.Walk(f, func(n syntax.Node) bool {
		c, ok := n.(*syntax.Comment)
		if !ok {
			return true
		}
		text, ok := strings.CutPrefix(c.Text, " coy:model-group ")
		if !ok {
			return true
		}
		id, raw, _ := strings.Cut(text, " ")
		name, err := strconv.Unquote(raw)
		start := int(c.Pos().Offset())
		line := src[:start]
		if _, tail, found := strings.CutLast(line, "\n"); found {
			line = tail
		}
		if err != nil || validateID("model", id) != nil || validGroup(name) != nil || name == "" || groups[id] || strings.TrimSpace(line) != "" {
			readOnly = "coy 分组注释无效或重复；当前仅可查看"
			return true
		}
		groups[id] = true
		found := false
		for _, e := range entries {
			if start >= e.start && start < e.deleteEnd {
				readOnly = "coy 分组注释必须独占一行，且位于配置命令之外；当前仅可查看"
				return true
			}
			if e.Kind == "model" && e.ID == id {
				found = true
				e.group, e.originalGroup = name, name
				e.groupStart, e.groupEnd = start, int(c.End().Offset())
			}
		}
		if !found {
			readOnly = "coy 分组注释引用了不存在的模型；当前仅可查看"
		}
		return true
	})
	return entries, refs, readOnly, nil
}
