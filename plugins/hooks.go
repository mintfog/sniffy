// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// HookExecutor 钩子执行器
type HookExecutor struct {
	manager *PluginManager
	logger  types.Logger
}

// NewHookExecutor 创建钩子执行器
func NewHookExecutor(manager *PluginManager, logger types.Logger) *HookExecutor {
	return &HookExecutor{
		manager: manager,
		logger:  logger,
	}
}

// ExecuteRequestHooks 执行请求钩子
func (he *HookExecutor) ExecuteRequestHooks(ctx context.Context, interceptCtx *InterceptContext) (*InterceptResult, error) {
	interceptors := he.manager.GetRequestInterceptors()
	
	he.logger.Debug("执行 %d 个请求拦截器", len(interceptors))
	
	for _, interceptor := range interceptors {
		if !interceptor.IsEnabled() {
			he.logger.Debug("跳过已禁用的插件: %s", interceptor.GetInfo().Name)
			continue
		}
		
		// 检查白名单和黑名单
		if !he.checkAccess(interceptor, interceptCtx.Request) {
			he.logger.Debug("插件访问被拒绝: %s", interceptor.GetInfo().Name)
			continue
		}
		
		startTime := time.Now()
		result, err := he.executeRequestInterceptor(ctx, interceptor, interceptCtx)
		duration := time.Since(startTime)
		
		he.logger.Debug("插件 %s 执行时间: %v", interceptor.GetInfo().Name, duration)
		
		if err != nil {
			he.logger.Error("请求拦截器执行失败 %s: %v", interceptor.GetInfo().Name, err)
			continue
		}
		
		if result != nil {
			// 如果插件要求停止继续处理
			if !result.Continue {
				he.logger.Info("插件 %s 要求停止处理请求", interceptor.GetInfo().Name)
				return result, nil
			}
			
			// 如果插件修改了请求
			if result.Modified {
				he.logger.Debug("插件 %s 修改了请求", interceptor.GetInfo().Name)
			}
		}
	}
	
	return &InterceptResult{
		Continue: true,
		Modified: false,
		Message:  "所有请求拦截器执行完成",
	}, nil
}

// ExecuteResponseHooks 执行响应钩子
func (he *HookExecutor) ExecuteResponseHooks(ctx context.Context, interceptCtx *InterceptContext) (*InterceptResult, error) {
	interceptors := he.manager.GetResponseInterceptors()
	
	he.logger.Debug("执行 %d 个响应拦截器", len(interceptors))
	
	for _, interceptor := range interceptors {
		if !interceptor.IsEnabled() {
			he.logger.Debug("跳过已禁用的插件: %s", interceptor.GetInfo().Name)
			continue
		}
		
		// 检查白名单和黑名单
		if !he.checkAccess(interceptor, interceptCtx.Request) {
			he.logger.Debug("插件访问被拒绝: %s", interceptor.GetInfo().Name)
			continue
		}
		
		startTime := time.Now()
		result, err := he.executeResponseInterceptor(ctx, interceptor, interceptCtx)
		duration := time.Since(startTime)
		
		he.logger.Debug("插件 %s 执行时间: %v", interceptor.GetInfo().Name, duration)
		
		if err != nil {
			he.logger.Error("响应拦截器执行失败 %s: %v", interceptor.GetInfo().Name, err)
			continue
		}
		
		if result != nil {
			// 如果插件要求停止继续处理
			if !result.Continue {
				he.logger.Info("插件 %s 要求停止处理响应", interceptor.GetInfo().Name)
				return result, nil
			}
			
			// 如果插件修改了响应
			if result.Modified {
				he.logger.Debug("插件 %s 修改了响应", interceptor.GetInfo().Name)
			}
		}
	}
	
	return &InterceptResult{
		Continue: true,
		Modified: false,
		Message:  "所有响应拦截器执行完成",
	}, nil
}

// ExecuteConnectionStartHooks 执行连接开始钩子
func (he *HookExecutor) ExecuteConnectionStartHooks(ctx context.Context, conn types.Connection) error {
	interceptors := he.manager.GetConnectionInterceptors()
	
	he.logger.Debug("执行 %d 个连接开始拦截器", len(interceptors))
	
	for _, interceptor := range interceptors {
		if !interceptor.IsEnabled() {
			continue
		}
		
		if err := interceptor.OnConnectionStart(ctx, conn); err != nil {
			he.logger.Error("连接开始拦截器执行失败 %s: %v", interceptor.GetInfo().Name, err)
			continue
		}
	}
	
	return nil
}

