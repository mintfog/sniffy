#!/bin/bash

# Sniffy Web 界面启动脚本

echo "🚀 启动 Sniffy Web 界面..."

# 检查是否安装了Node.js
if ! command -v node &> /dev/null; then
    echo "❌ 错误: 未找到 Node.js，请先安装 Node.js"
    echo "   下载地址: https://nodejs.org/"
    exit 1
fi

# 检查是否安装了npm
if ! command -v npm &> /dev/null; then
    echo "❌ 错误: 未找到 npm"
    exit 1
fi

echo "✅ Node.js 版本: $(node --version)"
echo "✅ npm 版本: $(npm --version)"

# 检查是否存在package.json
if [ ! -f "package.json" ]; then
    echo "❌ 错误: 未找到 package.json 文件"
    echo "   请确保在 web 目录中运行此脚本"
    exit 1
fi

# 检查是否已安装依赖
if [ ! -d "node_modules" ]; then
    echo "📦 安装依赖包..."
    npm install
    if [ $? -ne 0 ]; then
        echo "❌ 依赖安装失败"
        exit 1
    fi
    echo "✅ 依赖安装完成"
else
    echo "✅ 依赖已安装"
fi

echo ""
echo "🌐 启动开发服务器..."
echo "   访问地址: http://localhost:3000"
echo "   按 Ctrl+C 停止服务器"
echo ""

# 启动开发服务器
npm start