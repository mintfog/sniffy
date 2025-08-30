// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"testing"
	"time"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		actual   interface{}
		expected interface{}
	}{
		// 协议检测相关常量
		{"TLSHandshakeRecordType", int(TLSHandshakeRecordType), int(0x16)},
		{"HTTPGetByte", int(HTTPGetByte), int(0x47)},
		{"HTTPPostByte", int(HTTPPostByte), int(0x50)},

		// 连接池配置常量
		{"MaxIdleConns", MaxIdleConns, 1000},
		{"MaxIdleConnsPerHost", MaxIdleConnsPerHost, 100},
		{"MaxConnsPerHost", MaxConnsPerHost, 500},
		{"IdleConnTimeout", IdleConnTimeout, 90 * time.Second},
		{"ResponseHeaderTimeout", ResponseHeaderTimeout, 30 * time.Second},
		{"ExpectContinueTimeout", ExpectContinueTimeout, 1 * time.Second},
		{"ClientTimeout", ClientTimeout, 10 * time.Minute},

		// TLS相关常量
		{"TLSHandshakeTimeout", TLSHandshakeTimeout, 30 * time.Second},
		{"TLSConnectionTimeout", TLSConnectionTimeout, 5 * time.Minute},

		// HTTP响应模板
		{"ConnectEstablishedResponse", ConnectEstablishedResponse, "HTTP/1.1 200 Connection Established\r\n\r\n"},
		{"BadGatewayResponse", BadGatewayResponse, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\n\r\n502 Bad Gateway"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.actual != tt.expected {
				t.Errorf("%s = %v, expected %v", tt.name, tt.actual, tt.expected)
			}
		})
	}
}

func TestHTTPResponseTemplates(t *testing.T) {
	t.Run("ConnectEstablishedResponse format", func(t *testing.T) {
		expected := "HTTP/1.1 200 Connection Established\r\n\r\n"
		if ConnectEstablishedResponse != expected {
			t.Errorf("ConnectEstablishedResponse格式不正确: got %q, want %q", ConnectEstablishedResponse, expected)
		}

		// 验证是否包含正确的HTTP状态行
		if !contains(ConnectEstablishedResponse, "HTTP/1.1 200") {
			t.Error("ConnectEstablishedResponse应该包含HTTP/1.1 200状态")
		}

		// 验证是否以双回车换行结尾
		if !endsWith(ConnectEstablishedResponse, "\r\n\r\n") {
			t.Error("ConnectEstablishedResponse应该以\\r\\n\\r\\n结尾")
		}
	})

	t.Run("BadGatewayResponse format", func(t *testing.T) {
		expected := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\n\r\n502 Bad Gateway"
		if BadGatewayResponse != expected {
			t.Errorf("BadGatewayResponse格式不正确: got %q, want %q", BadGatewayResponse, expected)
		}

		// 验证HTTP状态码
		if !contains(BadGatewayResponse, "HTTP/1.1 502") {
			t.Error("BadGatewayResponse应该包含HTTP/1.1 502状态")
		}

		// 验证Content-Length头
		if !contains(BadGatewayResponse, "Content-Length: 15") {
			t.Error("BadGatewayResponse应该包含Content-Length: 15头")
		}

		// 验证响应体
		if !contains(BadGatewayResponse, "502 Bad Gateway") {
			t.Error("BadGatewayResponse应该包含'502 Bad Gateway'响应体")
		}
	})
}

func TestTimeoutValues(t *testing.T) {
	timeoutTests := []struct {
		name     string
		timeout  time.Duration
		minValue time.Duration
		maxValue time.Duration
	}{
		{"IdleConnTimeout", IdleConnTimeout, 60 * time.Second, 120 * time.Second},
		{"ResponseHeaderTimeout", ResponseHeaderTimeout, 10 * time.Second, 60 * time.Second},
		{"ExpectContinueTimeout", ExpectContinueTimeout, 500 * time.Millisecond, 5 * time.Second},
		{"ClientTimeout", ClientTimeout, 5 * time.Minute, 30 * time.Minute},
		{"TLSHandshakeTimeout", TLSHandshakeTimeout, 10 * time.Second, 60 * time.Second},
		{"TLSConnectionTimeout", TLSConnectionTimeout, 1 * time.Minute, 10 * time.Minute},
	}

	for _, tt := range timeoutTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.timeout < tt.minValue {
				t.Errorf("%s = %v, 可能过短，最小建议值: %v", tt.name, tt.timeout, tt.minValue)
			}
			if tt.timeout > tt.maxValue {
				t.Errorf("%s = %v, 可能过长，最大建议值: %v", tt.name, tt.timeout, tt.maxValue)
			}
		})
	}
}

func TestConnectionPoolConstants(t *testing.T) {
	connectionTests := []struct {
		name     string
		value    int
		minValue int
		maxValue int
	}{
		{"MaxIdleConns", MaxIdleConns, 100, 5000},
		{"MaxIdleConnsPerHost", MaxIdleConnsPerHost, 10, 1000},
		{"MaxConnsPerHost", MaxConnsPerHost, 50, 2000},
	}

	for _, tt := range connectionTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value < tt.minValue {
				t.Errorf("%s = %d, 可能过小，最小建议值: %d", tt.name, tt.value, tt.minValue)
			}
			if tt.value > tt.maxValue {
				t.Errorf("%s = %d, 可能过大，最大建议值: %d", tt.name, tt.value, tt.maxValue)
			}
		})
	}

	// 验证连接池常量之间的关系
	if MaxIdleConnsPerHost > MaxIdleConns {
		t.Errorf("MaxIdleConnsPerHost (%d) 不应该大于 MaxIdleConns (%d)", MaxIdleConnsPerHost, MaxIdleConns)
	}

	if MaxIdleConnsPerHost > MaxConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost (%d) 不应该大于 MaxConnsPerHost (%d)", MaxIdleConnsPerHost, MaxConnsPerHost)
	}
}

func TestProtocolDetectionConstants(t *testing.T) {
	t.Run("TLS handshake byte", func(t *testing.T) {
		if TLSHandshakeRecordType != 0x16 {
			t.Errorf("TLS握手记录类型应该是0x16，得到: 0x%02x", TLSHandshakeRecordType)
		}
	})

	t.Run("HTTP method bytes", func(t *testing.T) {
		if HTTPGetByte != 'G' {
			t.Errorf("HTTP GET字节应该是'G' (0x47)，得到: 0x%02x", HTTPGetByte)
		}

		if HTTPPostByte != 'P' {
			t.Errorf("HTTP POST字节应该是'P' (0x50)，得到: 0x%02x", HTTPPostByte)
		}
	})
}

// 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr) != -1
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func findSubstring(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
