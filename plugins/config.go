// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugins

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// ConfigManager 配置管理器
type ConfigManager struct {
	configDir string
	logger    Logger
}

// GlobalConfig 全局插件配置
type GlobalConfig struct {
	// 插件系统启用状态
	Enabled bool `json:"enabled"`
	
	// 插件目录
	PluginsDir string `json:"plugins_dir"`
	
	// 配置目录
	ConfigDir string `json:"config_dir"`
	
	// 自动加载插件
	AutoLoad bool `json:"auto_load"`
	
	// 热重载
	EnableHotReload bool `json:"enable_hot_reload"`
	
	// 默认插件优先级
	DefaultPriority int `json:"default_priority"`
	
	// 全局白名单
	GlobalWhitelist []string `json:"global_whitelist"`
	
	// 全局黑名单
	GlobalBlacklist []string `json:"global_blacklist"`
	
	// 插件超时设置
	LoadTimeout    int `json:"load_timeout_seconds"`
	ExecuteTimeout int `json:"execute_timeout_seconds"`
	
	// 安全设置
	Security SecurityConfig `json:"security"`
}

// SecurityConfig 安全配置
type SecurityConfig struct {
	// 允许插件访问的功能
	AllowedAPIs []string `json:"allowed_apis"`
	
	// 插件沙箱模式
	SandboxMode bool `json:"sandbox_mode"`
	
	// 资源限制
	MaxMemoryMB int `json:"max_memory_mb"`
	MaxCPUTime  int `json:"max_cpu_time_seconds"`
	
	// 签名验证
	RequireSignature bool `json:"require_signature"`
	TrustedKeys     []string `json:"trusted_keys"`
}

// DefaultGlobalConfig 默认全局配置
func DefaultGlobalConfig() GlobalConfig {
	return GlobalConfig{
		Enabled:         true,
		PluginsDir:      "plugins",
		ConfigDir:       "configs/plugins",
		AutoLoad:        true,
		EnableHotReload: false,
		DefaultPriority: 100,
		GlobalWhitelist: []string{},
		GlobalBlacklist: []string{},
		LoadTimeout:     30,
		ExecuteTimeout:  10,
		Security: SecurityConfig{
			AllowedAPIs:      []string{"*"},
			SandboxMode:      false,
			MaxMemoryMB:      256,
			MaxCPUTime:       5,
			RequireSignature: false,
			TrustedKeys:      []string{},
		},
	}
}

// NewConfigManager 创建配置管理器
func NewConfigManager(configDir string, logger Logger) *ConfigManager {
	return &ConfigManager{
		configDir: configDir,
		logger:    logger,
	}
}

// LoadGlobalConfig 加载全局配置
func (cm *ConfigManager) LoadGlobalConfig() (GlobalConfig, error) {
	configFile := filepath.Join(cm.configDir, "global.json")
	
	// 如果配置文件不存在，创建默认配置
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		cm.logger.Info("全局配置文件不存在，创建默认配置: %s", configFile)
		defaultConfig := DefaultGlobalConfig()
		if err := cm.SaveGlobalConfig(defaultConfig); err != nil {
			return defaultConfig, fmt.Errorf("保存默认配置失败: %w", err)
		}
		return defaultConfig, nil
	}
	
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("读取配置文件失败: %w", err)
	}
	
	var config GlobalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return GlobalConfig{}, fmt.Errorf("解析配置文件失败: %w", err)
	}
	
	// 验证配置
	if err := cm.validateGlobalConfig(&config); err != nil {
		return config, fmt.Errorf("配置验证失败: %w", err)
	}
	
	cm.logger.Info("成功加载全局配置")
	return config, nil
}

// SaveGlobalConfig 保存全局配置
func (cm *ConfigManager) SaveGlobalConfig(config GlobalConfig) error {
	// 确保目录存在
	if err := os.MkdirAll(cm.configDir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	
	configFile := filepath.Join(cm.configDir, "global.json")
	
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	
	if err := ioutil.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	
	cm.logger.Info("全局配置已保存到: %s", configFile)
	return nil
}

// LoadPluginConfig 加载插件配置
func (cm *ConfigManager) LoadPluginConfig(pluginName string) (PluginConfig, error) {
	configFile := filepath.Join(cm.configDir, pluginName+".json")
	
	// 如果配置文件不存在，返回默认配置
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		cm.logger.Debug("插件配置文件不存在，使用默认配置: %s", pluginName)
		return cm.getDefaultPluginConfig(), nil
	}
	
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return PluginConfig{}, fmt.Errorf("读取插件配置失败: %w", err)
	}
	
	var config PluginConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return PluginConfig{}, fmt.Errorf("解析插件配置失败: %w", err)
	}
	
	// 验证配置
	if err := cm.validatePluginConfig(&config); err != nil {
		return config, fmt.Errorf("插件配置验证失败: %w", err)
	}
	
	cm.logger.Debug("成功加载插件配置: %s", pluginName)
	return config, nil
}

// SavePluginConfig 保存插件配置
func (cm *ConfigManager) SavePluginConfig(pluginName string, config PluginConfig) error {
	// 确保目录存在
	if err := os.MkdirAll(cm.configDir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	
	configFile := filepath.Join(cm.configDir, pluginName+".json")
	
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化插件配置失败: %w", err)
	}
	
	if err := ioutil.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("写入插件配置文件失败: %w", err)
	}
	
	cm.logger.Info("插件配置已保存: %s", pluginName)
	return nil
}

