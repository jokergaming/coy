package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"coy/internal/config"
	"coy/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("coy", flag.ContinueOnError)
	flags.SetOutput(out)
	path := flags.String("config", "", "全局 crushrc 路径（默认按 Crush 环境变量解析）")
	dry := flags.Bool("dry-run", false, "预览模式：所有保存均不落盘、不创建备份")
	check := flags.Bool("check", false, "检查配置语法和可编辑性，不启动 TUI 或写入")
	showVersion := flags.Bool("version", false, "显示版本")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不接受位置参数；使用 --help 查看用法")
	}
	if *showVersion {
		fmt.Fprintf(out, "coy %s (crush-on-you)\n", version)
		return nil
	}
	if *path == "" {
		p, err := config.GlobalPath()
		if err != nil {
			return err
		}
		*path = p
	}
	store, doc, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *check {
		fmt.Fprintf(out, "配置：%s\n自定义条目：%d\n", store.Path, len(doc.Entries()))
		if doc.ReadOnly != "" {
			return fmt.Errorf("只读：%s", doc.ReadOnly)
		}
		fmt.Fprintln(out, "配置检查通过；未写入任何文件。")
		return nil
	}
	_, err = tea.NewProgram(tui.New(store, doc, *dry), tea.WithAltScreen()).Run()
	return err
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "coy:", err)
		os.Exit(1)
	}
}
