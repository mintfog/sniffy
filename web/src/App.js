import React, { useState, useEffect } from 'react';
import './App.css';
import NetworkTable from './components/NetworkTable';
import RequestDetail from './components/RequestDetail';
import InterceptorPanel from './components/InterceptorPanel';
import Toolbar from './components/Toolbar';
import ResizeHandle from './components/ResizeHandle';
import { mockRequests, mockInterceptRules } from './mockData';

function App() {
  const [requests, setRequests] = useState(mockRequests);
  const [selectedRequest, setSelectedRequest] = useState(null);
  const [interceptRules, setInterceptRules] = useState(mockInterceptRules);
  const [activeTab, setActiveTab] = useState('network'); // network, interceptor
  const [filter, setFilter] = useState({
    method: '',
    status: '',
    type: '',
    search: ''
  });
  const [isRecording, setIsRecording] = useState(true);
  const [detailPanelHeight, setDetailPanelHeight] = useState(300);

  // 模拟实时添加新请求
  useEffect(() => {
    if (isRecording) {
      const interval = setInterval(() => {
        const newRequest = {
          id: Date.now().toString(),
          method: ['GET', 'POST', 'PUT', 'DELETE'][Math.floor(Math.random() * 4)],
          url: `https://api.example.com/endpoint${Math.floor(Math.random() * 100)}`,
          domain: 'api.example.com',
          path: `/endpoint${Math.floor(Math.random() * 100)}`,
          status: [200, 201, 400, 404, 500][Math.floor(Math.random() * 5)],
          statusText: 'OK',
          type: ['xhr', 'fetch', 'document'][Math.floor(Math.random() * 3)],
          size: `${Math.floor(Math.random() * 50 + 1)} kB`,
          time: `${Math.floor(Math.random() * 500 + 50)}ms`,
          timestamp: new Date(),
          headers: {
            request: { 'Accept': 'application/json' },
            response: { 'Content-Type': 'application/json' }
          },
          requestBody: null,
          responseBody: '{"data": "mock"}',
          intercepted: false
        };
        
        setRequests(prev => [newRequest, ...prev.slice(0, 99)]); // 保持最多100条记录
      }, 3000 + Math.random() * 2000); // 3-5秒随机间隔

      return () => clearInterval(interval);
    }
  }, [isRecording]);

  const filteredRequests = requests.filter(request => {
    if (filter.method && request.method !== filter.method) return false;
    if (filter.status && !request.status.toString().startsWith(filter.status)) return false;
    if (filter.type && request.type !== filter.type) return false;
    return !(filter.search && !request.url.toLowerCase().includes(filter.search.toLowerCase()));

  });

  const clearRequests = () => {
    setRequests([]);
    setSelectedRequest(null);
  };

  const handleRequestSelect = (request) => {
    setSelectedRequest(request);
  };

  const handleInterceptRuleToggle = (ruleId) => {
    setInterceptRules(prev => 
      prev.map(rule => 
        rule.id === ruleId ? { ...rule, enabled: !rule.enabled } : rule
      )
    );
  };

  const handleInterceptRuleAdd = (newRule) => {
    setInterceptRules(prev => [...prev, { ...newRule, id: Date.now().toString() }]);
  };

  const handleInterceptRuleDelete = (ruleId) => {
    setInterceptRules(prev => prev.filter(rule => rule.id !== ruleId));
  };

  const handleResize = (newHeight) => {
    setDetailPanelHeight(newHeight);
  };

  return (
    <div className="app">
      <div className="app-header">
        <div className="tab-bar">
          <button 
            className={`tab ${activeTab === 'network' ? 'active' : ''}`}
            onClick={() => setActiveTab('network')}
          >
            网络
          </button>
          <button 
            className={`tab ${activeTab === 'interceptor' ? 'active' : ''}`}
            onClick={() => setActiveTab('interceptor')}
          >
            拦截器
          </button>
        </div>
        
        <Toolbar
          isRecording={isRecording}
          onToggleRecording={() => setIsRecording(!isRecording)}
          onClear={clearRequests}
          filter={filter}
          onFilterChange={setFilter}
          requestCount={filteredRequests.length}
        />
      </div>

      <div className="app-content">
        {activeTab === 'network' ? (
          <div className="network-panel">
            <div className="network-table-container">
              <NetworkTable
                requests={filteredRequests}
                selectedRequest={selectedRequest}
                onRequestSelect={handleRequestSelect}
              />
            </div>
            
            {selectedRequest && (
              <>
                <ResizeHandle onResize={handleResize} />
                <div 
                  className="request-detail-container"
                  style={{ height: detailPanelHeight }}
                >
                  <RequestDetail
                    request={selectedRequest}
                    onClose={() => setSelectedRequest(null)}
                  />
                </div>
              </>
            )}
          </div>
        ) : (
          <InterceptorPanel
            rules={interceptRules}
            onRuleToggle={handleInterceptRuleToggle}
            onRuleAdd={handleInterceptRuleAdd}
            onRuleDelete={handleInterceptRuleDelete}
          />
        )}
      </div>
    </div>
  );
}

export default App;