# adaa

Run your company's IT from the terminal. `adaa` is the command line for
[adaa](https://adaa.no): the people, computers, subscriptions, mailboxes,
domains, servers and backups you have, whether they work, and what happens next.

```console
$ adaa status
● Firma AS  attention
  One backup has not run since Friday.

  People    12 active · 1 starting soon
  Findings  1 warning
  Tasks     1 waiting for your approval
  Cost      8 250,00 kr per month
```

## Install

```sh
brew install getadaa/tap/adaa                                                      # macOS, Linux
scoop bucket add getadaa https://github.com/getadaa/scoop-bucket && scoop install adaa  # Windows
```

`.deb`, `.rpm` and `.apk` packages are attached to every
[release](https://github.com/getadaa/cli/releases). Then:

```sh
adaa login          # a sign-in link is sent to your email
adaa doctor --fix   # checks the version, config, API, session and agent skill
```

## Using it

```sh
adaa status                          # how everything is doing
adaa report "The printer jams"       # tell adaa something is wrong
adaa people add                      # onboard someone, with a cost preview
adaa people offboard kari@firma.no
adaa tasks list --awaiting-approval
adaa members list                    # who may sign in, and as what
adaa switch bjerk                    # act on another company you work for
adaa devices register                # record this computer
adaa devices discover                # find devices on your network adaa does not know
adaa domains check firma.no          # verify DNS from this computer
adaa show fnd_01JATX…                # show any id
adaa api /me                         # anything else the API offers
```

Every command has `--help` with examples. Some conventions:

- Records can be named the way you would say them: an email, a hostname, a
  domain. Leave the argument out and you get a picker.
- **Who works here and who may sign in are two lists.** `adaa people` is the
  estate — an employee with a mailbox and a laptop — and most of a company has
  no way into the portal. `adaa members` is access: a role held *in* a company,
  so if you work for two of them you hold one at each. `adaa switch` moves
  between them, without signing in again.
- Changes that cost money or remove something show a preview and ask first.
  `--dry-run` shows the preview only; `--yes` skips the question.
- `--json` prints exactly what the API returned. Tables become tab-separated
  when piped.
- Without a terminal, adaa never prompts. It fails with exit code 2 and names
  the flag it needed.
- `NO_COLOR`, `ADAA_TOKEN`, `ADAA_API_URL` and `ADAA_NO_INPUT` are respected.

Exit codes: `0` ok, `1` error, `2` usage, `3` not logged in, `4` not allowed,
`5` not found, `6` refused by the API, `7` API unreachable, `130` cancelled.

## AI agents

The adaa skill teaches agents such as Claude Code and Codex to use this CLI
safely. Install it from the marketplace:

```sh
/plugin marketplace add getadaa/plugins   # Claude Code
/plugin install adaa@adaa
```

or with `adaa skill install`, which downloads the same file. `adaa doctor`
checks that every installed copy matches the published one, and `--fix`
updates them.

## Development

```sh
nix develop          # go, goreleaser, gh, jq, nmap
go test ./...
go run ./cmd/adaa --help
```

The CLI talks to `https://adaa.no/api/v1`. Point it at a local API with
`ADAA_API_URL=http://localhost:8080/v1`.

The skill lives in `plugins/adaa/skills/adaa/SKILL.md`. Change it in the same
pull request as the commands it describes; `TestSkillMatchesCommandTree`
fails when it mentions a command or flag that does not exist.

Releasing is described in [docs/releasing.md](docs/releasing.md).
