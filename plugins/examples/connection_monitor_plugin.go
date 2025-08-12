// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

// ConnectionMonitorPlugin 连接监控插件，监控连接状态和统计信息
type ConnectionMonitorPlugin struct {
	*BasePlugin
	connections      map[string]*ConnectionInfo
	connectionsMutex sync.RWMutex
	totalConnections int64
	activeConnections int64
}

// ConnectionInfo 连接信息
type ConnectionInfo struct {
	RemoteAddr   string    `json:"remote_addr"`
	LocalAddr    string    `json:"local_addr"`
	StartTime    time.Time `json:"start_time"`
	LastActivity time.Time `json:"last_activity"`
	BytesRead    int64     `json:"bytes_read"`
	BytesWritten int64     `json:"bytes_written"`
	Protocol     string    `json:"protocol"`
	Duration     time.Duration `json:"duration"`
}

// NewConnectionMonitorPlugin 创建连接监控插件
func NewConnectionMonitorPlugin(api plugins.PluginAPI) plugins.Plugin {
	info := plugins.PluginInfo{
		Name:        "connection_monitor",
		Version:     "1.0.0",
		Description: "监控TCP连接状态和统计信息的插件",
		Author:      "sniffy",
		Category:    "monitoring",
	}
	
	return &ConnectionMonitorPlugin{
		BasePlugin:  NewBasePlugin(info, api),
		connections: make(map[string]*ConnectionInfo),
	}
}

// Start 启动插件
func (cmp *ConnectionMonitorPlugin) Start(ctx context.Context) error {
	if err := cmp.BasePlugin.Start(ctx); err != nil {
		return err
	}
	
	// 启动定期清理和统计任务
	go cmp.startPeriodicTasks(ctx)
	
	return nil
}

// OnConnectionStart 连接开始时调用
func (cmp *ConnectionMonitorPlugin) OnConnectionStart(ctx context.Context, conn types.Connection) error {
	cmp.connectionsMutex.Lock()
	defer cmp.connectionsMutex.Unlock()
	
	remoteAddr := conn.GetConn().RemoteAddr().String()
	localAddr := conn.GetConn().LocalAddr().String()
	
	connInfo := &ConnectionInfo{
		RemoteAddr:   remoteAddr,
		LocalAddr:    localAddr,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
		Protocol:     "TCP",
	}
	
	// 使用远程地址作为唯一标识
	connKey := cmp.getConnectionKey(conn)
	cmp.connections[connKey] = connInfo
	
	cmp.totalConnections++
	cmp.activeConnections++
	
	cmp.logger.Info("新连接建立: %s -> %s (活跃连接: %d)", remoteAddr, localAddr, cmp.activeConnections)
	
	// 检查连接限制
	if err := cmp.checkConnectionLimits(); err != nil {
		cmp.logger.Warn("连接限制检查: %v", err)
	}
	
	// 记录连接事件
	cmp.recordConnectionEvent("connect", connInfo)
	
	// 更新统计信息
	cmp.updateConnectionStats()
	
	return nil
}

// OnConnectionEnd 连接结束时调用
func (cmp *ConnectionMonitorPlugin) OnConnectionEnd(ctx context.Context, conn types.Connection, duration time.Duration) error {
	cmp.connectionsMutex.Lock()
	defer cmp.connectionsMutex.Unlock()
	
	connKey := cmp.getConnectionKey(conn)
	
	if connInfo, exists := cmp.connections[connKey]; exists {
		connInfo.Duration = duration
		
		cmp.logger.Info("连接结束: %s -> %s (持续时间: %v)", 
			connInfo.RemoteAddr, connInfo.LocalAddr, duration)
		
		// 记录连接事件
		cmp.recordConnectionEvent("disconnect", connInfo)
		
		// 移除连接记录
		delete(cmp.connections, connKey)
		cmp.activeConnections--
		
		// 检查异常连接
		if err := cmp.checkAbnormalConnection(connInfo); err != nil {
			cmp.logger.Warn("异常连接检测: %v", err)
		}
	}
	
	// 更新统计信息
	cmp.updateConnectionStats()
	
	return nil
}

// ProcessData 处理数据（用于统计流量）
func (cmp *ConnectionMonitorPlugin) ProcessData(ctx context.Context, data []byte, direction types.PacketDirection) ([]byte, error) {
	dataSize := int64(len(data))
	
	// 这里我们无法直接获取连接信息，但可以统计总流量
	cmp.updateTrafficStats(dataSize, direction)
	
	return data, nil
}

