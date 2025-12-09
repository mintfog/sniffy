// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestProxyRequest 测试通过代理请求百度
func TestProxyRequest(t *testing.T) {
	// 设置代理地址
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("解代8理U87747失败: %v", err)
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

// 全局变量用于WebSocket升级
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许所有跨域请求
	},
}

// createWebSocketEchoServer 创建一个WebSocket回显服务器
func createWebSocketEchoServer(t *testing.T) (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法创建监听器: %v", err)
	}

	addr := listener.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket升级失败: %v", err)
			return
		}
		defer conn.Close()

		// 回显所有收到的消息
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket读取错误: %v", err)
				}
				break
			}

			// 回显消息
			if err := conn.WriteMessage(messageType, message); err != nil {
				log.Printf("WebSocket写入错误: %v", err)
				break
			}
		}
	})

	server := &http.Server{Handler: mux}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("WebSocket服务器错误: %v", err)
		}
	}()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}

	return addr, cleanup
}

// createWebSocketChatServer 创建一个WebSocket聊天服务器用于测试双向通信
func createWebSocketChatServer(t *testing.T) (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法创建监听器: %v", err)
	}

	addr := listener.Addr().String()
	var clients sync.Map // 存储连接的客户端

	mux := http.NewServeMux()
	mux.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket升级失败: %v", err)
			return
		}
		defer conn.Close()

		clientID := fmt.Sprintf("client_%d", time.Now().UnixNano())
		clients.Store(clientID, conn)
		defer clients.Delete(clientID)

		// 发送欢迎消息
		welcomeMsg := fmt.Sprintf("欢迎 %s 加入聊天室", clientID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(welcomeMsg)); err != nil {
			log.Printf("发送欢迎消息失败: %v", err)
			return
		}

		// 处理消息
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket读取错误: %v", err)
				}
				break
			}

			// 广播消息到所有客户端
			broadcastMsg := fmt.Sprintf("%s: %s", clientID, string(message))
			clients.Range(func(key, value interface{}) bool {
				if clientConn, ok := value.(*websocket.Conn); ok {
					if err := clientConn.WriteMessage(messageType, []byte(broadcastMsg)); err != nil {
						log.Printf("广播消息失败: %v", err)
					}
				}
				return true
			})
		}
	})

	server := &http.Server{Handler: mux}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("WebSocket聊天服务器错误: %v", err)
		}
	}()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}

	return addr, cleanup
}

// TestWebSocketEcho 测试通过代理的WebSocket回显功能
func TestWebSocketEcho(t *testing.T) {
	// 创建WebSocket回显服务器
	serverAddr, cleanup := createWebSocketEchoServer(t)
	defer cleanup()

	fmt.Printf("WebSocket回显服务器启动在: %s\n", serverAddr)

	// 设置代理
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("解析代理URL失败: %v", err)
	}

	// 创建WebSocket拨号器，配置代理
	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: 45 * time.Second,
	}

	// 通过代理连接到WebSocket服务器
	wsURL := fmt.Sprintf("ws://%s/echo", serverAddr)
	fmt.Printf("通过代理连接到WebSocket服务器: %s\n", wsURL)

	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket连接失败: %v", err)
	}
	defer conn.Close()

	fmt.Println("WebSocket连接建立成功！")

	// 测试消息
	testMessages := []string{
		"Hello WebSocket!",
		"测试中文消息",
		"This is a test message through proxy",
		"🎉 Emoji测试",
	}

	for i, message := range testMessages {
		fmt.Printf("发送消息 %d: %s\n", i+1, message)

		// 发送消息
		if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			t.Fatalf("发送消息失败: %v", err)
		}

		// 接收回显消息
		messageType, receivedMessage, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("接收消息失败: %v", err)
		}

		if messageType != websocket.TextMessage {
			t.Errorf("期望消息类型为文本，实际得到: %d", messageType)
		}

		if string(receivedMessage) != message {
			t.Errorf("回显消息不匹配。期望: %s，实际: %s", message, string(receivedMessage))
		}

		fmt.Printf("接收到回显消息 %d: %s\n", i+1, string(receivedMessage))
	}

	fmt.Println("WebSocket回显测试完成！")
}

