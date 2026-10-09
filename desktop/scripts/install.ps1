param(
  [ValidateSet('Install', 'Update', 'Uninstall')][string]$Mode = 'Install',
  [string]$Root = (Join-Path $env:LOCALAPPDATA 'Programs\ARTEX'),
  [string]$Archive,
  [string]$Manifest,
  [string]$Trust,
  [string]$DataHome,
  [string]$ReadyFile,
  [string]$ReadyNonce,
  [string]$PermitFile,
  [string]$CancelFile,
  [string]$CancelledFile,
  [int]$ParentPid = 0,
  [switch]$Restart,
  [switch]$NoRegistration,
  [switch]$Development
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.IO.Compression.FileSystem
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Text;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
public static class ArtexInstallPath {
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)] static extern uint GetLongPathName(string path, StringBuilder buffer, uint length);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)] static extern SafeFileHandle CreateFile(string path, uint access, uint share, IntPtr security, uint disposition, uint flags, IntPtr template);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)] static extern uint GetFinalPathNameByHandle(SafeFileHandle handle, StringBuilder buffer, uint length, uint flags);
  public static string Canonical(string root) {
    string cursor = Path.GetFullPath(root).TrimEnd('\\'); List<string> tail = new List<string>();
    while (!Directory.Exists(cursor)) { tail.Insert(0, Path.GetFileName(cursor)); cursor = Path.GetDirectoryName(cursor); if (String.IsNullOrEmpty(cursor)) throw new Exception("설치 폴더의 실제 경로를 확인할 수 없습니다."); }
    StringBuilder buffer = new StringBuilder(32768);
    if (GetLongPathName(cursor, buffer, (uint)buffer.Capacity) == 0) throw new Exception("설치 폴더의 실제 경로를 확인할 수 없습니다.");
    string value = buffer.ToString(); foreach (string part in tail) value = Path.Combine(value, part);
    return value.TrimEnd('\\').ToUpperInvariant();
  }
  public static string ExistingCanonical(string path) {
    using (SafeFileHandle handle = CreateFile(path, 0, 7, IntPtr.Zero, 3, 0, IntPtr.Zero)) {
      if (handle.IsInvalid) throw new Exception("실행 파일의 실제 경로를 확인할 수 없습니다.");
      StringBuilder buffer = new StringBuilder(32768);
      uint length = GetFinalPathNameByHandle(handle, buffer, (uint)buffer.Capacity, 0);
      if (length == 0 || length >= buffer.Capacity) throw new Exception("실행 파일의 실제 경로를 확인할 수 없습니다.");
      string value = buffer.ToString();
      if (value.StartsWith(@"\\?\UNC\", StringComparison.OrdinalIgnoreCase)) value = @"\\" + value.Substring(8);
      else if (value.StartsWith(@"\\?\", StringComparison.Ordinal)) value = value.Substring(4);
      return value.TrimEnd('\\').ToUpperInvariant();
    }
  }
}
'@

function Assert-PlainPath([string]$Value) {
  $full = [IO.Path]::GetFullPath($Value)
  if ($full.TrimEnd('\') -eq [IO.Path]::GetPathRoot($full).TrimEnd('\')) { throw '드라이브 루트에는 앱을 설치할 수 없습니다.' }
  $cursor = $full
  while ($cursor) {
    if (Test-Path -LiteralPath $cursor) {
      if ((Get-Item -LiteralPath $cursor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw '연결된 경로에는 앱을 설치할 수 없습니다.' }
    }
    $parent = [IO.Directory]::GetParent($cursor)
    if ($null -eq $parent) { break }
    $cursor = $parent.FullName
  }
  return $full.TrimEnd('\')
}
function Assert-OwnedPath([string]$Value) {
  $full = Assert-PlainPath $Value
  if (-not $full.StartsWith($script:installRoot + '\', [StringComparison]::OrdinalIgnoreCase)) { throw '설치 폴더 밖의 파일은 변경할 수 없습니다.' }
  return $full
}
function Same-PlainPath([string]$Left, [string]$Right) {
  if (-not [IO.Path]::IsPathRooted($Left) -or -not [IO.Path]::IsPathRooted($Right)) { return $false }
  return [ArtexInstallPath]::Canonical((Assert-PlainPath $Left)) -eq [ArtexInstallPath]::Canonical((Assert-PlainPath $Right))
}
function Remove-OwnedPath([string]$Value) {
  $full = Assert-OwnedPath $Value
  if (Test-Path -LiteralPath $full) {
    if ((Get-Item -LiteralPath $full -Force).PSIsContainer) {
      foreach ($item in Get-ChildItem -LiteralPath $full -Force -Recurse) {
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw '연결된 파일이나 폴더는 제거할 수 없습니다.' }
      }
    }
    Remove-Item -LiteralPath $full -Recurse -Force
  }
}
function Read-Json([string]$Value) { return (Get-Content -LiteralPath $Value -Raw -Encoding UTF8 | ConvertFrom-Json) }
function File-SHA256([string]$Value) {
  $stream = [IO.File]::OpenRead($Value); $hash = [Security.Cryptography.SHA256]::Create()
  try { return ([BitConverter]::ToString($hash.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
  finally { $hash.Dispose(); $stream.Dispose() }
}
function Parse-Version([string]$Value) {
  if ($Value -notmatch '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$') { throw '앱 버전이 올바르지 않습니다.' }
  return [Version]$Value
}
function Write-Current([string]$Value) {
  [void](Parse-Version $Value)
  $pointer = Join-Path $script:installRoot 'current.txt'
  $temporary = Join-Path $script:installRoot ('current-' + [Guid]::NewGuid().ToString('N') + '.tmp')
  [IO.File]::WriteAllText($temporary, $Value, [Text.Encoding]::ASCII)
  if (Test-Path -LiteralPath $pointer) {
    $backup = Join-Path $script:installRoot ('current-' + [Guid]::NewGuid().ToString('N') + '.bak')
    [IO.File]::Replace($temporary, $pointer, $backup)
    Remove-OwnedPath $backup
  }
  else { [IO.File]::Move($temporary, $pointer) }
}
function Restart-Installed {
  $executable = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes((Join-Path $script:installRoot 'ARTEX.exe')))
  $home = if ($DataHome) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($DataHome)) } else { '' }
  # 새 앱은 설치 Job 종료 뒤 네이티브 Host가 시작한다.
  Write-Output ('ARTEX_RESTART ' + $executable + ' ' + $home)
}
function Assert-NotCancelled {
  if ($CancelFile -and (Test-Path -LiteralPath $CancelFile) -and [IO.File]::ReadAllText($CancelFile) -eq $ReadyNonce) { throw (New-Object OperationCanceledException('업데이트를 취소했습니다. 기존 앱을 유지합니다.')) }
}
function Assert-PublishPermitted {
  Assert-NotCancelled
  if ($Mode -eq 'Update' -and (-not (Test-Path -LiteralPath $PermitFile) -or [IO.File]::ReadAllText($PermitFile) -ne $ReadyNonce)) { throw (New-Object OperationCanceledException('백업과 종료 승인이 없어 업데이트를 적용하지 않았습니다.')) }
}
function Acquire-InstallLock {
  if ($script:lockHeld) { return }
  try { $script:lockHeld = $script:installMutex.WaitOne(60000) }
  catch [Threading.AbandonedMutexException] { $script:lockHeld = $true }
  if (-not $script:lockHeld) { throw '다른 설치 작업이 진행 중입니다. 작업이 끝난 뒤 다시 시도하세요.' }
}
function Release-InstallLock {
  if ($script:lockHeld) { $script:installMutex.ReleaseMutex(); $script:lockHeld = $false }
}
function Assert-AppStopped {
  foreach ($process in Get-Process -ErrorAction SilentlyContinue) {
    try { $location = $process.Path } catch { continue }
    if (-not $location) { continue }
    try { $existingPath = [ArtexInstallPath]::ExistingCanonical($location) } catch { continue }
    if ($existingPath.StartsWith($canonicalRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
      [void](Assert-PlainPath $location)
      throw 'ARTEX를 종료한 뒤 설치하거나 제거하세요.'
    }
  }
}
function Wait-ParentExit {
  if ($ParentPid -le 0) { return }
  $parentProcess = Get-Process -Id $ParentPid -ErrorAction SilentlyContinue
  $deadline = [DateTime]::UtcNow.AddSeconds(60)
  while ($parentProcess -and -not $parentProcess.WaitForExit(100)) {
    Assert-NotCancelled
    if ([DateTime]::UtcNow -ge $deadline) { throw '앱이 종료되지 않아 기존 설치를 유지했습니다.' }
  }
  Assert-NotCancelled
  $script:parentExited = $true
}
function Assert-FileName([string]$Value) {
  if ([string]::IsNullOrEmpty($Value) -or $Value.Contains('\') -or $Value.Contains(':') -or $Value.StartsWith('/')) { throw '설치 파일 경로가 올바르지 않습니다.' }
  foreach ($part in $Value.Split('/')) {
    if ($part -in @('', '.', '..') -or $part -match '[\x00-\x1f<>"|?*]|[. ]$' -or $part -match '^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)') { throw '설치 파일 경로가 올바르지 않습니다.' }
  }
}
function Decode-Base64URL([string]$Value) {
  $normal = $Value.Replace('-', '+').Replace('_', '/')
  while (($normal.Length % 4) -ne 0) { $normal += '=' }
  return [Convert]::FromBase64String($normal)
}
function Verify-Release([string]$Release, [string]$TrustedKey) {
  $envelope = Read-Json $Release
  $trusted = Read-Json $TrustedKey
  $bytes = [Convert]::FromBase64String($envelope.payload)
  if ($bytes.Length -gt 3000000) { throw '업데이트 정보가 너무 큽니다.' }
  if ($Development) {
    if (-not $trusted.development -or $envelope.signature -ne '') { throw '개발 검증용 설치 파일이 아닙니다.' }
  } else {
    $key = $trusted.publicKey
    if ($key.kty -ne 'RSA') { throw '고정된 RSA 배포 서명 키가 필요합니다.' }
    $rsa = New-Object Security.Cryptography.RSACryptoServiceProvider
    try {
      $parameters = New-Object Security.Cryptography.RSAParameters
      $parameters.Modulus = Decode-Base64URL $key.n
      $parameters.Exponent = Decode-Base64URL $key.e
      $rsa.ImportParameters($parameters)
      if ($rsa.KeySize -lt 3072 -or -not $rsa.VerifyData($bytes, 'SHA256', [Convert]::FromBase64String($envelope.signature))) { throw '업데이트 서명을 확인할 수 없습니다.' }
    } finally { $rsa.Dispose() }
  }
  return ([Text.Encoding]::UTF8.GetString($bytes) | ConvertFrom-Json)
}

$script:installRoot = Assert-PlainPath $Root
$canonicalRoot = [ArtexInstallPath]::Canonical($script:installRoot)
$hash = [Security.Cryptography.SHA256]::Create()
try { $mutexHash = [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes($canonicalRoot))).Replace('-', '').ToLowerInvariant() }
finally { $hash.Dispose() }
$script:installMutex = New-Object Threading.Mutex($false, ('Local\ARTEX-Install-' + $mutexHash))
$script:lockHeld = $false
$script:parentExited = $false
$ownershipPath = Join-Path $script:installRoot 'installation.json'
$currentPath = Join-Path $script:installRoot 'current.txt'
$previous = $null
$promoted = $false
$stage = $null
$release = $null
try {
Acquire-InstallLock
$registered = -not $NoRegistration
if (Test-Path -LiteralPath $ownershipPath) {
  $owned = Read-Json $ownershipPath
  if ($owned.product -ne 'ARTEX' -or $owned.schema -ne 1 -or -not (Same-PlainPath $owned.root $script:installRoot)) { throw '이 폴더는 ARTEX 설치 폴더가 아닙니다.' }
  $registered = [bool]$owned.registered
  if (Test-Path -LiteralPath $currentPath) { $previous = [IO.File]::ReadAllText($currentPath).Trim(); [void](Parse-Version $previous) }
} elseif ((Test-Path -LiteralPath $script:installRoot) -and @(Get-ChildItem -LiteralPath $script:installRoot -Force).Count -gt 0) { throw '기존 폴더의 파일을 덮어쓸 수 없습니다.' }
elseif ($Mode -ne 'Install') { throw '이 폴더에는 ARTEX가 설치되어 있지 않습니다.' }

$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ARTEX'
$shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'ARTEX.lnk'
if ($registered -and (Test-Path -LiteralPath $uninstallKey)) {
  $registeredRoot = (Get-ItemProperty -LiteralPath $uninstallKey).InstallLocation
  if (-not (Same-PlainPath $registeredRoot $script:installRoot)) { throw '다른 폴더의 ARTEX가 이미 설치 등록되어 있습니다.' }
}
if ($Mode -eq 'Uninstall') {
  Release-InstallLock
  Wait-ParentExit
  Acquire-InstallLock
  Assert-AppStopped
  if ($registered -and (Test-Path -LiteralPath $uninstallKey)) { Remove-Item -LiteralPath $uninstallKey -Recurse -Force }
  if ($registered -and (Test-Path -LiteralPath $shortcut)) {
    $shell = New-Object -ComObject WScript.Shell
    if (-not (Same-PlainPath ($shell.CreateShortcut($shortcut).TargetPath) (Join-Path $script:installRoot 'ARTEX.exe'))) { throw '다른 설치 폴더의 바로가기를 변경할 수 없습니다.' }
    Remove-Item -LiteralPath $shortcut -Force
  }
  foreach ($name in @('versions', 'ARTEX.exe', 'ARTEX-InstallHost.exe', 'install.ps1', 'trust.json', 'current.txt', 'pending.txt', 'installation.json', 'update-failure.txt')) { Remove-OwnedPath (Join-Path $script:installRoot $name) }
  foreach ($file in Get-ChildItem -LiteralPath $script:installRoot -Filter 'healthy-*.txt' -File) { Remove-OwnedPath $file.FullName }
  if (@(Get-ChildItem -LiteralPath $script:installRoot -Force).Count -eq 0) { Remove-Item -LiteralPath $script:installRoot -Force }
  Write-Output 'ARTEX를 제거했습니다. 사용자 데이터는 보존했습니다.'
  exit 0
}
if ($Mode -eq 'Update') {
  if ($Development) { throw '개발 검증용 서명으로 자동 업데이트할 수 없습니다.' }
  if (-not $DataHome -or -not [IO.Path]::IsPathRooted($DataHome)) { throw '재시작할 기존 사용자 데이터 폴더의 절대 경로가 필요합니다.' }
  $Trust = Join-Path $script:installRoot 'trust.json'
}
$release = Verify-Release $Manifest $Trust
if ($release.schema -ne 1 -or $release.product -ne 'ARTEX' -or $release.platform -ne 'win32' -or $release.arch -notin @('x64', 'arm64') -or $release.sha256 -notmatch '^[a-f0-9]{64}$' -or $release.size -lt 1 -or $release.size -gt 2147483648) { throw '배포 정보가 올바르지 않습니다.' }
$releaseVersion = Parse-Version $release.version
if ($previous -and $releaseVersion -le (Parse-Version $previous)) { throw '같은 버전이나 이전 버전으로 설치할 수 없습니다.' }
if ((Get-Item -LiteralPath $Archive).Length -ne $release.size -or (File-SHA256 $Archive) -ne $release.sha256) { throw '설치 파일의 크기 또는 SHA256이 배포 정보와 다릅니다.' }
$expected = @{}
foreach ($property in $release.files.PSObject.Properties) {
  Assert-FileName $property.Name
  if ($expected.ContainsKey($property.Name) -or $property.Value -notmatch '^[a-f0-9]{64}$') { throw '설치 파일 목록이 중복되거나 올바르지 않습니다.' }
  $expected[$property.Name] = $property.Value
}
if ($expected.ContainsKey('.artex-version-owner.txt') -or $expected.ContainsKey('.artex-ready-failed.txt')) { throw '설치 소유권 파일은 배포 묶음에 넣을 수 없습니다.' }
foreach ($required in @('ARTEX.exe', 'resources/app/package.json', 'resources/artex/artex.exe', 'resources/artex/installer/ARTEX.exe', 'resources/artex/installer/ARTEX-InstallHost.exe', 'resources/artex/installer/install.ps1')) { if (-not $expected.ContainsKey($required)) { throw 'Electron·Go·화면·설치 도우미를 포함한 통합 앱 묶음이 필요합니다.' } }
if ($expected.Count -gt 20000) { throw '설치 파일 개수가 너무 많습니다.' }
[IO.Directory]::CreateDirectory($script:installRoot) | Out-Null
$stage = Join-Path $script:installRoot ('staging-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($stage) | Out-Null
$destination = Join-Path $script:installRoot ('versions\' + $release.version)
$promoted = $false
try {
  $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
  try {
    $seen = @{}; [long]$expanded = 0
    foreach ($entry in $zip.Entries) {
      if ($entry.FullName.EndsWith('/')) { Assert-FileName $entry.FullName.TrimEnd('/'); continue }
      Assert-FileName $entry.FullName
      if ((($entry.ExternalAttributes -shr 16) -band 61440) -eq 40960 -or $seen.ContainsKey($entry.FullName) -or -not $expected.ContainsKey($entry.FullName)) { throw '예상하지 않은 파일·중복 파일·연결된 파일이 포함되어 있습니다.' }
      $expanded += $entry.Length
      if ($expanded -gt 4294967296) { throw '압축 해제 크기가 제한을 초과했습니다.' }
      $seen[$entry.FullName] = $true
    }
    if ($seen.Count -ne $expected.Count) { throw '압축 파일과 배포 파일 목록이 다릅니다.' }
    foreach ($entry in $zip.Entries) {
      if ($entry.FullName.EndsWith('/')) { continue }
      $file = Join-Path $stage $entry.FullName.Replace('/', '\')
      [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($file)) | Out-Null
      [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $file, $false)
      if ((File-SHA256 $file) -ne $expected[$entry.FullName]) { throw '압축 해제한 파일의 SHA256이 배포 정보와 다릅니다.' }
    }
  } finally { $zip.Dispose() }
  if ((Read-Json (Join-Path $stage 'resources/app/package.json')).version -ne $release.version) { throw '앱 버전과 배포 버전이 다릅니다.' }
  if (-not $Development) {
    foreach ($name in @('ARTEX.exe', 'resources/artex/artex.exe', 'resources/artex/installer/ARTEX.exe', 'resources/artex/installer/ARTEX-InstallHost.exe', 'resources/artex/installer/install.ps1')) {
      if ((Get-AuthenticodeSignature -LiteralPath (Join-Path $stage $name)).Status -ne 'Valid') { throw '필수 Authenticode signature를 확인할 수 없습니다.' }
    }
  }
  if ($Mode -eq 'Update') {
    if ($ReadyNonce -notmatch '^[a-f0-9]{64}$') { throw '업데이트 준비 응답 정보가 올바르지 않습니다.' }
    foreach ($controlPath in @($ReadyFile, $PermitFile, $CancelFile, $CancelledFile)) { if (-not $controlPath -or -not [IO.Path]::IsPathRooted($controlPath)) { throw '업데이트 제어 파일 경로가 올바르지 않습니다.' } }
    $trustHash = File-SHA256 $Trust
    Release-InstallLock
    [IO.File]::WriteAllText($ReadyFile, $ReadyNonce, [Text.Encoding]::ASCII)
    Wait-ParentExit
    Acquire-InstallLock
    Assert-PublishPermitted
    if (-not (Test-Path -LiteralPath $ownershipPath)) { throw '준비 중 설치 소유권이 바뀌어 업데이트를 중단했습니다.' }
    $readyOwnership = Read-Json $ownershipPath
    if ($readyOwnership.product -ne 'ARTEX' -or $readyOwnership.schema -ne 1 -or -not (Same-PlainPath $readyOwnership.root $script:installRoot) -or [IO.File]::ReadAllText($currentPath).Trim() -ne $previous -or (File-SHA256 $Trust) -ne $trustHash) { throw '준비 중 설치 상태가 바뀌어 업데이트를 중단했습니다.' }
  }
  Assert-AppStopped
  if (Test-Path -LiteralPath $destination) {
    [void](Assert-OwnedPath $destination)
    $ownerFile = Join-Path $destination '.artex-version-owner.txt'
    $failedFile = Join-Path $destination '.artex-ready-failed.txt'
    if (-not (Test-Path -LiteralPath $ownerFile) -or -not (Test-Path -LiteralPath $failedFile)) { throw '기존 버전 폴더의 설치 소유권과 실패 상태를 확인할 수 없습니다.' }
    [void](Assert-OwnedPath $ownerFile); [void](Assert-OwnedPath $failedFile)
    $versionOwner = [IO.File]::ReadAllLines($ownerFile)
    if ($versionOwner.Length -ne 4 -or $versionOwner[0] -ne 'ARTEX/1' -or $versionOwner[1] -ne $canonicalRoot -or $versionOwner[2] -ne $release.version -or $versionOwner[3] -notmatch '^[a-f0-9]{64}$' -or [IO.File]::ReadAllText($failedFile) -ne ($versionOwner[3] + "`n" + $release.version) -or [IO.File]::ReadAllText($currentPath).Trim() -eq $release.version) { throw '활성 버전이나 소유권이 다른 폴더를 교체할 수 없습니다.' }
    # 실패 묶음은 설치 폴더 밖에 보존한다. 제거 뒤 같은 경로의 재설치를 막지 않는다.
    $quarantineParent = [IO.Path]::GetDirectoryName($script:installRoot)
    $quarantine = Assert-PlainPath (Join-Path $quarantineParent ('ARTEX-quarantine-' + $mutexHash + '-' + $release.version + '-' + [Guid]::NewGuid().ToString('N')))
    if ([IO.Path]::GetDirectoryName($quarantine) -ne $quarantineParent -or (Test-Path -LiteralPath $quarantine)) { throw '실패 버전을 보존할 독립 폴더를 확인할 수 없습니다.' }
    foreach ($item in Get-ChildItem -LiteralPath $destination -Force -Recurse) {
      if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw '연결된 파일이나 폴더가 포함된 실패 버전은 이동할 수 없습니다.' }
    }
    Assert-PublishPermitted
    [IO.Directory]::Move($destination, $quarantine)
  }
  [IO.File]::WriteAllText((Join-Path $stage '.artex-version-owner.txt'), ('ARTEX/1' + "`n" + $canonicalRoot + "`n" + $release.version + "`n" + $release.sha256), [Text.Encoding]::UTF8)
  [IO.Directory]::CreateDirectory((Join-Path $script:installRoot 'versions')) | Out-Null
  Assert-PublishPermitted
  [IO.Directory]::Move($stage, $destination)
  foreach ($name in @('ARTEX.exe', 'ARTEX-InstallHost.exe', 'install.ps1')) { Copy-Item -LiteralPath (Join-Path $destination ('resources/artex/installer/' + $name)) -Destination (Join-Path $script:installRoot $name) -Force }
  if ($Mode -eq 'Install') { Copy-Item -LiteralPath $Trust -Destination (Join-Path $script:installRoot 'trust.json') -Force }
  @{ schema = 1; product = 'ARTEX'; root = $script:installRoot; registered = $registered } | ConvertTo-Json | Set-Content -LiteralPath $ownershipPath -Encoding UTF8
  if ($previous) {
    [IO.File]::WriteAllText((Join-Path $script:installRoot 'pending.txt'), ($previous + "`n" + $release.version), [Text.Encoding]::ASCII)
    $healthy = Join-Path $script:installRoot ('healthy-' + $release.version + '.txt')
    if (Test-Path -LiteralPath $healthy) { Remove-OwnedPath $healthy }
  }
  $failureFile = Join-Path $script:installRoot 'update-failure.txt'
  if (Test-Path -LiteralPath $failureFile) { Remove-OwnedPath $failureFile }
  Write-Current $release.version
  $promoted = $true
  if ($registered) {
    New-Item -Path $uninstallKey -Force | Out-Null
    $command = '"' + (Join-Path $script:installRoot 'ARTEX-InstallHost.exe') + '" "--script=' + (Join-Path $script:installRoot 'install.ps1') + '" -Mode Uninstall -Root "' + $script:installRoot + '"'
    foreach ($pair in @{ DisplayName = 'ARTEX'; DisplayVersion = $release.version; Publisher = 'ARTEX'; InstallLocation = $script:installRoot; DisplayIcon = (Join-Path $script:installRoot 'ARTEX.exe'); UninstallString = $command }.GetEnumerator()) { New-ItemProperty -LiteralPath $uninstallKey -Name $pair.Key -Value $pair.Value -PropertyType String -Force | Out-Null }
    $shell = New-Object -ComObject WScript.Shell
    if (Test-Path -LiteralPath $shortcut) { if (-not (Same-PlainPath ($shell.CreateShortcut($shortcut).TargetPath) (Join-Path $script:installRoot 'ARTEX.exe'))) { throw '다른 앱의 바로가기를 변경할 수 없습니다.' } }
    $link = $shell.CreateShortcut($shortcut); $link.TargetPath = Join-Path $script:installRoot 'ARTEX.exe'; $link.WorkingDirectory = $script:installRoot; $link.Save()
  }
  if ($Restart) { Restart-Installed }
  Write-Output ('ARTEX ' + $release.version + '를 설치했습니다. 사용자 데이터는 보존했습니다.')
} finally {
  Acquire-InstallLock
  if (Test-Path -LiteralPath $stage) { Remove-OwnedPath $stage }
}
} catch {
  $cancelled = $_.Exception -is [OperationCanceledException]
  if ($promoted -and $previous -and (Test-Path -LiteralPath $currentPath)) {
    Acquire-InstallLock
    if ([IO.File]::ReadAllText($currentPath).Trim() -eq $release.version) { Write-Current $previous }
  }
  if ($cancelled -and $CancelledFile) { [IO.File]::WriteAllText($CancelledFile, $ReadyNonce, [Text.Encoding]::ASCII) }
  if (-not $cancelled -and $Mode -eq 'Update' -and $Restart -and $script:parentExited -and (Test-Path -LiteralPath $ownershipPath)) {
    Acquire-InstallLock
    [IO.File]::WriteAllText((Join-Path $script:installRoot 'update-failure.txt'), ('업데이트를 적용하지 못해 기존 앱을 유지했습니다: ' + $_.Exception.Message))
    Restart-Installed
  }
  throw
} finally {
  Release-InstallLock
  $script:installMutex.Dispose()
}
