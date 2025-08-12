import React, { useCallback, useEffect, useState } from 'react';

const ResizeHandle = ({ onResize }) => {
  const [isDragging, setIsDragging] = useState(false);

  const handleMouseDown = useCallback((e) => {
    e.preventDefault();
    setIsDragging(true);
  }, []);

  const handleMouseMove = useCallback((e) => {
    if (!isDragging) return;
    
    const clientY = e.clientY;
    
    // 计算新的详情面板高度（从鼠标位置到窗口底部）
    const windowHeight = window.innerHeight;
    const newDetailHeight = windowHeight - clientY;
    
    // 只确保高度不为负数，其他由用户自由控制
    const finalHeight = Math.max(0, newDetailHeight);
    
    onResize(finalHeight);
  }, [isDragging, onResize]);

  const handleMouseUp = useCallback(() => {
    setIsDragging(false);
  }, []);

  useEffect(() => {
    if (isDragging) {
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = 'row-resize';
      document.body.style.userSelect = 'none';
      
      return () => {
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
      };
    }
  }, [isDragging, handleMouseMove, handleMouseUp]);

  return (
    <div 
      className="resize-handle"
      onMouseDown={handleMouseDown}
      style={{
        backgroundColor: isDragging ? 'var(--primary-color)' : undefined
      }}
    />
  );
};

export default ResizeHandle;