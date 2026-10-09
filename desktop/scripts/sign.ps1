param([Parameter(Mandatory = $true)][string]$File)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([string]::IsNullOrWhiteSpace($env:ARTEX_SIGN_CERTIFICATE)) { throw 'ARTEX_SIGN_CERTIFICATE must contain the thumbprint of a code-signing certificate in Cert:\CurrentUser\My.' }
if ([string]::IsNullOrWhiteSpace($env:ARTEX_SIGN_TIMESTAMP)) { throw 'ARTEX_SIGN_TIMESTAMP must be an HTTPS timestamp service URL.' }
$timestamp = [Uri]$env:ARTEX_SIGN_TIMESTAMP
if ($timestamp.Scheme -ne 'https' -or $timestamp.UserInfo) { throw 'A credential-free HTTPS timestamp URL is required.' }
$thumbprint = $env:ARTEX_SIGN_CERTIFICATE.Replace(' ', '')
if ($thumbprint -notmatch '^[a-fA-F0-9]{40}$') { throw 'The certificate thumbprint is invalid.' }
$certificate = Get-Item -LiteralPath ('Cert:\CurrentUser\My\' + $thumbprint) -ErrorAction Stop
if (-not $certificate.HasPrivateKey -or $certificate.NotAfter -le (Get-Date) -or $certificate.NotBefore -gt (Get-Date) -or -not ($certificate.EnhancedKeyUsageList.ObjectId -contains '1.3.6.1.5.5.7.3.3')) { throw 'A current certificate with a private code-signing key is required.' }
$result = Set-AuthenticodeSignature -LiteralPath ([IO.Path]::GetFullPath($File)) -Certificate $certificate -HashAlgorithm SHA256 -TimestampServer $timestamp.AbsoluteUri
if ($result.Status -ne 'Valid' -or -not $result.TimeStamperCertificate) { throw 'Authenticode signing or timestamp verification failed.' }
