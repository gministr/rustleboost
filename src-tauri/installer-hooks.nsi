; LindaVPN NSIS installer hooks
; Kills running processes before files are installed/overwritten

!macro customInit
  ; Kill all LindaVPN processes so files can be overwritten
  nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM "linda-vpn.exe" /T'
  Pop $0
  nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM "daemon.exe" /T'
  Pop $0
  nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM "sing-box.exe" /T'
  Pop $0
  ; Wait for processes to fully terminate
  Sleep 800
!macroend

!macro customInstall
!macroend

!macro customUninstall
  nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM "linda-vpn.exe" /T'
  Pop $0
  nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM "daemon.exe" /T'
  Pop $0
  nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM "sing-box.exe" /T'
  Pop $0
  Sleep 500
!macroend
