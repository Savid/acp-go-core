# Family Registry

Per-sibling facts that differ between siblings. Uniform rules live in the
contract and are not restated here. Update this page whenever a sibling
changes its public surface.

## Identity Summary

| Repo | Package | Vendor key | Extension prefix | Store format | Strategy |
|---|---|---|---|---|---|
| `acp-go-codex` | `codexacp` | `codex` | `_codex/` | `codex-rollout-jsonl-v1` | multiplexed runtime |
| `acp-go-pi` | `piacp` | `pi` | `_pi/` | `pi-session-jsonl-v1` | session runtime |

All import paths are `github.com/savid/acp-go-<vendor>`; all binaries are
`acp-go-<vendor>`. Both adopt the harness's own identity as the ACP session
id: the Codex thread id, the Pi session UUID.

## Native Surfaces and Process Models

| Sibling | Native surface | Process lifetime |
|---|---|---|
| codex | `codex app-server --listen stdio:// --disable plugins` | One app-server per Agent serves every thread. It starts on the first session-establishing request; after it exits the next explicit operation starts one replacement and rebinds the addressed thread through `thread/resume`. Rollouts live in `$CODEX_HOME/sessions/`. |
| pi | `pi --mode rpc` JSONL | One live process per session. A dead process is relaunched against the same native session file on the next prompt. |

Teardown signals the process group and waits for the root process on every
sibling.

## Native Version Probes

| Sibling | Minimum native version |
|---|---|
| codex | `0.153.4` |
| pi | `0.80.6` |

## Session Stores

| Sibling | Kind | Carrier record |
|---|---|---|
| codex | Append-only rollout rows plus a `config` subpath | Each commit appends the rows the app-server wrote to the thread's rollout since the last commit, then one session record naming the rollout path, the accepted session environment, the ordered paths, and the session's model, mode, effort, tier, personality, policies, and output schema. |
| pi | Append-only session JSONL rows plus a `config` subpath | Each commit appends the rows pi wrote since the last commit, then one session record naming pi's session file, the accepted session environment, and the ordered paths. |

Adapter-authored records use closed current schemas and reject unknown or
duplicate fields, malformed input, regressed counters, and mismatched
identity.

### Mirror-Commit Ordering

| Sibling | Settlement and close ordering |
|---|---|
| codex | Settlement reads the rollout after `turn/completed` and commits the new rows before the terminal idle and the response. Close interrupts any turn, unsubscribes the thread, commits, then fences; the shared app-server stays up for its peers. |
| pi | Settlement reads pi's session file after `agent_settled` and commits the new rows before the terminal idle and the response. Close aborts any turn, stops the process, commits, then fences. |

## Turn Failure and Turn Timeout

| Sibling | Causes | Native mapping and disclosed detail |
|---|---|---|
| codex | `process_exit`, `transport`, `provider`, `timeout` | A `turn/completed` outside the `completed` and `interrupted` statuses is provider, carrying the native error message, `httpStatusCode` as `statusCode`, and `codexErrorInfo.code` as `providerCode`; a refused `turn/start` and a non-retried `error` notification are provider with the native message. App-server death reports status plus the last stderr line. |
| pi | `process_exit`, `transport`, `provider`, `timeout`, `extension` | Command rejection preserves native text; process death reports status plus the last stderr line. Wrapper extension failure uses fixed `a pi extension failed`. |

Malformed or empty records are skipped or produce a typed failure.

## Raw Events

Both siblings emit every admitted record on their permanent channel and log
and continue on emit failure. Codex forwards the notification's params with
the method added as `method`; image `result` payloads are replaced by their
size.

## Lifecycle Extension

| Sibling | `updatesOutsidePrompt` | `activityKinds` |
|---|---|---|
| codex | yes | `[]` |
| pi | yes | `[]` |

A permanent-channel sibling delivers between-prompt native work as ordinary
updates under agent-origin turns.

### What each permanent channel delivers

| Sibling | Open / delivery / settle |
|---|---|
| codex | Work on the thread with no prompt in flight (`turn/started`, item, plan, or diff notifications) opens an agent-origin turn on the shared pump; `turn/completed` drives mirror → idle. |
| pi | An `agent_start` with no prompt in flight opens an agent-origin turn on the event pump; `agent_settled` drives usage → mirror → idle. |

### What each channel tolerates

