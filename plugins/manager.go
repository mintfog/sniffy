// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"plugin"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// PluginManager 插件管理器
type PluginManager struct {
	// 基础属性
	api         PluginAPI
	logger      types.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	pluginsDir  string
	configDir   string

	// 插件存储
	plugins         map[string]Plugin         // 所有插件实例
	metadata        map[string]*PluginMetadata // 插件元数据
	factories       map[string]PluginFactory   // 插件工厂

	// 按类型分类的插件
	requestInterceptors    []RequestInterceptor
	responseInterceptors   []ResponseInterceptor
	connectionInterceptors []ConnectionInterceptor
	dataProcessors        []DataProcessor

	// 并发控制
	mu sync.RWMutex

	// 配置
	config ManagerConfig
}

// ManagerConfig 管理器配置
type ManagerConfig struct {
	PluginsDir         string        `json:"plugins_dir"`
	ConfigDir          string        `json:"config_dir"`
	AutoLoad           bool          `json:"auto_load"`
	LoadTimeout        time.Duration `json:"load_timeout"`
	EnableHotReload    bool          `json:"enable_hot_reload"`
	WatchInterval      time.Duration `json:"watch_interval"`
}

// DefaultManagerConfig 默认管理器配置
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		PluginsDir:      "plugins",
		ConfigDir:       "configs/plugins",
		AutoLoad:        true,
		LoadTimeout:     30 * time.Second,
		EnableHotReload: false,
		WatchInterval:   5 * time.Second,
	}
}

// NewPluginManager 创建插件管理器
func NewPluginManager(api PluginAPI, logger types.Logger, config ManagerConfig) *PluginManager {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &PluginManager{
		api:                    api,
		logger:                 logger,
		ctx:                    ctx,
		cancel:                 cancel,
		pluginsDir:             config.PluginsDir,
		configDir:              config.ConfigDir,
		plugins:               make(map[string]Plugin),
		metadata:              make(map[string]*PluginMetadata),
		factories:             make(map[string]PluginFactory),
		requestInterceptors:   make([]RequestInterceptor, 0),
		responseInterceptors:  make([]ResponseInterceptor, 0),
		connectionInterceptors: make([]ConnectionInterceptor, 0),
		dataProcessors:        make([]DataProcessor, 0),
		config:                config,
	}
}

// RegisterFactory 注册插件工厂
func (pm *PluginManager) RegisterFactory(name string, factory PluginFactory) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	pm.factories[name] = factory
	pm.logger.Info("注册插件工厂: %s", name)
}

// LoadPlugins 加载所有插件
func (pm *PluginManager) LoadPlugins() error {
	pm.logger.Info("开始加载插件，插件目录: %s", pm.pluginsDir)

	// 确保目录存在
	if err := pm.ensureDirectories(); err != nil {
		return fmt.Errorf("确保目录存在失败: %w", err)
	}

	// 发现插件
	pluginFiles, err := pm.discoverPlugins()
	if err != nil {
		return fmt.Errorf("发现插件失败: %w", err)
	}

	pm.logger.Info("发现 %d 个插件", len(pluginFiles))

	// 加载每个插件
	for _, pluginFile := range pluginFiles {
		if err := pm.loadPlugin(pluginFile); err != nil {
			pm.logger.Error("加载插件失败 %s: %v", pluginFile, err)
			continue
		}
	}

	// 分类并排序插件
	pm.classifyPlugins()

	pm.logger.Info("成功加载 %d 个插件", len(pm.plugins))
	return nil
}

