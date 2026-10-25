package config

import (
	"fmt"
	"slices"
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
	Kind       string
	ID         string
	start, end int
	deleteEnd  int
	options    map[string]value
	changes    map[string]string
	deleted    bool
	fresh      bool
}

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

func (e Entry) Dynamic(name string) bool { return e.options[name].dynamic }

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
	return entries, refs, readOnly, nil
}
