param([ValidateSet('Detect','Probe','Prepare','Commit','Uninstall','Plan')][string]$Action,[string]$AppDir,[string]$DataDir,[string]$PayloadDir,[string]$StageDir,[string]$SourceDir,[string]$PasswordFile,[string]$StateFile,[int]$Lan=1,[int]$StartStreams=1,[int]$AutoService=1,[int]$Desktop=1,[int]$RemoveFFmpeg=0,[int]$RemoveMediaMTX=0,[int]$ShowProgress=0,[long]$ProgressOwner=0,[string]$ProgressFile,[string]$ResultFile,[string]$CancelFile,[string]$PreparationLog,[string]$ExpectedVersion)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';[Console]::OutputEncoding=[Text.Encoding]::Default
function AssertAdmin {if(-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Administratorrechte erforderlich. Setup erneut mit der Windows-Adminabfrage starten.'}}
function QuoteNativeArgument([string]$Value){
 $escaped=[regex]::Replace($Value,'(\\*)"','$1$1\"')
 $escaped=[regex]::Replace($escaped,'(\\+)$','$1$1')
 return '"'+$escaped+'"'
}
function WriteInstallerLine([string]$Line){
 if($ProgressFile){UpdatePreparationProgress $Line}
 if($PreparationLog){Add-Content -LiteralPath $PreparationLog -Value $Line -Encoding UTF8}
 Write-Output $Line
}
function RunNative([string]$File,[string[]]$NativeArguments){
 $info=New-Object Diagnostics.ProcessStartInfo
 $info.FileName=$File;$info.Arguments=(@($NativeArguments | ForEach-Object {QuoteNativeArgument $_}) -join ' ')
 $info.UseShellExecute=$false;$info.CreateNoWindow=$true;$info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
 $encoding=[Text.Encoding]::GetEncoding([Globalization.CultureInfo]::CurrentCulture.TextInfo.OEMCodePage)
 if([IO.Path]::GetFileName($File) -ieq 'Spherelay.exe'){$encoding=[Text.UTF8Encoding]::new($false)}
 $info.StandardOutputEncoding=$encoding;$info.StandardErrorEncoding=$encoding
 $process=New-Object Diagnostics.Process;$process.StartInfo=$info
 try{
  if(!$process.Start()){throw 'Einrichtungsprogramm konnte nicht gestartet werden.'}
  $outTask=$process.StandardOutput.ReadLineAsync();$errTask=$process.StandardError.ReadLineAsync()
  while($outTask -or $errTask){
   if($CancelFile -and (Test-Path -LiteralPath $CancelFile)){if(!$process.HasExited){$process.Kill();$process.WaitForExit()};throw 'Vorbereitung abgebrochen. Die laufende Installation bleibt unverändert.'}
   if($outTask -and $outTask.Wait(100)){$line=$outTask.Result;if($null -eq $line){$outTask=$null}else{WriteInstallerLine $line;$outTask=$process.StandardOutput.ReadLineAsync()}}
   if($errTask -and $errTask.Wait($(if($outTask){0}else{100}))){$line=$errTask.Result;if($null -eq $line){$errTask=$null}else{WriteInstallerLine $line;$errTask=$process.StandardError.ReadLineAsync()}}
  }
  $process.WaitForExit()
  if($process.ExitCode -ne 0){throw "Einrichtungsschritt fehlgeschlagen: $([IO.Path]::GetFileName($File)) (Exit $($process.ExitCode))"}
 }finally{try{if(!$process.HasExited){$process.Kill();$process.WaitForExit()}}catch{};$process.Dispose()}
}
function PreparedFileHash([string]$File){
 $stream=[IO.File]::OpenRead($File);$sha=[Security.Cryptography.SHA256]::Create()
 try{return [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-','')}finally{$sha.Dispose();$stream.Dispose()}
}
function PreparedFiles {
 $names=@('settings.json','bin\ffmpeg.exe','bin\mediamtx.exe')
 if(Test-Path -LiteralPath (Join-Path $StageDir 'discovery-credentials.json')){$names+='discovery-credentials.json'}
 return $names
}
function SavePreparedManifest {
 $hashes=@{}
 foreach($name in @(PreparedFiles)){$file=Join-Path $StageDir $name;if(!(Test-Path -LiteralPath $file -PathType Leaf) -or (Get-Item -LiteralPath $file).Length -eq 0){throw ('Vorbereitung unvollständig: '+$name+' fehlt oder ist leer. Die laufende Installation bleibt unverändert.')};$hashes[$name]=(PreparedFileHash $file)}
 $hashes | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $StageDir 'prepared-files.json') -Encoding UTF8
}
function AssertPreparedStage {
 $manifest=Join-Path $StageDir 'prepared-files.json'
 if(!(Test-Path -LiteralPath $manifest -PathType Leaf)){throw 'Prüfnachweis der Vorbereitung fehlt. Die laufende Installation bleibt unverändert.'}
 $hashes=Get-Content -LiteralPath $manifest -Raw -Encoding UTF8 | ConvertFrom-Json
 $names=@(PreparedFiles);if($hashes.PSObject.Properties['discovery-credentials.json'] -and $names -notcontains 'discovery-credentials.json'){$names+='discovery-credentials.json'}
 foreach($name in $names){
  $expected=$hashes.PSObject.Properties[$name];$file=Join-Path $StageDir $name
  if(!$expected -or !(Test-Path -LiteralPath $file -PathType Leaf)){throw ('Vorbereitung unvollständig: '+$name+'. Die laufende Installation bleibt unverändert.')}
  if((PreparedFileHash $file) -ne $expected.Value){throw ('Vorbereitete Datei wurde verändert: '+$name+'. Die laufende Installation bleibt unverändert.')}
 }
 if(!(SettingsAt $StageDir)){throw 'Vorbereitete Einstellungen fehlen.'}
}
function SettingsAt([string]$Folder){if($Folder -and (Test-Path -LiteralPath (Join-Path $Folder 'settings.json'))){return (Get-Content -LiteralPath (Join-Path $Folder 'settings.json') -Raw | ConvertFrom-Json)};return $null}
function WriteProbe([string]$Folder){$s=SettingsAt $Folder;$account=0;$network=1;if($s){if($s.passwordHash){$account=1};$network=[int][bool]$s.networkRtsp};[IO.File]::WriteAllText($StateFile,"[Setup]`r`nSource=$Folder`r`nAccount=$account`r`nLan=$network`r`n",[Text.Encoding]::Unicode)}
function DetectSource {$candidates=@();$target=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'Spherelay';$candidates+=$target;foreach($p in @(Get-CimInstance Win32_Process -Filter "Name='Spherelay.exe'" -ErrorAction SilentlyContinue)){if($p.CommandLine -match '--data\s+(?:"([^"]+)"|(\S+))'){$candidates+=$(if($Matches[1]){$Matches[1]}else{$Matches[2]})}};$candidates+=(Join-Path $env:LOCALAPPDATA 'Spherelay');foreach($d in $candidates){if(SettingsAt $d){WriteProbe $d;return}};WriteProbe ''}
function PrivateFolder([string]$Folder,[bool]$Service){[IO.Directory]::CreateDirectory($Folder)|Out-Null;$rights=@('*S-1-5-18:(OI)(CI)F','*S-1-5-32-544:(OI)(CI)F');if($Service){$rights+='*S-1-5-19:(OI)(CI)M'};RunNative "$env:WINDIR\System32\icacls.exe" (@($Folder,'/inheritance:r','/grant:r')+$rights+@('/q'));RunNative "$env:WINDIR\System32\icacls.exe" @($Folder,'/setowner','*S-1-5-32-544','/q')}
function AssertTargets {$expectedApp=Join-Path $env:ProgramFiles 'Spherelay';$expectedData=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'Spherelay';if([IO.Path]::GetFullPath($AppDir).TrimEnd('\') -ine $expectedApp.TrimEnd('\') -or [IO.Path]::GetFullPath($DataDir).TrimEnd('\') -ine $expectedData.TrimEnd('\')){throw 'Installationsziele sind nicht die vorgesehenen Spherelay-Verzeichnisse.'}}
function NormalPath([string]$Path){return [IO.Path]::GetFullPath($Path).TrimEnd('\')}
function StopExactProcess($Snapshot){
 $live=Get-Process -Id $Snapshot.ProcessId -ErrorAction SilentlyContinue
 if(!$live){return}
 try{if((NormalPath $live.Path) -ine (NormalPath $Snapshot.ExecutablePath)){throw 'Prozesszuordnung hat sich geändert; Einrichtung sicherheitshalber abgebrochen.'};Write-Output ('Beende '+$Snapshot.Name+' (PID '+$Snapshot.ProcessId+').');Stop-Process -InputObject $live -Force -ErrorAction Stop;if(!$live.WaitForExit(15000)){throw 'Prozess wurde nicht rechtzeitig beendet.'}}finally{$live.Dispose()}
}
function WaitInstallFiles {
 foreach($file in @((Join-Path $AppDir 'Spherelay.exe'),(Join-Path $DataDir 'bin\ffmpeg.exe'),(Join-Path $DataDir 'bin\mediamtx.exe'))){
  $ready=$false
  for($attempt=0;$attempt -lt 40;$attempt++){
   if(!(Test-Path -LiteralPath $file)){$ready=$true;break}
   try{$handle=[IO.File]::Open($file,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None);$handle.Dispose();$ready=$true;break}catch [IO.IOException]{Start-Sleep -Milliseconds 250}
  }
  if(!$ready){throw ('Programmdatei weiterhin gesperrt: '+$file+'. Es wurden noch keine Programmdateien ersetzt.')}
 }
}
function StopOwnedProcesses {
 if(Get-Service Spherelay -ErrorAction SilentlyContinue){Stop-Service Spherelay -Force -ErrorAction Stop;(Get-Service Spherelay).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(25))}
 $roots=@((NormalPath $DataDir));if($SourceDir){$roots+=(NormalPath $SourceDir)}
 foreach($p in @(Get-CimInstance Win32_Process -Filter "Name='Spherelay.exe'")){
  if(!$p.ExecutablePath -or ($PayloadDir -and (NormalPath $p.ExecutablePath) -ieq (NormalPath (Join-Path $PayloadDir 'Spherelay.exe')))){continue}
  $owned=(NormalPath $p.ExecutablePath) -ieq (NormalPath (Join-Path $AppDir 'Spherelay.exe'))
  if($p.CommandLine -match '--data\s+(?:"([^"]+)"|(\S+))'){$d=$(if($Matches[1]){$Matches[1]}else{$Matches[2]});if($roots -icontains (NormalPath $d)){$owned=$true}}
  if($owned){StopExactProcess $p}
 }
 # A service can leave children behind. Resolve ownership by exact executable
 # path independently of parent PID, including processes whose parent has exited.
 $componentPaths=@();foreach($root in $roots){foreach($name in @('ffmpeg.exe','mediamtx.exe')){$componentPaths+=(NormalPath (Join-Path $root ('bin\'+$name)))}}
 foreach($p in @(Get-CimInstance Win32_Process -Filter "Name='ffmpeg.exe' OR Name='mediamtx.exe'")){if($p.ExecutablePath -and $componentPaths -icontains (NormalPath $p.ExecutablePath)){StopExactProcess $p}}
 WaitInstallFiles
 Write-Output 'Spherelay-Prozesse beendet; Programmdateien sind zum Austausch freigegeben.'
}
function ConfigureRules {foreach($name in @('Spherelay-RTSP-LAN','Spherelay-Discovery-SADP','Spherelay-Discovery-ONVIF','Spherelay-ONVIF-HTTP','Spherelay-ONVIF-WSDiscovery')){Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue | Remove-NetFirewallRule};New-NetFirewallRule -Name 'Spherelay-RTSP-LAN' -DisplayName 'Spherelay RTSP (LAN)' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet -Protocol TCP -LocalPort 8554 -Program (Join-Path $DataDir 'bin\mediamtx.exe')|Out-Null;New-NetFirewallRule -Name 'Spherelay-Discovery-SADP' -DisplayName 'Spherelay Kamerasuche (SADP)' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet -Protocol UDP -LocalPort 37020 -Program (Join-Path $AppDir 'Spherelay.exe')|Out-Null;New-NetFirewallRule -Name 'Spherelay-Discovery-ONVIF' -DisplayName 'Spherelay Kamerasuche (ONVIF)' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet -Protocol UDP -RemotePort 3702 -Program (Join-Path $AppDir 'Spherelay.exe')|Out-Null;New-NetFirewallRule -Name 'Spherelay-ONVIF-HTTP' -DisplayName 'Spherelay ONVIF (LAN)' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet -Protocol TCP -LocalPort 8899 -Program (Join-Path $AppDir 'Spherelay.exe')|Out-Null;New-NetFirewallRule -Name 'Spherelay-ONVIF-WSDiscovery' -DisplayName 'Spherelay ONVIF WS-Discovery (LAN)' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet -Protocol UDP -LocalPort 3702 -Program (Join-Path $AppDir 'Spherelay.exe')|Out-Null;foreach($name in @('Spherelay-RTSP-LAN','Spherelay-Discovery-SADP','Spherelay-Discovery-ONVIF','Spherelay-ONVIF-HTTP','Spherelay-ONVIF-WSDiscovery')){if(-not(Get-NetFirewallRule -Name $name -ErrorAction Stop).Enabled){throw 'Firewallregel konnte nicht bestätigt werden'}}}
function ShortcutAt([string]$Path){$sh=New-Object -ComObject WScript.Shell;$link=$sh.CreateShortcut($Path);$link.TargetPath=Join-Path $AppDir 'Spherelay.exe';$link.Arguments='--open-ui --data "'+$DataDir+'"';$link.WorkingDirectory=$AppDir;$link.IconLocation=(Join-Path $AppDir 'Spherelay.ico')+',0';$link.Description='Spherelay öffnen';$link.WindowStyle=7;$link.Save()}
try{
 if($ProgressFile -or $ResultFile){. (Join-Path (Split-Path $PSCommandPath) 'PreparationProgress.ps1');$script:WorkerCode=1;$script:WorkerMessage='Vorbereitung nicht abgeschlossen.';SavePreparationProgress}
 if($Action -eq 'Detect'){DetectSource;exit 0};if($Action -eq 'Probe'){if($SourceDir -and !(SettingsAt $SourceDir)){throw 'Im gewählten Datenordner fehlen Einstellungen.'};if(!$SourceDir -and (SettingsAt $DataDir)){$SourceDir=$DataDir};WriteProbe $SourceDir;exit 0}
 if($Action -eq 'Plan'){@{app=$AppDir;data=$DataDir;service='Spherelay';account='NT AUTHORITY\LocalService';automatic=[bool]$AutoService;lan=[bool]$Lan;firewall=@('TCP 8554 Private,Domain LocalSubnet','UDP 37020 Private,Domain LocalSubnet','UDP remote 3702 Private,Domain LocalSubnet','TCP 8899 Private,Domain LocalSubnet','UDP local 3702 Private,Domain LocalSubnet');web='127.0.0.1:8787';dataRetainedOnUninstall=$true}|ConvertTo-Json -Depth 4;exit 0}
 AssertAdmin;AssertTargets
 if($Action -eq 'Prepare'){

  Write-Output 'Daten und Videokomponenten werden vorbereitet. Die laufende Installation bleibt zunächst aktiv.';PrivateFolder $StageDir $false;$bin=Join-Path $StageDir 'bin';[IO.Directory]::CreateDirectory($bin)|Out-Null
  $source=$SourceDir;if(!$source -and (SettingsAt $DataDir)){$source=$DataDir};if($source){if(!(SettingsAt $source)){throw 'Im gewählten Datenordner fehlen gültige Einstellungen.'};foreach($f in @('settings.json','discovery-credentials.json')){$p=Join-Path $source $f;if(Test-Path -LiteralPath $p){Copy-Item -LiteralPath $p -Destination (Join-Path $StageDir $f) -Force}};foreach($f in @('ffmpeg.exe','mediamtx.exe')){$p=Join-Path $source ('bin\'+$f);if(Test-Path -LiteralPath $p){Copy-Item -LiteralPath $p -Destination (Join-Path $bin $f) -Force}}}
  $exe=Join-Path $PayloadDir 'Spherelay.exe';$provisionArgs=@('--installer-provision','--data',$StageDir,'--lan',"$Lan",'--start-streams',"$StartStreams");if($PasswordFile){$provisionArgs+=@('--setup-password-file',$PasswordFile)};RunNative $exe $provisionArgs
  foreach($component in @('ffmpeg','mediamtx')){Write-Output "Videokomponente: $component";if($ProgressFile){UpdatePreparationProgress ($component+': Wird vorbereitet …')};RunNative $exe @('--installer-component',$component,'--data',$StageDir);if($ProgressFile){CompletePreparationProgress $component}};if($CancelFile -and (Test-Path -LiteralPath $CancelFile)){throw 'Vorbereitung abgebrochen. Die laufende Installation bleibt unverändert.'};SavePreparedManifest;AssertPreparedStage;Write-Output 'Vorbereitung und Integritätsprüfungen abgeschlossen.';$script:WorkerCode=0;$script:WorkerMessage='Komponenten geprüft und bereit.';exit 0
 }
 if($Action -eq 'Commit'){
  if(!$ExpectedVersion){throw 'Die erwartete Programmversion fehlt im Installationspaket.'}
  AssertPreparedStage
  $healthFile=Join-Path $StageDir 'previously-online.json'
  try {RunNative (Join-Path $PayloadDir 'Spherelay.exe') @('--installer-update-snapshot',$healthFile)} catch {Write-Output 'Vorherige Streamzustände konnten nicht ermittelt werden.'}
  Write-Output 'Windows-Dienst, geschützte Daten und Firewall werden eingerichtet.'
  StopOwnedProcesses
  [IO.Directory]::CreateDirectory($AppDir)|Out-Null;PrivateFolder $DataDir $true;$backup=Join-Path $DataDir ('backups\'+(Get-Date -Format 'yyyyMMdd-HHmmss'));[IO.Directory]::CreateDirectory($backup)|Out-Null
  foreach($f in @('settings.json','discovery-credentials.json')){$old=Join-Path $DataDir $f;if(Test-Path -LiteralPath $old){Copy-Item -LiteralPath $old -Destination (Join-Path $backup $f) -Force}}
  $oldExe=Join-Path $AppDir 'Spherelay.exe';if(Test-Path -LiteralPath $oldExe){Copy-Item -LiteralPath $oldExe -Destination (Join-Path $backup 'Spherelay.previous.exe') -Force}
  try{
   Copy-Item -LiteralPath (Join-Path $PayloadDir 'Spherelay.exe') -Destination $oldExe -Force;Copy-Item -LiteralPath (Join-Path $PayloadDir 'Spherelay.ico') -Destination (Join-Path $AppDir 'Spherelay.ico') -Force;Copy-Item -LiteralPath (Join-Path $PayloadDir 'NSIS-COPYING.txt') -Destination (Join-Path $AppDir 'NSIS-COPYING.txt') -Force
   [IO.Directory]::CreateDirectory((Join-Path $DataDir 'bin'))|Out-Null;foreach($f in @('settings.json','discovery-credentials.json')){$p=Join-Path $StageDir $f;if(Test-Path -LiteralPath $p){Copy-Item -LiteralPath $p -Destination (Join-Path $DataDir $f) -Force}};foreach($f in @('ffmpeg.exe','mediamtx.exe')){Copy-Item -LiteralPath (Join-Path $StageDir ('bin\'+$f)) -Destination (Join-Path $DataDir ('bin\'+$f)) -Force}
   $command='"'+$oldExe+'" --service --data "'+$DataDir+'"';$startMode=$(if($AutoService){'Automatic'}else{'Manual'});$serviceArgs=@{PathName=$command;StartMode=$startMode;StartName='NT AUTHORITY\LocalService';StartPassword=''};$existing=Get-CimInstance Win32_Service -Filter "Name='Spherelay'";if($existing){$result=Invoke-CimMethod -InputObject $existing -MethodName Change -Arguments $serviceArgs}else{$serviceArgs.Name='Spherelay';$serviceArgs.DisplayName='Spherelay Stream Studio';$serviceArgs.ServiceType=[byte]16;$serviceArgs.ErrorControl=[byte]1;$serviceArgs.DesktopInteract=$false;$result=Invoke-CimMethod -ClassName Win32_Service -MethodName Create -Arguments $serviceArgs};if($result.ReturnValue -ne 0){throw "Windows-Dienstregistrierung fehlgeschlagen (Code $($result.ReturnValue))."};RunNative sc.exe @('config','Spherelay','start=',$(if($AutoService){'auto'}else{'demand'}));RunNative sc.exe @('description','Spherelay','Spherelay Kamera- und Streamverwaltung');RunNative sc.exe @('failure','Spherelay','reset=','86400','actions=','restart/5000/restart/15000/restart/30000')
   ConfigureRules
   $menu=Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'Spherelay';[IO.Directory]::CreateDirectory($menu)|Out-Null;ShortcutAt (Join-Path $menu 'Spherelay.lnk');if($Desktop){ShortcutAt (Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Spherelay.lnk');$own=Join-Path ([Environment]::GetFolderPath('Desktop')) 'Spherelay.lnk';if(Test-Path -LiteralPath $own){$shell=New-Object -ComObject WScript.Shell;$link=$shell.CreateShortcut($own);if([IO.Path]::GetFileName($link.TargetPath) -ieq 'Spherelay.exe'){ShortcutAt $own}}}
   $run=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Run',$true);if($run){$value=[string]$run.GetValue('Spherelay');if($value -match 'Spherelay\.exe'){$run.DeleteValue('Spherelay',$false)};$run.Dispose()}
   New-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\Spherelay' -Name DelayedAutoStart -Value 0 -PropertyType DWord -Force | Out-Null;Start-Service Spherelay
   $ready=$false
   $observed='keine Antwort von der Weboberfläche'
   for($i=0;$i -lt 60;$i++){
    try{
     $info=Invoke-RestMethod 'http://127.0.0.1:8787/api/info' -TimeoutSec 2
     $state=(Get-Service Spherelay).Status
     $observed="Version $($info.version), Einrichtung $($info.setup), Dienst $state"
     if($info.version -eq $ExpectedVersion -and $info.setup -and $state -eq 'Running'){$ready=$true;break}
    }catch{}
    Start-Sleep -Milliseconds 500
   }
   if(!$ready){throw "Startprüfung fehlgeschlagen. Erwartet: Version $ExpectedVersion und laufender Dienst. Erkannt: $observed."}
   Copy-Item -LiteralPath (Join-Path $PayloadDir 'Installer.ps1') -Destination (Join-Path $AppDir 'Installer.ps1') -Force
   $protocol='HKLM:\SOFTWARE\Classes\spherelay'
   $openCommand=Join-Path $protocol 'shell\open\command'
   if(!(Test-Path -LiteralPath $openCommand)){New-Item -Path $openCommand -Force | Out-Null}
   Set-Item -LiteralPath $protocol -Value 'URL:Spherelay'
   New-ItemProperty -LiteralPath $protocol -Name 'URL Protocol' -PropertyType String -Value '' -Force | Out-Null
   Set-Item -LiteralPath $openCommand -Value ('"'+$oldExe+'" --windows-action "%1"')
   if(Test-Path -LiteralPath $healthFile -PathType Leaf){try {RunNative $oldExe @('--installer-update-report',$healthFile)} catch {Write-Output 'Streamzustände nach der Installation konnten nicht geprüft werden.'}}
   Write-Output "Spherelay ${ExpectedVersion}: Dienst und Weboberfläche sind bereit."
  }catch{Stop-Service Spherelay -Force -ErrorAction SilentlyContinue;foreach($f in @('settings.json','discovery-credentials.json')){$p=Join-Path $backup $f;if(Test-Path -LiteralPath $p){Copy-Item -LiteralPath $p -Destination (Join-Path $DataDir $f) -Force}};$p=Join-Path $backup 'Spherelay.previous.exe';if(Test-Path -LiteralPath $p){Copy-Item -LiteralPath $p -Destination $oldExe -Force;Start-Service Spherelay};throw};exit 0
 }
 if($Action -eq 'Uninstall'){Write-Output 'Dienst und ausschließlich Spherelay-Firewallregeln werden entfernt. Daten bleiben erhalten.';StopOwnedProcesses;if(Get-Service Spherelay -ErrorAction SilentlyContinue){Stop-Service Spherelay -Force -ErrorAction SilentlyContinue;RunNative sc.exe @('delete','Spherelay')};foreach($name in @('Spherelay-RTSP-LAN','Spherelay-Discovery-SADP','Spherelay-Discovery-ONVIF','Spherelay-ONVIF-HTTP','Spherelay-ONVIF-WSDiscovery')){Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue | Remove-NetFirewallRule};foreach($component in @(@{Name='ffmpeg.exe';Remove=$RemoveFFmpeg},@{Name='mediamtx.exe';Remove=$RemoveMediaMTX})){if($component.Remove -eq 1){$bin=Join-Path $DataDir 'bin';if(Test-Path -LiteralPath $bin){if((Get-Item -LiteralPath $bin).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Komponentenordner ist eine Verknüpfung; Entfernung abgebrochen.'};$file=Join-Path $bin $component.Name;if(Test-Path -LiteralPath $file){Remove-Item -LiteralPath $file -Force;Write-Output ($component.Name+' entfernt.')}}}};exit 0}
}catch{$script:WorkerCode=1;$script:WorkerMessage=$_.Exception.Message;Write-Output ('FEHLER: '+$_.Exception.Message);exit 1}finally{
 if($ResultFile){$nl=[Environment]::NewLine;$message=($script:WorkerMessage -replace '[\r\n]',' ');WritePreparationIni $ResultFile ("[Result]"+$nl+"Code=$($script:WorkerCode)"+$nl+"Message=$message"+$nl)}
 if($PasswordFile -and (Test-Path -LiteralPath $PasswordFile)){Remove-Item -LiteralPath $PasswordFile -Force}}