// ExecuteConnectionEndHooks 执行连接结束钩子
func (he *HookExecutor) ExecuteConnectionEndHooks(ctx context.Context, conn types.Connection, duration time.Duration) error {
	interceptors := he.manager.GetConnectionInterceptors()
	
	he.logger.Debug("执行 %d 个连接结束拦截器", len(interceptors))
	
	for _, interceptor := range interceptors {
		if !interceptor.IsEnabled() {
			continue
		}
		
		if err := interceptor.OnConnectionEnd(ctx, conn, duration); err != nil {
			he.logger.Error("连接结束拦截器执行失败 %s: %v", interceptor.GetInfo().Name, err)
			continue
		}
	}
	
	return nil
}

// ExecuteDataProcessHooks 执行数据处理钩子
func (he *HookExecutor) ExecuteDataProcessHooks(ctx context.Context, data []byte, direction types.PacketDirection) ([]byte, error) {
	processors := he.manager.GetDataProcessors()
	
	he.logger.Debug("执行 %d 个数据处理器", len(processors))
	
	processedData := data
	
	for _, processor := range processors {
		if !processor.IsEnabled() {
			continue
		}
		
		result, err := processor.ProcessData(ctx, processedData, direction)
		if err != nil {
			he.logger.Error("数据处理器执行失败 %s: %v", processor.GetInfo().Name, err)
			continue
		}
		
		processedData = result
		he.logger.Debug("插件 %s 处理了数据", processor.GetInfo().Name)
	}
	
	return processedData, nil
}

// executeRequestInterceptor 执行请求拦截器（带错误恢复）
func (he *HookExecutor) executeRequestInterceptor(ctx context.Context, interceptor RequestInterceptor, interceptCtx *InterceptContext) (result *InterceptResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("插件 panic: %v", r)
			result = &InterceptResult{
				Continue: true,
				Modified: false,
				Error:    err,
			}
		}
	}()
	
	return interceptor.InterceptRequest(ctx, interceptCtx)
}

// executeResponseInterceptor 执行响应拦截器（带错误恢复）
func (he *HookExecutor) executeResponseInterceptor(ctx context.Context, interceptor ResponseInterceptor, interceptCtx *InterceptContext) (result *InterceptResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("插件 panic: %v", r)
			result = &InterceptResult{
				Continue: true,
				Modified: false,
				Error:    err,
			}
		}
	}()
	
	return interceptor.InterceptResponse(ctx, interceptCtx)
}

// checkAccess 检查插件访问权限
func (he *HookExecutor) checkAccess(plugin Plugin, request *http.Request) bool {
	// 获取插件元数据
	pluginList := he.manager.GetPluginList()
	metadata, exists := pluginList[plugin.GetInfo().Name]
	if !exists {
		return true // 默认允许
	}
	
	if request == nil {
		return true
	}
	
	requestURL := request.URL.String()
	
	// 检查黑名单
	for _, pattern := range metadata.Config.Blacklist {
		if he.matchPattern(requestURL, pattern) {
			he.logger.Debug("请求被黑名单拒绝: %s, 模式: %s", requestURL, pattern)
			return false
		}
	}
	
	// 检查白名单（如果存在白名单）
	if len(metadata.Config.Whitelist) > 0 {
		for _, pattern := range metadata.Config.Whitelist {
			if he.matchPattern(requestURL, pattern) {
				return true
			}
		}
		he.logger.Debug("请求不在白名单中: %s", requestURL)
		return false
	}
	
	return true
}

// matchPattern 匹配URL模式（简单的通配符匹配）
func (he *HookExecutor) matchPattern(url, pattern string) bool {
	// 简单实现：检查URL是否包含模式
	if pattern == "*" {
		return true
	}
	
	// 检查前缀匹配
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(url) >= len(prefix) && url[:len(prefix)] == prefix
	}
	
	// 检查后缀匹配
	if len(pattern) > 0 && pattern[0] == '*' {
		suffix := pattern[1:]
		return len(url) >= len(suffix) && url[len(url)-len(suffix):] == suffix
	}
	
	// 精确匹配
	return url == pattern
}

// GetHookStats 获取钩子统计信息
func (he *HookExecutor) GetHookStats() map[string]interface{} {
	stats := make(map[string]interface{})
	
	stats["request_interceptors"] = len(he.manager.GetRequestInterceptors())
	stats["response_interceptors"] = len(he.manager.GetResponseInterceptors())
	stats["connection_interceptors"] = len(he.manager.GetConnectionInterceptors())
	stats["data_processors"] = len(he.manager.GetDataProcessors())
	
	return stats
}