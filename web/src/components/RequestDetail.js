import React, { useState } from 'react';
import { getStatusColorClass } from '../mockData';
import './RequestDetail.css';

const RequestDetail = ({ request, onClose }) => {
  const [activeTab, setActiveTab] = useState('headers');

  const formatHeaders = (headers) => {
    return Object.entries(headers || {}).map(([key, value]) => ({
      name: key,
      value: value
    }));
  };

  const formatJson = (jsonString) => {
    try {
      return JSON.stringify(JSON.parse(jsonString), null, 2);
    } catch {
      return jsonString;
    }
  };

  const copyToClipboard = (text) => {
    navigator.clipboard.writeText(text).then(() => {
      // 可以添加提示消息
    });
  };

  const tabs = [
    { id: 'headers', label: 'Headers', icon: '📄' },
    { id: 'preview', label: 'Preview', icon: '👁️' },
    { id: 'response', label: 'Response', icon: '📥' },
    { id: 'request', label: 'Request', icon: '📤' },
    { id: 'timing', label: 'Timing', icon: '⏱️' }
  ];

  const renderTabContent = () => {
    switch (activeTab) {
      case 'headers':
        return (
          <div className="tab-content">
            <div className="headers-section">
              <div className="section-title">
                <h3>General</h3>
              </div>
              <div className="header-table">
                <div className="header-row">
                  <span className="header-name">Request URL:</span>
                  <span className="header-value">{request.url}</span>
                </div>
                <div className="header-row">
                  <span className="header-name">Request Method:</span>
                  <span className="header-value">{request.method}</span>
                </div>
                <div className="header-row">
                  <span className="header-name">Status Code:</span>
                  <span className={`header-value ${getStatusColorClass(request.status)}`}>
                    {request.status} {request.statusText}
                  </span>
                </div>
                <div className="header-row">
                  <span className="header-name">Remote Address:</span>
                  <span className="header-value">{request.domain}:443</span>
                </div>
              </div>
            </div>

            <div className="headers-section">
              <div className="section-title">
                <h3>Response Headers</h3>
                <button 
                  className="copy-button"
                  onClick={() => copyToClipboard(JSON.stringify(request.headers.response, null, 2))}
                  title="复制响应头"
                >
                  📋
                </button>
              </div>
              <div className="header-table">
                {formatHeaders(request.headers.response).map((header, index) => (
                  <div key={index} className="header-row">
                    <span className="header-name">{header.name}:</span>
                    <span className="header-value">{header.value}</span>
                  </div>
                ))}
              </div>
            </div>

            <div className="headers-section">
              <div className="section-title">
                <h3>Request Headers</h3>
                <button 
                  className="copy-button"
                  onClick={() => copyToClipboard(JSON.stringify(request.headers.request, null, 2))}
                  title="复制请求头"
                >
                  📋
                </button>
              </div>
              <div className="header-table">
                {formatHeaders(request.headers.request).map((header, index) => (
                  <div key={index} className="header-row">
                    <span className="header-name">{header.name}:</span>
                    <span className="header-value">{header.value}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        );

      case 'preview':
        return (
          <div className="tab-content">
            <div className="preview-content">
              {request.responseBody ? (
                <div className="json-preview">
                  <div className="section-title">
                    <h3>响应预览</h3>
                    <button 
                      className="copy-button"
                      onClick={() => copyToClipboard(request.responseBody)}
                      title="复制响应内容"
                    >
                      📋
                    </button>
                  </div>
                  <pre className="json-content">
                    {formatJson(request.responseBody)}
                  </pre>
                </div>
              ) : (
                <div className="empty-content">
                  <div className="empty-icon">📄</div>
                  <div className="empty-text">无预览内容</div>
                </div>
              )}
            </div>
          </div>
        );

      case 'response':
        return (
          <div className="tab-content">
            <div className="response-content">
              <div className="section-title">
                <h3>响应体</h3>
                <div className="response-info">
                  <span className="content-type">
                    {request.headers.response?.['Content-Type'] || 'unknown'}
                  </span>
                  <button 
                    className="copy-button"
                    onClick={() => copyToClipboard(request.responseBody || '')}
                    title="复制响应体"
                  >
                    📋
                  </button>
                </div>
              </div>
              <div className="raw-content">
                <pre>{request.responseBody || '无响应内容'}</pre>
              </div>
            </div>
          </div>
        );

      case 'request':
        return (
          <div className="tab-content">
            <div className="request-content">
              {request.requestBody ? (
                <>
                  <div className="section-title">
                    <h3>请求体</h3>
                    <button 
                      className="copy-button"
                      onClick={() => copyToClipboard(request.requestBody)}
                      title="复制请求体"
                    >
                      📋
                    </button>
                  </div>
                  <div className="raw-content">
                    <pre>{formatJson(request.requestBody)}</pre>
                  </div>
                </>
              ) : (
                <div className="empty-content">
                  <div className="empty-icon">📤</div>
                  <div className="empty-text">无请求体</div>
                </div>
              )}
            </div>
          </div>
        );

      case 'timing':
        return (
          <div className="tab-content">
            <div className="timing-content">
              <div className="section-title">
                <h3>请求时间线</h3>
              </div>
              <div className="timing-chart">
                <div className="timing-row">
                  <span className="timing-label">排队:</span>
                  <div className="timing-bar">
                    <div className="timing-segment queuing" style={{width: '10%'}}></div>
                  </div>
                  <span className="timing-value">5ms</span>
                </div>
                <div className="timing-row">
                  <span className="timing-label">连接:</span>
                  <div className="timing-bar">
                    <div className="timing-segment connecting" style={{width: '20%'}}></div>
                  </div>
                  <span className="timing-value">12ms</span>
                </div>
                <div className="timing-row">
                  <span className="timing-label">发送:</span>
                  <div className="timing-bar">
                    <div className="timing-segment sending" style={{width: '5%'}}></div>
                  </div>
                  <span className="timing-value">2ms</span>
                </div>
                <div className="timing-row">
                  <span className="timing-label">等待:</span>
                  <div className="timing-bar">
                    <div className="timing-segment waiting" style={{width: '50%'}}></div>
                  </div>
                  <span className="timing-value">89ms</span>
                </div>
                <div className="timing-row">
                  <span className="timing-label">下载:</span>
                  <div className="timing-bar">
                    <div className="timing-segment downloading" style={{width: '15%'}}></div>
                  </div>
                  <span className="timing-value">48ms</span>
                </div>
                <div className="timing-total">
                  <strong>总计: {request.time}</strong>
                </div>
              </div>
            </div>
          </div>
        );

      default:
        return <div>未知标签页</div>;
    }
  };

  return (
    <div className="request-detail">
      <div className="detail-header">
        <div className="detail-title">
          <span className="request-method">{request.method}</span>
          <span className="request-url">{request.url}</span>
          {request.intercepted && (
            <span className="intercept-badge">🔓 已拦截</span>
          )}
        </div>
        <button className="close-button" onClick={onClose} title="关闭详情">
          ✕
        </button>
      </div>

      <div className="detail-tabs">
        {tabs.map(tab => (
          <button
            key={tab.id}
            className={`detail-tab ${activeTab === tab.id ? 'active' : ''}`}
            onClick={() => setActiveTab(tab.id)}
          >
            <span className="tab-icon">{tab.icon}</span>
            {tab.label}
          </button>
        ))}
      </div>

      <div className="detail-content">
        {renderTabContent()}
      </div>
    </div>
  );
};

export default RequestDetail;