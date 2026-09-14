# Family Registry

Per-sibling facts that differ between siblings. Uniform rules live in the
contract and are not restated here. Update this page whenever a sibling
changes its public surface.

## Identity Summary

| Repo | Package | Vendor key | Extension prefix | Store format | Strategy |
|---|---|---|---|---|---|
| `acp-go-claude` | `claudeacp` | `claude` | `_claude/` | `claude-transcript-jsonl-v1` | session runtime |
| `acp-go-codex` | `codexacp` | `codex` | `_codex/` | `codex-rollout-jsonl-v1` | multiplexed runtime |
| `acp-go-pi` | `piacp` | `pi` | `_pi/` | `pi-session-jsonl-v1` | session runtime |
| `acp-go-hermes` | `hermesacp` | `hermes` | `_hermes/` | `hermes-session-json-v1` | session runtime |

All import paths are `github.com/savid/acp-go-<vendor>`; all binaries are
`acp-go-<vendor>`. Each uses the native conversation id as the ACP session id: the Claude
conversation UUID, the Codex thread id, the Hermes stored session key, or the Pi session UUID.

## Native Surfaces and Process Models

| Sibling | Native surface | Process lifetime |
|---|---|---|
| claude | Claude Code stream-json plus control protocol | One process per session. The adapter passes a UUID with `--session-id`, and relaunches with `--resume`. Transcripts live under `CLAUDE_CONFIG_DIR/projects/<ProjectDirName(cwd)>/<uuid>.jsonl`; cwd is canonicalized before deriving the project directory. |
| codex | `codex app-server --listen stdio:// --disable plugins` | One app-server per Agent serves every thread and holds `.acp-go-codex.lock` in its home until the process is waited on. It starts on the first session-establishing request; after it exits the next explicit operation starts one replacement and rebinds the addressed thread through `thread/resume`. Rollouts live in `$CODEX_HOME/sessions/`. |
| pi | `pi --mode rpc` JSONL | One live process per session. A dead process is relaunched against the same native session file on the next prompt. |
| hermes | `hermes serve --host 127.0.0.1 --port <port>` | One authenticated gateway per session. ACP uses the durable conversation key; the transient gateway id is internal. Persistence uses native per-session HTTP export/import. |

## Native Version Probes

| Sibling | Minimum native version |
|---|---|
| claude | `2.0.0` |
| codex | `0.153.4` |
| pi | `0.80.6` |
| hermes | `0.21.2` |

## Session Stores

| Sibling | Kind | Carrier record |
|---|---|---|
| claude | Native transcript rows plus a `config` subpath | The current record holds cwd, transcript location, accepted environment and paths, model, permission mode, effort, system prompt, bare mode, output schema, and output style. |
| codex | Append-only rollout rows plus a `config` subpath | A generation contains the rollout rows and one session record naming the rollout path, the accepted session environment, the ordered paths, and the session's model, mode, effort, tier, personality, policies, and output schema. |
| pi | Append-only session JSONL rows plus a `config` subpath | A generation contains the native rows and one session record naming pi's session file, the accepted session environment, and the ordered paths. |
| hermes | Native per-conversation JSON export plus a `config` subpath | The configuration holds cwd, additional directories, environment, ordered paths, model, and effort. An empty conversation receives a native row before establishment succeeds. Missing state imports before binding a native session. Existing shorter or divergent history fails restore. |

### Mirror-Commit Ordering

| Sibling | Settlement and close ordering |
|---|---|
| claude | A native `result` settles a prompt. Context usage and transcript rows commit before idle and the response. Close interrupts the turn, signals and waits the process, commits the final transcript, then fences. |
| codex | Settlement reads the rollout after `turn/completed` and commits the new rows before the terminal idle and the response. Close interrupts any turn, unsubscribes the thread, commits, then fences; the shared app-server stays up for its peers. |
| pi | Settlement reads pi's session file after `agent_settled` and commits the new rows before the terminal idle and the response. Close aborts any turn, stops the process, commits, then fences. |
| hermes | `message.complete` settles the turn; callbacks finish before HTTP export, atomic mirror, idle, and response. Close interrupts pending work, exports while the gateway is available, then stops it. |

## Turn Failure and Turn Timeout

| Sibling | Causes | Native mapping and disclosed detail |
|---|---|---|
| claude | `process_exit`, `transport`, `provider`, `timeout` | Native error results supply `errors`, `error`, or `result`; process death reports exit status and the last stderr line. |
| codex | `process_exit`, `transport`, `provider`, `timeout` | A `turn/completed` outside the `completed` and `interrupted` statuses is provider, carrying the native error message, `httpStatusCode` as `statusCode`, and `codexErrorInfo.code` as `providerCode`; a refused `turn/start` and a non-retried `error` notification are provider with the native message. App-server death reports status plus the last stderr line. |
| pi | `process_exit`, `transport`, `provider`, `timeout`, `extension` | Command rejection preserves native text; process death reports status plus the last stderr line. Wrapper extension failure uses fixed `a pi extension failed`. |
| hermes | `process_exit`, `transport`, `provider`, `timeout` | Native result status or RPC rejection supplies provider detail. Process death reports status and the final stderr line. |

