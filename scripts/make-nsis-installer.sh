#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# make-nsis-installer.sh — 用已有的 Windows 桌面 exe 生成 NSIS 安装包。
#
# CI 与本地构建共用打包入口，安装包包含调用方指定的二进制。
#
# 用法:
#   bash scripts/make-nsis-installer.sh \
#     --binary dist/sniffy-desktop-windows-amd64.exe \
#     --version 1.2.3 \
#     --out dist/sniffy-setup.exe
#
# 前置依赖:makensis(apt: nsis / choco: nsis);
#           Windows 上经 Git Bash 运行时用 cygpath 转换路径(随 Git for Windows 提供)。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# 相对路径以调用方工作目录为基准。
CALLER_PWD="$PWD"
abs() {
  case "$1" in
    /* | [A-Za-z]:/* | [A-Za-z]:\\*) printf '%s\n' "$1" ;;
    *) printf '%s\n' "$CALLER_PWD/$1" ;;
  esac
}
cd "$ROOT"

# Git Bash 文件操作使用 POSIX 路径，写入 NSIS 模板时转换为原生路径。
# MSYS 不会转换模板文件内的路径。
if command -v cygpath >/dev/null 2>&1; then
  to_posix()  { cygpath -u -- "$1"; }
  # NSIS File 的文件匹配需要 Windows 反斜杠路径。
  to_native() { cygpath -w -- "$1"; }
else
  to_posix()  { printf '%s\n' "$1"; }
  to_native() { printf '%s\n' "$1"; }
fi
ROOT_NATIVE="$(to_native "$ROOT")"

BINARY=""
VERSION=""
OUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)  BINARY="$2";  shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --out)     OUT="$2";     shift 2 ;;
    -h|--help)
      echo "用法: $0 --binary <exe> [--version <ver>] [--out <path>]"
      exit 0
      ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

# 先归一到 POSIX 再定为绝对路径:调用方可能给盘符形式(C:/b 或 C:\b),
# 而后续的 dirname/basename/test -f 只认 POSIX。
if [ -n "$BINARY" ]; then BINARY="$(abs "$(to_posix "$BINARY")")"; fi
if [ -n "$OUT" ]; then OUT="$(abs "$(to_posix "$OUT")")"; fi

if [ -z "$BINARY" ]; then
  echo "错误: --binary 必填(指向已构建的 Windows 桌面 exe)" >&2
  exit 1
fi
if [ ! -f "$BINARY" ]; then
  echo "错误: 找不到二进制 $BINARY" >&2
  exit 1
fi

if [ -z "$VERSION" ]; then
  VERSION=$(grep -oP '"productVersion"\s*:\s*"\K[^"]+' wails.json 2>/dev/null || echo "0.0.0")
fi

if [ -z "$OUT" ]; then
  OUT="$(dirname "$BINARY")/sniffy-installer.exe"
fi
if [ "$BINARY" -ef "$OUT" ]; then
  echo "错误: --out 不能覆盖输入二进制" >&2
  exit 1
fi
mkdir -p "$(dirname "$OUT")"

BINARY_INSTALL="$(to_native "$BINARY")"
OUT_INSTALL="$(to_native "$OUT")"
ICON_INSTALL="$(to_native "$ROOT/build/windows/icon.ico")"

if ! command -v makensis >/dev/null 2>&1; then
  echo "错误: 未找到 makensis。安装: apt install nsis / choco install nsis" >&2
  exit 1
fi

NSIS_SCRIPT="$(mktemp -t sniffy-installer-XXXXXX.nsi)"
trap 'rm -f "$NSIS_SCRIPT"' EXIT

cat > "$NSIS_SCRIPT" << NSIS_EOF
; Sniffy NSIS 安装脚本 - 自动生成
; 版本: ${VERSION}

!include "MUI2.nsh"

Name "Sniffy"
; 静默安装(/S)是应用内自动更新走的路径:见 internal/desktop 的 InstallUpdate。
OutFile "${OUT_INSTALL}"
InstallDir "\$PROGRAMFILES64\\Sniffy"
InstallDirRegKey HKLM "Software\\Sniffy" "InstallDir"
RequestExecutionLevel admin

!define MUI_ABORTWARNING
!define MUI_ICON "${ICON_INSTALL}"
!define MUI_UNICON "${ICON_INSTALL}"
!define MUI_WELCOMEPAGE_TITLE "Sniffy 安装向导"
!define MUI_WELCOMEPAGE_TEXT "Sniffy 是一款跨平台抓包/代理工具，支持可脚本化插件。\$\\n\$\\n点击下一步继续安装。"

!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_FUNCTION LaunchSniffy
!define MUI_FINISHPAGE_RUN_TEXT "立即运行 Sniffy"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${ROOT_NATIVE}/LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; 以独占写入方式打开现有文件,等待运行中的应用解除占用。
; OPEN_EXISTING 保留原文件内容,超时后旧版本仍可启动。
Function WaitForAppExit
  StrCpy \$0 0
  wait_loop:
    IfFileExists "\$INSTDIR\\Sniffy.exe" 0 wait_done
    System::Call 'kernel32::CreateFileW(w "\$INSTDIR\\Sniffy.exe", i 0x40000000, i 0, p 0, i 3, i 0, p 0) p.r1'
    StrCmp \$1 -1 0 wait_close
    IntOp \$0 \$0 + 1
    IntCmp \$0 40 wait_fail wait_retry wait_fail
  wait_retry:
    Sleep 500
    Goto wait_loop
  wait_fail:
    SetErrorLevel 2
    Abort "Sniffy 仍在运行,请退出后重新安装。"
  wait_close:
    System::Call 'kernel32::CloseHandle(p r1)'
  wait_done:
FunctionEnd

; 通过当前用户的资源管理器启动,避免应用继承安装程序的管理员权限。
Function LaunchSniffy
  Exec '"\$WINDIR\\explorer.exe" "\$INSTDIR\\Sniffy.exe"'
FunctionEnd

Function .onInstSuccess
  IfSilent 0 +2
    Call LaunchSniffy
FunctionEnd

Section "Sniffy 主程序" SecMain
  SetOutPath "\$INSTDIR"
  Call WaitForAppExit
  ClearErrors
  File /oname=Sniffy.exe "${BINARY_INSTALL}"
  IfErrors 0 +3
    SetErrorLevel 1
    Abort "无法写入 Sniffy.exe,请检查安装目录后重试。"
  ; 开始菜单读取快捷方式的图标文件，Wails 运行时图标仅供运行中的窗口使用。
  File /oname=Sniffy.ico "${ICON_INSTALL}"

  WriteUninstaller "\$INSTDIR\\Uninstall.exe"

  WriteRegStr HKLM "Software\\Sniffy" "InstallDir" "\$INSTDIR"
  WriteRegStr HKLM "Software\\Sniffy" "Version" "${VERSION}"

  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "DisplayName" "Sniffy"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "DisplayIcon" '"\$INSTDIR\\Sniffy.ico",0'
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "UninstallString" '"\$INSTDIR\\Uninstall.exe"'
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "Publisher" "goSniffy authors"
  WriteRegDWORD HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "NoModify" 1
  WriteRegDWORD HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Sniffy" "NoRepair" 1
SectionEnd

Section "开始菜单快捷方式" SecShortcuts
  CreateDirectory "\$SMPROGRAMS\\Sniffy"
  CreateShortCut "\$SMPROGRAMS\\Sniffy\\Sniffy.lnk" "\$INSTDIR\\Sniffy.exe" "" "\$INSTDIR\\Sniffy.ico" 0
  CreateShortCut "\$SMPROGRAMS\\Sniffy\\卸载 Sniffy.lnk" "\$INSTDIR\\Uninstall.exe" "" "\$INSTDIR\\Sniffy.ico" 0
  CreateShortCut "\$DESKTOP\\Sniffy.lnk" "\$INSTDIR\\Sniffy.exe" "" "\$INSTDIR\\Sniffy.ico" 0
SectionEnd

Section "Uninstall"
  Delete "\$INSTDIR\\Sniffy.exe"
  Delete "\$INSTDIR\\Sniffy.ico"
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

echo ">> 生成安装包: ${OUT} (版本 ${VERSION})"
makensis -V2 -INPUTCHARSET UTF8 -NOCD "$NSIS_SCRIPT"

if [ -f "$OUT" ]; then
  echo ">> 完成: ${OUT} ($(du -h "$OUT" | cut -f1))"
else
  echo "错误: 安装包未生成" >&2
  exit 1
fi
