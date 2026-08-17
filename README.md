# ChatInfra voice

This module contains the `voiced` daemon: the per-agent voice bridge that turns inbound phone-call speech into prompts for a local OpenCode agent and returns the reply text the caller hears.

One `voiced` process serves one agent with a bound voice number. The ChatInfra API forwards each recognized speech turn to the daemon's loopback turn endpoint; the daemon creates or reuses an OpenCode session keyed by the call identifier, submits the prompt, and answers with the spoken reply for that turn.

## Public mirror

The public repository is <https://github.com/chatinfra/voice.git>. Its root is a mirror of this monorepo's canonical `go/voice/` subtree, so public checkouts contain `go.mod`, `cmd/voiced`, `internal`, tests, and these docs directly at repository root.

`go/voice` in the ChatInfra monorepo remains canonical. Maintainers import accepted public changes back into the monorepo first, then update the public mirror from the canonical subtree. The mirror sync rewrites published Go and Markdown module-path references so mirror checkouts use the public module path in examples such as `github.com/chatinfra/voice/cmd/voiced`.

## Build and test

```sh
go test ./...
go build ./cmd/voiced
```

The module declares Go 1.24 in `go.mod`. It depends only on the Go standard library, so it declares no `go.sum` and a fresh mirror clone builds with no module downloads and no monorepo context. From a published mirror checkout, module-path installation uses the same public path shown after sync:

```sh
go install github.com/chatinfra/voice/cmd/voiced@latest
```

## `voiced` output contract

`voiced` takes no subcommands. It runs until interrupted; `voiced --help`, `-h`, or `help` prints deterministic terminal text and exits.

- **stdout** is reserved for that help text. It is not a structured contract.
- **stderr** carries runtime logs in the standard log-line format with the `voiced:` prefix.
- The machine-readable surface is the daemon's HTTP endpoints and its state files, not its console output.

The loopback listener serves two routes: `POST /turn` accepts a JSON turn (call identifier, caller number, transcript) and returns the JSON reply text, and `GET /health` returns the same status document the daemon persists. Request bodies are capped, a missing call identifier or empty transcript is rejected, and the listener binds a loopback address only.

Required environment:

| Variable | Purpose |
| --- | --- |
| `OPENCODE_BASE_URL` or `OPENCODE_URL` | OpenCode API base URL |
| `OPENCODE_PORT` | OpenCode API port when the base URL is unset |
| `OPENCODE_HOST` | Host used with `OPENCODE_PORT` (default `127.0.0.1`) |
| `OPENCODE_DIRECTORY` or `OPENCODE_DIR` | OpenCode working directory |
| `OPENCODE_AGENT_ID` or `AGENT_ID` | Agent identifier |
| `OPENCODE_AGENT_NAME`, `AGENT_NAME`, or `OPENCODE_AGENT` | OpenCode agent name |
| `VOICE_NUMBER_E164` | Bound voice number in E.164 form |
| `VOICED_STATE_DIR` or `STATE_DIR` | Directory for `calls.json` and `status.json` |

Optional environment:

| Variable | Purpose |
| --- | --- |
| `VOICED_TURN_ADDR` | Loopback turn endpoint address (default `127.0.0.1:0`) |
| `OPENCODE_PROMPT_TIMEOUT` | Prompt timeout as a Go duration; unset means no timeout |
| `VOICED_SHUTDOWN_TIMEOUT` | Graceful listener shutdown budget (default `5s`) |

Startup fails with a single diagnostic naming every missing required variable, and rejects a non-loopback turn address.

## Runtime state

`voiced` writes two files in its state directory:

| File | Purpose |
| --- | --- |
| `calls.json` | Call identifier to OpenCode session map, mode `0600`, preserved across daemon restarts |
| `status.json` | Health contract read by ChatInfra: turn-endpoint readiness, listen address, bound number, last turn and reply timestamps, latest error, active call count, and daemon start time |

Session creation failures, stale-session recreation failures, prompt errors, timeouts, and empty assistant responses are log-only: `voiced` records the error in `status.json`, returns a generic retry-later spoken reply for that turn, and continues serving later turns.

## OpenCode host layout

`voiced` is **image-packaged**, not cloned per host. Unlike the mirror-backed ChatInfra Go CLIs, no OpenCode host clones, fetches, or merges this repository at runtime.

The OPENCODE instance image packages the `voice` source, the `/data/opencode/bin/voiced` launcher, and a warmed `/data/opencode/.cache/voiced` payload. Signup, default-agent provisioning, and runtime reconfigure validate that packaged payload and reseed it from the delivered seed archive when it is missing or stale; they never reach GitHub for it. A host that lacks the packaged source or the warmed launcher and cache fails reconfigure with a diagnostic naming `voiced` and the expected paths, and the remediation is to rebuild the OPENCODE image — not to clone this mirror.

| Path | Purpose |
| ---- | ------- |
| `/data/opencode/bin/voiced` | Stable launcher referenced by the rendered per-agent systemd units |
| `/data/opencode/.cache/voiced` | Warmed build output and source hash shipped with the image |
| `/data/opencode/.cache/go-build` and `/data/opencode/.cache/go-mod` | OpenCode-owned Go build and module caches |

Each agent with a bound active voice number gets one user-systemd service running `/data/opencode/bin/voiced` as that agent's OpenCode runtime user with `Restart=always`, reading its credentials from a protected environment file rather than from the unit.

This mirror therefore exists for inspection, forks, and pull requests. Changes reach hosts through a new OPENCODE image, so a published mirror commit is not by itself a deployment.

## Contribution workflow

1. Fork <https://github.com/chatinfra/voice.git>.
2. Clone your fork and create a topic branch.
3. Make changes, run `go test ./...`, and push the branch.
4. Open a pull request against the public mirror.

Accepted public changes are reviewed and imported into canonical `go/voice` in the ChatInfra monorepo before the public mirror is synchronized again. See [CONTRIBUTING.md](./CONTRIBUTING.md) for details.