Malformed or empty records are skipped or produce a typed failure.

## Raw Events

The siblings emit every admitted record on their permanent channel and log
and continue on emit failure. Codex forwards the notification's params with
the method added as `method`; image `result` payloads are replaced by their
size.

## Lifecycle Extension

| Sibling | `updatesOutsidePrompt` | `activityKinds` |
|---|---|---|
| claude | yes | `[]` |
| codex | yes | `[]` |
| pi | yes | `[]` |
| hermes | yes | `[]` |

A permanent-channel sibling delivers between-prompt native work as ordinary
updates under agent-origin turns.

### What each permanent channel delivers

| Sibling | Open / delivery / settle |
|---|---|
| claude | Assistant, user, or stream work outside a prompt opens an agent-origin cycle. Native `result` or an agent-origin `task_notification` settles it. Delegated records retain their parent tool-use provenance. |
| codex | Work on the thread with no prompt in flight (`turn/started`, item, plan, or diff notifications) opens an agent-origin turn on the shared pump; `turn/completed` drives mirror → idle. |
| pi | An `agent_start` with no prompt in flight opens an agent-origin turn on the event pump; `agent_settled` drives usage → mirror → idle. |
| hermes | Native message, thought, tool, or dialog events outside a prompt open an agent-origin cycle. `message.complete` drives mirror → idle. |

### What each channel tolerates

| Sibling | Tolerance and remaining fences |
|---|---|
| claude | Unmodelled system records remain session-scoped. Invalid JSON fails the transport. A changed native session id poisons the session. Native control cancellation resolves its matching callback. |
| codex | Usage reports, retried `error` notifications, and unmodelled methods are session-scoped and open nothing. Notifications for threads this Agent does not hold are dropped. Records naming a turn other than the cycle's are ignored. Repeated text completion frames contribute only their new suffix. |
| pi | Queue reports, compaction and retry pairs, custom messages, and unmodelled types are session-scoped and open nothing. A wrapper extension error fails the cycle with cause `extension`; an operator extension error is pi's own. |
| hermes | `streaming` owns the current turn; `queued` waits for the next native start after its response. `redirected` and `steered` keep work with the running native turn and return a non-retry failure. Unknown dispositions fail the generation. Events use one bounded 256-record queue; unbound live ids are dropped. |

### Opening publication

| Sibling | Order after the establishing response |
|---|---|
| claude | snapshot, then catalog; non-stdio connections publish inline |
| codex | snapshot only; non-stdio connections publish inline |
| pi | catalog, then snapshot; non-stdio connections publish inline |
| hermes | snapshot only; non-stdio connections publish inline |

### Close and delete boundaries

| Sibling | `session/close` | `session/delete` |
|---|---|---|
| claude | Cancels callbacks and the turn, signals and waits the process, commits final transcript rows, terminalizes an agent-origin cycle, and fences. | Tombstones first, then the same close. |
| codex | Cancels the turn and its dialogs, interrupts the native turn, unsubscribes the thread, commits rollout rows and the session record, terminalizes an open agent-origin cycle, and fences. The app-server and its other threads continue. | Tombstones first, then the same close. |
| pi | Cancels the turn and its dialogs, signals and waits the process, commits native rows and the session record, terminalizes an open agent-origin cycle, and fences. | Tombstones first, then the same close. |
| hermes | Cancels and joins callbacks, interrupts pending work, commits the native export, stops and waits the gateway, then fences. | Tombstones first, then the same close. |

## Usage `size`

- **hermes:** native `message.complete.usage.context_max` and `context_used`.
  Session-wide token totals are not presented as per-prompt usage.

- **claude:** `get_context_usage.maxTokens`, else `result.modelUsage` context
  window, else `0`. `used` comes from native `totalTokens`.

- **codex:** `thread/tokenUsage/updated` `modelContextWindow`, else the
  selected model's catalog `contextWindow`, else `0`. `used` is the latest
  model request's total.
- **pi:** `get_session_stats.contextUsage.contextWindow`, else the selected
  model's catalog `contextWindow`, else `0`.

## Delegated Agents

- **Tagged (claude).** Native `parent_tool_use_id` is retained as
  `_meta.claude.parentToolUseId` on derived updates.