// startPeriodicTasks 启动定期任务
func (cmp *ConnectionMonitorPlugin) startPeriodicTasks(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(cmp.GetIntSetting("stats_interval_seconds", 60)) * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cmp.performPeriodicTasks()
		}
	}
}

// performPeriodicTasks 执行定期任务
func (cmp *ConnectionMonitorPlugin) performPeriodicTasks() {
	// 清理过期连接
	cmp.cleanupStaleConnections()
	
	// 生成统计报告
	if cmp.GetBoolSetting("generate_periodic_reports", true) {
		cmp.generateStatsReport()
	}
	
	// 检查连接健康状态
	cmp.checkConnectionHealth()
	
	// 更新统计信息
	cmp.updateConnectionStats()
}

// cleanupStaleConnections 清理过期连接
func (cmp *ConnectionMonitorPlugin) cleanupStaleConnections() {
	cmp.connectionsMutex.Lock()
	defer cmp.connectionsMutex.Unlock()
	
	timeout := time.Duration(cmp.GetIntSetting("connection_timeout_minutes", 30)) * time.Minute
	now := time.Now()
	
	for connKey, connInfo := range cmp.connections {
		if now.Sub(connInfo.LastActivity) > timeout {
			cmp.logger.Warn("清理过期连接: %s (最后活动: %v)", 
				connInfo.RemoteAddr, connInfo.LastActivity)
			
			delete(cmp.connections, connKey)
			cmp.activeConnections--
		}
	}
}

// generateStatsReport 生成统计报告
func (cmp *ConnectionMonitorPlugin) generateStatsReport() {
	cmp.connectionsMutex.RLock()
	activeCount := len(cmp.connections)
	cmp.connectionsMutex.RUnlock()
	
	stats := map[string]interface{}{
		"timestamp":          time.Now().Format(time.RFC3339),
		"total_connections":  cmp.totalConnections,
		"active_connections": activeCount,
		"connection_details": cmp.getConnectionSummary(),
	}
	
	cmp.logger.Info("连接统计报告: 总连接 %d, 活跃连接 %d", 
		cmp.totalConnections, activeCount)
	
	// 发送通知
	if cmp.GetBoolSetting("send_notifications", false) {
		title := "连接监控报告"
		message := fmt.Sprintf("总连接: %d, 活跃连接: %d", cmp.totalConnections, activeCount)
		cmp.GetAPI().SendNotification(title, message)
	}
	
	// 存储统计信息
	cmp.GetAPI().StoreData("connection_stats", stats)
}

// checkConnectionHealth 检查连接健康状态
func (cmp *ConnectionMonitorPlugin) checkConnectionHealth() {
	cmp.connectionsMutex.RLock()
	defer cmp.connectionsMutex.RUnlock()
	
	maxConnections := cmp.GetIntSetting("max_connections", 1000)
	currentConnections := len(cmp.connections)
	
	if currentConnections > maxConnections {
		cmp.logger.Warn("连接数超过限制: 当前 %d, 最大 %d", currentConnections, maxConnections)
		
		// 发送警告通知
		title := "连接数警告"
		message := fmt.Sprintf("当前连接数 %d 超过限制 %d", currentConnections, maxConnections)
		cmp.GetAPI().SendNotification(title, message)
	}
	
	// 检查连接持续时间
	maxDuration := time.Duration(cmp.GetIntSetting("max_connection_duration_hours", 24)) * time.Hour
	now := time.Now()
	
	for _, connInfo := range cmp.connections {
		if now.Sub(connInfo.StartTime) > maxDuration {
			cmp.logger.Warn("发现长时间连接: %s (持续时间: %v)", 
				connInfo.RemoteAddr, now.Sub(connInfo.StartTime))
		}
	}
}

// checkConnectionLimits 检查连接限制
func (cmp *ConnectionMonitorPlugin) checkConnectionLimits() error {
	maxConnections := cmp.GetIntSetting("max_connections", 1000)
	
	if cmp.activeConnections > int64(maxConnections) {
		return fmt.Errorf("连接数超过限制: %d/%d", cmp.activeConnections, maxConnections)
	}
	
	return nil
}