// discoverPlugins 发现插件
func (pm *PluginManager) discoverPlugins() ([]string, error) {
	var pluginFiles []string

	// 扫描 .so 文件（编译时插件）
	err := filepath.Walk(pm.pluginsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".so") {
			pluginFiles = append(pluginFiles, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 扫描注册的工厂插件
	pm.mu.RLock()
	for name := range pm.factories {
		pluginFiles = append(pluginFiles, fmt.Sprintf("factory:%s", name))
	}
	pm.mu.RUnlock()

	return pluginFiles, nil
}

// loadPlugin 加载单个插件
func (pm *PluginManager) loadPlugin(pluginFile string) error {
	pm.logger.Debug("加载插件: %s", pluginFile)

	var pluginInstance Plugin
	var err error

	// 判断是工厂插件还是 .so 文件插件
	if strings.HasPrefix(pluginFile, "factory:") {
		// 工厂插件
		factoryName := strings.TrimPrefix(pluginFile, "factory:")
		pluginInstance, err = pm.loadFactoryPlugin(factoryName)
	} else {
		// .so 文件插件
		pluginInstance, err = pm.loadSharedLibraryPlugin(pluginFile)
	}

	if err != nil {
		return err
	}

	// 获取插件信息
	info := pluginInstance.GetInfo()
	pluginName := info.Name

	// 加载插件配置
	config, err := pm.loadPluginConfig(pluginName)
	if err != nil {
		pm.logger.Warn("加载插件配置失败 %s: %v，使用默认配置", pluginName, err)
		config = PluginConfig{
			Enabled:  true,
			Priority: 100,
			Settings: make(map[string]interface{}),
		}
	}

	// 检查是否启用
	if !config.Enabled {
		pm.logger.Info("插件已禁用: %s", pluginName)
		return nil
	}

	// 初始化插件
	ctx, cancel := context.WithTimeout(pm.ctx, pm.config.LoadTimeout)
	defer cancel()

	if err := pluginInstance.Initialize(ctx, config); err != nil {
		return fmt.Errorf("初始化插件失败: %w", err)
	}

	// 存储插件
	pm.mu.Lock()
	pm.plugins[pluginName] = pluginInstance
	pm.metadata[pluginName] = &PluginMetadata{
		Info:     info,
		Config:   config,
		FilePath: pluginFile,
	}
	pm.mu.Unlock()

	pm.logger.Info("成功加载插件: %s v%s", info.Name, info.Version)
	return nil
}

// loadFactoryPlugin 加载工厂插件
func (pm *PluginManager) loadFactoryPlugin(factoryName string) (Plugin, error) {
	pm.mu.RLock()
	factory, exists := pm.factories[factoryName]
	pm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("插件工厂不存在: %s", factoryName)
	}

	return factory(pm.api), nil
}

// loadSharedLibraryPlugin 加载共享库插件
func (pm *PluginManager) loadSharedLibraryPlugin(pluginFile string) (Plugin, error) {
	// 打开插件文件
	p, err := plugin.Open(pluginFile)
	if err != nil {
		return nil, fmt.Errorf("打开插件文件失败: %w", err)
	}

	// 查找插件工厂函数
	factorySymbol, err := p.Lookup("NewPlugin")
	if err != nil {
		return nil, fmt.Errorf("查找 NewPlugin 函数失败: %w", err)
	}

	// 类型断言为工厂函数
	factory, ok := factorySymbol.(func(PluginAPI) Plugin)
	if !ok {
		return nil, fmt.Errorf("NewPlugin 函数签名不正确")
	}

	// 创建插件实例
	return factory(pm.api), nil
}

// loadPluginConfig 加载插件配置
func (pm *PluginManager) loadPluginConfig(pluginName string) (PluginConfig, error) {
	configFile := filepath.Join(pm.configDir, pluginName+".json")

	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return PluginConfig{}, err
	}

	var config PluginConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return PluginConfig{}, err
	}

	return config, nil
}

