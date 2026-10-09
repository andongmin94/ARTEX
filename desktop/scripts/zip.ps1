param([Parameter(Mandatory = $true)][string]$Source, [Parameter(Mandatory = $true)][string]$Destination)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
Add-Type -AssemblyName System.IO.Compression
$root = [IO.Path]::GetFullPath($Source).TrimEnd('\') + '\'
$output = [IO.File]::Open([IO.Path]::GetFullPath($Destination), [IO.FileMode]::CreateNew)
$archive = New-Object IO.Compression.ZipArchive($output, [IO.Compression.ZipArchiveMode]::Create, $false)
try {
  foreach ($file in Get-ChildItem -LiteralPath $root -Recurse -File) {
    $name = $file.FullName.Substring($root.Length).Replace('\', '/')
    $entry = $archive.CreateEntry($name, [IO.Compression.CompressionLevel]::Optimal)
    $input = [IO.File]::OpenRead($file.FullName); $stream = $entry.Open()
    try { $input.CopyTo($stream) } finally { $stream.Dispose(); $input.Dispose() }
  }
} finally { $archive.Dispose(); $output.Dispose() }
