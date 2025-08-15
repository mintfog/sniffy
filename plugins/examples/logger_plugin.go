// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mintfog/sniffy/plugins"
)

// LoggerPlugin 日志插件，记录请求和响应信息
type LoggerPlugin struct {
	*BasePlugin
	requestCount  int64
	responseCount int64
}

// NewLoggerPlugin 创建日志插件
func NewLoggerPlugin(api plugins.PluginAPI) plugins.Plugin {
	info := plugins.PluginInfo{
		Name:        "logger",
		Version:     "1.0.0",
		Description: "记录HTTP请求和响应的日志插件",
		Author:      "sniffy",
		Category:    "logging",
	}
	
	return &LoggerPlugin{
		BasePlugin: NewBasePlugin(info, api),
	}
}

// InterceptRequest 拦截并记录请求
func (lp *LoggerPlugin) InterceptRequest(ctx context.Context, interceptCtx *plugins.InterceptContext) (*plugins.InterceptResult, error) {
	lp.requestCount++
	
	// 检查是否启用请求日志
	if !lp.GetBoolSetting("log_requests", true) {
		return &plugins.InterceptResult{Continue: true}, nil
	}
	
	// 获取配置
	logHeaders := lp.GetBoolSetting("log_headers", true)
	logBody := lp.GetBoolSetting("log_body", false)
	maxBodySize := lp.GetIntSetting("max_body_size", 1024)
	sensitiveHeaders := lp.getSensitiveHeaders()
	
	// 构建日志信息
	logInfo := map[string]interface{}{
		"timestamp":    interceptCtx.Timestamp.Format(time.RFC3339),
		"type":         "request",
		"method":       interceptCtx.Request.Method,
		"url":          interceptCtx.Request.URL.String(),
		"remote_addr":  interceptCtx.Request.RemoteAddr,
		"user_agent":   interceptCtx.Request.UserAgent(),
		"content_length": interceptCtx.Request.ContentLength,
		"request_count": lp.requestCount,
	}
	
	// 记录请求头
	if logHeaders && interceptCtx.Request.Header != nil {
		headers := make(map[string]string)
		for key, values := range interceptCtx.Request.Header {
			if lp.isSensitiveHeader(key, sensitiveHeaders) {
				headers[key] = "[MASKED]"
			} else {
				headers[key] = strings.Join(values, ", ")
			}
		}
		logInfo["headers"] = headers
	}
	
	// 记录请求体
	if logBody && len(interceptCtx.RequestBody) > 0 {
		bodyStr := string(interceptCtx.RequestBody)
		if len(bodyStr) > maxBodySize {
			bodyStr = bodyStr[:maxBodySize] + "...[TRUNCATED]"
		}
		logInfo["body"] = bodyStr
	}
	
	// 记录查询参数
	if interceptCtx.Request.URL.RawQuery != "" {
		logInfo["query"] = interceptCtx.Request.URL.RawQuery
	}
	
	// 输出日志
	lp.logRequestInfo(logInfo)
	
	// 存储统计信息
	lp.updateStats("requests")
	
	return &plugins.InterceptResult{
		Continue: true,
		Modified: false,
		Message:  fmt.Sprintf("请求已记录: %s %s", interceptCtx.Request.Method, interceptCtx.Request.URL.Path),
	}, nil
}