- **Not applicable (codex, hermes, pi).** No subscribed provenance channel; nothing is
  synthesized.

## Vendor Session Options

| Sibling | Extra fields | Structured output |
|---|---|---|
| claude | `permissionMode`, `systemPrompt`, `bare`, `effort` | Native `--json-schema`; the result is `_meta.claude.structuredOutput` on `usage_update`. An empty schema is refused. |
| codex | `effort`, `serviceTier`, `personality`, `approvalPolicy`, `sandboxPolicy` | Native: `outputSchema` rides `turn/start`; the parsed final answer is `_meta.codex.structuredOutput` on the prompt response. Non-JSON output omits the key and the turn still succeeds. An empty `outputSchema` object is refused at parse time. |
| pi | `thinkingLevel`, `permission` (`ask`\|`allow`), `autoRetry` | Not advertised; `outputSchema` fails at session start. |
| hermes | `effort` | Not advertised; `outputSchema` is refused. |

Session `env` and `extraPathDirs` reach the native boundary as: the addressed
thread's `config.shell_environment_policy.set` on `thread/start` and
`thread/resume`, with ordered extra directories prepended to the session-selected `PATH`,
falling back to the app-server's `PATH` when omitted (codex);
the session's native process environment (claude, hermes, pi).

## Session Config Options

