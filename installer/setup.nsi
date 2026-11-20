Unicode true
RequestExecutionLevel admin
SetCompressor /SOLID lzma
Name "Spherelay ${VERSION}"
OutFile "${OUTPUT}"
InstallDir "$PROGRAMFILES64\Spherelay"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "Spherelay"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}.0"
VIAddVersionKey "FileDescription" "Spherelay Windows-x64-Setup"
ShowInstDetails show
ShowUninstDetails show

!include "MUI2.nsh"
!include "nsDialogs.nsh"
!include "x64.nsh"

!define MUI_ICON "${PAYLOAD}\Spherelay.ico"
!define MUI_UNICON "${PAYLOAD}\Spherelay.ico"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "${ART}\modern-header.bmp"
!define MUI_WELCOMEFINISHPAGE_BITMAP "${ART}\modern-wizard.bmp"
!define MUI_WELCOMEPAGE_TITLE "Willkommen bei Spherelay"
!define MUI_WELCOMEPAGE_TEXT "Dieser Assistent installiert Spherelay ${VERSION} als Windows-Dienst. Vorhandene Kameras, Ansichten und Einstellungen können übernommen werden.$\r$\n$\r$\nBei Bedarf lädt der Assistent FFmpeg und MediaMTX und prüft deren Integrität."
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Spherelay öffnen"
!define MUI_FINISHPAGE_RUN_FUNCTION OpenSpherelay

!insertmacro MUI_PAGE_WELCOME
Page custom OptionsPage ReadOptions
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "German"

Var DataDir
Var StageDir
Var SourceDir
Var WithAccount
Var StateFile
Var PasswordInput
Var PasswordFile
Var LanOption
Var StreamsOption
Var ServiceOption
Var DesktopOption
Var Lan
Var Streams
Var AutoService
Var DesktopFlag
Var PowerShell
Var ExitCode

Function .onInit
  IfSilent 0 +2
  Abort
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "Spherelay benötigt Windows x64."
    Abort
  ${EndIf}
  SetRegView 64
  SetShellVarContext all
  StrCpy $DataDir "$APPDATA\Spherelay"
  StrCpy $PowerShell "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
  InitPluginsDir
  nsExec::Exec '"$SYSDIR\icacls.exe" "$PLUGINSDIR" /inheritance:r /grant:r *S-1-5-18:(OI)(CI)F *S-1-5-32-544:(OI)(CI)F /q'
  Pop $ExitCode
  ${If} $ExitCode != 0
    MessageBox MB_ICONSTOP "Der temporäre Installationsordner konnte nicht geschützt werden."
    Abort
  ${EndIf}
  StrCpy $StageDir "$PLUGINSDIR\stage"
  StrCpy $StateFile "$PLUGINSDIR\state.ini"
  StrCpy $PasswordFile "$PLUGINSDIR\account.txt"
  SetOutPath "$PLUGINSDIR\payload"
  File "${PAYLOAD}\Installer.ps1"
  File "${PAYLOAD}\Spherelay.ico"
  File "${PAYLOAD}\NSIS-COPYING.txt"
  nsExec::ExecToLog '"$PowerShell" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\payload\Installer.ps1" -Action Detect -StateFile "$StateFile"'
  Pop $ExitCode
  ${If} $ExitCode != 0
    MessageBox MB_ICONSTOP "Vorhandene Spherelay-Konfiguration konnte nicht geprüft werden."
    Abort
  ${EndIf}
  ReadINIStr $SourceDir "$StateFile" "Setup" "Source"
  ReadINIStr $WithAccount "$StateFile" "Setup" "Account"
  ReadINIStr $Lan "$StateFile" "Setup" "Lan"
FunctionEnd

Function OptionsPage
  !insertmacro MUI_HEADER_TEXT "Einrichtung" "Spherelay für den lokalen Betrieb konfigurieren"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}
  ${If} $WithAccount == 1
    ${NSD_CreateLabel} 0 0 100% 26u "Vorhandene Kameras, Ansichten, Einstellungen und Zugangsdaten werden übernommen."
    Pop $0
  ${Else}
    ${NSD_CreateLabel} 0 0 100% 16u "Administratorpasswort (mindestens 12 Zeichen)"
    Pop $0
    ${NSD_CreatePassword} 0 16u 100% 13u ""
    Pop $PasswordInput
  ${EndIf}
  ${NSD_CreateCheckbox} 0 43u 100% 12u "RTSP im lokalen Netzwerk freigeben"
  Pop $LanOption
  ${If} $Lan == 1
    ${NSD_Check} $LanOption
  ${EndIf}
  ${NSD_CreateCheckbox} 0 62u 100% 12u "Windows-Dienst automatisch starten"
  Pop $ServiceOption
  ${NSD_Check} $ServiceOption
  ${NSD_CreateCheckbox} 0 81u 100% 12u "Konfigurierte Streams automatisch starten"
  Pop $StreamsOption
  ${NSD_Check} $StreamsOption
  ${NSD_CreateCheckbox} 0 100u 100% 12u "Desktop-Verknüpfung erstellen"
  Pop $DesktopOption
  ${NSD_Check} $DesktopOption
  nsDialogs::Show
