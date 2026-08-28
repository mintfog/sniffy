// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// latin1Disposition 模拟含 Latin-1 文件名的 Content-Disposition 值。
const latin1Disposition = "attachment; filename=\"caf\xe9.pdf\""

// 验证蓝本请求经过 JSON 边界后发送时恢复头部原始字节。
func TestSendRequestRestoresSeededHeaderBytes(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	src := flow.New(flow.ProtoHTTP)
	src.Request = &flow.Request{
		Method: "GET",
		URL:    "http://" + addr + "/report",
		Host:   addr,
		Path:   "/report",
		Header: map[string][]string{
			"Content-Disposition": {latin1Disposition},
			"Accept":              {"*/*"},
		},
		RawHeaders: [][2]string{
			{"Host", addr},
			{"Content-Disposition", latin1Disposition},
			{"Accept", "*/*"},
		},
	}
	app.Service.ImportFlowStarted(src)

	seed, ok := app.Service.ComposeSeed(src.ID)
	if !ok {
		t.Fatal("取不到蓝本")
	}
	// 模拟构造器读取的 JSON 蓝本。
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	var seen service.ComposeSeedDTO
	if err := json.Unmarshal(raw, &seen); err != nil {
		t.Fatal(err)
	}
	if headerRowValue(t, seen.Headers, "Content-Disposition") == latin1Disposition {
		t.Fatal("蓝本经 JSON 出境后仍是原始字节,本用例已失去区分力")
	}

	if _, err := app.SendRequest(flow.RequestSpec{
		Method:  "GET",
		URL:     "http://" + addr + "/report",
		Headers: seen.Headers,
		FromID:  src.ID,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case wire := <-got:
		if !strings.Contains(wire, "Content-Disposition: "+latin1Disposition) {
			t.Fatalf("出线的头值不是原始字节:\n%q", wire)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

// 验证编辑后的头值优先于蓝本还原值。
func TestSendRequestKeepsEditedSeedHeader(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	src := flow.New(flow.ProtoHTTP)
	src.Request = &flow.Request{
		Method:     "GET",
		URL:        "http://" + addr + "/report",
		Host:       addr,
		Path:       "/report",
		Header:     map[string][]string{"Content-Disposition": {latin1Disposition}},
		RawHeaders: [][2]string{{"Host", addr}, {"Content-Disposition", latin1Disposition}},
	}
	app.Service.ImportFlowStarted(src)

	if _, err := app.SendRequest(flow.RequestSpec{
		Method:  "GET",
		URL:     "http://" + addr + "/report",
		Headers: [][2]string{{"Host", addr}, {"Content-Disposition", "inline"}},
		FromID:  src.ID,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case wire := <-got:
		if !strings.Contains(wire, "Content-Disposition: inline") {
			t.Fatalf("改过的头值没有出线:\n%q", wire)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

func headerRowValue(t *testing.T, pairs [][2]string, name string) string {
	t.Helper()
	for _, kv := range pairs {
		if strings.EqualFold(kv[0], name) {
			return kv[1]
		}
	}
	t.Fatalf("头 %s 不在 %v 里", name, pairs)
	return ""
}
