#!/usr/bin/env bash
#
# Assemble the pages directory that GitHub Pages deploys from the Actions
# artifact, and keep the same tree on a branch so past reports survive a run.
#
# The layout:
#
#   /                     the project site, built by Hugo into SITE_DIR
#   /allure/<run>/        the Allure report for one workflow run
#   /allure/latest/       a copy of the most recent one, for a stable link
#   /allure/              an index of the runs still kept
#
# Each run gets its own directory on purpose. A single "latest" report would
# make every link in every past run's summary point at whatever ran last, which
# is exactly the question a summary is asked to answer months later.
#
# Pages deploys an artifact, so the artifact has to be the whole site every
# time — which means the previous reports have to be fetched from the branch
# that stores them before this run's report is added to it.
#
# Environment:
#   GITHUB_REPOSITORY  owner/name                  (required)
#   GITHUB_RUN_NUMBER  the run number              (required)
#   GITHUB_TOKEN       pushes the history branch   (optional: without it the
#                                                  tree is assembled, not stored)
#   GITHUB_SHA         the commit published        (optional, recorded)
#   SITE_DIR           the built Hugo site         (default: public)
#   REPORT_DIR         the Allure report           (default: allure-report)
#   SITE_OUTPUT        where to assemble           (default: _site)
#   HISTORY_BRANCH     where reports are stored    (default: gh-pages)
#   KEEP_RUNS          how many reports kept       (default: 10)
#   PAGES_URL          the site's base URL         (default: derived from the repo)
#
set -euo pipefail

repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
run="${GITHUB_RUN_NUMBER:?GITHUB_RUN_NUMBER is required}"
sha="${GITHUB_SHA:-unknown}"
branch="${HISTORY_BRANCH:-gh-pages}"
keep="${KEEP_RUNS:-10}"

site_dir="${SITE_DIR:-public}"
report_dir="${REPORT_DIR:-allure-report}"
output="${SITE_OUTPUT:-_site}"

# Resolve both inputs before changing directory, or the copies below read from
# the wrong place.
site_dir=$(cd "$site_dir" && pwd)
report_dir=$(cd "$report_dir" && pwd)

owner="${repo%%/*}"
name="${repo##*/}"
pages_url="${PAGES_URL:-https://$(printf '%s' "$owner" | tr '[:upper:]' '[:lower:]').github.io/${name}}"
pages_url="${pages_url%/}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# A token turns the clone into one that can see a private repository; without
# one, a public repository clones anonymously, which is what lets this script be
# run by hand.
if [ -n "${GITHUB_TOKEN:-}" ]; then
  auth="x-access-token:${GITHUB_TOKEN}@"
else
  auth=""
fi

# 1. Fetch the reports from previous runs. They live on a branch rather than in
#    the artifact because an artifact belongs to one run and is not carried to
#    the next one.
history="$work/history"
mkdir -p "$history"
if git clone --quiet --depth=1 --branch "$branch" \
     "https://${auth}github.com/${repo}.git" \
     "$history" 2>/dev/null; then
  echo "carrying forward the reports on ${branch}"
else
  echo "no ${branch} branch yet; this run starts the history"
  rm -rf "$history"
  mkdir -p "$history"
fi

# 2. Assemble the tree that will be deployed. It is built outside the checkout
#    so a run never leaves a half-written site behind in the working tree.
rm -rf "$output"
mkdir -p "$output"
cp -R "${site_dir}"/. "$output"/
: > "$output/.nojekyll"

if [ -d "$history/allure" ]; then
  cp -R "$history/allure" "$output/allure"
fi
mkdir -p "$output/allure"
rm -rf "$output/allure/${run}"
cp -R "${report_dir}" "$output/allure/${run}"
rm -rf "$output/allure/latest"
cp -R "$output/allure/${run}" "$output/allure/latest"

# 3. Keep the newest reports and drop the rest: a report is tens of megabytes,
#    and a branch nobody prunes becomes a clone nobody wants.
kept=$(ls -1 "$output/allure" | grep -E '^[0-9]+$' | sort -n || true)
count=$(printf '%s\n' "$kept" | grep -c . || true)
if [ "${count:-0}" -gt "$keep" ]; then
  printf '%s\n' "$kept" | head -n "-${keep}" | while read -r old; do
    [ -n "$old" ] && rm -rf "$output/allure/${old}"
  done
fi
kept=$(ls -1 "$output/allure" | grep -E '^[0-9]+$' | sort -n || true)

# 4. An index, so /allure/ is browsable rather than a 403.
{
  cat <<'HTML'
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>hammer — test reports</title>
    <style>
      body { background: #272822; color: #f8f8f2; font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 0; }
      .wrap { max-width: 40rem; margin: 0 auto; padding: 3rem 1.5rem; }
      h1 { font-size: 1.4rem; letter-spacing: -0.02em; }
      p { color: #a8a8a2; }
      a { color: #a6e22e; text-decoration: none; }
      a:hover { border-bottom: 1px solid #a6e22e; }
      ul { list-style: none; padding: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
      li { padding: 0.45rem 0; border-bottom: 1px solid #49483e; }
      .latest { color: #a8a8a2; font-size: 0.85rem; margin-left: 0.5rem; }
    </style>
  </head>
  <body>
    <div class="wrap">
      <h1>Test reports</h1>
      <p>One Allure report per CI run. The newest is also available as <a href="latest/">latest</a>.</p>
      <ul>
HTML
  newest=$(printf '%s\n' "$kept" | sort -n | tail -1)
  printf '%s\n' "$kept" | sort -nr | while read -r one; do
    [ -z "$one" ] && continue
    note=""
    if [ "$one" = "$newest" ]; then
      note='<span class="latest">newest</span>'
    fi
    printf '        <li><a href="%s/">run %s</a>%s</li>\n' "$one" "$one" "$note"
  done
  cat <<'HTML'
      </ul>
      <p><a href="../">← the project</a></p>
    </div>
  </body>
</html>
HTML
} > "$output/allure/index.html"

url="${pages_url}/allure/${run}/"
echo "assembled ${output} for ${url}"

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  {
    echo "url=${url}"
    echo "site=${pages_url}/"
  } >> "$GITHUB_OUTPUT"
fi

# 5. Store the tree on the history branch. Without a token this is a dry run —
#    the artifact is still assembled and deployed, it just will not be carried
#    to the next run.
if [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "no token: assembled only, nothing stored for the next run"
  exit 0
fi

cd "$output"
git init -q
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git remote add origin "https://x-access-token:${GITHUB_TOKEN}@github.com/${repo}.git"
if git fetch -q --depth=1 origin "$branch" 2>/dev/null; then
  git checkout -q -B "$branch" FETCH_HEAD
else
  git checkout -q --orphan "$branch"
fi
git add -A
if git diff --cached --quiet; then
  echo "history branch already holds this run"
else
  git commit -q -m "Publish the site and the test report for ${sha:0:7} (run ${run})"
  git push -q origin "$branch"
  echo "stored on ${branch}"
fi