| Sibling | Advertised IDs | Value authority and read-back |
|---|---|---|
| claude | `model`, `mode`, `effort`, `output_style` | Model and permission mode use native control requests. Effort and output style use `apply_flag_settings` followed by `get_settings`. Optional selectors require native availability and a known current value. |
| codex | `model`, `mode`, `effort`, `service_tier`, `personality` | Values forward to the next `turn/start`; only `mode`, `effort`, and `personality` reject empty. `mode` is `default` or `plan`, sent as `collaborationMode`. `service_tier` and `personality` appear only while set. |
| pi | `model`, `thought_level` | Model checks `<provider>/<id>`; `get_state` reports the adopted thought level. Menu `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. |
| hermes | `model`, `effort` | Session-scoped `config.set`, followed by `model.options` and `config.get` read-back. Effort: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, `ultra`. |

### How each model catalog is built

| Sibling | Source | Metadata |
|---|---|---|
| claude | `initialize.models`, snapshotted once per process | `modelId` and native `supportedEffortLevels`; configured and selected ids append after the native entries. Unknown full ids forward unchanged. |
| codex | `model/list`, the presets the CLI build ships, read once per app-server generation | `modelId`, plus `contextWindow` and `supportedEffortLevels` when the row carries them; the effort menu is the selected model's `supportedReasoningEfforts`, else a fixed menu once an effort is set |
| pi | `get_available_models`, pi's own registry filtered by its configured providers, snapshotted once at native start | `modelId`, plus `contextWindow` and `maxOutputTokens` when the row carries them |
| hermes | `model.options` provider catalogs | Provider-qualified `modelId` and the native effort menu. Configured and selected ids append after native entries. |

Hermes and Pi refuse a host-listed id with no provider prefix at construction.

## Vendor Process Options

| Sibling | Options | `WithHome` variable |
|---|---|---|
| claude | `WithClaudeSettingSources`, `WithClaudeSettingsFile`, `WithClaudeInitializeTimeout` | `CLAUDE_CONFIG_DIR` |
| codex | `WithCodexConfigOverrides` (`-c key=value`; the `shell_environment_policy` keyspace fails construction) | `CODEX_HOME` |
| pi | none | `PI_CODING_AGENT_DIR` |
| hermes | none | `HERMES_HOME` |

## Slash Commands

| Sibling | Discovery | Advertises | Invoke-denied (ACP alternative) |
|---|---|---|---|
| claude | `initialize.commands` | Native names after sanitizing and excluding session/account/config control commands | none |
| codex | none | Nothing; command silence is test-pinned. `/x args` reaches `turn/start` as text. | n/a |
| pi | `get_commands` at session setup, re-fetched on relaunch | Native list minus names the shared sanitizer rejects | none |
| hermes | none | Nothing; slash text reaches `prompt.submit`. | n/a |

## Image Input and Output

### Advertised media envelope

| Sibling | `maxBytes` | `maxPromptBytes` | `maxDimension` | `documentFormats` |
|---|---:|---:|---:|---|
| claude | 6,291,456 | 6,291,456 | 0 | `["application/pdf"]` |
| codex | 6,291,456 | 6,291,456 | 0 | `[]` |
| pi | 6,291,456 | 6,291,456 | 0 | `[]` |
| hermes | 6,291,456 | 6,291,456 | 0 | `[]` |

### Handoff native form

| Sibling | Native form for a validated image |
|---|---|
| claude | Inline base64 `source` blocks in the stream-json user message |
| codex | a `data:` URL on the turn input |
| pi | inline base64 |
| hermes | inline base64 through `image.attach_bytes` before `prompt.submit` |

### Non-raster blobs

| Sibling | Non-`image/` blob | Post-gate disposition |
|---|---|---|
| claude | Gates `application/pdf`; refuses other non-image MIME types | PDF becomes a native document block |
| codex, pi | refuses every non-`image/` MIME before decode | nothing decoded |
| hermes | refuses every non-`image/` MIME before decode | nothing decoded |

Text resources share the aggregate: all charge raw text before XML
expansion.

### Selected-model gate authority

| Sibling | Source | `unsupported_by_model` |
|---|---|---|
| claude | No authoritative native modality field | never; unknown forwards |
| codex | `model/list` per-model `inputModalities` | yes |
| pi | `get_available_models` per-model `input` list | yes |
| hermes | No authoritative native modality field | never; unknown forwards |

An absent model, absent field, or empty list resolves to `unknown`. A failed
catalog call fails the app-server generation start (codex) or session
establishment (claude, hermes, pi).

### Output surfaces

| Sibling | Surfaces |
|---|---|
| claude | Native image source blocks in assistant or tool-result content: inline base64 is validated; remote URL-only sources become resource links without fetching. |
| codex | `imageGeneration` and `imageView` items: inline base64 `result`, else `savedPath` bounded read → tool-call image content |
| pi | tool-call content images via the wrapper-owned bridge; assistant image blocks one per chunk; inline base64 only |
| hermes | None; image output is not advertised. |

### Output refusal placement

| Sibling | Where a refusal appears |
|---|---|
| claude | Tool-call image refusal marks the tool failed with guidance; assistant image refusal emits guidance as agent text. |
| codex | the image tool call reports `failed` with the guidance as content |
| pi | the tool call reports `failed` with the guidance as content; an assistant image is replaced by the guidance as agent text |

### Local read roots

| Sibling | Output read roots |
|---|---|
| claude | none; native output is inline or remote URL-only |
| codex | `cwd`, `WithScratchDir`, `os.TempDir()`, `$CODEX_HOME/generated_images` |
| pi | none |
| hermes | none |

### Replay source

| Sibling | Source and validation |
|---|---|
| claude | Native transcript entries use their own UUID for deduplication. Entries sharing an API message id retain each content fragment; images pass through the output gate. |
| codex | replay decodes `image_generation_call` rows from the mirrored rollout through the output gate; a live `imageView` is an ordinary function call in the rollout, so its replay carries no image |
| pi | replay decodes image blocks from the mirrored native rows through the output gate |
| hermes | The native export supplies user/assistant text parts, reasoning, tool calls, and tool results. Image output is not projected. |

## Native Verification

Hermes `0.21.2`, native source `dd497c3d`, verified 2026-09-14:
race-enabled ACP → native `hermes chat --cli --resume` → ACP continuation,
followed by a store-backed import into a fresh native home and a further prompt.
Both earlier facts survive. Native permission approval, form clarification, raw
events, PATH rotation on resume, command cancellation, and deletion also pass.
The permission fixture selects native manual approvals in its temporary home.
The export/import surface is defined by the
[native session router](https://github.com/NousResearch/hermes-agent/blob/dd497c3d/hermes_cli/web_routers/sessions.py).

Claude Code `2.1.270`, verified 2026-09-14: native initialization and settings
controls, permission callbacks, AskUserQuestion elicitation, raw events,
PATH changes on resume, running-command cancellation, and deletion;
race-enabled ACP → native `claude --resume` → ACP load and continued
prompt, retaining the same conversation id and both earlier turns. The native
transcript can contain multiple entries with one API message id.

## Known Deviations

- **Hermes native compression:** a changed durable key at mirror time poisons
  the session with `native_session_identity_drift`. Native approval responses
  follow request order and preserve a request id when the native event supplies
  one; unresolved host callbacks deny or skip the pending native input.
- **Hermes restore:** its native HTTP import creates missing conversations and
  refuses replacement of an existing id, so a shorter native conversation fails
  restore. The gateway process starts before import; session binding waits until
  import and validation finish.

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
| claude | the refused option name | load, resume | never | `native_session_identity_drift` | `native_start`; bare token on close, delete, list, and config-commit failures |
| codex | the refused option name | load, resume | yes, when a replacement app-server cannot start | never | `native_start`; bare token on close, delete, list, and config-commit failures |
| pi | the refused option name | load, resume | never | `native_session_identity_drift` | `native_start`; bare token on close, delete, list, and config-commit failures |
| hermes | the refused option name | load, resume | never | `native_session_identity_drift` | `native_start`; bare token on close, delete, list, and config-commit failures |
