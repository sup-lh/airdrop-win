$ErrorActionPreference = "Stop"

Write-Host "==> 1/3 构建前端界面 (web/dist)..." -ForegroundColor Cyan
Push-Location "$PSScriptRoot\web"
try {
    if (-not (Test-Path "node_modules")) {
        pnpm install
    }
    pnpm build
} finally {
    Pop-Location
}

Write-Host "==> 2/3 编译 Go 单文件可执行程序 (Windows GUI 无黑框版 + 命令行调试版)..." -ForegroundColor Cyan
New-Item -ItemType Directory -Path "$PSScriptRoot\dist" -Force | Out-Null

$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -H=windowsgui" -o "$PSScriptRoot\dist\AirDropWin.exe" .\cmd\airdrop-win
go build -trimpath -ldflags "-s -w" -o "$PSScriptRoot\dist\AirDropWin-cli.exe" .\cmd\airdrop-win

Write-Host "==> 3/3 构建完成！" -ForegroundColor Green
Get-Item "$PSScriptRoot\dist\AirDropWin*.exe" | Select-Object Name, @{Name="SizeMB";Expression={[math]::Round($_.Length/1MB, 2)}}, FullName | Format-Table -AutoSize
