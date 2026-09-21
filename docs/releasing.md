# Releasing

Push a `v*` tag from `main`:

```sh
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` then does two things, in order:

1. **`release`** runs GoReleaser: binaries for macOS, Linux and Windows (amd64
   and arm64), archives with shell completions, `checksums.txt`, `.deb`/`.rpm`/`.apk`
   packages, the Homebrew cask in `getadaa/homebrew-tap` and the Scoop manifest
   in `getadaa/scoop-bucket`.
2. **`plugin`** copies `plugins/adaa/` into `getadaa/plugins`, stamps the
   version into both plugin manifests, lists the plugin in both marketplace
   catalogues, and merges that as a pull request.

The plugin goes out after the binaries so the published skill never describes
a command nobody can install yet.

## The skill

`plugins/adaa/skills/adaa/SKILL.md` is the only copy of the skill. It is
edited here, in the same pull request as the commands it describes, and
`TestSkill*` fails the build when it mentions a command or flag the binary does
not have.

Users get it from the marketplace (`/plugin install adaa@adaa`) or with
`adaa skill install`, which downloads the published file from
`getadaa/plugins`. Both are the same bytes, and `adaa doctor` compares every
copy on a computer against the published one.

## Credentials

Both jobs run in the `release` environment and mint short-lived tokens from
the `getadaa-release` GitHub App (see the infra repository). The App must be
installed on `homebrew-tap`, `scoop-bucket` **and `plugins`**, with
`contents: write` and, for `plugins`, `pull_requests: write`.

## Checking a release locally

```sh
goreleaser release --snapshot --clean --skip=publish
scripts/publish-plugin.sh 0.0.0-local /path/to/plugins-checkout
```
