import React from 'react';

const Toolbar = ({
  isRecording,
  onToggleRecording,
  onClear,
  filter,
  onFilterChange,
  requestCount
}) => {
  const handleFilterChange = (key, value) => {
    onFilterChange({
      ...filter,
      [key]: value
    });
  };

  return (
    <div className="toolbar">
      <div className="toolbar-group">
        <button
          className={`record-button ${isRecording ? 'recording' : ''}`}
          onClick={onToggleRecording}
          title={isRecording ? '停止录制' : '开始录制'}
        >
          <span className={`record-indicator ${isRecording ? 'recording' : ''}`}></span>
          {isRecording ? '录制中' : '已停止'}
        </button>
        
        <button
          className="clear-button"
          onClick={onClear}
          title="清除所有请求"
        >
          🗑️ 清除
        </button>
      </div>

      <div className="toolbar-divider"></div>

      <div className="toolbar-group filter-group">
        <label>
          方法:
          <select
            className="filter-select"
            value={filter.method}
            onChange={(e) => handleFilterChange('method', e.target.value)}
          >
            <option value="">全部</option>
            <option value="GET">GET</option>
            <option value="POST">POST</option>
            <option value="PUT">PUT</option>
            <option value="DELETE">DELETE</option>
            <option value="PATCH">PATCH</option>
          </select>
        </label>

        <label>
          状态:
          <select
            className="filter-select"
            value={filter.status}
            onChange={(e) => handleFilterChange('status', e.target.value)}
          >
            <option value="">全部</option>
            <option value="2">2xx</option>
            <option value="3">3xx</option>
            <option value="4">4xx</option>
            <option value="5">5xx</option>
          </select>
        </label>

        <label>
          类型:
          <select
            className="filter-select"
            value={filter.type}
            onChange={(e) => handleFilterChange('type', e.target.value)}
          >
            <option value="">全部</option>
            <option value="xhr">XHR</option>
            <option value="fetch">Fetch</option>
            <option value="document">Document</option>
            <option value="stylesheet">CSS</option>
            <option value="script">JS</option>
            <option value="image">Image</option>
            <option value="font">Font</option>
            <option value="websocket">WebSocket</option>
          </select>
        </label>
      </div>

      <div className="toolbar-divider"></div>

      <div className="toolbar-group">
        <input
          type="text"
          className="filter-input"
          placeholder="搜索URL..."
          value={filter.search}
          onChange={(e) => handleFilterChange('search', e.target.value)}
        />
      </div>

      <div className="request-count">
        {requestCount} 个请求
      </div>
    </div>
  );
};

export default Toolbar;