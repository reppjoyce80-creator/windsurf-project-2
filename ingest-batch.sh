#!/usr/bin/env bash
# Bash port of ingest-batch.ps1 for running directly on the VPS (Linux has no
# PowerShell by default). Same source list, same behavior: runs `ingest` against
# a prioritized list of the largest source board files (the ones with the most
# companies/postings), draining each one into the live search index right after
# so the site stays browsable while this runs. Safe to re-run or interrupt --
# `ingest` upserts, it never duplicates.
#
# One-time setup before running this for the first time:
#   docker compose up -d --build app     # picks up search-drain + the sources volume mount
#
# Usage:
#   ./ingest-batch.sh                    # runs the full list below
#   ./ingest-batch.sh 3                  # just the first N, to sanity-check timing first

set -uo pipefail

first="${1:-0}"

# Ordered roughly by file size (a proxy for company count) -- these are the
# real ATS platforms with the deepest company coverage in sources/.
sources=(
  paylocity.yml
  bamboohr.yml
  workday.yml
  ukg.yml
  greenhouse.yml
  jazzhr.yml
  join.yml
  personio.yml
  smartrecruiters.yml
  ashby.yml
  apploi.yml
  hireology.yml
  teamtailor.yml
  isolvedhire.yml
  breezy.yml
  applicantpro.yml
  zohorecruit.yml
  recruitee.yml
  lever.yml
  workable.yml
  icims.yml
  solides.yml
  oracle.yml
  gupy.yml
)

if [ "$first" -gt 0 ] 2>/dev/null; then
  sources=("${sources[@]:0:$first}")
fi

started_at="$(date '+%H:%M:%S')"
for src in "${sources[@]}"; do
  echo ""
  echo "==> [$started_at -> now] Ingesting $src ..."
  docker compose exec -T app /app/ingest "sources/$src"
  if [ $? -ne 0 ]; then
    echo "    ingest reported a non-zero exit for $src -- continuing to the next source"
  fi

  echo "==> Draining into the live search index ..."
  docker compose exec -T app /app/search-drain
  if [ $? -ne 0 ]; then
    echo "    search-drain reported an issue -- continuing"
  fi
done

echo ""
echo "==> Recomputing company counts/facets ..."
docker compose exec -T app /app/recount-companies

echo ""
echo "Batch done. Started $started_at, finished $(date '+%H:%M:%S')."
