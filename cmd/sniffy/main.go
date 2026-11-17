// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mintfog/sniffy/internal/api"
	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/notices"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/tunhost"
	"github.com/mintfog/sniffy/internal/version"
)

var (
	listenAddr  = flag.String("addr", "", "代理监听地址（仅支持通配地址：空值、*、0.0.0.0 或 ::）")
	listenPort  = flag.Int("port", 8080, "代理监听端口")
	apiAddr     = flag.String("api-addr", "127.0.0.1", "管理 API 监听地址")
	apiPort     = flag.Int("api-port", 8888, "管理 API(HTTP+WebSocket)端口")
	apiTLSCert  = flag.String("api-tls-cert", "", "管理 API TLS 证书路径(与 -api-tls-key 同时提供则启用 HTTPS)")
	apiTLSKey   = flag.String("api-tls-key", "", "管理 API TLS 私钥路径")
	apiInsecure = flag.Bool("allow-insecure-api", false, "允许管理 API 在非回环地址上以明文 HTTP 监听(仅当已由 TLS 反代/VPN 兜底时使用)")
	tunMode     = flag.Bool("tun", false, "开启虚拟网卡,不走代理的程序的 TCP 流量也进入抓包(没有 root / 管理员权限时提示另行以管理员身份运行 helper)")
	verbose     = flag.Bool("v", false, "启用详细日志输出")
	showVersion = flag.Bool("version", false, "打印版本号后退出")
	showNotices = flag.Bool("licenses", false, "打印项目与第三方许可证声明后退出")
	_           = flag.String("config", "", "配置文件路径(预留)")
)

func main() {
	tunhost.RunIfRequested()

	flag.Parse()
	if *showVersion {
		fmt.Println(version.Get())
		return
	}
	if *showNotices {
		fmt.Print(notices.Text)
		return
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("启动 sniffy(headless 服务器模式)...")

	// 创建并校验配置:默认值 < 持久化配置(config.json) < 命令行显式参数。
	config := DefaultConfig()
	config.Address, config.Port = app.ResolveListen(config.Address, config.Port)
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			config.Address = *listenAddr
		case "port":
			config.Port = *listenPort
		}
	})
	config.EnableLogging = *verbose
	if err := config.Validate(); err != nil {
		app.Fatalf("配置无效: %v", err)
	}

	// 装配核心组件(引擎 + 服务 + 管道 + 插件)。
	application, err := app.Build(config, *verbose)
	if err != nil {
		app.Fatalf("初始化失败: %v", err)
	}

	// 启动抓包引擎。
	if err := application.Start(); err != nil {
		if application.Service.ProxyStatus().ErrorCode == service.ProxyErrorAddressInUse {
			app.Fatalf("代理端口 %d 已被占用，请释放该端口或使用 -port 指定其他端口: %v", config.Port, err)
		}
		app.Fatalf("启动引擎失败: %v", err)
	}

	if *tunMode {
		if err := startTun(application); err != nil {
			app.Fatalf("开启虚拟网卡失败: %v", err)
		}
	}

	application.StartUpdateCheck()

	// 配置并启动管理 API。
	apiToken := app.LoadAPIToken()
	apiLoopback := app.IsLoopbackHost(*apiAddr)
	if apiToken == "" {
		token, path, err := app.EnsureAPIToken()
		if err != nil {
			app.Fatalf("生成管理 API token 失败: %v", err)
		}
		apiToken = token
		log.Printf("已生成管理 API token 并保存到 %s;请求需携带 Authorization: Bearer <token>,WebSocket 可用 ?token=", path)
	} else {
		finalTok, rotated, err := app.EnsureTokenSecrecy()
		if err != nil {
			app.Fatalf("无法安全处理 API token 文件,拒绝启动: %v", err)
		}
		if finalTok != "" {
			apiToken = finalTok
		}
		if rotated {
			log.Printf("检测到 API token 文件权限过宽(旧值可能已泄漏),已轮换为新凭证,旧凭证即时失效")
		}
	}
	log.Println("管理 API 已启用 Bearer token 认证")

	if (*apiTLSCert == "") != (*apiTLSKey == "") {
		app.Fatalf("管理 API TLS 需同时提供 -api-tls-cert 与 -api-tls-key")
	}
	apiTLS := *apiTLSCert != "" && *apiTLSKey != ""
	if !apiTLS && !apiLoopback {
		if !*apiInsecure {
			app.Fatalf("管理 API 绑定非回环地址 %s 且未启用 TLS:token 与抓包数据会明文传输,拒绝启动。"+
				"请用 -api-tls-cert/-api-tls-key 启用 HTTPS,或在已由 TLS 反代/VPN 兜底时加 -allow-insecure-api 显式放行", *apiAddr)
		}
		log.Printf("警告: 管理 API 明文监听非回环地址 %s(-allow-insecure-api);请确认已由 TLS 反代/VPN 兜底", *apiAddr)
	}

	apiListen := net.JoinHostPort(*apiAddr, strconv.Itoa(*apiPort))
	apiServer := api.New(application.Service, application.Pipeline, application.Plugins, application, apiListen, apiToken)
	apiServer.SetRequestSender(application)
	apiServer.SetWebSocketComposer(application)
	apiScheme := "http"
	if apiTLS {
		apiServer.SetTLS(*apiTLSCert, *apiTLSKey)
		apiScheme = "https"
	}
	if err := apiServer.Listen(); err != nil {
		app.Fatalf("管理 API 监听 %s://%s 失败: %v", apiScheme, apiListen, err)
	}
	log.Printf("管理 API 监听于 %s://%s", apiScheme, apiListen)
	go func() {
		if err := apiServer.Serve(); err != nil && err.Error() != "http: Server closed" {
			log.Printf("管理 API 退出: %v", err)
		}
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	log.Printf("sniffy 代理运行于 %s", config.GetListenAddress())
	log.Println("按 Ctrl+C 停止...")
	<-signalChan

	log.Println("收到关闭信号,正在优雅关闭...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = apiServer.Stop(shutdownCtx)
		if err := application.Stop(); err != nil {
			log.Printf("停止引擎出错: %v", err)
		}
		log.Println("所有服务已停止")
	}()

	select {
	case <-done:
		log.Println("关闭完成")
	case <-shutdownCtx.Done():
		log.Println("关闭超时,强制退出")
	}
	app.FlushLogs() // os.Exit 不走 defer,显式把缓冲日志落盘
	os.Exit(0)
}

