// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/release"
	"golang.org/x/mod/semver"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：sniffy-release <manifest|stage|promote|version|wintun|portable> [参数]")
	}
	switch args[0] {
	case "manifest":
		return generateManifest(ctx, args[1:], stdout, stderr)
	case "stage", "promote":
		return publish(ctx, args[0], args[1:], stdout, stderr)
	case "wintun":
		return writeWintun(ctx, args[1:], stdout, stderr)
	case "portable":
		return writePortable(ctx, args[1:], stdout, stderr)
	case "version":
		if len(args) != 2 {
			return fmt.Errorf("用法：sniffy-release version <版本号>")
		}
		version, err := release.ParseVersion(args[1])
		if err != nil {
			return err
		}
		prerelease := semver.Prerelease("v"+version) != ""
		_, err = fmt.Fprintf(stdout, "version=%s\nprerelease=%t\n", version, prerelease)
		return err
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, "用法：sniffy-release <manifest|stage|promote|version|wintun|portable> [参数]；除 version 外均可用 --help 查看参数")
		return nil
	default:
		return fmt.Errorf("未知操作：%s", args[0])
	}
}

func generateManifest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options release.ManifestOptions
	flags.StringVar(&options.Version, "version", "", "完整语义版本；默认读取最近的 Git 标签")
	flags.StringVar(&options.Directory, "dir", "dist", "制品目录")
	flags.StringVar(&options.BaseURL, "base", "https://cdn.gosniffy.com/releases", "版本目录的 HTTPS 根地址")
	flags.StringVar(&options.NotesURL, "notes", "", "更新说明地址")
	flags.StringVar(&options.Notes, "notes-content", "", "Markdown 更新说明正文")
	flags.StringVar(&options.PublishedAt, "published", "", "发布日期或 RFC3339 时间")
	flags.BoolVar(&options.RequireComplete, "require-complete", false, "要求全部发布目标的制品齐全")
	output := flags.String("out", "site/src/data/release.json", "清单路径")
	checksums := flags.String("checksums", "", "SHA256SUMS 路径")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if options.Version == "" {
		data, err := exec.CommandContext(ctx, "git", "describe", "--tags", "--abbrev=0").Output()
		if err != nil {
			return fmt.Errorf("读取 Git 版本标签：%w", err)
		}
		options.Version = strings.TrimSpace(string(data))
	}
	manifest, err := release.GenerateManifest(options)
	if err != nil {
		return err
	}
	data, err := release.EncodeManifest(manifest)
	if err != nil {
		return err
	}
	if *checksums != "" {
		if err := release.WriteFile(*checksums, release.Checksums(manifest)); err != nil {
			return err
		}
	}
	if err := release.WriteFile(*output, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "已生成 %s：%s，%d 个制品\n", *output, manifest.Version, len(manifest.Assets))
	return err
}

func publish(ctx context.Context, operation string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("dir", "release", "包含 release.json 的制品目录")
	timeout := flags.Duration("timeout", 30*time.Minute, "本次操作的总超时")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("发布超时必须大于 0")
	}
	data, err := os.ReadFile(filepath.Join(*directory, "release.json"))
	if err != nil {
		return err
	}
	manifest, err := release.ParseManifest(data)
	if err != nil {
		return err
	}
	store, err := release.NewR2Store(release.R2Config{
		Account:   os.Getenv("R2_ACCOUNT_ID"),
		Bucket:    os.Getenv("R2_BUCKET"),
		AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	publisher := release.Publisher{Store: store}
	if operation == "stage" {
		if err := publisher.Stage(ctx, *directory, manifest); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "R2 版本 %s 上传校验完成，共 %d 个制品\n", manifest.Version, len(manifest.Assets))
		return err
	}
	promoted, err := publisher.Promote(ctx, manifest)
	if err != nil {
		return err
	}
	// promoted 行供工作流写入 GITHUB_OUTPUT，仅在最新清单确实更新时重建官网。
	_, err = fmt.Fprintf(stdout, "R2 版本 %s 下载清单处理完成\npromoted=%t\n", manifest.Version, promoted)
	return err
}

var downloadClient = &http.Client{Timeout: 2 * time.Minute}

func writeWintun(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("wintun", flag.ContinueOnError)
	flags.SetOutput(stderr)
	arch := flags.String("arch", "", "目标架构：amd64 或 arm64")
	output := flags.String("out", "", "wintun.dll 的输出路径")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *arch == "" || *output == "" {
		return fmt.Errorf("wintun 需要 --arch 与 --out")
	}
	data, err := release.WintunDLL(ctx, downloadClient, *arch)
	if err != nil {
		return err
	}
	if err := writeArtifact(*output, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "已写入 wintun %s（%s）：%s\n", release.WintunVersion, *arch, *output)
	return err
}

func writePortable(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("portable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	arch := flags.String("arch", "", "目标架构：amd64 或 arm64")
	binary := flags.String("binary", "", "已构建的 Windows 可执行文件")
	name := flags.String("name", "", "可执行文件在 zip 里的文件名")
	output := flags.String("out", "", "zip 的输出路径")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *arch == "" || *binary == "" || *name == "" || *output == "" {
		return fmt.Errorf("portable 需要 --arch、--binary、--name 与 --out")
	}
	if sameFile(*binary, *output) {
		return fmt.Errorf("--out 不能覆盖输入的可执行文件")
	}
	data, err := release.WindowsPortable(ctx, downloadClient, *arch, *binary, *name)
	if err != nil {
		return err
	}
	if err := writeArtifact(*output, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "已生成免安装包：%s\n", *output)
	return err
}

// parseFlags 解析子命令参数，不接受多余的位置参数。
func parseFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的参数：%s", strings.Join(flags.Args(), " "))
	}
	return nil
}

func sameFile(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

// writeArtifact 原子写入打包产物；临时文件默认仅属主可读，产物按普通文件放开读权限。
func writeArtifact(path string, data []byte) error {
	if err := release.WriteFile(path, data); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}
