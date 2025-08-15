// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"context"

	"github.com/mintfog/sniffy/plugins"
)

// BasePlugin 基础插件实现，提供通用功能
type BasePlugin struct {
	info     plugins.PluginInfo
	config   plugins.PluginConfig
	api      plugins.PluginAPI
	logger   plugins.Logger
	enabled  bool
	priority int
}

// NewBasePlugin 创建基础插件
func NewBasePlugin(info plugins.PluginInfo, api plugins.PluginAPI) *BasePlugin {
	return &BasePlugin{
		info:     info,
		api:      api,
		logger:   api.GetLogger(info.Name),
		enabled:  true,
		priority: 100,
	}
}

// GetInfo 获取插件信息
func (bp *BasePlugin) GetInfo() plugins.PluginInfo {
	return bp.info
}

// Initialize 初始化插件
func (bp *BasePlugin) Initialize(ctx context.Context, config plugins.PluginConfig) error {
	bp.config = config
	bp.enabled = config.Enabled
	bp.priority = config.Priority
	
	bp.logger.Info("插件初始化完成: %s v%s", bp.info.Name, bp.info.Version)
	return nil
}

// Start 启动插件
func (bp *BasePlugin) Start(ctx context.Context) error {
	bp.logger.Info("插件已启动: %s", bp.info.Name)
	return nil
}

// Stop 停止插件
func (bp *BasePlugin) Stop(ctx context.Context) error {
	bp.logger.Info("插件已停止: %s", bp.info.Name)
	return nil
}

// IsEnabled 检查插件是否启用
func (bp *BasePlugin) IsEnabled() bool {
	return bp.enabled
}

// GetPriority 获取插件优先级
func (bp *BasePlugin) GetPriority() int {
	return bp.priority
}

// GetAPI 获取插件API
func (bp *BasePlugin) GetAPI() plugins.PluginAPI {
	return bp.api
}

// GetLogger 获取日志器
func (bp *BasePlugin) GetLogger() plugins.Logger {
	return bp.logger
}

// GetConfig 获取配置
func (bp *BasePlugin) GetConfig() plugins.PluginConfig {
	return bp.config
}

// GetSetting 获取配置项
func (bp *BasePlugin) GetSetting(key string, defaultValue interface{}) interface{} {
	if value, exists := bp.config.Settings[key]; exists {
		return value
	}
	return defaultValue
}

// GetStringSetting 获取字符串配置项
func (bp *BasePlugin) GetStringSetting(key string, defaultValue string) string {
	if value := bp.GetSetting(key, defaultValue); value != nil {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return defaultValue
}

// GetIntSetting 获取整数配置项
func (bp *BasePlugin) GetIntSetting(key string, defaultValue int) int {
	if value := bp.GetSetting(key, defaultValue); value != nil {
		if intVal, ok := value.(int); ok {
			return intVal
		}
		if floatVal, ok := value.(float64); ok {
			return int(floatVal)
		}
	}
	return defaultValue
}

// GetBoolSetting 获取布尔配置项
func (bp *BasePlugin) GetBoolSetting(key string, defaultValue bool) bool {
	if value := bp.GetSetting(key, defaultValue); value != nil {
		if boolVal, ok := value.(bool); ok {
			return boolVal
		}
	}
	return defaultValue
}

// ValidateConfig 验证配置
func (bp *BasePlugin) ValidateConfig() error {
	// 子类可以重写此方法进行特定验证
	return nil
}