// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
		return fmt.Errorf("用法：sniffy-release <manifest|stage|promote|version> [参数]")
	}
	switch args[0] {
	case "manifest":
		return generateManifest(ctx, args[1:], stdout, stderr)
	case "stage", "promote":
		return publish(ctx, args[0], args[1:], stdout, stderr)
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
		fmt.Fprintln(stdout, "用法：sniffy-release <manifest|stage|promote|version> [参数]；manifest、stage、promote 可用 --help 查看参数")
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
	flags.StringVar(&options.PublishedAt, "published", "", "发布日期或 RFC3339 时间")
	flags.BoolVar(&options.RequireComplete, "require-complete", false, "要求全部发布目标的制品齐全")
	output := flags.String("out", "site/src/data/release.json", "清单路径")
	checksums := flags.String("checksums", "", "SHA256SUMS 路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的参数：%s", strings.Join(flags.Args(), " "))
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的参数：%s", strings.Join(flags.Args(), " "))
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
	_, err = fmt.Fprintf(stdout, "R2 版本 %s 下载清单处理完成，更新最新版：%t\n", manifest.Version, promoted)
	return err
}