| Sibling | Tolerance and remaining fences |
|---|---|
| codex | Usage reports, retried `error` notifications, and unmodelled methods are session-scoped and open nothing. Notifications for threads this Agent does not hold are dropped. A `turn/completed` naming a turn other than the cycle's is ignored. |
| pi | Queue reports, compaction and retry pairs, custom messages, and unmodelled types are session-scoped and open nothing. A wrapper extension error fails the cycle with cause `extension`; an operator extension error is pi's own. |

### Opening publication

| Sibling | Order after the establishing response |
|---|---|
| codex | snapshot only; non-stdio connections publish inline |
| pi | catalog, then snapshot; non-stdio connections publish inline |

### Close and delete boundaries

| Sibling | `session/close` | `session/delete` |
|---|---|---|
| codex | Cancels the turn and its dialogs, interrupts the native turn, unsubscribes the thread, commits rollout rows and the session record, terminalizes an open agent-origin cycle, and fences. The app-server and its other threads continue. | Tombstones first, then the same close. |
| pi | Cancels the turn and its dialogs, signals and waits the process, commits native rows and the session record, terminalizes an open agent-origin cycle, and fences. | Tombstones first, then the same close. |

## Usage `size`

- **codex:** `thread/tokenUsage/updated` `modelContextWindow`, else the
  selected model's catalog `contextWindow`, else `0`. `used` is the latest
  model request's total.
- **pi:** `get_session_stats.contextUsage.contextWindow`, else the selected
  model's catalog `contextWindow`, else `0`.

## Delegated Agents

- **Not applicable (codex, pi).** No subscribed provenance channel; nothing is
  synthesized.

## Vendor Session Options

| Sibling | Extra fields | Structured output |
|---|---|---|
| codex | `effort`, `serviceTier`, `personality`, `approvalPolicy`, `sandboxPolicy` | Native: `outputSchema` rides `turn/start`; the parsed final answer is `_meta.codex.structuredOutput` on the prompt response. Non-JSON output omits the key and the turn still succeeds. An empty `outputSchema` object is refused at parse time. |
| pi | `thinkingLevel`, `permission` (`ask`\|`allow`), `autoRetry` | Not advertised; `outputSchema` fails at session start. |

Session `env` and `extraPathDirs` reach the native boundary as: the addressed
thread's `config.shell_environment_policy.set` on `thread/start` and
`thread/resume`, with `PATH` composed ahead of the app-server's own (codex);
the session's pi process environment (pi).

## Session Config Options