// classifyPlugins 分类并排序插件
func (pm *PluginManager) classifyPlugins() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 清空分类列表
	pm.requestInterceptors = pm.requestInterceptors[:0]
	pm.responseInterceptors = pm.responseInterceptors[:0]
	pm.connectionInterceptors = pm.connectionInterceptors[:0]
	pm.dataProcessors = pm.dataProcessors[:0]

	// 分类插件
	for _, p := range pm.plugins {
		if interceptor, ok := p.(RequestInterceptor); ok {
			pm.requestInterceptors = append(pm.requestInterceptors, interceptor)
		}
		if interceptor, ok := p.(ResponseInterceptor); ok {
			pm.responseInterceptors = append(pm.responseInterceptors, interceptor)
		}
		if interceptor, ok := p.(ConnectionInterceptor); ok {
			pm.connectionInterceptors = append(pm.connectionInterceptors, interceptor)
		}
		if processor, ok := p.(DataProcessor); ok {
			pm.dataProcessors = append(pm.dataProcessors, processor)
		}
	}

	// 按优先级排序
	sort.Slice(pm.requestInterceptors, func(i, j int) bool {
		return pm.requestInterceptors[i].GetPriority() < pm.requestInterceptors[j].GetPriority()
	})
	sort.Slice(pm.responseInterceptors, func(i, j int) bool {
		return pm.responseInterceptors[i].GetPriority() < pm.responseInterceptors[j].GetPriority()
	})
	sort.Slice(pm.connectionInterceptors, func(i, j int) bool {
		return pm.connectionInterceptors[i].GetPriority() < pm.connectionInterceptors[j].GetPriority()
	})
	sort.Slice(pm.dataProcessors, func(i, j int) bool {
		return pm.dataProcessors[i].GetPriority() < pm.dataProcessors[j].GetPriority()
	})
}

// StartPlugins 启动所有插件
func (pm *PluginManager) StartPlugins() error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for name, p := range pm.plugins {
		if err := p.Start(pm.ctx); err != nil {
			pm.logger.Error("启动插件失败 %s: %v", name, err)
			continue
		}
		pm.logger.Debug("插件已启动: %s", name)
	}

	pm.logger.Info("所有插件启动完成")
	return nil
}

// StopPlugins 停止所有插件
func (pm *PluginManager) StopPlugins() error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for name, p := range pm.plugins {
		if err := p.Stop(pm.ctx); err != nil {
			pm.logger.Error("停止插件失败 %s: %v", name, err)
			continue
		}
		pm.logger.Debug("插件已停止: %s", name)
	}

	pm.logger.Info("所有插件停止完成")
	return nil
}

// GetRequestInterceptors 获取请求拦截器
func (pm *PluginManager) GetRequestInterceptors() []RequestInterceptor {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	result := make([]RequestInterceptor, len(pm.requestInterceptors))
	copy(result, pm.requestInterceptors)
	return result
}

// GetResponseInterceptors 获取响应拦截器
func (pm *PluginManager) GetResponseInterceptors() []ResponseInterceptor {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	result := make([]ResponseInterceptor, len(pm.responseInterceptors))
	copy(result, pm.responseInterceptors)
	return result
}

// GetConnectionInterceptors 获取连接拦截器
func (pm *PluginManager) GetConnectionInterceptors() []ConnectionInterceptor {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	result := make([]ConnectionInterceptor, len(pm.connectionInterceptors))
	copy(result, pm.connectionInterceptors)
	return result
}

// GetDataProcessors 获取数据处理器
func (pm *PluginManager) GetDataProcessors() []DataProcessor {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	result := make([]DataProcessor, len(pm.dataProcessors))
	copy(result, pm.dataProcessors)
	return result
}

// GetPluginList 获取插件列表
func (pm *PluginManager) GetPluginList() map[string]*PluginMetadata {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	result := make(map[string]*PluginMetadata)
	for name, metadata := range pm.metadata {
		result[name] = metadata
	}
	return result
}

// ensureDirectories 确保目录存在
func (pm *PluginManager) ensureDirectories() error {
	for _, dir := range []string{pm.pluginsDir, pm.configDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown 关闭插件管理器
func (pm *PluginManager) Shutdown() error {
	pm.logger.Info("开始关闭插件管理器")
	
	// 停止所有插件
	if err := pm.StopPlugins(); err != nil {
		pm.logger.Error("停止插件失败: %v", err)
	}
	
	// 取消上下文
	pm.cancel()
	
	pm.logger.Info("插件管理器已关闭")
	return nil
}