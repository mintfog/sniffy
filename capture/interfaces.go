// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"github.com/f-dong/sniffy/capture/core"
)

// 重新导出核心包中的类型，保持向后兼容
type (
	PacketHandler   = core.PacketHandler
	ConnectionInfo  = core.ConnectionInfo
	PacketInfo      = core.PacketInfo
	PacketDirection = core.PacketDirection
	Logger          = core.Logger
	Config          = core.Config
)

// 重新导出核心包中的常量，保持向后兼容
const (
	DirectionInbound  = core.DirectionInbound
	DirectionOutbound = core.DirectionOutbound
)
