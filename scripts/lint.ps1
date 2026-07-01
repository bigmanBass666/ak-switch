<#
.SYNOPSIS
    Run Go static analysis tools on the Alvus project
.DESCRIPTION
    Runs go vet. Exits 1 if any warnings found.
#>

$ErrorActionPreference = "Stop"
$hasError = $false

Write-Host "Running go vet..." -ForegroundColor Cyan
$vetResult = go vet ./... 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "go vet found issues:" -ForegroundColor Red
    Write-Host $vetResult -ForegroundColor Red
    $hasError = $true
} else {
    Write-Host "go vet: clean" -ForegroundColor Green
}

if ($hasError) {
    Write-Host "Lint check failed" -ForegroundColor Red
    exit 1
}

Write-Host "All lint checks passed!" -ForegroundColor Green
