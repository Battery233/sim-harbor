package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Battery233/sim/internal/maintenance"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("请使用 inspect / backup / restore，恢复前先校验并预览备份。")
	}
	flags := flag.NewFlagSet("sim-data", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backup := flags.String("backup", "", "backup archive")
	target := flags.String("to", "", "new restore directory")
	confirm := flags.String("confirm-archive", "", "inspected archive SHA-256")
	directory := flags.String("data-directory", "", "offline source directory")
	output := flags.String("output", "", "new archive file")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("维护参数无效，请查看数据维护手册。")
	}
	allowed := map[string]bool{}
	switch args[0] {
	case "inspect":
		allowed["backup"] = true
	case "restore":
		allowed["backup"], allowed["to"], allowed["confirm-archive"] = true, true, true
	case "backup":
		allowed["data-directory"], allowed["output"] = true, true
	default:
		return errors.New("维护操作无效：使用 inspect / backup / restore。")
	}
	invalid := false
	flags.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			invalid = true
		}
	})
	if invalid {
		return errors.New("本操作包含不适用的参数。")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if args[0] == "restore" {
		if *backup == "" || *target == "" || *confirm == "" {
			return errors.New("恢复需指定 -backup、全新目录 -to 和 inspect 所得 -confirm-archive。")
		}
		result, err := maintenance.Restore(ctx, *backup, *target, *confirm)
		if err != nil {
			return fmt.Errorf("安全恢复未完成（%s）；原数据库未覆盖。若新目录留下未完成标记，请保留并另选全新目录重试。", problem(err))
		}
		return json.NewEncoder(out).Encode(result)
	}
	if args[0] == "backup" {
		if *directory == "" || *output == "" {
			return errors.New("停服备份需指定 -data-directory 和不存在的 -output 文件。")
		}
		if err := maintenance.BackupDirectory(ctx, *directory, *output); err != nil {
			return fmt.Errorf("停服备份未完成（%s）；正在运行时请使用网页备份。", problem(err))
		}
		return json.NewEncoder(out).Encode(map[string]bool{"backup_created": true})
	}
	if *backup == "" {
		return errors.New("inspect 需指定 -backup 文件。")
	}
	preview, err := maintenance.Inspect(ctx, *backup)
	if err != nil {
		return errors.New("备份校验失败：请核对文件完整性、格式和数据库版本；未恢复任何数据。")
	}
	return json.NewEncoder(out).Encode(preview)
}

func problem(err error) string {
	switch {
	case errors.Is(err, maintenance.ErrConfirmation):
		return "备份摘要与确认不符"
	case errors.Is(err, maintenance.ErrBusy):
		return "数据目录正在使用"
	case errors.Is(err, maintenance.ErrSchema):
		return "数据库版本不支持"
	case errors.Is(err, maintenance.ErrInvalidBackup):
		return "备份校验未通过"
	case errors.Is(err, os.ErrExist):
		return "目标已存在"
	default:
		return "文件、目录或数据库检查失败"
	}
}
