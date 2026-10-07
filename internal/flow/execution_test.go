// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import "testing"

func TestCloneExecutionSnapshot(t *testing.T) {
	f := New(ProtoHTTP)
	f.Executions = []Execution{{Kind: "rule", ID: "rule", Name: "原名称", Phase: PhaseRequest}}
	snapshot := f.Clone()
	f.Executions[0].Name = "新名称"
	f.Executions = append(f.Executions, Execution{Kind: "plugin", ID: "plugin", Phase: PhaseResponse})
	if len(snapshot.Executions) != 1 || snapshot.Executions[0].Name != "原名称" {
		t.Fatalf("快照执行记录被后续修改影响: %+v", snapshot.Executions)
	}
}
