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
    
    // 计算相对于视口的位置
    const windowHeight = window.innerHeight;
    const toolbarHeight = 100; // 大概的工具栏高度
    const availableHeight = windowHeight - toolbarHeight;
    
    // 计算新的详情面板高度
    const newDetailHeight = windowHeight - clientY;
    const minDetailHeight = 150;
    const maxDetailHeight = availableHeight * 0.7;
    
    const constrainedHeight = Math.max(minDetailHeight, 
      Math.min(maxDetailHeight, newDetailHeight)
    );
    
    onResize(constrainedHeight);
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