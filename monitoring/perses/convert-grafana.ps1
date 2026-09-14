# Converts Grafana dashboard JSON (PromQL-only) to Perses dashboard JSON.
# Usage: pwsh ./convert-grafana.ps1
$ErrorActionPreference = "Stop"

$src = Join-Path $PSScriptRoot "..\grafana\dashboards"
$out = Join-Path $PSScriptRoot "provisioning\dashboards"
New-Item -ItemType Directory -Force -Path $out | Out-Null

function Get-PanelPluginKind([string]$type) {
  switch ($type) {
    "stat" { "StatChart" }
    "gauge" { "GaugeChart" }
    "bargauge" { "BarChart" }
    "table" { "Table" }
    default { "TimeSeriesChart" }
  }
}

$project = "air"
$converted = 0

Get-ChildItem -Path $src -Filter *.json | ForEach-Object {
  $g = Get-Content -LiteralPath $_.FullName -Raw | ConvertFrom-Json

  $name = if ($g.uid) { $g.uid } else { ($g.title -replace '[^A-Za-z0-9]+', '-').ToLower().Trim('-') }

  $panels = [ordered]@{}
  $items = @()
  $i = 0

  foreach ($p in $g.panels) {
    if (-not $p.targets) { continue }
    $i++
    $key = "panel-$i"

    $queries = @()
    foreach ($t in $p.targets) {
      if (-not $t.expr) { continue }
      $qspec = [ordered]@{ query = $t.expr }
      if ($t.legendFormat) { $qspec.seriesNameFormat = $t.legendFormat }
      $queries += [ordered]@{
        kind = "TimeSeriesQuery"
        spec = [ordered]@{
          plugin = [ordered]@{
            kind = "PrometheusTimeSeriesQuery"
            spec = $qspec
          }
        }
      }
    }
    if ($queries.Count -eq 0) { continue }

    $panels[$key] = [ordered]@{
      kind = "Panel"
      spec = [ordered]@{
        display = [ordered]@{ name = $p.title }
        plugin  = [ordered]@{ kind = (Get-PanelPluginKind $p.type); spec = [ordered]@{} }
        queries = $queries
      }
    }

    $gpos = $p.gridPos
    $items += [ordered]@{
      x       = [int]$gpos.x
      y       = [int]$gpos.y
      width   = [int]$gpos.w
      height  = [int]$gpos.h
      content = [ordered]@{ '$ref' = "#/spec/panels/$key" }
    }
  }

  $duration = "1h"
  if ($g.time.from -match '^now-(\d+)([mhdw])$') { $duration = "$($matches[1])$($matches[2])" }

  $dash = [ordered]@{
    kind     = "Dashboard"
    metadata = [ordered]@{ name = $name; project = $project }
    spec     = [ordered]@{
      display         = [ordered]@{ name = $g.title }
      duration        = $duration
      refreshInterval = if ($g.refresh) { $g.refresh } else { "30s" }
      panels          = $panels
      layouts         = @(
        [ordered]@{
          kind = "Grid"
          spec = [ordered]@{
            display = [ordered]@{ title = "" }
            items   = $items
          }
        }
      )
    }
  }

  $target = Join-Path $out "$name.json"
  $dash | ConvertTo-Json -Depth 40 | Set-Content -LiteralPath $target -Encoding utf8
  $converted++
  Write-Output "converted $($_.Name) -> $name.json ($i panels)"
}

Write-Output "total dashboards converted: $converted"
