// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"github.com/mintfog/sniffy/plugins"
)

// RegisterExamplePlugins 注册所有示例插件
func RegisterExamplePlugins(manager *plugins.PluginManager) {
	// 注册日志插件
	manager.RegisterFactory("logger", NewLoggerPlugin)
	
	// 注册请求修改插件
	manager.RegisterFactory("request_modifier", NewRequestModifierPlugin)
	
	// 注册连接监控插件
	manager.RegisterFactory("connection_monitor", NewConnectionMonitorPlugin)
	
	// 注册WebSocket日志插件
	manager.RegisterFactory("websocket_logger", NewWebSocketLoggerPlugin)
}

// GetAvailablePlugins 获取可用插件列表
func GetAvailablePlugins() []plugins.PluginInfo {
	return []plugins.PluginInfo{
		{
			Name:        "logger",
			Version:     "1.0.0",
			Description: "记录HTTP请求和响应的日志插件",
			Author:      "sniffy",
			Category:    "logging",
		},
		{
			Name:        "request_modifier",
			Version:     "1.0.0",
			Description: "修改HTTP请求头和参数的插件",
			Author:      "sniffy",
			Category:    "modifier",
		},
		{
			Name:        "connection_monitor",
			Version:     "1.0.0",
			Description: "监控TCP连接状态和统计信息的插件",
			Author:      "sniffy",
			Category:    "monitoring",
		},
		{
			Name:        "websocket_logger",
			Version:     "1.0.0",
			Description: "记录WebSocket消息的日志插件",
			Author:      "sniffy team",
			Category:    "monitoring",
		},
	}
}