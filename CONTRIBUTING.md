# Contributing to voice

Thanks for improving `voiced`.

## Repository model

- Public mirror: <https://github.com/chatinfra/voice.git>
- Canonical source: `go/voice/` inside the ChatInfra monorepo

The public mirror exists for inspection, forks, and pull requests. It is downstream of the monorepo. Maintainers import accepted public PRs into canonical `go/voice` first, then synchronize the mirror.

Canonical `go/voice` keeps the monorepo module path used by every module under `go/` (the `super/go/<tool>` form). The mirror sync rewrites published `*.go`, `go.mod`, and `*.md` module-path references so the public repository declares `module github.com/chatinfra/voice` and public-facing examples use the mirror module path, for example:

```sh
go install github.com/chatinfra/voice/cmd/voiced@latest
```

Maintainers apply the inverse transform when importing an accepted public PR, so canonical stays on the monorepo module path. Files outside `*.go`, `go.mod`, and `*.md` are published byte-for-byte without transform.

Because Markdown is transformed too, a published `*.md` file never mentions the canonical monorepo module path. Write module documentation so every sentence carrying a module path stays true after the rewrite — the mechanical half is enforced by the mirror tooling tests, but a sentence that transforms into a false claim is caught only in review.

## Fork-and-PR flow

```sh
git clone git@github.com:<you>/voice.git
cd voice
git checkout -b my-voice-change
go test ./...
git push -u origin my-voice-change
```

Then open a pull request against `chatinfra/voice`. Include any turn-endpoint, state-file, or systemd-unit implications in the PR description.

`voiced` reserves stdout for help text and treats `status.json` as its health contract, so a change to either surface is a contract change: say so explicitly in the PR, and keep the daemon's log-only failure behavior intact — a failed prompt must still answer the caller and continue serving later turns.

## Deployment model

`voiced` is image-packaged rather than cloned per host. OpenCode hosts receive the `voice` source, the `/data/opencode/bin/voiced` launcher, and a warmed cache from the OPENCODE instance image; provisioning and reconfigure validate that packaged payload and reseed it from the delivered seed archive, and never fetch this repository at runtime.

Two consequences for contributors:

- There is no supported "edit it on the host" path here. Host-side source is a packaged, validated artifact; treat a fork and a pull request as the way to change `voiced`.
- Merging a public PR and syncing the mirror does not deploy anything. The change reaches hosts only when a new OPENCODE image is built and rolled out.

## Maintainer import and mirror sync

Maintainers import accepted public changes into canonical `go/voice`, preserving the monorepo as source of truth. For reviewed public mirror commits, generate an `mbox` patch and apply it with the monorepo helper so patch hunks are reverse-transformed back to the canonical module path:

```sh
git -C /path/to/chatinfra-voice-mirror format-patch -1 --stdout <accepted-commit> > /path/to/pr.patch
bin/import_sched_public_pr --tool voice /path/to/pr.patch /path/to/monorepo/go/voice
```

`bin/import_sched_public_pr` lives in the monorepo next to the mirror sync tooling. It rewrites patch hunk content for `*.go`, `go.mod`, and `*.md`, refuses binary or non-allowlisted path-bearing patches, and then runs `git am` in the target canonical worktree. The same helper serves the companion CLI mirrors with `--tool sched`, `--tool jmap`, `--tool specd`, or `--tool xmpp`.

For a one-off text-only patch that touches only `*.go`, `go.mod`, and `*.md`, the equivalent `git format-patch | sed | git am` flow is:

```sh
canonical_module='super/go'/'voice'
public_module_regex='github[.]com/chatinfra'/'voice'
git -C /path/to/chatinfra-voice-mirror format-patch -1 --stdout <accepted-commit> \
  | sed -E "/^[ +-]/ s#${public_module_regex}#${canonical_module}#g" \
  | git -C /path/to/monorepo/go/voice am -
```

Prefer the helper for normal imports; it validates patch shape before applying. After the monorepo change lands, run the mirror sync tooling from the monorepo root to update the public repository:

```sh
bin/sync_go_github --tool voice
```

The sync treats `./go/voice` as canonical source when run from the monorepo root, clones or reuses the public mirror checkout under `$SUPER_TMP_DIR/voice-public-mirror-checkout` or `./tmp/voice-public-mirror-checkout` via the SSH remote `git@github.com:chatinfra/voice.git`, refuses dirty canonical or mirror state, requires mirror `HEAD` to match its fetched upstream exactly, copies only this module's subtree into the public mirror checkout, commits generated changes, and pushes the mirror branch. Use `--dry-run` to run the same preflight checks and prepare the transformed staging tree without touching the mirror checkout.

Verify a published result by cloning the public mirror over `https://` and running `go build -trimpath ./cmd/voiced` — it must build standalone, since the packaged host payload is produced from this module with no monorepo context.
