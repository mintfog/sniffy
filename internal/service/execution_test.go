// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func TestSessionExecutionSnapshot(t *testing.T) {
	f := flow.New(flow.ProtoHTTP)
	f.Executions = []flow.Execution{{Kind: "plugin", ID: "plugin", Name: "原名称", Phase: flow.PhaseRequest}}
	dto := SessionDTO(f)
	f.Executions[0].Name = "新名称"
	if len(dto.Executions) != 1 || dto.Executions[0].Name != "原名称" {
		t.Fatalf("DTO 执行记录被后续修改影响: %+v", dto.Executions)
	}
	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"executions":[{"kind":"plugin","id":"plugin","name":"原名称","phase":"request"}]`) {
		t.Fatalf("会话 JSON 缺少执行记录: %s", data)
	}
}