FunctionEnd

Function ReadOptions
  ${NSD_GetState} $LanOption $Lan
  ${NSD_GetState} $ServiceOption $AutoService
  ${NSD_GetState} $StreamsOption $Streams
  ${NSD_GetState} $DesktopOption $DesktopFlag
  ${If} $WithAccount != 1
    ${NSD_GetText} $PasswordInput $0
    StrLen $1 $0
    ${If} $1 < 12
      MessageBox MB_ICONEXCLAMATION "Das Passwort muss mindestens zwölf Zeichen enthalten."
      Abort
    ${EndIf}
    ClearErrors
    FileOpen $1 "$PasswordFile" w
    ${If} ${Errors}
      MessageBox MB_ICONSTOP "Die geschützte Passwortdatei konnte nicht angelegt werden."
      Abort
    ${EndIf}
    FileWriteUTF16LE /BOM $1 $0
    FileClose $1
    StrCpy $0 ""
  ${EndIf}
FunctionEnd

Section "Spherelay"
  SetOutPath "$PLUGINSDIR\payload"
  File "${PAYLOAD}\Spherelay.exe"
  StrCpy $0 '-AppDir "$INSTDIR" -DataDir "$DataDir" -PayloadDir "$PLUGINSDIR\payload" -StageDir "$StageDir" -Lan $Lan -StartStreams $Streams -AutoService $AutoService -Desktop $DesktopFlag'
  ${If} $SourceDir != ""
    StrCpy $0 '$0 -SourceDir "$SourceDir"'
  ${EndIf}
  StrCpy $1 $0
  ${If} $WithAccount != 1
    StrCpy $1 '$1 -PasswordFile "$PasswordFile"'
  ${EndIf}
  nsExec::ExecToLog '"$PowerShell" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\payload\Installer.ps1" -Action Prepare $1'
  Pop $ExitCode
  Delete "$PasswordFile"
  ${If} $ExitCode != 0
    SetErrorLevel 1
    MessageBox MB_ICONSTOP "Die Vorbereitung ist fehlgeschlagen. Einzelheiten stehen im Installationsprotokoll."
    Abort
  ${EndIf}
  nsExec::ExecToLog '"$PowerShell" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\payload\Installer.ps1" -Action Commit -ExpectedVersion "${VERSION}" $0'
  Pop $ExitCode
  ${If} $ExitCode != 0
    SetErrorLevel 1
    MessageBox MB_ICONSTOP "Die Installation ist fehlgeschlagen. Einzelheiten stehen im Installationsprotokoll."
    Abort
  ${EndIf}
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Spherelay" "DisplayName" "Spherelay"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Spherelay" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Spherelay" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Spherelay" "DisplayIcon" "$INSTDIR\Spherelay.ico"
SectionEnd

Function OpenSpherelay
  ExecShell "open" "http://127.0.0.1:8787/"
FunctionEnd

Function un.onInit
  SetRegView 64
  SetShellVarContext all
  StrCpy $DataDir "$APPDATA\Spherelay"
  StrCpy $PowerShell "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
FunctionEnd

Section "Uninstall"
  nsExec::ExecToLog '"$PowerShell" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$INSTDIR\Installer.ps1" -Action Uninstall -AppDir "$INSTDIR" -DataDir "$DataDir"'
  Pop $ExitCode
  ${If} $ExitCode != 0
    SetErrorLevel 1
    MessageBox MB_ICONSTOP "Der Spherelay-Dienst konnte nicht entfernt werden."
    Abort
  ${EndIf}
  Delete "$INSTDIR\Spherelay.exe"
  Delete "$INSTDIR\Spherelay.ico"
  Delete "$INSTDIR\Installer.ps1"
  Delete "$INSTDIR\NSIS-COPYING.txt"
  Delete "$INSTDIR\Uninstall.exe"
  Delete "$SMPROGRAMS\Spherelay\Spherelay.lnk"
  RMDir "$SMPROGRAMS\Spherelay"
  Delete "$DESKTOP\Spherelay.lnk"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Spherelay"
  DeleteRegKey HKLM "Software\Classes\spherelay"
  RMDir "$INSTDIR"
SectionEnd