| Sibling | Advertised IDs | Value authority and read-back |
|---|---|---|
| codex | `model`, `mode`, `effort`, `service_tier`, `personality` | Values forward to the next `turn/start`; only `mode`, `effort`, and `personality` reject empty. `mode` is `default` or `plan`, sent as `collaborationMode`. `service_tier` and `personality` appear only while set. |
| pi | `model`, `thought_level` | Model checks `<provider>/<id>`; `get_state` reports the adopted thought level. Menu `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. |

### How each model catalog is built

| Sibling | Source | Metadata |
|---|---|---|
| codex | `model/list`, the presets the CLI build ships, read once per app-server generation | `modelId`, plus `contextWindow` and `supportedEffortLevels` when the row carries them; the effort menu is the selected model's `supportedReasoningEfforts`, else a fixed menu once an effort is set |
| pi | `get_available_models`, pi's own registry filtered by its configured providers, snapshotted once at native start | `modelId`, plus `contextWindow` and `maxOutputTokens` when the row carries them |

Pi refuses a host-listed id with no provider prefix at construction.

## Vendor Process Options

| Sibling | Options | `WithHome` variable |
|---|---|---|
| codex | `WithCodexConfigOverrides` (`-c key=value`; the `shell_environment_policy` keyspace fails construction) | `CODEX_HOME` |
| pi | none | `PI_CODING_AGENT_DIR` |

## Slash Commands

| Sibling | Discovery | Advertises | Invoke-denied (ACP alternative) |
|---|---|---|---|
| codex | none | Nothing; command silence is test-pinned. `/x args` reaches `turn/start` as text. | n/a |
| pi | `get_commands` at session setup, re-fetched on relaunch | Native list minus names the shared sanitizer rejects | none |

## Image Input and Output

### Advertised media envelope

| Sibling | `maxBytes` | `maxPromptBytes` | `maxDimension` | `documentFormats` |
|---|---:|---:|---:|---|
| codex | 6,291,456 | 6,291,456 | 0 | `[]` |
| pi | 6,291,456 | 6,291,456 | 0 | `[]` |

### Handoff native form

| Sibling | Native form for a validated image |
|---|---|
| codex | a `data:` URL on the turn input |
| pi | inline base64 |

Both retain one root handle per prompt.

### Non-raster blobs

| Sibling | Non-`image/` blob | Post-gate disposition |
|---|---|---|
| codex, pi | refuses every non-`image/` MIME before decode | nothing decoded |

Text resources share the aggregate: both charge raw text before XML
expansion.

### Selected-model gate authority

| Sibling | Source | `unsupported_by_model` |
|---|---|---|
| codex | `model/list` per-model `inputModalities` | yes |
| pi | `get_available_models` per-model `input` list | yes |

An absent model, absent field, or empty list resolves to `unknown`. A failed
catalog call fails the app-server generation start (codex) or session
establishment (pi).

### Output surfaces

| Sibling | Surfaces |
|---|---|
| codex | `imageGeneration` and `imageView` items: inline base64 `result`, else `savedPath` bounded read → tool-call image content |
| pi | tool-call content images via the wrapper-owned bridge; assistant image blocks one per chunk; inline base64 only |

### Output refusal placement

| Sibling | Where a refusal appears |
|---|---|
| codex | the image tool call reports `failed` with the guidance as content |
| pi | the tool call reports `failed` with the guidance as content; an assistant image is replaced by the guidance as agent text |

### Local read roots

| Sibling | Output read roots |
|---|---|
| codex | `cwd`, `WithScratchDir`, `os.TempDir()`, `$CODEX_HOME/generated_images` |
| pi | none |

### Replay source

| Sibling | Source and validation |
|---|---|
| codex | replay decodes `image_generation_call` rows from the mirrored rollout through the output gate; a live `imageView` is an ordinary function call in the rollout, so its replay carries no image |
| pi | replay decodes image blocks from the mirrored native rows through the output gate |

## Converged Uniform Semantics

- **Listing:** RawURL-base64 offset cursors. Empty cwd means no filter.
- **Dispatch:** closed Agents fail `-32600`.
- **Admission:** one in-flight prompt per session; contention is `-32600`
  `{error:"backpressure",limit:<token>}` with token `session_prompt`, plus
  `session_restore` while a load or resume is in flight.

## Known Postures

- **Handoff hardlinks:** no adapter checks `Nlink`; the declared digest still
  binds accepted bytes.
- **Decode allocation:** adapters parse raster structure without full decode
  and impose no allocation budget.

## Known Deviations

- **Codex publishes the CLI build's presets** whatever provider the home
  routes to, and snapshots them once per app-server generation.
- **Codex stored restore:** restore materializes
  `$CODEX_HOME/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<threadId>.jsonl`
  from the `session_meta` row's timestamp, then resumes by thread id. A thread
  id is letters, digits, `-`, `_`, at most 128 bytes; anything else is an
  unknown session.
- **Codex sandbox:** the native default workspace-write policy can refuse
  writes outside cwd; hosts configure `sandboxPolicy` explicitly. Additional
  directories become the thread's workspace permission profile and the turn's
  writable roots.
- **Codex MCP surfaces:** the adapter configures no MCP server, but an
  operator's own `config.toml` may. An `mcpServer/elicitation/request` marked
  as a tool approval is answered as a permission; any other is relayed in the
  mode it asks for when the client advertised it, else cancelled natively.
- **Pi snapshots its catalog once** at native start; a mid-session credential
  change does not move the menu.
- **Pi extensions:** the wrapper-owned bridge and PATH extensions load with
  `-e` from a content-addressed scratch directory; pi's own discovery of the
  operator's extensions, skills, and prompt templates stays on.
- **Pi retry and settings:** native retry defaults off; `autoRetry` opts in
  per session. Seed files are written into pi's config root under a manifest;
  a seeded `settings.json` must parse. Turn process-exit detail retains one
  last stderr line.

Off-prompt `-32603` reachability:

| Sibling | `_invalid_options` `field` | `_restore_failed` | `_runtime_unavailable` | `_session_poisoned` `cause` | `_internal_failure` `class` |
|---|---|---|---|---|---|
| codex | the refused option name | load, resume | yes, when a replacement app-server cannot start | never | `native_start`; bare token on close, delete, and list failures |
| pi | the refused option name | load, resume | never | `native_session_identity_drift` | `native_start`; bare token on close, delete, and list failures |