// CreatePluginConfigTemplate 创建插件配置模板
func (cm *ConfigManager) CreatePluginConfigTemplate(pluginName string, info PluginInfo) error {
	config := PluginConfig{
		Enabled:  true,
		Priority: 100,
		Settings: map[string]interface{}{
			"description": fmt.Sprintf("Configuration for %s plugin", info.Name),
			"version":     info.Version,
			"category":    info.Category,
		},
		Whitelist: []string{},
		Blacklist: []string{},
	}
	
	return cm.SavePluginConfig(pluginName, config)
}

// ListPluginConfigs 列出所有插件配置
func (cm *ConfigManager) ListPluginConfigs() ([]string, error) {
	files, err := ioutil.ReadDir(cm.configDir)
	if err != nil {
		return nil, fmt.Errorf("读取配置目录失败: %w", err)
	}
	
	var configs []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") && file.Name() != "global.json" {
			pluginName := strings.TrimSuffix(file.Name(), ".json")
			configs = append(configs, pluginName)
		}
	}
	
	return configs, nil
}

// DeletePluginConfig 删除插件配置
func (cm *ConfigManager) DeletePluginConfig(pluginName string) error {
	configFile := filepath.Join(cm.configDir, pluginName+".json")
	
	if err := os.Remove(configFile); err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，认为删除成功
		}
		return fmt.Errorf("删除插件配置失败: %w", err)
	}
	
	cm.logger.Info("插件配置已删除: %s", pluginName)
	return nil
}

// validateGlobalConfig 验证全局配置
func (cm *ConfigManager) validateGlobalConfig(config *GlobalConfig) error {
	if config.PluginsDir == "" {
		return fmt.Errorf("插件目录不能为空")
	}
	
	if config.ConfigDir == "" {
		return fmt.Errorf("配置目录不能为空")
	}
	
	if config.LoadTimeout <= 0 {
		config.LoadTimeout = 30
		cm.logger.Warn("无效的加载超时时间，使用默认值: 30秒")
	}
	
	if config.ExecuteTimeout <= 0 {
		config.ExecuteTimeout = 10
		cm.logger.Warn("无效的执行超时时间，使用默认值: 10秒")
	}
	
	if config.DefaultPriority < 0 {
		config.DefaultPriority = 100
		cm.logger.Warn("无效的默认优先级，使用默认值: 100")
	}
	
	// 验证安全配置
	if config.Security.MaxMemoryMB <= 0 {
		config.Security.MaxMemoryMB = 256
		cm.logger.Warn("无效的内存限制，使用默认值: 256MB")
	}
	
	if config.Security.MaxCPUTime <= 0 {
		config.Security.MaxCPUTime = 5
		cm.logger.Warn("无效的CPU时间限制，使用默认值: 5秒")
	}
	
	return nil
}

// validatePluginConfig 验证插件配置
func (cm *ConfigManager) validatePluginConfig(config *PluginConfig) error {
	if config.Priority < 0 {
		config.Priority = 100
		cm.logger.Warn("无效的插件优先级，使用默认值: 100")
	}
	
	if config.Settings == nil {
		config.Settings = make(map[string]interface{})
	}
	
	return nil
}

// getDefaultPluginConfig 获取默认插件配置
func (cm *ConfigManager) getDefaultPluginConfig() PluginConfig {
	return PluginConfig{
		Enabled:   true,
		Priority:  100,
		Settings:  make(map[string]interface{}),
		Whitelist: []string{},
		Blacklist: []string{},
	}
}

// ValidateConfigFiles 验证所有配置文件
func (cm *ConfigManager) ValidateConfigFiles() error {
	// 验证全局配置
	if _, err := cm.LoadGlobalConfig(); err != nil {
		return fmt.Errorf("全局配置验证失败: %w", err)
	}
	
	// 验证所有插件配置
	plugins, err := cm.ListPluginConfigs()
	if err != nil {
		return fmt.Errorf("列出插件配置失败: %w", err)
	}
	
	for _, pluginName := range plugins {
		if _, err := cm.LoadPluginConfig(pluginName); err != nil {
			return fmt.Errorf("插件配置验证失败 %s: %w", pluginName, err)
		}
	}
	
	cm.logger.Info("所有配置文件验证通过")
	return nil
}

// ExportConfigs 导出所有配置
func (cm *ConfigManager) ExportConfigs(exportDir string) error {
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return fmt.Errorf("创建导出目录失败: %w", err)
	}
	
	// 复制所有配置文件
	files, err := ioutil.ReadDir(cm.configDir)
	if err != nil {
		return fmt.Errorf("读取配置目录失败: %w", err)
	}
	
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			srcFile := filepath.Join(cm.configDir, file.Name())
			dstFile := filepath.Join(exportDir, file.Name())
			
			if err := cm.copyFile(srcFile, dstFile); err != nil {
				return fmt.Errorf("复制配置文件失败 %s: %w", file.Name(), err)
			}
		}
	}
	
	cm.logger.Info("配置已导出到: %s", exportDir)
	return nil
}

// copyFile 复制文件
func (cm *ConfigManager) copyFile(src, dst string) error {
	data, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}
	
	return ioutil.WriteFile(dst, data, 0644)
}