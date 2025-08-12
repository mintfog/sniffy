// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugins

import (
	"fmt"
	"sync"

	"github.com/mintfog/sniffy/capture/types"
)

// APIImplementation 插件API实现
type APIImplementation struct {
	config  types.Config
	logger  types.Logger
	storage *DataStorage
	metrics *MetricsCollector
}

// DataStorage 数据存储
type DataStorage struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

// MetricsCollector 指标收集器
type MetricsCollector struct {
	metrics map[string]interface{}
	mu      sync.RWMutex
}

// PluginLogger 插件专用日志器
type PluginLogger struct {
	pluginName string
	logger     types.Logger
}

// NewAPIImplementation 创建API实现
func NewAPIImplementation(config types.Config, logger types.Logger) *APIImplementation {
	return &APIImplementation{
		config:  config,
		logger:  logger,
		storage: NewDataStorage(),
		metrics: NewMetricsCollector(),
	}
}

// NewDataStorage 创建数据存储
func NewDataStorage() *DataStorage {
	return &DataStorage{
		data: make(map[string]interface{}),
	}
}

// NewMetricsCollector 创建指标收集器
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		metrics: make(map[string]interface{}),
	}
}

// GetLogger 获取日志器
func (api *APIImplementation) GetLogger(pluginName string) Logger {
	return &PluginLogger{
		pluginName: pluginName,
		logger:     api.logger,
	}
}

// GetConfig 获取应用配置
func (api *APIImplementation) GetConfig() types.Config {
	return api.config
}

// SendNotification 发送通知
func (api *APIImplementation) SendNotification(title, message string) error {
	// todo 发送到web
	api.logger.Info("通知 [%s]: %s", title, message)
	return nil
}

// GetMetrics 获取指标
func (api *APIImplementation) GetMetrics() map[string]interface{} {
	return api.metrics.GetAll()
}

// StoreData 存储数据
func (api *APIImplementation) StoreData(key string, value interface{}) error {
	api.storage.Set(key, value)
	return nil
}

// GetData 获取数据
func (api *APIImplementation) GetData(key string) (interface{}, error) {
	value, exists := api.storage.Get(key)
	if !exists {
		return nil, fmt.Errorf("数据不存在: %s", key)
	}
	return value, nil
}

// PluginLogger 实现 Logger 接口

// Info 信息日志
func (pl *PluginLogger) Info(msg string, args ...interface{}) {
	formattedMsg := fmt.Sprintf("[插件:%s] %s", pl.pluginName, msg)
	pl.logger.Info(formattedMsg, args...)
}

// Error 错误日志
func (pl *PluginLogger) Error(msg string, args ...interface{}) {
	formattedMsg := fmt.Sprintf("[插件:%s] %s", pl.pluginName, msg)
	pl.logger.Error(formattedMsg, args...)
}

// Debug 调试日志
func (pl *PluginLogger) Debug(msg string, args ...interface{}) {
	formattedMsg := fmt.Sprintf("[插件:%s] %s", pl.pluginName, msg)
	pl.logger.Debug(formattedMsg, args...)
}

// Warn 警告日志
func (pl *PluginLogger) Warn(msg string, args ...interface{}) {
	formattedMsg := fmt.Sprintf("[插件:%s] %s", pl.pluginName, msg)
	pl.logger.Warn(formattedMsg, args...)
}

// DataStorage 方法

// Set 设置数据
func (ds *DataStorage) Set(key string, value interface{}) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.data[key] = value
}

// Get 获取数据
func (ds *DataStorage) Get(key string) (interface{}, bool) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	value, exists := ds.data[key]
	return value, exists
}

// Delete 删除数据
func (ds *DataStorage) Delete(key string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	delete(ds.data, key)
}

// GetAll 获取所有数据
func (ds *DataStorage) GetAll() map[string]interface{} {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	
	result := make(map[string]interface{})
	for k, v := range ds.data {
		result[k] = v
	}
	return result
}

// MetricsCollector 方法

// Set 设置指标
func (mc *MetricsCollector) Set(key string, value interface{}) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.metrics[key] = value
}

// Get 获取指标
func (mc *MetricsCollector) Get(key string) (interface{}, bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	value, exists := mc.metrics[key]
	return value, exists
}

// Increment 递增指标
func (mc *MetricsCollector) Increment(key string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	
	if value, exists := mc.metrics[key]; exists {
		if count, ok := value.(int64); ok {
			mc.metrics[key] = count + 1
		} else {
			mc.metrics[key] = int64(1)
		}
	} else {
		mc.metrics[key] = int64(1)
	}
}

// Add 添加指标值
func (mc *MetricsCollector) Add(key string, value int64) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	
	if existing, exists := mc.metrics[key]; exists {
		if count, ok := existing.(int64); ok {
			mc.metrics[key] = count + value
		} else {
			mc.metrics[key] = value
		}
	} else {
		mc.metrics[key] = value
	}
}

// GetAll 获取所有指标
func (mc *MetricsCollector) GetAll() map[string]interface{} {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	
	result := make(map[string]interface{})
	for k, v := range mc.metrics {
		result[k] = v
	}
	return result
}