# coy · crush-on-you

用 Go + Bubble Tea 管理 **全局 `crushrc` 中的自定义 providers 和 models**。

提供商和模型增删改、表单校验、变更预览、保存前备份、原子替换及 dry-run。不管理 Crush 内置目录，不访问项目 `.crush/`，不执行配置脚本或调用模型 API。

## 运行

需要 Go 1.27.1 或更新版本：

```sh
go run ./cmd/coy
go run ./cmd/coy --dry-run
go run ./cmd/coy --check
```

`--dry-run` 打开同样的交互界面，允许编辑和预览，但所有保存操作都不写配置、不创建备份或目录。退出会丢弃草稿。

`--check` 是不启动 TUI 的只读语法与可编辑性检查；发现不支持的动态配置时非零退出。它不执行 Crush，也不验证服务连通性。

```sh
make build
./bin/coy --version
./bin/coy --config /absolute/path/to/crushrc --dry-run
```

默认全局路径依次由 `$CRUSH_GLOBAL_CONFIG/crushrc`、`$XDG_CONFIG_HOME/crush/crushrc`、`~/.config/crush/crushrc` 决定。`CRUSH_GLOBAL_CONFIG` 是**目录**。`--config` 显式指定文件，便于独立配置和临时目录测试；不会自动查找项目配置。

## 按键

| 按键 | 操作 |
| --- | --- |
| `n` | 新增自定义 provider |
| `m` | 给自定义 provider 新增 model |
| `↑` / `↓`、`j` / `k` | 选择条目 |
| `e` / `Enter` | 编辑条目 |
| `d` | 删除草稿中的条目，需 `y` 确认 |
| `p` | 预览变更，不写入 |
| `s` / `Ctrl+S` | 保存，写入前需 `y` 确认；dry-run 下仅预览 |
| `q` / `Ctrl+C` | 退出；未保存时提示确认 |
| 表单内 `Tab` / `Shift+Tab` | 切换字段 |
| 表单内 `Enter` / `Ctrl+S` | 更新内存中的草稿 |
| `Esc` | 取消表单或返回列表 |

建议为自定义提供商使用独立 ID，例如 `my-openai`；`openai` 是内置 ID，不能在这里管理。API 类型可选 `openai`、`openai-compat`、`anthropic` 等；第三方 OpenAI 兼容服务通常使用 `openai-compat`。

API key 输入 `env:MY_API_KEY` 会生成 `--api-key "${MY_API_KEY}"`。其他新输入按 Shell 字面值转义，`$(...)` 不会意外变成命令；已有环境变量表达式不改动时保持原样。API key 始终遮蔽，预览也会隐藏 URL、名称等可能包含凭据的自由文本。

## 配置与安全边界

- 直接生成标准 `provider add` / `model add` 命令，不维护平行数据库或自定义配置格式。
- 可编辑顶层、单次定义、使用 `--flag value` 参数的条目。支持多行续行和引用；已有额外参数、内置条目、注释及无关设置保留原文。
- provider 表单支持名称、类型、地址、API key、禁用、自动发现和固定费率；model 表单支持名称、上下文、输出 token 上限、推理/图片能力、价格和推理强度。其他参数原样保留，当前不提供表单编辑。
- `source`、控制流、命令替换、重复定义、重定向及无法确定语义的脚本明确设为只读。不会运行 Shell 来“发现”配置，不自动迁移旧 JSON。
- 只列出显式自定义 provider 及其模型；内置 provider 上的模型、自动发现结果和运行时状态不纳入管理。内置 ID 保护名单依据 2026-10-25 的 [Catwalk provider 定义](https://github.com/charmbracelet/catwalk/blob/main/pkg/catwalk/provider.go)及 Crush Hyper；后续新增的内置 ID 需要更新名单。
- ID 创建后不可重命名。删除 provider 前先删除其模型；被 `model large/small` 引用的条目不能删除，需先在 Crush 中调整选择。
- 已有文件每次实际变更前创建同目录 `crushrc.backup-*`，保留完整原始内容并限制为仅文件所有者可访问。备份失败停止写入。无变化不生成备份；首次创建时明确提示无原件可备份。
- 保存会检测外部变更，以独占锁协调多个 coy 实例，并使用同目录临时文件、同步文件内容及原子重命名。符号链接和特殊文件不予覆盖。与不遵守锁的其他编辑器仍存在最后检查到重命名间的短暂竞争窗口，编辑时请避免并行保存同一文件。
- 备份路径在保存后显示。恢复前退出编辑器并核对备份，再手动将所选备份复制回 `crushrc`。不自动删除历史备份。

配置命令契约依据 [Crush 配置文档](https://github.com/charmbracelet/crush/tree/main/docs/config)（2026-10-25 上游 main）。目标 Crush 需支持 `crushrc`；只支持旧 JSON 的旧版不适用。

## 开发与打包

```sh
make check
go test -race -timeout 60s ./...
make dist VERSION=v0.1.0
```

测试均使用临时文件，不触碰真实全局配置。覆盖原文保留、引号转义、内置边界、引用、备份、dry-run、外部冲突、凭据遮蔽和 TUI 操作。

`dist/` 包含 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 的原始二进制和 `SHA256SUMS`，不是压缩包。下载到同一目录后运行 `sha256sum --check --ignore-missing SHA256SUMS`；Linux/macOS 的二进制需添加执行权限。

GitHub Actions 在 push/PR 时执行验证；`v*` 标签通过验证后构建并发布 Release。开发分支推送不会创建 Release。
