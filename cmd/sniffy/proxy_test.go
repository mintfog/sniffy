// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestProxyRequest 测试通过代理请求百度
func TestProxyRequest(t *testing.T) {
	// 设置代理地址
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("解析代理URL失败: %v", err)
	}

	// 创建HTTP客户端，配置代理
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 30 * time.Second,
	}

	// 请求百度
	fmt.Println("通过代理 127.0.0.1:8080 请求百度...")
	resp, err := client.Get("https://www.baidu.com")
	if err != nil {
		t.Fatalf("请求百度失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应内容
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应内容失败: %v", err)
	}

	// 输出响应信息
	fmt.Printf("响应状态码: %s\n", resp.Status)
	fmt.Printf("响应头信息:\n")
	for key, values := range resp.Header {
		for _, value := range values {
			fmt.Printf("  %s: %s\n", key, value)
		}
	}
	fmt.Printf("\n响应内容长度: %d 字节\n", len(body))
	fmt.Printf("\n响应内容:\n%s\n", string(body))

	// 验证响应
	if resp.StatusCode != http.StatusOK {
		t.Errorf("期望状态码 200，实际得到 %d", resp.StatusCode)
	}

	if len(body) == 0 {
		t.Error("响应内容为空")
	}

	fmt.Println("代理测试完成！")
}

// TestDirectRequest 测试直接请求百度（不使用代理）
func TestDirectRequest(t *testing.T) {
	fmt.Println("正在直接请求百度进行对比...")

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get("https://www.baidu.com")
	if err != nil {
		t.Fatalf("直接请求百度失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应内容失败: %v", err)
	}

	fmt.Printf("直接请求 - 响应状态码: %s\n", resp.Status)
	fmt.Printf("直接请求 - 响应内容长度: %d 字节\n", len(body))

	// 只显示前500个字符，避免输出过长
	preview := string(body)
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	fmt.Printf("直接请求 - 响应内容预览:\n%s\n", preview)

	fmt.Println("直接请求测试完成！")
}

// BenchmarkProxyRequest 性能测试
func BenchmarkProxyRequest(b *testing.B) {
	// proxyURL, err := url.Parse("http://127.0.0.1:8080")
	// if err != nil {
	// 	b.Fatalf("解析代理URL失败: %v", err)
	// }

	client := &http.Client{
		Transport: &http.Transport{
			// Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 30 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get("http://www.baidu.com")
		if err != nil {
			b.Fatalf("请求失败: %v", err)
		}
		_, err = io.ReadAll(resp.Body)
		if err != nil {
			b.Fatalf("读取响应失败: %v", err)
		}
		resp.Body.Close()
	}
}