// startTun 有权限时在本进程里开启虚拟网卡，否则等用户以 root / 管理员身份运行 helper，
// Ctrl+C 放弃等待。
func startTun(application *app.App) error {
	err := application.StartTun()
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return application.StartTunHelper(ctx, func(cmd []string) error {
		if runtime.GOOS == "windows" {
			log.Printf("当前进程没有管理员权限，请在以管理员身份打开的 PowerShell 中运行以下命令开启虚拟网卡:\n  %s", quoteCommand(runtime.GOOS, cmd))
		} else {
			log.Printf("当前进程没有 root 权限，请另开终端以 root 身份运行以下命令开启虚拟网卡（如在前面加 sudo）:\n  %s", quoteCommand(runtime.GOOS, cmd))
		}
		return nil
	})
}

// quoteCommand 把命令行拼成可直接粘贴到终端运行的形式：Windows 上是 PowerShell，其它平台是
// POSIX shell。需要引号的参数一律用单引号，其中的字符不做任何展开。
func quoteCommand(goos string, cmd []string) string {
	quoted := make([]string, len(cmd))
	for i, arg := range cmd {
		quoted[i] = quoteArg(goos, arg)
	}
	if goos == "windows" {
		// PowerShell 把以引号开头的一行当作字符串表达式，要用 & 才会执行它。
		return "& " + strings.Join(quoted, " ")
	}
	return strings.Join(quoted, " ")
}

func quoteArg(goos, arg string) string {
	// 不加引号也不会被 shell 另作解释的字符。PowerShell 里 @ 开头是展开、逗号构成数组；
	// POSIX shell 里反斜杠是转义。
	plain := "-_./:=+%,@"
	if goos == "windows" {
		plain = "-_./:\\"
	}
	safe := arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(plain, r))
	}) < 0
	if safe {
		return arg
	}
	if goos == "windows" {
		return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
