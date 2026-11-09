#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 Andrea Casali
#
# How many Macs ran Benbow, and how many times the app was downloaded:
#
#   scripts/stats.sh          # the last 30 days
#   scripts/stats.sh -d 90
#
# Daily checks come from stats/manifest-checks.csv in benbowdev/benbow-binaries
# (docs/updates.md): one row per life of the release asset Benbow's
# automatic update check downloads, at most once a day per Mac. Rows are
# added to the UTC day their period ends. App downloads are the DMG assets
# of benbowdev/benbow's releases. Needs gh (logged in) and jq.
set -euo pipefail

days=30
while getopts "d:" opt; do
	case $opt in
	d) days=$OPTARG ;;
	*)
		echo "usage: scripts/stats.sh [-d days]" >&2
		exit 2
		;;
	esac
done
[[ "$days" =~ ^[0-9]+$ ]] || { echo "-d wants a number of days" >&2; exit 2; }

echo "Daily update checks (≈ Macs running Benbow), last $days days"
# gh prints GitHub's error body on stdout: only keep the answer if it worked.
# BENBOW_STATS_CSV reads a local copy instead.
if [[ -n "${BENBOW_STATS_CSV:-}" ]]; then
	csv=$(cat "$BENBOW_STATS_CSV")
else
	csv=$(gh api -H "Accept: application/vnd.github.raw" repos/benbowdev/benbow-binaries/contents/stats/manifest-checks.csv 2>/dev/null) || csv=""
fi
if [[ -z "$csv" ]]; then
	echo "  no data yet (the first row comes the day after the manifest release is created)"
else
	since=$(date -u -v-"${days}"d +%Y-%m-%d)
	printf '%s\n' "$csv" | awk -F, -v since="$since" '
		NR == 1 { next }
		{
			day = substr($2, 1, 10)
			if (day < since) next
			checks[day] += $3
			periods[day]++
		}
		END {
			n = 0
			for (d in checks) days[++n] = d
			# insertion sort, oldest first
			for (i = 2; i <= n; i++) { v = days[i]; j = i - 1; while (j > 0 && days[j] > v) { days[j + 1] = days[j]; j-- } days[j + 1] = v }
			for (i = 1; i <= n; i++) {
				note = periods[days[i]] > 1 ? "  (" periods[days[i]] " periods)" : ""
				printf "  %s  %6d%s\n", days[i], checks[days[i]], note
			}
		}'
fi
current=$(gh api repos/benbowdev/benbow-binaries/releases/tags/manifest --jq '.assets[] | select(.name == "manifest.json") | "\(.download_count) since \(.created_at)"' 2>/dev/null) || current=""
echo "  now:        ${current:-no release asset yet}"

echo
echo "App downloads (DMG) per version"
gh api --paginate repos/benbowdev/benbow/releases \
	--jq '.[] | select(.draft | not) | [.tag_name, ([.assets[] | select(.name | endswith(".dmg")) | .download_count] | add // 0)] | @tsv' |
	awk -F'\t' '{ printf "  %-20s %6d\n", $1, $2; total += $2 } END { printf "  %-20s %6d\n", "total", total }'
