Set-StrictMode -Version latest
$ErrorActionPreference = "Stop"

$RunningAsAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")
if (!$RunningAsAdmin) {
  Write-Error "Must be executed in Administrator level shell."
  exit 1
}

Try {
    Write-Output "Updating to a Docker version that supports LCOW"
    Invoke-WebRequest `
      -OutFile "$env:TEMP\docker-master.zip" `
      "https://master.dockerproject.com/windows/x86_64/docker.zip"
    Expand-Archive `
      -Path "$env:TEMP\docker-master.zip" `
      -DestinationPath $env:ProgramFiles `
      -Force

    New-Item -ItemType Directory -Force -Path 'C:\Program Data\Docker\config'
    '{ "experimental":true }' | Out-File -FilePath 'C:\Program Data\Docker\config\daemon.json'


    Write-Output "Installing LinuxKit"
    Invoke-WebRequest `
      -UseBasicParsing `
      -OutFile release.zip `
      -uri https://github.com/linuxkit/lcow/releases/download/v4.14.35-v0.3.9/release.zip
    Expand-Archive release.zip `
      -DestinationPath "$Env:ProgramFiles\Linux Containers\."

} Catch {
    Write-Error "Failed to install Docker."
    $host.SetShouldExit(-1)
    throw
}

Write-Output "Installed Docker."
