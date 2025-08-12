import React, { useState } from 'react';
import './InterceptorPanel.css';

const InterceptorPanel = ({ rules, onRuleToggle, onRuleAdd, onRuleDelete }) => {
  const [showAddForm, setShowAddForm] = useState(false);
  const [editingRule, setEditingRule] = useState(null);
  const [newRule, setNewRule] = useState({
    name: '',
    enabled: true,
    condition: {
      url: '',
      method: '*'
    },
    action: {
      type: 'modify_response',
      statusCode: 200,
      headers: {},
      body: '',
      delay: 0
    }
  });

  const actionTypes = [
    { value: 'modify_response', label: '修改响应' },
    { value: 'modify_request', label: '修改请求' },
    { value: 'delay', label: '延迟请求' },
    { value: 'block', label: '阻止请求' },
    { value: 'redirect', label: '重定向' }
  ];

  const methods = ['*', 'GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS', 'HEAD'];

  const handleAddRule = () => {
    if (newRule.name.trim()) {
      onRuleAdd({
        ...newRule,
        created: new Date()
      });
      setNewRule({
        name: '',
        enabled: true,
        condition: {
          url: '',
          method: '*'
        },
        action: {
          type: 'modify_response',
          statusCode: 200,
          headers: {},
          body: '',
          delay: 0
        }
      });
      setShowAddForm(false);
    }
  };

  const handleEditRule = (rule) => {
    setEditingRule(rule);
    setNewRule({ ...rule });
    setShowAddForm(true);
  };

  const handleUpdateRule = () => {
    // 这里应该调用更新规则的回调
    setEditingRule(null);
    setShowAddForm(false);
  };

  const formatDate = (date) => {
    return date.toLocaleString('zh-CN');
  };

  const renderRuleForm = () => (
    <div className="rule-form">
      <div className="form-header">
        <h3>{editingRule ? '编辑拦截规则' : '添加拦截规则'}</h3>
        <button 
          className="close-form-button"
          onClick={() => {
            setShowAddForm(false);
            setEditingRule(null);
          }}
        >
          ✕
        </button>
      </div>

      <div className="form-content">
        <div className="form-group">
          <label>规则名称</label>
          <input
            type="text"
            value={newRule.name}
            onChange={(e) => setNewRule({...newRule, name: e.target.value})}
            placeholder="请输入规则名称"
          />
        </div>

        <div className="form-section">
          <h4>匹配条件</h4>
          <div className="form-row">
            <div className="form-group">
              <label>URL模式</label>
              <input
                type="text"
                value={newRule.condition.url}
                onChange={(e) => setNewRule({
                  ...newRule,
                  condition: {...newRule.condition, url: e.target.value}
                })}
                placeholder="例如: **/api/users/**"
              />
              <small>支持通配符 * 和 **</small>
            </div>
            <div className="form-group">
              <label>HTTP方法</label>
              <select
                value={newRule.condition.method}
                onChange={(e) => setNewRule({
                  ...newRule,
                  condition: {...newRule.condition, method: e.target.value}
                })}
              >
                {methods.map(method => (
                  <option key={method} value={method}>{method}</option>
                ))}
              </select>
            </div>
          </div>
        </div>

        <div className="form-section">
          <h4>执行动作</h4>
          <div className="form-group">
            <label>动作类型</label>
            <select
              value={newRule.action.type}
              onChange={(e) => setNewRule({
                ...newRule,
                action: {...newRule.action, type: e.target.value}
              })}
            >
              {actionTypes.map(type => (
                <option key={type.value} value={type.value}>{type.label}</option>
              ))}
            </select>
          </div>

          {newRule.action.type === 'modify_response' && (
            <>
              <div className="form-row">
                <div className="form-group">
                  <label>状态码</label>
                  <input
                    type="number"
                    value={newRule.action.statusCode}
                    onChange={(e) => setNewRule({
                      ...newRule,
                      action: {...newRule.action, statusCode: parseInt(e.target.value) || 200}
                    })}
                  />
                </div>
              </div>
              <div className="form-group">
                <label>响应体</label>
                <textarea
                  value={newRule.action.body}
                  onChange={(e) => setNewRule({
                    ...newRule,
                    action: {...newRule.action, body: e.target.value}
                  })}
                  placeholder="JSON格式的响应内容"
                  rows={6}
                />
              </div>
            </>
          )}

          {newRule.action.type === 'delay' && (
            <div className="form-group">
              <label>延迟时间 (毫秒)</label>
              <input
                type="number"
                value={newRule.action.delay}
                onChange={(e) => setNewRule({
                  ...newRule,
                  action: {...newRule.action, delay: parseInt(e.target.value) || 0}
                })}
              />
            </div>
          )}

          {newRule.action.type === 'redirect' && (
            <div className="form-group">
              <label>重定向URL</label>
              <input
                type="text"
                value={newRule.action.redirectUrl || ''}
                onChange={(e) => setNewRule({
                  ...newRule,
                  action: {...newRule.action, redirectUrl: e.target.value}
                })}
                placeholder="https://example.com/api/..."
              />
            </div>
          )}
        </div>
      </div>

      <div className="form-actions">
        <button 
          className="primary"
          onClick={editingRule ? handleUpdateRule : handleAddRule}
        >
          {editingRule ? '更新规则' : '添加规则'}
        </button>
        <button 
          onClick={() => {
            setShowAddForm(false);
            setEditingRule(null);
          }}
        >
          取消
        </button>
      </div>
    </div>
  );

  return (
    <div className="interceptor-panel">
      <div className="panel-header">
        <h2>请求拦截器</h2>
        <div className="header-actions">
          <button 
            className="add-rule-button primary"
            onClick={() => setShowAddForm(true)}
          >
            ➕ 添加规则
          </button>
        </div>
      </div>

      {showAddForm && (
        <div className="form-overlay">
          {renderRuleForm()}
        </div>
      )}

      <div className="rules-list">
        {rules.length === 0 ? (
          <div className="empty-rules">
            <div className="empty-icon">🔧</div>
            <div className="empty-text">暂无拦截规则</div>
            <div className="empty-hint">添加规则以开始拦截和修改请求</div>
          </div>
        ) : (
          rules.map(rule => (
            <div key={rule.id} className={`rule-card ${rule.enabled ? 'enabled' : 'disabled'}`}>
              <div className="rule-header">
                <div className="rule-info">
                  <div className="rule-name">
                    {rule.name}
                    {rule.enabled && <span className="active-badge">启用</span>}
                  </div>
                  <div className="rule-meta">
                    创建于 {formatDate(rule.created)}
                  </div>
                </div>
                <div className="rule-actions">
                  <button
                    className={`toggle-button ${rule.enabled ? 'enabled' : 'disabled'}`}
                    onClick={() => onRuleToggle(rule.id)}
                    title={rule.enabled ? '禁用规则' : '启用规则'}
                  >
                    {rule.enabled ? '🟢' : '🔴'}
                  </button>
                  <button
                    className="edit-button"
                    onClick={() => handleEditRule(rule)}
                    title="编辑规则"
                  >
                    ✏️
                  </button>
                  <button
                    className="delete-button"
                    onClick={() => onRuleDelete(rule.id)}
                    title="删除规则"
                  >
                    🗑️
                  </button>
                </div>
              </div>

              <div className="rule-content">
                <div className="rule-condition">
                  <div className="condition-item">
                    <span className="condition-label">URL:</span>
                    <code className="condition-value">{rule.condition.url || '*'}</code>
                  </div>
                  <div className="condition-item">
                    <span className="condition-label">方法:</span>
                    <code className="condition-value">{rule.condition.method}</code>
                  </div>
                </div>

                <div className="rule-action">
                  <div className="action-type">
                    {actionTypes.find(t => t.value === rule.action.type)?.label || rule.action.type}
                  </div>
                  {rule.action.type === 'modify_response' && (
                    <div className="action-details">
                      <span>状态码: {rule.action.statusCode}</span>
                      {rule.action.body && <span>• 自定义响应体</span>}
                    </div>
                  )}
                  {rule.action.type === 'delay' && (
                    <div className="action-details">
                      <span>延迟: {rule.action.delay}ms</span>
                    </div>
                  )}
                  {rule.action.type === 'redirect' && (
                    <div className="action-details">
                      <span>目标: {rule.action.redirectUrl}</span>
                    </div>
                  )}
                </div>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
};

export default InterceptorPanel;