// checkAbnormalConnection 检查异常连接
func (cmp *ConnectionMonitorPlugin) checkAbnormalConnection(connInfo *ConnectionInfo) error {
	minDuration := time.Duration(cmp.GetIntSetting("min_connection_duration_seconds", 1)) * time.Second
	
	if connInfo.Duration < minDuration {
		return fmt.Errorf("连接持续时间过短: %v", connInfo.Duration)
	}
	
	return nil
}

// recordConnectionEvent 记录连接事件
func (cmp *ConnectionMonitorPlugin) recordConnectionEvent(eventType string, connInfo *ConnectionInfo) {
	if !cmp.GetBoolSetting("record_events", true) {
		return
	}
	
	event := map[string]interface{}{
		"timestamp":   time.Now().Format(time.RFC3339),
		"event_type":  eventType,
		"remote_addr": connInfo.RemoteAddr,
		"local_addr":  connInfo.LocalAddr,
		"duration":    connInfo.Duration.String(),
	}
	
	// 存储事件
	eventKey := fmt.Sprintf("connection_event_%d", time.Now().UnixNano())
	cmp.GetAPI().StoreData(eventKey, event)
}

// updateConnectionStats 更新连接统计信息
func (cmp *ConnectionMonitorPlugin) updateConnectionStats() {
	stats := map[string]interface{}{
		"total_connections":  cmp.totalConnections,
		"active_connections": cmp.activeConnections,
		"last_update":        time.Now().Format(time.RFC3339),
	}
	
	cmp.GetAPI().StoreData("connection_monitor_stats", stats)
}

// updateTrafficStats 更新流量统计
func (cmp *ConnectionMonitorPlugin) updateTrafficStats(bytes int64, direction types.PacketDirection) {
	// 这里可以实现流量统计
	// 例如按方向统计流量
	statsKey := fmt.Sprintf("traffic_%s", direction.String())
	
	if currentStats, err := cmp.GetAPI().GetData(statsKey); err == nil {
		if stats, ok := currentStats.(map[string]interface{}); ok {
			if totalBytes, ok := stats["total_bytes"].(int64); ok {
				stats["total_bytes"] = totalBytes + bytes
			}
			cmp.GetAPI().StoreData(statsKey, stats)
		}
	} else {
		// 初始化统计信息
		stats := map[string]interface{}{
			"total_bytes": bytes,
			"direction":   direction.String(),
			"last_update": time.Now().Format(time.RFC3339),
		}
		cmp.GetAPI().StoreData(statsKey, stats)
	}
}

// getConnectionSummary 获取连接摘要
func (cmp *ConnectionMonitorPlugin) getConnectionSummary() map[string]interface{} {
	cmp.connectionsMutex.RLock()
	defer cmp.connectionsMutex.RUnlock()
	
	summary := map[string]interface{}{
		"count": len(cmp.connections),
		"connections": make([]map[string]interface{}, 0, len(cmp.connections)),
	}
	
	for _, connInfo := range cmp.connections {
		connectionSummary := map[string]interface{}{
			"remote_addr":   connInfo.RemoteAddr,
			"local_addr":    connInfo.LocalAddr,
			"start_time":    connInfo.StartTime.Format(time.RFC3339),
			"duration":      time.Since(connInfo.StartTime).String(),
			"last_activity": connInfo.LastActivity.Format(time.RFC3339),
		}
		
		connections := summary["connections"].([]map[string]interface{})
		summary["connections"] = append(connections, connectionSummary)
	}
	
	return summary
}

// getConnectionKey 获取连接唯一标识
func (cmp *ConnectionMonitorPlugin) getConnectionKey(conn types.Connection) string {
	return conn.GetConn().RemoteAddr().String()
}

// GetStats 获取插件统计信息
func (cmp *ConnectionMonitorPlugin) GetStats() map[string]interface{} {
	cmp.connectionsMutex.RLock()
	defer cmp.connectionsMutex.RUnlock()
	
	return map[string]interface{}{
		"total_connections":  cmp.totalConnections,
		"active_connections": len(cmp.connections),
		"enabled":            cmp.IsEnabled(),
		"priority":           cmp.GetPriority(),
	}
}

// 确保实现了正确的接口
var _ plugins.ConnectionInterceptor = (*ConnectionMonitorPlugin)(nil)
var _ plugins.DataProcessor = (*ConnectionMonitorPlugin)(nil)