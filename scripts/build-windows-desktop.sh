#!/usr/bin/env bash
# Copyright 2025 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# build-windows-desktop.sh — 交叉编译 Windows 桌面 GUI（Wails v3）
#
# 用法:
#   bash scripts/build-windows-desktop.sh              # 默认编译 amd64
#   bash scripts/build-windows-desktop.sh --arch arm64 # 编译 arm64
#   bash scripts/build-windows-desktop.sh --nsis       # 编译 + 生成 NSIS 安装包
#   bash scripts/build-windows-desktop.sh --skip-frontend  # 跳过前端构建（已有 dist）
#
# 前置依赖:
#   - Go >= 1.24
#   - Node.js >= 16 + npm
#   - [可选] nsis (apt: nsis) 用于生成安装包
#
# 说明: Wails v3 的 Windows 后端使用纯 Go 的 go-webview2，无需 CGO / mingw，
#       因此可在任意操作系统上用 `CGO_ENABLED=0 GOOS=windows` 直接交叉编译，无需 wails CLI。
#
set -euo pipefail

# ── 颜色输出 ──────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

log_info()  { echo -e "${CYAN}[INFO]${NC}  $*"; }
log_ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_step()  { echo -e "\n${BOLD}══════════════════════════════════════════${NC}"; echo -e "${BOLD}  $*${NC}"; echo -e "${BOLD}══════════════════════════════════════════${NC}"; }

# ── 项目根目录 ────────────────────────────────────────────
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# ── 参数解析 ──────────────────────────────────────────────
ARCH="amd64"
BUILD_NSIS=false
SKIP_FRONTEND=false
CLEAN_BUILD=false
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      ARCH="$2"; shift 2 ;;
    --nsis)
      BUILD_NSIS=true; shift ;;
    --skip-frontend)
      SKIP_FRONTEND=true; shift ;;
    --clean)
      CLEAN_BUILD=true; shift ;;
    --version)
      VERSION="$2"; shift 2 ;;
    -h|--help)
      echo "用法: $0 [OPTIONS]"
      echo ""
      echo "选项:"
      echo "  --arch <amd64|arm64>  目标架构 (默认: amd64)"
      echo "  --nsis                生成 NSIS 安装包"
      echo "  --skip-frontend       跳过前端构建（使用已有 web/dist）"
      echo "  --clean               清理旧构建产物后再编译"
      echo "  --version <ver>       覆盖版本号 (默认读取 wails.json)"
      echo "  -h, --help            显示帮助"
      exit 0
      ;;
    *)
      log_error "未知参数: $1"; exit 1 ;;
  esac
done

# 版本号：从 wails.json 读取或使用参数覆盖
if [ -z "$VERSION" ]; then
  VERSION=$(grep -oP '"productVersion"\s*:\s*"\K[^"]+' wails.json 2>/dev/null || echo "0.0.0")
fi

# ── 变量 ──────────────────────────────────────────────────
DIST_DIR="$ROOT/dist"
DESKTOP_CMD_DIR="$ROOT/cmd/sniffy-desktop"
FRONTEND_DIR="$ROOT/web"
OUTPUT_NAME="sniffy-desktop-windows-${ARCH}"
OUTPUT_EXE="${DIST_DIR}/${OUTPUT_NAME}.exe"

# 架构校验（Wails v3 Windows 无需 CGO，故不需要 mingw 交叉编译器）
case "$ARCH" in
  amd64|arm64) ;;
  *)
    log_error "不支持的架构: $ARCH (仅支持 amd64, arm64)"
    exit 1
    ;;
esac

# ── 打印构建信息 ──────────────────────────────────────────
log_step "Sniffy Windows Desktop 构建"
log_info "版本:         ${VERSION}"
log_info "目标架构:     windows/${ARCH}"
log_info "构建方式:     CGO_ENABLED=0 纯 Go(go-webview2)"
log_info "输出路径:     ${OUTPUT_EXE}"
log_info "生成安装包:   ${BUILD_NSIS}"
log_info "跳过前端:     ${SKIP_FRONTEND}"
echo ""

# ── 环境检查 ──────────────────────────────────────────────
log_step "Step 1/5: 检查构建环境"

check_cmd() {
  local cmd="$1"
  local install_hint="$2"
  if command -v "$cmd" &>/dev/null; then
    local ver
    ver=$("$cmd" --version 2>&1 | head -1 || echo "unknown")
    log_ok "$cmd 已安装: $ver"
    return 0
  else
    log_error "$cmd 未找到。安装方式: $install_hint"
    return 1
  fi
}

MISSING=0

# Go
if ! check_cmd "go" "https://go.dev/dl/"; then
  MISSING=$((MISSING+1))
fi

# Node.js
if ! check_cmd "node" "apt install nodejs 或 https://nodejs.org/"; then
  MISSING=$((MISSING+1))
fi

# npm
if ! check_cmd "npm" "随 Node.js 一起安装"; then
  MISSING=$((MISSING+1))
