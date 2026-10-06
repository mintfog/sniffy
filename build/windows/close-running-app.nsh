; Copyright 2026 The mintfog Authors
; SPDX-License-Identifier: Apache-2.0

!ifndef SNIFFY_CLOSE_RUNNING_APP
!define SNIFFY_CLOSE_RUNNING_APP

!include "FileFunc.nsh"

Var UpdateProcessHandle
Var UpdateProcessInfo

Function ReadUpdateProcess
  StrCpy $UpdateProcessHandle 0
  StrCpy $UpdateProcessInfo 0
  ${GetParameters} $0
  ${GetOptions} $0 "/UPDATEPID=" $1
  IfErrors done
  ${GetOptions} $0 "/UPDATESTARTLOW=" $2
  IfErrors failed
  ${GetOptions} $0 "/UPDATESTARTHIGH=" $3
  IfErrors failed

  IntFmt $4 "%u" $1
  StrCmp $4 $1 0 failed
  StrCmp $1 0 failed
  IntFmt $4 "%u" $2
  StrCmp $4 $2 0 failed
  IntFmt $4 "%u" $3
  StrCmp $4 $3 0 failed

  ; 创建时间与 PID 共同标识进程，保留句柄可在安装前确认它已完全退出。
  System::Call 'kernel32::OpenProcess(i 0x101000, i 0, i r1) p.r4 ?e'
  Pop $5
  StrCpy $UpdateProcessHandle $4
  StrCmp $4 0 0 check_identity
  ; ERROR_INVALID_PARAMETER 表示进程已经退出。
  StrCmp $5 87 done failed

  check_identity:
    System::Call 'kernel32::GetProcessTimes(p $UpdateProcessHandle, *l .r5, *l, *l, *l) i.r4'
    StrCmp $4 0 failed
    System::Int64Op $3 << 32
    Pop $4
    System::Int64Op $4 | $2
    Pop $4
    StrCmp $4 $5 0 failed
    System::Call 'kernel32::WaitForSingleObject(p $UpdateProcessHandle, i 0) i.r4'
    StrCmp $4 0 done
    StrCmp $4 258 0 failed
    ; RM_UNIQUE_PROCESS 的布局为 DWORD PID + FILETIME。
    System::Call '*(i r1, i r2, i r3) p.s'
    Pop $UpdateProcessInfo
    StrCmp $UpdateProcessInfo 0 failed
  done:
    Return
  failed:
    Call ReleaseUpdateProcess
    SetErrorLevel 2
    Abort "无法确认发起更新的进程，安装已停止。请重新发起更新。"
FunctionEnd

Function ReleaseUpdateProcess
  System::Free $UpdateProcessInfo
  StrCmp $UpdateProcessHandle 0 done
  System::Call 'kernel32::CloseHandle(p $UpdateProcessHandle)'
  done:
FunctionEnd