// InterceptResponse 拦截并记录响应
func (lp *LoggerPlugin) InterceptResponse(ctx context.Context, interceptCtx *plugins.InterceptContext) (*plugins.InterceptResult, error) {
	lp.responseCount++
	
	// 检查是否启用响应日志
	if !lp.GetBoolSetting("log_responses", true) {
		return &plugins.InterceptResult{Continue: true}, nil
	}
	
	// 获取配置
	logHeaders := lp.GetBoolSetting("log_headers", true)
	logBody := lp.GetBoolSetting("log_body", false)
	maxBodySize := lp.GetIntSetting("max_body_size", 1024)
	sensitiveHeaders := lp.getSensitiveHeaders()
	
	// 构建日志信息
	logInfo := map[string]interface{}{
		"timestamp":      interceptCtx.Timestamp.Format(time.RFC3339),
		"type":           "response",
		"status_code":    interceptCtx.Response.StatusCode,
		"status":         interceptCtx.Response.Status,
		"content_length": interceptCtx.Response.ContentLength,
		"response_count": lp.responseCount,
	}
	
	// 记录响应头
	if logHeaders && interceptCtx.Response.Header != nil {
		headers := make(map[string]string)
		for key, values := range interceptCtx.Response.Header {
			if lp.isSensitiveHeader(key, sensitiveHeaders) {
				headers[key] = "[MASKED]"
			} else {
				headers[key] = strings.Join(values, ", ")
			}
		}
		logInfo["headers"] = headers
	}
	
	// 记录响应体
	if logBody && len(interceptCtx.ResponseBody) > 0 {
		bodyStr := string(interceptCtx.ResponseBody)
		if len(bodyStr) > maxBodySize {
			bodyStr = bodyStr[:maxBodySize] + "...[TRUNCATED]"
		}
		logInfo["body"] = bodyStr
	}
	
	// 输出日志
	lp.logResponseInfo(logInfo)
	
	// 存储统计信息
	lp.updateStats("responses")
	
	return &plugins.InterceptResult{
		Continue: true,
		Modified: false,
		Message:  fmt.Sprintf("响应已记录: %d %s", interceptCtx.Response.StatusCode, interceptCtx.Response.Status),
	}, nil
}

// logRequestInfo 输出请求日志
func (lp *LoggerPlugin) logRequestInfo(logInfo map[string]interface{}) {
	format := lp.GetStringSetting("log_format", "json")
	
	switch format {
	case "json":
		jsonData, _ := json.MarshalIndent(logInfo, "", "  ")
		lp.logger.Info("请求日志:\n%s", string(jsonData))
	case "simple":
		lp.logger.Info("请求: %s %s [%s] UA: %s",
			logInfo["method"],
			logInfo["url"],
			logInfo["remote_addr"],
			logInfo["user_agent"])
	default:
		lp.logger.Info("请求日志: %v", logInfo)
	}
}

// logResponseInfo 输出响应日志
func (lp *LoggerPlugin) logResponseInfo(logInfo map[string]interface{}) {
	format := lp.GetStringSetting("log_format", "json")
	
	switch format {
	case "json":
		jsonData, _ := json.MarshalIndent(logInfo, "", "  ")
		lp.logger.Info("响应日志:\n%s", string(jsonData))
	case "simple":
		lp.logger.Info("响应: %d %s 长度: %v",
			logInfo["status_code"],
			logInfo["status"],
			logInfo["content_length"])
	default:
		lp.logger.Info("响应日志: %v", logInfo)
	}
}

// getSensitiveHeaders 获取敏感头部列表
func (lp *LoggerPlugin) getSensitiveHeaders() []string {
	defaultSensitive := []string{
		"Authorization",
		"Cookie",
		"Set-Cookie",
		"X-Auth-Token",
		"X-API-Key",
		"Proxy-Authorization",
	}
	
	if customSensitive := lp.GetSetting("sensitive_headers", nil); customSensitive != nil {
		if headers, ok := customSensitive.([]interface{}); ok {
			var result []string
			for _, h := range headers {
				if str, ok := h.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	
	return defaultSensitive
}

// isSensitiveHeader 检查是否为敏感头部
func (lp *LoggerPlugin) isSensitiveHeader(header string, sensitiveHeaders []string) bool {
	headerLower := strings.ToLower(header)
	for _, sensitive := range sensitiveHeaders {
		if strings.ToLower(sensitive) == headerLower {
			return true
		}
	}
	return false
}

// updateStats 更新统计信息
func (lp *LoggerPlugin) updateStats(statType string) {
	stats := map[string]interface{}{
		"requests":  lp.requestCount,
		"responses": lp.responseCount,
		"last_activity": time.Now().Format(time.RFC3339),
	}
	
	lp.GetAPI().StoreData("logger_stats", stats)
}

// GetStats 获取统计信息
func (lp *LoggerPlugin) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"requests":  lp.requestCount,
		"responses": lp.responseCount,
		"enabled":   lp.IsEnabled(),
		"priority":  lp.GetPriority(),
	}
}

// 确保实现了正确的接口
var _ plugins.RequestInterceptor = (*LoggerPlugin)(nil)
var _ plugins.ResponseInterceptor = (*LoggerPlugin)(nil)