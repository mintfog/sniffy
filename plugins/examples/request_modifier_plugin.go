// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mintfog/sniffy/plugins"
)

// RequestModifierPlugin 请求修改插件，可以修改请求头、参数等
type RequestModifierPlugin struct {
	*BasePlugin
	modificationCount int64
}

// NewRequestModifierPlugin 创建请求修改插件
func NewRequestModifierPlugin(api plugins.PluginAPI) plugins.Plugin {
	info := plugins.PluginInfo{
		Name:        "request_modifier",
		Version:     "1.0.0",
		Description: "修改HTTP请求头和参数的插件",
		Author:      "sniffy",
		Category:    "modifier",
	}
	
	return &RequestModifierPlugin{
		BasePlugin: NewBasePlugin(info, api),
	}
}

// InterceptRequest 拦截并修改请求
func (rmp *RequestModifierPlugin) InterceptRequest(ctx context.Context, interceptCtx *plugins.InterceptContext) (*plugins.InterceptResult, error) {
	modified := false
	modifications := []string{}
	
	// 添加自定义头部
	if err := rmp.addCustomHeaders(interceptCtx.Request, &modified, &modifications); err != nil {
		return nil, fmt.Errorf("添加自定义头部失败: %w", err)
	}
	
	// 移除指定头部
	if err := rmp.removeHeaders(interceptCtx.Request, &modified, &modifications); err != nil {
		return nil, fmt.Errorf("移除头部失败: %w", err)
	}
	
	// 修改头部值
	if err := rmp.modifyHeaders(interceptCtx.Request, &modified, &modifications); err != nil {
		return nil, fmt.Errorf("修改头部失败: %w", err)
	}
	
	// 添加代理信息
	if err := rmp.addProxyInfo(interceptCtx.Request, &modified, &modifications); err != nil {
		return nil, fmt.Errorf("添加代理信息失败: %w", err)
	}
	
	// 修改用户代理
	if err := rmp.modifyUserAgent(interceptCtx.Request, &modified, &modifications); err != nil {
		return nil, fmt.Errorf("修改用户代理失败: %w", err)
	}
	
	// 修改请求路径
	if err := rmp.modifyRequestPath(interceptCtx.Request, &modified, &modifications); err != nil {
		return nil, fmt.Errorf("修改请求路径失败: %w", err)
	}
	
	if modified {
		rmp.modificationCount++
		rmp.logger.Info("请求已修改: %s %s - 修改项: %s", 
			interceptCtx.Request.Method, 
			interceptCtx.Request.URL.Path,
			strings.Join(modifications, ", "))
		
		// 更新统计信息
		rmp.updateStats()
	}
	
	return &plugins.InterceptResult{
		Continue: true,
		Modified: modified,
		Message:  fmt.Sprintf("请求处理完成，修改项: %d", len(modifications)),
		Metadata: map[string]interface{}{
			"modifications": modifications,
			"count":        len(modifications),
		},
	}, nil
}

// addCustomHeaders 添加自定义头部
func (rmp *RequestModifierPlugin) addCustomHeaders(req *http.Request, modified *bool, modifications *[]string) error {
	customHeaders := rmp.getCustomHeaders()
	
	for name, value := range customHeaders {
		// 检查是否覆盖现有头部
		overwrite := rmp.GetBoolSetting("overwrite_existing_headers", false)
		
		if req.Header.Get(name) == "" || overwrite {
			req.Header.Set(name, value)
			*modified = true
			*modifications = append(*modifications, fmt.Sprintf("添加头部 %s", name))
			rmp.logger.Debug("添加头部: %s = %s", name, value)
		}
	}
	
	return nil
}

// removeHeaders 移除指定头部
func (rmp *RequestModifierPlugin) removeHeaders(req *http.Request, modified *bool, modifications *[]string) error {
	headersToRemove := rmp.getHeadersToRemove()
	
	for _, headerName := range headersToRemove {
		if req.Header.Get(headerName) != "" {
			req.Header.Del(headerName)
			*modified = true
			*modifications = append(*modifications, fmt.Sprintf("移除头部 %s", headerName))
			rmp.logger.Debug("移除头部: %s", headerName)
		}
	}
	
	return nil
}

// modifyHeaders 修改头部值
func (rmp *RequestModifierPlugin) modifyHeaders(req *http.Request, modified *bool, modifications *[]string) error {
	headerModifications := rmp.getHeaderModifications()
	
	for headerName, modification := range headerModifications {
		if existingValue := req.Header.Get(headerName); existingValue != "" {
			newValue := rmp.applyModification(existingValue, modification)
			if newValue != existingValue {
				req.Header.Set(headerName, newValue)
				*modified = true
				*modifications = append(*modifications, fmt.Sprintf("修改头部 %s", headerName))
				rmp.logger.Debug("修改头部: %s = %s -> %s", headerName, existingValue, newValue)
			}
		}
	}
	
	return nil
}