fi

# Wails v3 Windows 后端纯 Go(go-webview2)，无需 mingw / CGO。此处仅做信息提示，不作硬性依赖。
log_info "Wails v3 Windows 构建使用 CGO_ENABLED=0，无需 mingw-w64 交叉编译器。"

# NSIS（仅在需要生成安装包时检查）
if [ "$BUILD_NSIS" = true ]; then
  if ! check_cmd "makensis" "apt install nsis"; then
    MISSING=$((MISSING+1))
  fi
fi

if [ "$MISSING" -gt 0 ]; then
  echo ""
  log_error "缺少 ${MISSING} 个必要依赖，请先安装后重试。"
  echo ""
  log_info "快速安装所有依赖（Ubuntu/Debian）:"
  echo "  sudo apt update && sudo apt install -y nodejs npm nsis"
  exit 1
fi

echo ""
log_ok "环境检查通过！"

# ── 清理旧产物 ────────────────────────────────────────────
if [ "$CLEAN_BUILD" = true ]; then
  log_step "清理旧构建产物"
  rm -rf "${DESKTOP_CMD_DIR}/dist"
  rm -f "${OUTPUT_EXE}"
  rm -f "${DIST_DIR}/${OUTPUT_NAME}-installer.exe"
  log_ok "清理完成"
fi

# ── 构建前端 ──────────────────────────────────────────────
log_step "Step 2/5: 构建前端"

if [ "$SKIP_FRONTEND" = true ]; then
  if [ -d "${FRONTEND_DIR}/dist" ] && [ "$(ls -A "${FRONTEND_DIR}/dist" 2>/dev/null)" ]; then
    log_warn "跳过前端构建（使用已有 web/dist）"
  else
    log_error "web/dist 不存在或为空，无法跳过前端构建！"
    exit 1
  fi
else
  log_info "安装前端依赖..."
  (cd "$FRONTEND_DIR" && npm install --prefer-offline 2>&1 | tail -3)
  log_ok "前端依赖安装完成"

  log_info "构建前端..."
  (cd "$FRONTEND_DIR" && npm run build 2>&1 | tail -5)

  if [ ! -d "${FRONTEND_DIR}/dist" ]; then
    log_error "前端构建失败: web/dist 不存在"
    exit 1
  fi
  log_ok "前端构建完成: web/dist"
fi

# ── 拷贝前端到桌面 embed 目录 ─────────────────────────────
log_step "Step 3/5: 准备桌面前端资源"

rm -rf "${DESKTOP_CMD_DIR}/dist"
mkdir -p "${DESKTOP_CMD_DIR}/dist"
cp -r "${FRONTEND_DIR}/dist/"* "${DESKTOP_CMD_DIR}/dist/"

ASSET_COUNT=$(find "${DESKTOP_CMD_DIR}/dist" -type f | wc -l)
log_ok "已拷贝 ${ASSET_COUNT} 个前端文件到 cmd/sniffy-desktop/dist/"

# ── 交叉编译 Go 二进制 ───────────────────────────────────
log_step "Step 4/5: 交叉编译 Windows 桌面二进制"

mkdir -p "$DIST_DIR"

# 构建参数
BUILD_TAGS="desktop"
LDFLAGS="-s -w"
LDFLAGS="${LDFLAGS} -X 'main.version=${VERSION}'"
LDFLAGS="${LDFLAGS} -X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'"

# Windows GUI 应用需要 -H windowsgui 来隐藏控制台窗口
LDFLAGS="${LDFLAGS} -H windowsgui"

log_info "编译参数:"
log_info "  GOOS=windows GOARCH=${ARCH}"
log_info "  CGO_ENABLED=0 (Wails v3 Windows 纯 Go)"
log_info "  Tags: ${BUILD_TAGS}"
log_info "  LDFlags: ${LDFLAGS}"
echo ""

log_info "正在编译（这可能需要几分钟）..."

CGO_ENABLED=0 \
  GOOS=windows \
  GOARCH="$ARCH" \
  go build \
    -tags "$BUILD_TAGS" \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUTPUT_EXE" \
    ./cmd/sniffy-desktop

if [ ! -f "$OUTPUT_EXE" ]; then
  log_error "编译失败: ${OUTPUT_EXE} 未生成"
  exit 1
fi

FILE_SIZE=$(du -h "$OUTPUT_EXE" | cut -f1)
log_ok "编译成功: ${OUTPUT_EXE} (${FILE_SIZE})"

# ── 生成 NSIS 安装包（可选） ──────────────────────────────
if [ "$BUILD_NSIS" = true ]; then
  log_step "Step 5/5: 生成 NSIS 安装包"

  NSIS_SCRIPT="${DIST_DIR}/sniffy-installer.nsi"
  INSTALLER_EXE="${DIST_DIR}/${OUTPUT_NAME}-installer.exe"

  cat > "$NSIS_SCRIPT" << NSIS_EOF
