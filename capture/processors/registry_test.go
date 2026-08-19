// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package processors

import (
	"bufio"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/capture/processors/tcp"
	"github.com/mintfog/sniffy/capture/types"
)

type testServer struct {
	logs []string
}

func (s *testServer) GetConfig() types.Config { return nil }
func (s *testServer) LogInfo(msg string, args ...interface{}) {
	s.logs = append(s.logs, "info: "+fmt.Sprintf(msg, args...))
}
func (s *testServer) LogError(msg string, args ...interface{}) {
	s.logs = append(s.logs, "error: "+fmt.Sprintf(msg, args...))
}
func (s *testServer) LogDebug(msg string, args ...interface{}) {
	s.logs = append(s.logs, "debug: "+fmt.Sprintf(msg, args...))
}
func (s *testServer) FormatDataPreview(data []byte) string { return string(data) }

func (s *testServer) contains(fragment string) bool {
	for _, entry := range s.logs {
		if strings.Contains(entry, fragment) {
			return true
		}
	}
	return false
}

type customProcessor struct{}

func (*customProcessor) Process() error          { return nil }
func (*customProcessor) GetProtocolName() string { return "CUSTOM" }

func TestRegistryRegistrationAndProcessorSelection(t *testing.T) {
	r := NewRegistry()
	protocols := r.GetRegisteredProtocols()
	for _, protocol := range []string{"HTTP", "SOCKS5", "TCP"} {
		if !slices.Contains(protocols, protocol) {
			t.Errorf("default protocol %q is not registered: %v", protocol, protocols)
		}
	}

	processor := &customProcessor{}
	r.Register("CUSTOM", func(types.Connection) types.ProtocolProcessor { return processor })
	if got := r.GetProcessor("CUSTOM", nil); got != processor {
		t.Fatal("registered factory result was not returned")
	}

	r.Unregister("CUSTOM")
	if _, ok := r.GetProcessor("CUSTOM", nil).(*tcp.Processor); !ok {
		t.Fatal("unknown protocol should fall back to TCP processor")
	}
}

func TestRegistryDetectProtocol(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantLog string
	}{
		{name: "empty", data: "", wantLog: "Failed to peek"},
		{name: "http", data: "GET / HTTP/1.1\r\n", wantLog: ""},
		{name: "socks5", data: string([]byte{SocksFive, 0x01}), wantLog: ""},
		{name: "tls", data: string([]byte{TLSHandshake, 0x03, 0x03}), wantLog: "TLS/SSL"},
		{name: "ssh2", data: "SSH-2.0-test", wantLog: "SSH-2.0"},
		{name: "ssh199", data: "SSH-1.99-test", wantLog: "SSH-1.99"},
		{name: "unknown ssh", data: "SSH-9.99-test", wantLog: ""},
		{name: "short ssh", data: "S", wantLog: ""},
		{name: "ftp", data: "220 welcome!\r\n", wantLog: "FTP"},
		{name: "smtp", data: "250 accepted\r\n", wantLog: "SMTP"},
		{name: "unknown numeric", data: "299 unknown\r\n", wantLog: ""},
		{name: "short numeric", data: "2", wantLog: ""},
		{name: "mqtt", data: string([]byte{MQTTConnect}), wantLog: ""},
		{name: "rdp", data: string([]byte{RDPRequest}), wantLog: ""},
		{name: "advanced", data: string(make([]byte, 16)), wantLog: "高级协议"},
		{name: "short advanced", data: "x", wantLog: ""},
	}

	r := NewRegistry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &testServer{}
			got := r.DetectProtocol(bufio.NewReader(strings.NewReader(tt.data)), server)
			want := "TCP"
			if tt.name == "http" {
				want = "HTTP"
			} else if tt.name == "socks5" {
				want = "SOCKS5"
			}
			if got != want {
				t.Fatalf("DetectProtocol() = %q, want %q", got, want)
			}
			if tt.wantLog != "" && !server.contains(tt.wantLog) {
				t.Fatalf("logs %v do not contain %q", server.logs, tt.wantLog)
			}
		})
	}
}