// addProxyInfo 添加代理信息
func (rmp *RequestModifierPlugin) addProxyInfo(req *http.Request, modified *bool, modifications *[]string) error {
	if rmp.GetBoolSetting("add_proxy_headers", true) {
		// 添加 X-Forwarded-For
		if req.Header.Get("X-Forwarded-For") == "" {
			clientIP := rmp.extractClientIP(req)
			if clientIP != "" {
				req.Header.Set("X-Forwarded-For", clientIP)
				*modified = true
				*modifications = append(*modifications, "添加 X-Forwarded-For")
			}
		}
		
		// 添加 X-Forwarded-Proto
		if req.Header.Get("X-Forwarded-Proto") == "" {
			proto := "http"
			if req.TLS != nil {
				proto = "https"
			}
			req.Header.Set("X-Forwarded-Proto", proto)
			*modified = true
			*modifications = append(*modifications, "添加 X-Forwarded-Proto")
		}
		
		// 添加 X-Forwarded-Host
		if req.Header.Get("X-Forwarded-Host") == "" && req.Host != "" {
			req.Header.Set("X-Forwarded-Host", req.Host)
			*modified = true
			*modifications = append(*modifications, "添加 X-Forwarded-Host")
		}
		
		// 添加代理标识
		proxyName := rmp.GetStringSetting("proxy_name", "sniffy")
		req.Header.Set("X-Proxy-By", proxyName)
		*modified = true
		*modifications = append(*modifications, "添加代理标识")
	}
	
	return nil
}

// modifyUserAgent 修改用户代理
func (rmp *RequestModifierPlugin) modifyUserAgent(req *http.Request, modified *bool, modifications *[]string) error {
	uaModification := rmp.GetStringSetting("user_agent_modification", "")
	
	switch uaModification {
	case "append":
		suffix := rmp.GetStringSetting("user_agent_suffix", " (via sniffy)")
		currentUA := req.UserAgent()
		if currentUA != "" && !strings.Contains(currentUA, suffix) {
			req.Header.Set("User-Agent", currentUA+suffix)
			*modified = true
			*modifications = append(*modifications, "修改 User-Agent")
		}
		
	case "replace":
		newUA := rmp.GetStringSetting("user_agent_value", "")
		if newUA != "" && req.UserAgent() != newUA {
			req.Header.Set("User-Agent", newUA)
			*modified = true
			*modifications = append(*modifications, "替换 User-Agent")
		}
		
	case "remove":
		if req.Header.Get("User-Agent") != "" {
			req.Header.Del("User-Agent")
			*modified = true
			*modifications = append(*modifications, "移除 User-Agent")
		}
	}
	
	return nil
}

// modifyRequestPath 修改请求路径
func (rmp *RequestModifierPlugin) modifyRequestPath(req *http.Request, modified *bool, modifications *[]string) error {
	pathRules := rmp.getPathModificationRules()
	
	originalPath := req.URL.Path
	newPath := originalPath
	
	for _, rule := range pathRules {
		if rule.Matches(originalPath) {
			newPath = rule.Apply(originalPath)
			break
		}
	}
	
	if newPath != originalPath {
		req.URL.Path = newPath
		*modified = true
		*modifications = append(*modifications, fmt.Sprintf("修改路径 %s -> %s", originalPath, newPath))
		rmp.logger.Debug("修改请求路径: %s -> %s", originalPath, newPath)
	}
	
	return nil
}

// 辅助方法

// getCustomHeaders 获取自定义头部配置
func (rmp *RequestModifierPlugin) getCustomHeaders() map[string]string {
	headers := make(map[string]string)
	
	if customHeaders := rmp.GetSetting("custom_headers", nil); customHeaders != nil {
		if headerMap, ok := customHeaders.(map[string]interface{}); ok {
			for name, value := range headerMap {
				if str, ok := value.(string); ok {
					headers[name] = str
				}
			}
		}
	}
	
	// 添加时间戳头部
	if rmp.GetBoolSetting("add_timestamp", false) {
		headers["X-Timestamp"] = time.Now().Format(time.RFC3339)
	}
	
	return headers
}

