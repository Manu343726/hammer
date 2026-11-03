#!/usr/bin/env bash
#
# Publish the project site and a test report to GitHub Pages.
#
# The layout on the branch is:
#
#   /                     the project page, from site/
#   /allure/<run>/        the Allure report for one workflow run
#   /allure/latest/       a copy of the most recent one, for a stable link
#   /allure/              an index of the runs still kept
#
# Each run gets its own directory on purpose. A single "latest" report would
# make every link in every past run's summary point at whatever ran last, which
# is exactly the question a summary is asked to answer months later.
#
# Environment:
#   GITHUB_REPOSITORY  owner/name            (required)
#   GITHUB_RUN_NUMBER  the run number        (required)
#   GITHUB_TOKEN       a token allowed to push the branch (required)
#   GITHUB_SHA         the commit published  (optional, recorded in the message)
#   SITE_DIR           the built Hugo site    (default: public)
#   REPORT_DIR         the Allure report     (default: allure-report)
#   BRANCH             the Pages branch      (default: gh-pages)
#   KEEP_RUNS          how many reports kept (default: 10)
#
set -euo pipefail

repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
run="${GITHUB_RUN_NUMBER:?GITHUB_RUN_NUMBER is required}"
token="${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
sha="${GITHUB_SHA:-unknown}"
branch="${BRANCH:-gh-pages}"
keep="${KEEP_RUNS:-10}"

site_dir="${SITE_DIR:-public}"
report_dir="${REPORT_DIR:-allure-report}"

# Resolve both inputs before changing directory, or the copies below read from
# the wrong place.
site_dir=$(cd "$site_dir" && pwd)
report_dir=$(cd "$report_dir" && pwd)

owner="${repo%%/*}"
name="${repo##*/}"
pages_url="https://$(printf '%s' "$owner" | tr '[:upper:]' '[:lower:]').github.io/${name}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
publish="$work/publish"
mkdir -p "$publish"
cd "$publish"

git init -q
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git remote add origin "https://x-access-token:${token}@github.com/${repo}.git"

if git fetch -q --depth=1 origin "$branch" 2>/dev/null; then
  git checkout -q -B "$branch" FETCH_HEAD
else
  echo "no ${branch} branch yet; creating it"
  git checkout -q --orphan "$branch"
fi

# The project page owns the root. Everything except the reports is replaced, so
# a page that is deleted from site/ is deleted from the branch too rather than
# lingering.
find . -mindepth 1 -maxdepth 1 ! -name .git ! -name allure -exec rm -rf {} +
cp -R "${site_dir}"/. .
# Pages runs Jekyll by default, which ignores paths beginning with an underscore
# and costs a build for files that are already static.
: > .nojekyll

mkdir -p allure
rm -rf "allure/${run}"
cp -R "${report_dir}" "allure/${run}"
rm -rf allure/latest
cp -R "allure/${run}" allure/latest

# Keep the newest reports and drop the rest: a report is tens of megabytes, and
# a branch nobody ever prunes becomes a clone nobody wants.
kept=$(ls -1 allure | grep -E '^[0-9]+$' | sort -n || true)
count=$(printf '%s\n' "$kept" | grep -c . || true)
if [ "${count:-0}" -gt "$keep" ]; then
  printf '%s\n' "$kept" | head -n "-${keep}" | while read -r old; do
    [ -n "$old" ] && rm -rf "allure/${old}"
  done
fi

# An index, so /allure/ is browsable rather than a 403.
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
} > allure/index.html

git add -A
if git diff --cached --quiet; then
  echo "nothing to publish"
else
  git commit -q -m "Publish site and test report for ${sha:0:7} (run ${run})"
  git push -q origin "$branch"
fi

url="${pages_url}/allure/${run}/"
echo "$url"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  {
    echo "url=${url}"
    echo "site=${pages_url}/"
  } >> "$GITHUB_OUTPUT"
fi
