; Copyright 2026 The mintfog Authors
; SPDX-License-Identifier: Apache-2.0

Unicode true
!include "${__FILEDIR__}/../../build/windows/close-running-app.nsh"

Name "Sniffy 更新关闭测试"
OutFile "${TEST_INSTALLER}"
RequestExecutionLevel user
XPStyle on
AutoCloseWindow true
Page instfiles

; 用隔离目录中的文件替换验证真实退出流程，避免修改注册表和快捷方式。
Section
  Call CloseRunningApp
  SetOutPath "$INSTDIR"
  ClearErrors
  FileOpen $0 "$INSTDIR\Sniffy.exe" w
  IfErrors failed
  FileWrite $0 "updated"
  FileClose $0
  IfErrors failed
  Goto done
  failed:
    SetErrorLevel 1
    Abort
  done:
SectionEnd
