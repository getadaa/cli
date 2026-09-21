---
name: adaa
description: Run a company's IT through adaa with the `adaa` CLI — people (onboarding and offboarding), devices, findings (problems), tasks, requests, subscriptions and cost, mail, domains and DNS, workspaces (Microsoft 365, Google Workspace), servers, backups and credentials. Use whenever the user mentions adaa, the `adaa` command, or asks to check, change or report something about their company's IT that adaa manages.
allowed-tools: Bash(adaa *), Bash(adaa)
---

# adaa

adaa runs IT for small companies. `adaa` is its command line. Everything it
touches is real: a person added here gets a mailbox and a paid seat, an
offboarding revokes access, a restore overwrites live data. Treat every change
as something the user will be billed for or will have to live with.

## Start of every session

```sh
adaa doctor --fix
adaa --help
```

`adaa doctor --fix` checks, all at once: that this is the latest adaa, that
the config is intact, that the API is reachable (and the clock is right), that
the session works, and that **this skill** is installed and identical to the
published copy for every agent on the computer. It updates what it can: the
CLI (through Homebrew, Scoop or a verified download), the skill, and a session
kept outside the keychain. What it cannot fix it prints as a hint. Relay those
to the user, especially "Not logged in" — signing in needs a person.

`adaa doctor --json` gives the same checks as `{"ok": bool, "checks": [...]}`.

Then trust `adaa <command> --help` over this file for flags. This file
explains how to work; the help explains what exists in the version installed.

## Staying up to date

- `adaa update` updates adaa the way it was installed. `adaa update --check`
  only reports.
- `adaa doctor --fix` does the same and also brings this skill up to date.
- The skill is published in the `getadaa/plugins` marketplace. Claude Code
  users can install it with `/plugin marketplace add getadaa/plugins` and
  `/plugin install adaa@adaa`; anyone can use `adaa skill install`. Both are
  the same file. `adaa skill status` lists every copy and whether it is current.
- If a command in this file does not exist, the CLI is older than the skill:
  run `adaa update`.

## You have no terminal

adaa never prompts without one. Instead it fails with **exit code 2** and
names the flag it needed. So:

- **Changes need `--yes`** (`-y`). Onboarding, offboarding, revoking, deleting,
  restarting, restoring, approving: without a terminal they refuse unless
  `--yes` is passed. `--yes` answers the confirmation; it lowers no other guard.
- **Preview first with `--dry-run`.** Commands that cost money or raise work
  (`adaa people add`, `adaa people offboard`, `adaa people grant`,
  `adaa findings resolve`, `adaa licenses seats`, `adaa servers order`,
  `adaa backups restore`, `adaa mail setup`, …) take `--dry-run` and print
  what would happen, what needs approval and what it does to the monthly bill.
  Show that to the user and get their go-ahead before running it again with
  `--yes`. Never pass `--yes` to a change the user has not agreed to.
- **Arguments must be explicit.** Given no record, commands open a picker;
  without a terminal that fails, so always pass the record.
- **Sign-in is two steps.** `adaa login --email <address>` sends a link; the
  user pastes it back and you run `adaa login --code '<link>'`. Or the user
  sets `ADAA_TOKEN` (from `adaa tokens create`).

## Reading output

- `--json` prints exactly what the API returned. Lists are
  `{"items": [...], "next_cursor": ...}`. Prefer it whenever you parse.
- Lists fetch 50 items by default; `--all` follows every page.
- Data goes to stdout; progress, hints and errors go to stderr. With `--json`,
  an API error is printed to stderr as its JSON problem document.
- API errors carry `title`, `detail` and `how_to_resolve`, written for people.
  Pass them on rather than guessing past them. `resolvable_by: adaa` means
  nobody on the customer side can fix it.

Exit codes: `0` ok · `1` error · `2` usage, or input needed without a terminal ·
`3` not logged in · `4` not allowed · `5` not found · `6` refused by the API
(conflict, invalid, blocked) · `7` API unreachable or failing · `130` cancelled.

## Naming things

