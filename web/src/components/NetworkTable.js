import React from 'react';
import { getRequestTypeIcon, getStatusColorClass } from '../mockData';
import './NetworkTable.css';

const NetworkTable = ({ requests, selectedRequest, onRequestSelect }) => {
  const formatTime = (timestamp) => {
    const now = new Date();
    const diff = now - timestamp;
    if (diff < 1000) return '刚刚';
    if (diff < 60000) return `${Math.floor(diff / 1000)}秒前`;
    if (diff < 3600000) return `${Math.floor(diff / 60000)}分钟前`;
    return timestamp.toLocaleTimeString();
  };

  const formatSize = (sizeStr) => {
    return sizeStr;
  };

  const getMethodClass = (method) => {
    const classes = {
      'GET': 'method-get',
      'POST': 'method-post',
      'PUT': 'method-put',
      'DELETE': 'method-delete',
      'PATCH': 'method-patch'
    };
    return classes[method] || 'method-other';
  };

  return (
    <div className="network-table">
      <div className="table-header">
        <div className="header-cell name-column">名称</div>
        <div className="header-cell method-column">方法</div>
        <div className="header-cell status-column">状态</div>
        <div className="header-cell type-column">类型</div>
        <div className="header-cell size-column">大小</div>
        <div className="header-cell time-column">时间</div>
        <div className="header-cell timestamp-column">时刻</div>
      </div>
      
      <div className="table-body">
        {requests.map((request) => (
          <div
            key={request.id}
            className={`table-row ${selectedRequest?.id === request.id ? 'selected' : ''} ${request.intercepted ? 'intercepted' : ''}`}
            onClick={() => onRequestSelect(request)}
          >
            <div className="cell name-column">
              <div className="request-name">
                <span className="request-icon">
                  {getRequestTypeIcon(request.type)}
                </span>
                <span className="request-path" title={request.url}>
                  {request.path}
                </span>
                {request.intercepted && (
                  <span className="intercept-badge" title="已拦截">🔓</span>
                )}
              </div>
            </div>
            
            <div className={`cell method-column ${getMethodClass(request.method)}`}>
              {request.method}
            </div>
            
            <div className={`cell status-column ${getStatusColorClass(request.status)}`}>
              {request.status}
            </div>
            
            <div className="cell type-column">
              {request.type}
            </div>
            
            <div className="cell size-column">
              {formatSize(request.size)}
            </div>
            
            <div className="cell time-column">
              {request.time}
            </div>
            
            <div className="cell timestamp-column">
              {formatTime(request.timestamp)}
            </div>
          </div>
        ))}
        
        {requests.length === 0 && (
          <div className="empty-state">
            <div className="empty-icon">📡</div>
            <div className="empty-text">暂无网络请求</div>
            <div className="empty-hint">开始录制以查看网络活动</div>
          </div>
        )}
      </div>
    </div>
  );
};

export default NetworkTable;