// getHeadersToRemove 获取要移除的头部列表
func (rmp *RequestModifierPlugin) getHeadersToRemove() []string {
	if headersToRemove := rmp.GetSetting("remove_headers", nil); headersToRemove != nil {
		if headerList, ok := headersToRemove.([]interface{}); ok {
			var result []string
			for _, header := range headerList {
				if str, ok := header.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	
	return []string{}
}

// getHeaderModifications 获取头部修改配置
func (rmp *RequestModifierPlugin) getHeaderModifications() map[string]HeaderModification {
	modifications := make(map[string]HeaderModification)
	
	if headerMods := rmp.GetSetting("header_modifications", nil); headerMods != nil {
		if modMap, ok := headerMods.(map[string]interface{}); ok {
			for headerName, modConfig := range modMap {
				if modConfigMap, ok := modConfig.(map[string]interface{}); ok {
					modifications[headerName] = HeaderModification{
						Type:  getString(modConfigMap, "type"),
						Value: getString(modConfigMap, "value"),
						Pattern: getString(modConfigMap, "pattern"),
					}
				}
			}
		}
	}
	
	return modifications
}

// getPathModificationRules 获取路径修改规则
func (rmp *RequestModifierPlugin) getPathModificationRules() []PathRule {
	var rules []PathRule
	
	if pathRules := rmp.GetSetting("path_rules", nil); pathRules != nil {
		if ruleList, ok := pathRules.([]interface{}); ok {
			for _, rule := range ruleList {
				if ruleMap, ok := rule.(map[string]interface{}); ok {
					rules = append(rules, PathRule{
						Pattern:     getString(ruleMap, "pattern"),
						Replacement: getString(ruleMap, "replacement"),
						Type:        getString(ruleMap, "type"),
					})
				}
			}
		}
	}
	
	return rules
}

// extractClientIP 提取客户端IP
func (rmp *RequestModifierPlugin) extractClientIP(req *http.Request) string {
	// 从 RemoteAddr 提取IP
	if req.RemoteAddr != "" {
		if idx := strings.LastIndex(req.RemoteAddr, ":"); idx > 0 {
			return req.RemoteAddr[:idx]
		}
		return req.RemoteAddr
	}
	return ""
}

// applyModification 应用头部修改
func (rmp *RequestModifierPlugin) applyModification(value string, mod HeaderModification) string {
	switch mod.Type {
	case "append":
		return value + mod.Value
	case "prepend":
		return mod.Value + value
	case "replace":
		return mod.Value
	case "regex":
		// 这里可以实现正则表达式替换
		return strings.ReplaceAll(value, mod.Pattern, mod.Value)
	default:
		return value
	}
}

// updateStats 更新统计信息
func (rmp *RequestModifierPlugin) updateStats() {
	stats := map[string]interface{}{
		"modifications": rmp.modificationCount,
		"last_activity": time.Now().Format(time.RFC3339),
	}
	
	rmp.GetAPI().StoreData("request_modifier_stats", stats)
}

// 辅助结构体

// HeaderModification 头部修改配置
type HeaderModification struct {
	Type    string `json:"type"`    // append, prepend, replace, regex
	Value   string `json:"value"`   // 新值
	Pattern string `json:"pattern"` // 匹配模式（用于regex类型）
}

// PathRule 路径修改规则
type PathRule struct {
	Pattern     string `json:"pattern"`     // 匹配模式
	Replacement string `json:"replacement"` // 替换值
	Type        string `json:"type"`        // exact, prefix, suffix, regex
}

// Matches 检查路径是否匹配规则
func (pr *PathRule) Matches(path string) bool {
	switch pr.Type {
	case "exact":
		return path == pr.Pattern
	case "prefix":
		return strings.HasPrefix(path, pr.Pattern)
	case "suffix":
		return strings.HasSuffix(path, pr.Pattern)
	case "contains":
		return strings.Contains(path, pr.Pattern)
	default:
		return false
	}
}

// Apply 应用路径修改规则
func (pr *PathRule) Apply(path string) string {
	switch pr.Type {
	case "exact":
		if path == pr.Pattern {
			return pr.Replacement
		}
	case "prefix":
		if strings.HasPrefix(path, pr.Pattern) {
			return strings.Replace(path, pr.Pattern, pr.Replacement, 1)
		}
	case "suffix":
		if strings.HasSuffix(path, pr.Pattern) {
			return strings.TrimSuffix(path, pr.Pattern) + pr.Replacement
		}
	case "contains":
		return strings.ReplaceAll(path, pr.Pattern, pr.Replacement)
	}
	return path
}

// getString 从map中获取字符串值
func getString(m map[string]interface{}, key string) string {
	if value, exists := m[key]; exists {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return ""
}

// 确保实现了正确的接口
var _ plugins.RequestInterceptor = (*RequestModifierPlugin)(nil)