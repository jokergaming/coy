package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Field struct {
	Name  string
	Label string
	Kind  string
}

var ProviderFields = []Field{
	{"name", "名称", "text"}, {"type", "API 类型", "type"},
	{"base-url", "API 地址", "url"}, {"api-key", "API Key（env:变量名 引用环境变量）", "secret"},
	{"disable", "禁用（true/false）", "bool"}, {"discover-models", "自动发现模型（true/false）", "bool"},
	{"flat-rate", "固定费率（true/false）", "bool"},
}

var ModelFields = []Field{
	{"name", "名称", "text"}, {"context-window", "上下文窗口", "int"},
	{"default-max-tokens", "最大输出 tokens", "int"}, {"can-reason", "推理能力（true/false）", "bool"},
	{"supports-images", "图片输入（true/false）", "bool"},
	{"price-input", "输入价格 / 百万 tokens", "float"}, {"price-output", "输出价格 / 百万 tokens", "float"},
	{"price-cache-create", "缓存写入价格", "float"}, {"price-cache-hit", "缓存读取价格", "float"},
	{"reasoning-effort", "推理强度（low/medium/high）", "effort"},
}

func Fields(kind string) []Field {
	if kind == "provider" {
		return ProviderFields
	}
	return ModelFields
}

// Reserved IDs are an offline snapshot of Catwalk provider.go plus Crush Hyper,
// checked 2026-10-25. This protects built-in overrides without loading Crush.
const reservedIDs = "hyper openai anthropic synthetic gemini azure bedrock bedrock-europe vertexai xai zai deepseek zhipu zhipu-coding groq openrouter cerebras venice chutes huggingface aihubmix kimi-coding copilot cortecs vercel minimax minimax-china ionet qiniucloud avian nebius neuralwatt opencode-zen opencode-go alibaba-singapore alibaba-us fireworks baseten moonshot atlascloud coralbricks scaleway"

func Builtin(id string) bool { return slices.Contains(strings.Fields(reservedIDs), id) }

var providerID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var modelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]*$`)
var envName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validateID(kind, id string) error {
	if kind == "provider" {
		if !providerID.MatchString(id) {
			return errors.New("provider ID 只能包含字母、数字、点、下划线和连字符")
		}
		if Builtin(id) {
			return errors.New("该 ID 属于内置提供商，请使用独立的自定义 ID")
		}
		return nil
	}
	p, m, ok := strings.Cut(id, "/")
	if !ok || !providerID.MatchString(p) || !modelID.MatchString(m) {
		return errors.New("model ID 必须为 provider/model，且不能包含空白或 Shell 运算符")
	}
	return nil
}

func validateField(f Field, v string) error {
	if v == "" {
		return nil
	}
	valid := true
	switch f.Kind {
	case "type":
		valid = slices.Contains(strings.Fields("openai openai-compat anthropic openrouter vercel google azure bedrock google-vertex ollama llamacpp omlx lmstudio litellm"), v)
	case "url":
		u, err := url.Parse(v)
		valid = err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
	case "bool":
		valid = v == "true" || v == "false"
	case "int":
		n, err := strconv.ParseInt(v, 10, 64)
		valid = err == nil && n > 0
	case "float":
		n, err := strconv.ParseFloat(v, 64)
		valid = err == nil && n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "effort":
		valid = slices.Contains([]string{"low", "medium", "high"}, v)
	case "secret":
		if name, ok := strings.CutPrefix(v, "env:"); ok {
			valid = envName.MatchString(name)
		}
	}
	if !valid {
		return fmt.Errorf("%s 的值无效", f.Label)
	}
	if strings.ContainsAny(v, "\x00\r\n") {
		return fmt.Errorf("%s 不允许换行或 NUL", f.Label)
	}
	return nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func encode(f Field, value string) string {
	if f.Kind == "secret" {
		if name, ok := strings.CutPrefix(value, "env:"); ok {
			return `"${` + name + `}"`
		}
	}
	return quote(value)
}