Every id says what it is: `per_` person, `dev_` device, `fnd_` finding,
`tsk_` task, `req_` request, `dom_` domain, `mbx_` mailbox, `srv_` server,
`bkp_` backup, `crd_` credential, `lic_` license, `sub_` subscription,
`wsp_` workspace. `adaa show <id>` displays any of them.

Commands also accept what a person would say: an email or name for people
(`me` for the current user), a hostname or serial number for devices, a
domain name, a mailbox address. When a name matches several records the
command fails and lists their ids; pick one and pass the id.

## How adaa thinks

adaa compares what the company **should** have (people and their
entitlements) with what **exists** (resources, measured by signals). The
difference is a **finding**. Closing it is a **task**. A **request** is a
conversation with adaa's people.

- Onboarding is `adaa people add` with the services the person should have.
  adaa works out the mailbox, seats and accounts and raises the tasks.
  Offboarding is `adaa people offboard`. There is no separate provisioning.
- Something broken is a finding, whoever noticed it: `adaa report "..."`.
  A request (`adaa requests new`) is for wanting or asking something.
- Anything that revokes, deletes or wipes waits for a person's approval.
  `adaa tasks list --awaiting-approval` shows them; `adaa tasks approve <task>`
  approves. **Only approve when the user explicitly asks for that task.**
- Changes that take time return a task. `--wait` follows it to the end;
  `adaa tasks wait <task>` does the same later.

## Common work

```sh
adaa status                                   # the front page: health, open problems, cost
adaa findings list --severity critical
adaa findings view fnd_…
adaa findings resolve fnd_… --dry-run         # what fixing it does and costs
adaa report "Printer on 2nd floor jams" --blocking --diagnostics --yes

adaa people list
adaa people view kari@firma.no
adaa people add --name "Ola Hansen" --email ola@firma.no --starts-on 2026-10-01 --service m365-business --dry-run
adaa people offboard ola@firma.no --last-day 2026-12-31 --forward-mail-to kari@firma.no --dry-run

adaa tasks list --awaiting-approval
adaa requests new --kind question --title "..." --body "..."
adaa requests reply req_… "Thanks, that works."

adaa devices list --unassigned
adaa devices register --assign me             # records the computer adaa runs on
adaa devices discover --json                  # hosts on the local network, known or new
adaa licenses list --spare-seats
adaa cost
adaa activity --since 24h
```

Products each have their own group: `adaa mail`, `adaa domains` (with `dns`,
`spf`, `transfer`), `adaa workspaces`, `adaa servers`, `adaa backups`.
`adaa products` lists them. For anything without a command, `adaa api <path>`
calls the API directly (`{org}` in the path is filled in), e.g.
`adaa api '/organizations/{org}/people' --paginate`.

## Checks you can run from this computer

- `adaa domains check <domain>` resolves every record adaa expects against
  public DNS and reports ok / propagating / missing / different. Exit 1 while
  anything is not live. `--watch` repeats until it is.
- `adaa devices discover` scans only the networks this computer is on, with
  nmap when installed. Only run it when the user asks, on a network they may
  scan. Without a terminal it records nothing unless given `--add --yes`.
- `adaa devices register --print` shows what would be recorded about this
  computer without sending anything.
- `adaa report --diagnostics` attaches a snapshot of this computer (OS, disk,
  network, DNS). Mention to the user that it is attached.

## Rules

1. Run `adaa doctor --fix` at the start and whenever something fails
   unexpectedly. Report what it could not fix.
2. Use `--dry-run` before any change that costs money or removes something,
   and show the user the preview. Pass `--yes` only after they agree.
3. Never approve tasks, restore backups, reveal credentials
   (`adaa credentials reveal`), print a domain auth code (`adaa domains auth-code`)
   or reset passwords unless the user asked for exactly that. Reveals are
   logged against the user, and the API may refuse agents outright
   (`agents-may-not-reveal`): tell the user to do it themselves.
4. Never put secrets in command lines. `adaa credentials add` and
   `adaa credentials rotate` read them with `--secret-stdin`.
5. Prefer `--json` when you need to read results, and ids over names once
   you have them.
6. When the API refuses something (exit 6), read `how_to_resolve` and relay
   it; do not retry the same call hoping for a different answer.