// TestWebSocketBidirectional 测试双向WebSocket通信
func TestWebSocketBidirectional(t *testing.T) {
	// 创建WebSocket聊天服务器
	serverAddr, cleanup := createWebSocketChatServer(t)
	defer cleanup()

	fmt.Printf("WebSocket聊天服务器启动在: %s\n", serverAddr)

	// 设置代理
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("解析代理URL失败: %v", err)
	}

	// 创建两个客户端连接测试双向通信
	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: 45 * time.Second,
	}

	wsURL := fmt.Sprintf("ws://%s/chat", serverAddr)

	// 客户端1
	fmt.Println("创建客户端1连接...")
	conn1, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("客户端1 WebSocket连接失败: %v", err)
	}
	defer conn1.Close()

	// 读取客户端1的欢迎消息
	_, welcomeMsg1, err := conn1.ReadMessage()
	if err != nil {
		t.Fatalf("客户端1读取欢迎消息失败: %v", err)
	}
	fmt.Printf("客户端1收到: %s\n", string(welcomeMsg1))

	// 客户端2
	fmt.Println("创建客户端2连接...")
	conn2, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("客户端2 WebSocket连接失败: %v", err)
	}
	defer conn2.Close()

	// 读取客户端2的欢迎消息
	_, welcomeMsg2, err := conn2.ReadMessage()
	if err != nil {
		t.Fatalf("客户端2读取欢迎消息失败: %v", err)
	}
	fmt.Printf("客户端2收到: %s\n", string(welcomeMsg2))

	// 客户端1发送消息
	message1 := "大家好！这是客户端1的消息"
	fmt.Printf("客户端1发送: %s\n", message1)
	if err := conn1.WriteMessage(websocket.TextMessage, []byte(message1)); err != nil {
		t.Fatalf("客户端1发送消息失败: %v", err)
	}

	// 客户端1和客户端2都应该收到广播消息
	_, broadcastMsg1, err := conn1.ReadMessage()
	if err != nil {
		t.Fatalf("客户端1读取广播消息失败: %v", err)
	}
	fmt.Printf("客户端1收到广播: %s\n", string(broadcastMsg1))

	_, broadcastMsg2, err := conn2.ReadMessage()
	if err != nil {
		t.Fatalf("客户端2读取广播消息失败: %v", err)
	}
	fmt.Printf("客户端2收到广播: %s\n", string(broadcastMsg2))

	// 验证广播消息包含原始消息
	if !strings.Contains(string(broadcastMsg1), message1) {
		t.Errorf("客户端1收到的广播消息不包含原始消息")
	}
	if !strings.Contains(string(broadcastMsg2), message1) {
		t.Errorf("客户端2收到的广播消息不包含原始消息")
	}

	fmt.Println("WebSocket双向通信测试完成！")
}

// TestWebSocketProxyConnection 测试WebSocket代理连接的建立和关闭
func TestWebSocketProxyConnection(t *testing.T) {
	// 创建WebSocket回显服务器
	serverAddr, cleanup := createWebSocketEchoServer(t)
	defer cleanup()

	fmt.Printf("WebSocket服务器启动在: %s\n", serverAddr)

	// 设置代理
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("解析代理URL失败: %v", err)
	}

	// 测试多个并发连接
	const numConnections = 5
	var wg sync.WaitGroup

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(connID int) {
			defer wg.Done()

			fmt.Printf("创建WebSocket连接 %d...\n", connID)

			dialer := websocket.Dialer{
				Proxy:            http.ProxyURL(proxyURL),
				HandshakeTimeout: 45 * time.Second,
			}

			wsURL := fmt.Sprintf("ws://%s/echo", serverAddr)
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				t.Errorf("连接 %d WebSocket连接失败: %v", connID, err)
				return
			}

			// 发送测试消息
			testMessage := fmt.Sprintf("来自连接 %d 的消息", connID)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
				t.Errorf("连接 %d 发送消息失败: %v", connID, err)
				conn.Close()
				return
			}

			// 接收回显消息
			_, receivedMessage, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("连接 %d 接收消息失败: %v", connID, err)
				conn.Close()
				return
			}

			if string(receivedMessage) != testMessage {
				t.Errorf("连接 %d 回显消息不匹配。期望: %s，实际: %s", connID, testMessage, string(receivedMessage))
			}

			fmt.Printf("连接 %d 测试成功: %s\n", connID, string(receivedMessage))

			// 正常关闭连接
			conn.Close()
			fmt.Printf("连接 %d 已关闭\n", connID)
		}(i)
	}

	wg.Wait()
	fmt.Println("WebSocket代理连接测试完成！")
}
