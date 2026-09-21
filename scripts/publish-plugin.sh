#!/usr/bin/env bash
# Copies plugins/adaa into a checkout of getadaa/plugins, stamps the release
# version, and makes sure both marketplace catalogues list it.
#
#   scripts/publish-plugin.sh <version> <path-to-plugins-checkout>
set -euo pipefail

version="${1:?version}"
dest="${2:?path to getadaa/plugins checkout}"
src="$(cd "$(dirname "$0")/.." && pwd)/plugins/adaa"

rm -rf "$dest/plugins/adaa"
mkdir -p "$dest/plugins"
cp -R "$src" "$dest/plugins/adaa"

for manifest in "$dest/plugins/adaa/.claude-plugin/plugin.json" "$dest/plugins/adaa/plugin.json"; do
  jq --arg v "$version" '.version = $v' "$manifest" > "$manifest.tmp" && mv "$manifest.tmp" "$manifest"
done

description="$(jq -r .description "$src/.claude-plugin/plugin.json")"

claude="$dest/.claude-plugin/marketplace.json"
jq --arg d "$description" --arg v "$version" '
  .plugins |= (map(select(.name != "adaa")) + [{name: "adaa", source: "./plugins/adaa", description: $d, version: $v}])
' "$claude" > "$claude.tmp" && mv "$claude.tmp" "$claude"

codex="$dest/.agents/plugins/marketplace.json"
jq '
  .plugins |= (map(select(.name != "adaa")) + [{
    name: "adaa",
    source: {source: "local", path: "./plugins/adaa"},
    policy: {installation: "AVAILABLE", authentication: "ON_INSTALL"},
    category: "Productivity"
  }])
' "$codex" > "$codex.tmp" && mv "$codex.tmp" "$codex"
