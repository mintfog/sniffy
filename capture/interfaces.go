// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"github.com/mintfog/sniffy/capture/types"
)

// 重新导出基础类型，保持向后兼容
type (
	PacketHandler   = types.PacketHandler
	ConnectionInfo  = types.ConnectionInfo
	PacketInfo      = types.PacketInfo
	PacketDirection = types.PacketDirection
	Logger          = types.Logger
	Config          = types.Config
)

// 重新导出基础常量，保持向后兼容
const (
	DirectionInbound  = types.DirectionInbound
	DirectionOutbound = types.DirectionOutbound
)
