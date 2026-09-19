# ChatInfra voice

This module contains the `voiced` daemon: the per-agent voice bridge that turns inbound phone-call speech into prompts for a local OpenCode agent and returns the reply text the caller hears.

One `voiced` process serves one agent with a bound voice number. The ChatInfra API forwards each recognized speech turn to the daemon's authenticated Unix socket endpoint; the daemon creates or reuses an OpenCode session keyed by the call identifier, submits the prompt, and answers with the spoken reply for that turn.

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

The Linux Unix listener admits only kernel-authenticated UID-0 peers before parsing HTTP and serves two routes: `POST /turn` accepts a JSON turn (`runtimeId`, `agentId`, call identifier, caller number, transcript) and returns the JSON reply text, and `GET /health` returns the same status document the daemon persists. Request bodies are capped, a missing call identifier or empty transcript is rejected, and missing or mismatched runtime/agent audience is rejected before call/session work. There is no TCP listener or retry fallback.

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

| `VOICED_TURN_SOCKET` | Required absolute Linux Unix socket path, at most 107 bytes |
| `VOICED_RUNTIME_ID` | Runtime audience resolved by the API |

Optional environment:

| Variable | Purpose |
| --- | --- |
| `OPENCODE_PROMPT_TIMEOUT` | Prompt timeout as a Go duration; default 2 minutes |
| `VOICED_SHUTDOWN_TIMEOUT` | Graceful listener shutdown budget (default `5s`) |

Startup rejects missing configuration, unsupported peer credentials, invalid socket paths, unexpected objects, and unsafe directory ownership/modes. Legacy TCP-only configuration cannot start a listener. The API derives `/run/chatinfra-voice/<uid>/<sha256(agentId)>.sock` from the resolved runtime account and agent; the daemon's parent must be runtime-owned mode 0700 under root-owned ancestors, and the socket is mode 0600.

Readiness is published only after binding the owned endpoint and installing peer admission. Shutdown clears readiness and removes the socket only if it still has the listener's recorded identity; replacement endpoints and unrelated files are preserved. An unavailable authenticated endpoint produces the existing graceful API failure. Roll back to a secure socket-capable payload or disable voice; never restore the unauthenticated TCP transport. These source contracts do not attest an installed host or a completed migration.

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
3. Make changes and run the focused tests below before `go test ./...`. The Linux peer tests require Docker with UID switching and container-build support; missing capability fails the tests. The unsupported-platform CLI test also requires Node.js with WASI preview1 support and executes the real WASI daemon startup. The peer tests build a static test executable containing the production daemon into an isolated `FROM scratch` image and remove only their named container/image. Set `TMPDIR` and `GOTMPDIR` beneath a lane-owned scratch root (in the monorepo, source `bin/_super_env` and use `super_lane_scratch voice-peer-tests`).

   ```bash
   go test -json -count=1 -timeout=240s ./cmd/voiced -run '^TestUnixTurnPeerIsolationContainer$'
   go test -json -count=1 -timeout=240s ./cmd/voiced -run '^TestUnixTurn(PeerAdmission|AudienceBinding|ListenerLifecycle)$'
   ```

4. Push the branch for review. Tests and publication do not establish live-host readiness.
4. Open a pull request against the public mirror.

Accepted public changes are reviewed and imported into canonical `go/voice` in the ChatInfra monorepo before the public mirror is synchronized again. See [CONTRIBUTING.md](./CONTRIBUTING.md) for details.
