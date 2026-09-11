# Runs `ingest` against a prioritized list of the largest source board files
# (the ones with the most companies/postings), draining each one into the
# live search index right after so the site stays browsable while this runs.
# Safe to re-run or interrupt: `ingest` upserts, it never duplicates.
#
# One-time setup before running this for the first time:
#   docker compose up -d --build app     # picks up search-drain + the sources volume mount
#
# Usage:
#   .\ingest-batch.ps1                   # runs the full list below
#   .\ingest-batch.ps1 -First 3          # just the first N, to sanity-check timing first

param(
    [int]$First = 0
)

# Ordered roughly by file size (a proxy for company count) -- these are the
# real ATS platforms with the deepest company coverage in sources/.
$sources = @(
    "paylocity.yml",
    "bamboohr.yml",
    "workday.yml",
    "ukg.yml",
    "greenhouse.yml",
    "jazzhr.yml",
    "join.yml",
    "personio.yml",
    "smartrecruiters.yml",
    "ashby.yml",
    "apploi.yml",
    "hireology.yml",
    "teamtailor.yml",
    "isolvedhire.yml",
    "breezy.yml",
    "applicantpro.yml",
    "zohorecruit.yml",
    "recruitee.yml",
    "lever.yml",
    "workable.yml",
    "icims.yml",
    "solides.yml",
    "oracle.yml",
    "gupy.yml"
)

if ($First -gt 0) {
    $sources = $sources[0..($First - 1)]
}

$startedAt = Get-Date
foreach ($src in $sources) {
    Write-Host "`n==> [$($startedAt.ToString('HH:mm:ss')) -> now] Ingesting $src ..." -ForegroundColor Cyan
    docker compose exec -T app /app/ingest "sources/$src"
    if ($LASTEXITCODE -ne 0) {
        Write-Host "    ingest reported a non-zero exit for $src -- continuing to the next source" -ForegroundColor Yellow
    }

    Write-Host "==> Draining into the live search index ..." -ForegroundColor Cyan
    docker compose exec -T app /app/search-drain
    if ($LASTEXITCODE -ne 0) {
        Write-Host "    search-drain reported an issue -- continuing" -ForegroundColor Yellow
    }
}

Write-Host "`n==> Recomputing company counts/facets ..." -ForegroundColor Cyan
docker compose exec -T app /app/recount-companies

Write-Host "`nBatch done. Started $($startedAt), finished $(Get-Date)." -ForegroundColor Green