Function CloseRunningApp
  Call ReadUpdateProcess
  IfFileExists "$INSTDIR\Sniffy.exe" start_session
  StrCmp $UpdateProcessInfo 0 done

  start_session:
    ; Restart Manager 按文件路径定位占用者，也能处理隐藏到托盘的旧版本。
    System::Call 'rstrtmgr::RmStartSession(*i .r0, i 0, w .r1) i.r3'
    StrCmp $3 0 0 failed
    IfFileExists "$INSTDIR\Sniffy.exe" 0 register_initiator
    System::Call '*(&w${NSIS_MAX_STRLEN} "$INSTDIR\Sniffy.exe") p.r1'
    System::Call '*(p r1) p.r2'
    System::Call 'rstrtmgr::RmRegisterResources(i r0, i 1, p r2, i 0, p 0, i 0, p 0) i.r3'
    System::Free $2
    System::Free $1
    StrCmp $3 0 0 end_session

  register_initiator:
    StrCmp $UpdateProcessInfo 0 list_processes
    System::Call 'rstrtmgr::RmRegisterResources(i r0, i 0, p 0, i 1, p $UpdateProcessInfo, i 0, p 0) i.r3'
    StrCmp $3 0 0 end_session

  list_processes:
    ; 零长度缓冲区返回 ERROR_MORE_DATA，表示仍有需要关闭的进程。
    System::Call 'rstrtmgr::RmGetList(i r0, *i .r1, *i 0, p 0, *i .r2) i.r3'
    StrCmp $3 0 end_session
    StrCmp $3 234 0 end_session
    System::Call '*(i 1001, w "关闭软件并更新", i 2, w "放弃更新") p.r4'
    ; TASKDIALOGCONFIG 的按钮段依次为数量、数组、默认按钮 ID，其后的可选字段置零。
    ; 默认按钮为 IDCANCEL（2），标志 TDF_ALLOW_DIALOG_CANCELLATION（8）允许 Esc/关闭取消。
    System::Call '*(&l4, p $HWNDPARENT, p 0, i 8, i 0, w "Sniffy 更新", p 0, \
      w "Sniffy 正在运行", w "更新需要关闭 Sniffy，正在进行的抓包和连接将结束。请先保存需要的内容，再选择是否继续。", \
      i 2, p r4, i 2, \
      i 0, p 0, i 0, \
      p 0, p 0, p 0, p 0, p 0, p 0, \
      p 0, p 0, i 0) p.r5'
    StrCpy $6 2
    StrCpy $7 -1
    System::Call 'comctl32::TaskDialogIndirect(p r5, *i .r6, p 0, p 0) i.r7'
    ; 结构中的 w 字符串由 System 单独分配，需与容器分别释放。
    System::Call '*$5(i, p, p, i, i, p .r1, p, p .r2, p .r3)'
    System::Free $1
    System::Free $2
    System::Free $3
    System::Call '*$4(i, p .r1, i, p .r2)'
    System::Free $1
    System::Free $2
    System::Free $5
    System::Free $4
    StrCmp $7 0 0 cancel_update
    StrCmp $6 1001 shutdown
  cancel_update:
    System::Call 'rstrtmgr::RmEndSession(i r0)'
    Call ReleaseUpdateProcess
    SetErrorLevel 2
    Quit

  shutdown:
    DetailPrint "正在关闭 Sniffy…"
    ; RmForceShutdown 允许系统在应用未响应退出请求时结束进程。
    System::Call 'rstrtmgr::RmShutdown(i r0, i 1, p 0) i.r3'
  end_session:
    System::Call 'rstrtmgr::RmEndSession(i r0)'
    StrCmp $3 0 0 failed

    StrCmp $UpdateProcessHandle 0 wait_for_file
    System::Call 'kernel32::WaitForSingleObject(p $UpdateProcessHandle, i 20000) i.r3'
    StrCmp $3 0 0 failed

  wait_for_file:
    ; OPEN_EXISTING 不改动旧文件，确认解除占用后才允许覆盖。
    StrCpy $0 0
  wait_loop:
    IfFileExists "$INSTDIR\Sniffy.exe" 0 done
    System::Call 'kernel32::CreateFileW(w "$INSTDIR\Sniffy.exe", i 0x40000000, i 0, p 0, i 3, i 0, p 0) p.r1'
    StrCmp $1 -1 0 unlocked
    IntOp $0 $0 + 1
    IntCmp $0 40 timed_out
    Sleep 500
    Goto wait_loop
  timed_out:
    StrCpy $3 1460
    Goto failed
  unlocked:
    System::Call 'kernel32::CloseHandle(p r1)'
  done:
    Call ReleaseUpdateProcess
    Return
  failed:
    Call ReleaseUpdateProcess
    SetErrorLevel 2
    Abort "无法关闭更新所需的进程或解除程序文件的占用，安装已停止。请稍后重试。（错误码：$3）"
FunctionEnd

!endif