; Sniffy NSIS 安装脚本 - 自动生成
; 版本: ${VERSION}

!include "MUI2.nsh"

Name "Sniffy ${VERSION}"
OutFile "${INSTALLER_EXE}"
InstallDir "\$PROGRAMFILES64\\Sniffy"
InstallDirRegKey HKLM "Software\\Sniffy" "InstallDir"
RequestExecutionLevel admin

; ── 界面设置 ──
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "Sniffy ${VERSION} 安装向导"
!define MUI_WELCOMEPAGE_TEXT "Sniffy 是一款跨平台抓包/代理工具，支持可脚本化插件。\$\\n\$\\n点击下一步继续安装。"

; ── 页面 ──
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${ROOT}/LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

; ── 语言 ──
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; ── 安装段 ──
Section "Sniffy 主程序" SecMain
  SetOutPath "\$INSTDIR"
  File "${OUTPUT_EXE}"

  ; 重命名为简洁名称
  Rename "\$INSTDIR\\${OUTPUT_NAME}.exe" "\$INSTDIR\\Sniffy.exe"

  ; 创建卸载程序
  WriteUninstaller "\$INSTDIR\\Uninstall.exe"

  ; 写注册表
  WriteRegStr HKLM "Software\\Sniffy" "InstallDir" "\$INSTDIR"
  WriteRegStr HKLM "Software\\Sniffy" "Version" "${VERSION}"

  ; 添加/删除程序
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "DisplayName" "Sniffy ${VERSION}"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "UninstallString" '"\$INSTDIR\\Uninstall.exe"'
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "Publisher" "goSniffy authors"
  WriteRegDWORD HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "NoModify" 1
  WriteRegDWORD HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "NoRepair" 1
SectionEnd

Section "开始菜单快捷方式" SecShortcuts
  CreateDirectory "\$SMPROGRAMS\\Sniffy"
  CreateShortCut "\$SMPROGRAMS\\Sniffy\\Sniffy.lnk" "\$INSTDIR\\Sniffy.exe"
  CreateShortCut "\$SMPROGRAMS\\Sniffy\\卸载 Sniffy.lnk" "\$INSTDIR\\Uninstall.exe"
  CreateShortCut "\$DESKTOP\\Sniffy.lnk" "\$INSTDIR\\Sniffy.exe"
SectionEnd

; ── 卸载段 ──
Section "Uninstall"
  Delete "\$INSTDIR\\Sniffy.exe"
  Delete "\$INSTDIR\\Uninstall.exe"
  RMDir "\$INSTDIR"

  Delete "\$SMPROGRAMS\\Sniffy\\Sniffy.lnk"
  Delete "\$SMPROGRAMS\\Sniffy\\卸载 Sniffy.lnk"
  RMDir "\$SMPROGRAMS\\Sniffy"
  Delete "\$DESKTOP\\Sniffy.lnk"

  DeleteRegKey HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy"
  DeleteRegKey HKLM "Software\\Sniffy"
SectionEnd
NSIS_EOF

  log_info "正在生成安装包..."
  makensis -V2 "$NSIS_SCRIPT"

  if [ -f "$INSTALLER_EXE" ]; then
    INSTALLER_SIZE=$(du -h "$INSTALLER_EXE" | cut -f1)
    log_ok "安装包生成成功: ${INSTALLER_EXE} (${INSTALLER_SIZE})"
  else
    log_warn "安装包生成失败，但 .exe 二进制已构建成功"
  fi

  # 清理 .nsi 脚本
  rm -f "$NSIS_SCRIPT"
else
  log_step "Step 5/5: 跳过（未指定 --nsis）"
  log_info "如需生成安装包，请添加 --nsis 参数"
fi

# ── 构建摘要 ──────────────────────────────────────────────
log_step "构建完成 ✓"

echo ""
log_info "构建产物:"
echo "  📦 二进制:  ${OUTPUT_EXE} ($(du -h "$OUTPUT_EXE" | cut -f1))"
if [ "$BUILD_NSIS" = true ] && [ -f "${DIST_DIR}/${OUTPUT_NAME}-installer.exe" ]; then
  echo "  📦 安装包:  ${DIST_DIR}/${OUTPUT_NAME}-installer.exe ($(du -h "${DIST_DIR}/${OUTPUT_NAME}-installer.exe" | cut -f1))"
fi

echo ""
log_info "使用方式:"
echo "  1. 将 ${OUTPUT_EXE} 拷贝到 Windows 机器"
echo "  2. 确保 Windows 已安装 WebView2 Runtime"
echo "     (Windows 10/11 通常自带，或从 https://developer.microsoft.com/microsoft-edge/webview2/ 下载)"
echo "  3. 双击运行 Sniffy"
echo ""
log_info "提示: WebView2 Runtime 是 Wails v3 在 Windows 上的必需组件。"
log_info "      Windows 11 和较新版本的 Windows 10 已预装。"
echo ""
