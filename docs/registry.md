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
| `acp-go-opencode` | `opencodeacp` | `opencode` | `_opencode/` | `opencode-sync-events-v1` | multiplexed runtime |
| `acp-go-amp` | `ampacp` | `amp` | `_amp/` | `amp-thread-json-v1` | prompt runtime |

Native bindings name the Claude conversation UUID, Codex thread id, Hermes stored
session key, OpenCode session ID, Pi session UUID, or Amp thread id. The
[identity contract](04-sessions-and-store.md#lifecycle-stream-and-incarnation-identity)
defines storage and wire publication.

## Native Surfaces and Process Models

| Sibling | Native surface | Process lifetime |
|---|---|---|
| claude | Claude Code stream-json plus control protocol | One process per session. The adapter passes a UUID with `--session-id`, and relaunches with `--resume` once a transcript exists; an empty conversation retains its UUID with `--session-id`. Transcripts live under `CLAUDE_CONFIG_DIR/projects/<ProjectDirName(cwd)>/<uuid>.jsonl`; cwd is canonicalized before deriving the project directory. |
| codex | `codex app-server --listen stdio:// --disable plugins` | One app-server per Agent serves every thread and holds `.acp-go-codex.lock` in its home until the process is waited on. It starts on the first session-establishing request; after it exits the next explicit operation starts one replacement and rebinds the addressed thread through `thread/resume`. Rollouts live in `$CODEX_HOME/sessions/`. |
| pi | `pi --mode rpc` JSONL | One live process per session. A dead process is relaunched against the same native session file on the next prompt. |
| hermes | `hermes serve --host 127.0.0.1 --port <port>` | One authenticated gateway per session. The native binding uses the durable conversation key; the transient gateway id is internal. Persistence uses native per-session HTTP export/import. |
| opencode | `opencode serve` authenticated loopback HTTP and global SSE; `opencode db` for scoped history reads | One server per Agent serves every session and holds a native-data-directory file lock. Short-lived native database commands read a conversation graph and its stability fence. Close releases a logical binding; a dead server is replaced on the next operation and the addressed session is rebound. |
| amp | `amp threads continue <thread> --execute --stream-json-input` stream-json plus a temporary native lifecycle plugin | One process per prompt. `session/new` runs `amp threads new` eagerly. Each prompt attaches to the remote thread, refuses to submit while remote work is active, and is reaped before export and publication; restore and observation attach without input. The plugin is installed uniquely under the native plugin directory and removed after its process is reaped. |

## Session Stores

| Sibling | Kind | Carrier record |
|---|---|---|
| claude | Native transcript rows plus a `config` subpath | The current record holds cwd, transcript location, accepted environment and paths, model, permission mode, effort, system prompt, bare mode, output schema, and output style. Empty conversations commit configuration with an empty main record. |
| codex | Append-only rollout rows plus a `config` subpath | A generation contains the rollout rows and one session record naming the rollout path, the accepted session environment, the ordered paths, and the session's model, mode, effort, tier, personality, policies, output schema, and admitted image bytes keyed by native tool identity. Empty conversations commit configuration with an empty main record. If native resume reports no rollout and neither the store nor the recorded native file has rows, restore creates a new native thread and commits its binding under the unchanged ACP session id. |
| pi | Append-only session JSONL rows plus a `config` subpath | A generation contains the native rows and one session record naming pi's session file, the accepted session environment, and the ordered paths. Empty conversations commit configuration with an empty main record; restore then resumes the recorded session file without a native header. |
| hermes | Native per-conversation JSON export plus a `config` subpath | The configuration holds cwd, additional directories, environment, ordered paths, model, and effort. An empty conversation receives a native row before establishment succeeds. Missing state imports before binding a native session. Existing shorter or divergent history fails restore. |
| opencode | Online native sync-event graph plus a `config` subpath | The graph contains the root conversation and its descendants; it always carries the root creation event, so an empty main record never occurs and fails restore. The configuration holds cwd, additional directories, environment, ordered paths, model, mode, permission, variant, output schema, and captured local image bytes or refusal records. |
| amp | Raw native thread export plus a `config` subpath | The main record is one raw `threads export` document, so an empty conversation is an export with no messages. The configuration holds cwd, additional directories, ACP and native session ids, service origin, mode, environment, ordered paths, update time, and historic usage keyed by native protocol message id. A confirmed missing thread is imported into a private replacement under the same ACP id; compaction summaries stay in the export and cannot be imported. |

### Mirror-Commit Ordering

| Sibling | Settlement and close ordering |
|---|---|
| claude | A native `result` settles a prompt. Context usage and transcript rows commit before idle and the response. Close interrupts the turn, signals and waits the process, commits the final transcript, then fences. |
| codex | Settlement reads the rollout after `turn/completed` and commits the new rows before the terminal idle and the response. Close interrupts any turn, unsubscribes the thread, commits, then fences; the shared app-server stays up for its peers. |
| pi | Settlement reads pi's session file after `agent_settled` and commits the new rows before the terminal idle and the response. Close aborts any turn, stops the process, commits, then fences. |
| hermes | `message.complete` settles the turn; callbacks finish before HTTP export, atomic mirror, idle, and response. Close interrupts pending work, exports while the gateway is available, then stops it. |
| opencode | Native prompt HTTP completion refetches every owned assistant step; callbacks join before the stable sync snapshot, atomic mirror, idle, and response. Close snapshots while HTTP remains available, then releases the binding. |
| amp | The plugin's `agent.end` receipt and a quiet thread view settle the turn; the process is reaped, then the export must match the observed conversation, retried with backoff under a 30-second deadline, before the atomic mirror, idle, and response. Close joins the prompt process, commits, then fences; the remote thread stays. |

## Turn Failure

Only pi adds a vendor cause: `extension`.

| Sibling | Native mapping and disclosed detail |
|---|---|
| claude | Native error results supply `errors`, `error`, or `result`. |
| codex | A `turn/completed` outside the `completed` and `interrupted` statuses is provider, carrying the native error message, `httpStatusCode` as `statusCode`, and `codexErrorInfo.code` as `providerCode`; a refused `turn/start` and a non-retried `error` notification are provider with the native message. |
| pi | Command rejection preserves native text. Wrapper extension failure is `extension` with the fixed text `a pi extension failed`. |
| hermes | Native result status or RPC rejection supplies provider detail. |
| opencode | Native HTTP errors or assistant error records supply provider detail. |
| amp | A native receipt status `error` or an error stream record supplies provider detail. A refused frame, a missing receipt, or a failed mirror commit is `transport` with the adapter's cause. |

## Raw Events

The siblings emit every admitted record on their permanent channel and log
and continue on emit failure. Codex forwards the notification's params with
the method added as `method`; image `result` payloads are replaced by their
size. OpenCode replaces image data URLs with their encoded size. Pi empties
image `data` members and adds their decoded size as `sizeBytes`. Amp has no
permanent channel: it emits the admitted stream-json records of the current
prompt process and removes image payloads.

## Lifecycle Extension

### What each permanent channel delivers

| Sibling | Open / delivery / settle |
|---|---|
| claude | Assistant, user, or stream work outside a prompt opens an agent-origin cycle. Native `result` or an agent-origin `task_notification` settles it. Delegated records retain their parent tool-use provenance. |
| codex | Work on the thread with no prompt in flight (`turn/started`, item, plan, or diff notifications) opens an agent-origin turn; `turn/completed` drives mirror → idle. |
| pi | An `agent_start` with no prompt in flight opens an agent-origin turn on the event pump; `agent_settled` drives usage → mirror → idle. |
| hermes | Native message, thought, tool, dialog, or error events outside a prompt open an agent-origin cycle. `message.complete` drives mirror → idle. |
| opencode | Native user or assistant message work outside a prompt opens an agent-origin cycle. Native idle drives mirror → idle. Todo updates are session-scoped plans. |
| amp | None. `updatesOutsidePrompt` is `false`; each prompt process opens its own incarnation with a snapshot and ends it after the terminal idle. |

### What each channel tolerates

| Sibling | Tolerance and remaining fences |
|---|---|
| claude | Unmodelled system records remain session-scoped. Invalid JSON fails the transport. A changed native session id poisons the session. Native control cancellation resolves its matching callback. |
| codex | Usage reports, retried `error` notifications, and unmodelled methods are session-scoped and open nothing. Notifications for threads this Agent does not hold are dropped. Records naming a turn other than the cycle's are ignored. Repeated text completion frames contribute only their new suffix. |
| pi | Queue reports, compaction and retry pairs, custom messages, and unmodelled types are session-scoped and open nothing. A wrapper extension error fails the cycle with cause `extension`; an operator extension error is pi's own. |
| hermes | `streaming` owns the current turn; `queued` waits for the next native start after its response. `redirected` and `steered` keep work with the running native turn and return a non-retry failure. Unknown dispositions fail the generation. Events use one bounded 256-record queue; unbound live ids are dropped. |
| opencode | Only held native session IDs reach a binding. Message parent IDs correlate prompt ownership; completed submissions reject late frames. Unknown event kinds are inert. Each binding has a bounded 256-event queue; overflow ends that binding. |
| amp | A frame naming another thread poisons the session; a frame without identity fails the turn. Unmodelled stream records open nothing. Compaction summaries are info messages the plugin view does not expose; verification compares the conversation around them. |

### Opening publication

| Sibling | Order after the establishing response |
|---|---|
| claude | snapshot, then catalog |
| codex | snapshot only |
| pi | catalog, then snapshot |
| hermes | snapshot only |
| opencode | catalog, then snapshot |
| amp | none; the snapshot is the first notification inside each prompt |

### Close boundaries

| Sibling | `session/close` |
|---|---|
| claude | Cancels callbacks and the turn, signals and waits the process, commits final transcript rows, terminalizes an agent-origin cycle, and fences. |
| codex | Cancels the turn and its dialogs, interrupts the native turn, unsubscribes the thread, commits rollout rows and the session record, terminalizes an open agent-origin cycle, and fences. The app-server and its other threads continue. |
| pi | Cancels the turn and its dialogs, signals and waits the process, commits native rows and the session record, terminalizes an open agent-origin cycle, and fences. |
| hermes | Cancels and joins callbacks, interrupts pending work, commits the native export, stops and waits the gateway, then fences. |
| opencode | Cancels callbacks and turns, snapshots the native graph, releases the logical binding, and fences. The shared server continues for peers. |
| amp | Cancels the native turn through the thread API, waits for the acknowledgement and a settled thread, joins the process, commits the captured export, and fences. The remote thread stays. |

## Usage `size`

- **opencode:** the selected provider catalog model’s `limit.context`; used tokens
  come from the latest native assistant request, including cache input.

- **hermes:** native `message.complete.usage.context_max` and `context_used`.
  Session-wide token totals are not presented as per-prompt usage.

- **claude:** `get_context_usage.maxTokens` when nonzero, else the
  `result.modelUsage` context window, else `0`. `used` is the native
  context-usage total when available, else the turn's summed message usage.

- **codex:** `thread/tokenUsage/updated` `modelContextWindow`, else the
  selected model's catalog `contextWindow`, else `0`. `used` is the latest
  model request's total.
- **pi:** `get_session_stats.contextUsage.contextWindow`, else the selected
  model's catalog `contextWindow`, else `0`.
- **amp:** the native assistant message `usage.maxInputTokens`; `used` is that
  message's input, output, cache-read, and cache-creation tokens.

## Account Usage

| Sibling | Scope | Native source and mapping | Native `plan` | Native `usageAllowed` |
|---|---|---|---|---|
| claude | `session` | `get_usage` control request with `skip_behaviors: true` on the session's process. `rate_limits.five_hour` is the `session` limit, `rate_limits.seven_day` is `weekly_all`; `seven_day_oauth_apps`, `seven_day_opus`, and `seven_day_sonnet` retain their native keys as distinct ids without labels; each `rate_limits.model_scoped[]` entry is `weekly_scoped/<display_name>` with that name as `label`; a window without `utilization` is left out; a `get_usage` window carries no `windowSeconds`. When supplied, enabled native `spend` uses the shared Anthropic projection, with currency and unit exponent supplied by the source. `rate_limits_available: false` or an observation without windows or spending is `not_reported`; the CLI reports a logged-out home the same way. `rate_limits_available: true` with a null `rate_limits` is a report claude could not fetch: the `account_usage` internal failure, unless an effective setup token supplies windows through the probe. `CLAUDE_CONFIG_DIR` selects the native credential location; that location must have its own login. Effective setup tokens use the [bounded probe exception](03-wire-contract.md#claude-setup-token-probes): Haiku supplies `session` and `weekly_all`; a Fable request supplies `weekly_scoped/Fable`; these carry `windowSeconds` of 18000 and 604800. Native turn quota events update or invalidate those cached windows. `providers`: `anthropic` natively; `openai-codex`, `opencode-go`, and `openrouter` only through the gateway `ANTHROPIC_BASE_URL` names, which also answers `anthropic` when neither the native report nor a probe supplies windows. | `subscription_type` | absent |
| codex | `agent` | `account/read`, then `account/rateLimits/read` with `excludeResetCreditDetails: true`, on the shared app-server. A null account is `not_authenticated`; an account whose `type` is not `chatgpt` is `not_reported` without the second read. Each `rateLimitsByLimitId` key yields `<key>/primary` and `<key>/secondary` for each window present, with `limitName` as `label`, `windowDurationMins × 60` as `windowSeconds`, and Unix `resetsAt`; the bare `rateLimits` snapshot is not read. No window at all is `not_reported`. A read on an idle agent starts the app-server and takes the native-home lock as session establishment would. `providers`: `openai-codex` natively, and every provider through the routes `config.toml` `model_providers` declares with a `base_url`, keyed by `env_key`, in name order. | `account.planType`, always present; an unrecognized tier is the literal `unknown` | `ordinaryUsageAllowed`; absent when the app-server nulls it, which includes an identity that does not match the active account |
| pi | `session` | `providers`: `opencode-go`, `openrouter`, `openai-codex`, `anthropic`. An authenticated loopback extension reads the addressed process’s native model registry, resolving API keys, OAuth tokens, account IDs, endpoints, and authentication headers. Custom provider implementations and unverified routes are refused. Shared readers supply subscription windows, monetary balances and spending, and request counts. A provider pi holds no native account for is read through the routes of extension-registered providers in registration order; the first gateway reporting the provider answers. | ChatGPT `plan_type`; absent for other providers | absent account-wide; Go and ChatGPT report each window’s status |
| hermes | `agent` | `providers`: `anthropic`, `openai-codex`, `opencode-go`, `openrouter`, each only through the routes `config.yaml` `providers` declares with an `api` base, keyed by `key_env`, in name order; hermes exposes no provider credentials natively. `providerId` is required. | absent | absent account-wide; gateway windows carry their status |
| opencode | `session` | `providers`: `anthropic`, `openai-codex`, `opencode-go`, `openrouter`. Directory-scoped `GET /provider/auth` and `GET /config/providers` supply effective API keys and routes. Authentication plugins for the requested provider and unverified overrides are refused. `anthropic` and `openai-codex` are read only through the gateways the catalog routes to: providers with their own `baseURL`, keyed by the catalog's key or an `{env:NAME}` reference resolved from the session environment; `opencode-go` and `openrouter` fall back to those gateways when no native account holds them. | absent | absent account-wide; Go reports each window’s status |
| amp | `none` | | | |

Provider response mappings were checked on 2026-09-18 against the
[OpenCode Go endpoint source](https://github.com/anomalyco/opencode/blob/dev/packages/console/app/src/routes/zen/go/v1/usage.ts),
[OpenRouter key API](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key),
and [credits API](https://openrouter.ai/docs/api/api-reference/credits/get-remaining-credits).
OpenCode Go reports no money. OpenRouter documents the credits endpoint as
requiring a management key; denial omits the balance while retaining key data.
ChatGPT reads `/backend-api/wham/usage` with the native account ID and refuses a
response bound to another account. Native primary, secondary, code-review, and
additional allowances retain their percentages, durations, resets, and window
status. Its credit balance has no verified currency unit and is not money.
Anthropic reads `/api/oauth/usage`; its `limits[]` and enabled `spend` retain
native percentage and explicit currency/exponent units.
A gateway a harness routes a provider through publishes an aggregate report
at `/v1/usage` beneath its API root, one section per upstream account with
the gateway's own fetch time; `usage/gateway` reads the requested provider's
section with the bearer the harness sends that gateway. Percent limits become
windows named as the provider's own reader names them, from the gateway's
window and tier: Anthropic `session`, `weekly_all`, `weekly_scoped/<Model>`;
ChatGPT `<feature>/primary` and `/secondary` labelled by the scoped model;
OpenCode Go `rolling`, `weekly`, `monthly`; a window without a mapping keeps
the gateway's id. Only ChatGPT windows carry `windowSeconds`, as natively. USD amounts
become balances and request counts request limits; OpenRouter purchased credits
are a wallet balance without a spending cap. Other units are left out. A base without the report is a plain proxy and answers
`not_reported`. A covered provider without measurements retains that answer;
an explicit upstream error fails the read even when partial windows are present.
Gateway `metadata.planType` and `metadata.allowed` supply `plan` and account-wide
`usageAllowed` for every gateway-backed sibling, independently of the native
columns above. A gateway also publishes the models it routes to at `/v1/models`,
which `gateway.Models` reads for a harness that cannot discover them itself.

Verified on 2026-09-18 with Pi 0.85.1: real reads returned ChatGPT and
Claude windows, OpenCode Go percentages, and OpenRouter dollar balances.
Verified on 2026-09-19 with Pi 0.85.1 through omp 18.2.6: gateway reads returned
Claude, ChatGPT, and OpenCode Go windows for an extension-registered provider.
Hermes 0.21.3 has no native surface exposing effective provider credentials
for these reads.
OpenCode 1.18.31, checked on 2026-09-19: subscription auth is not
established by an API-key catalog entry. Its [Anthropic provider documentation](https://opencode.ai/docs/providers/#anthropic)
describes subscription authentication through plugins, whose effective credentials
are not exposed by the native catalog.

## Delegated Agents

- **Tagged (claude).** Native `parent_tool_use_id` is retained as
  `_meta.claude.parentToolUseId` on derived updates.

- **Not applicable (amp, codex, hermes, opencode, pi).** No subscribed provenance channel; nothing is
  synthesized.

## Vendor Session Options

| Sibling | Extra fields | Structured output |
|---|---|---|
| claude | `permissionMode`, `systemPrompt`, `bare` (an explicit `false` travels so a resume can override a stored `true`), `effort` | Native `--json-schema`; the result is `_meta.claude.structuredOutput` on `usage_update`. An empty schema is refused. |
| codex | `effort`, `serviceTier`, `personality`, `approvalPolicy`, `sandboxPolicy` | Native: `outputSchema` rides `turn/start`; the parsed final answer is `_meta.codex.structuredOutput` on the prompt response. Non-JSON output omits the key and the turn still succeeds. An empty `outputSchema` object is refused at parse time. |
| pi | `thinkingLevel`, `permission` (`ask`\|`allow`), `autoRetry` (an explicit `false` travels so a resume can override a stored `true`) | Not advertised; `outputSchema` fails at session start. |
| hermes | `effort` | Not advertised; `outputSchema` is refused. |
| opencode | `mode`, `permission` (`ask`\|`allow`\|`deny`), `effort` | Native `format: json_schema`; startup requires the native `OutputFormatJsonSchema` schema. The result is `_meta.opencode.structuredOutput` on the prompt response. Empty schemas are refused. |
| amp | `mode` | Not advertised; `outputSchema` and `model` are refused. |

Session `env` and `extraPathDirs` reach the native boundary as: the addressed
thread's `config.shell_environment_policy.set` on `thread/start` and
`thread/resume`, with ordered extra directories prepended to the session-selected `PATH`,
falling back to the app-server's `PATH` when omitted (codex);
the session's native process environment (amp, claude, hermes, pi); or a native
`shell.env` plugin reading the addressed session’s metadata, following parent
IDs for child sessions (opencode).

Codex's local execution tools prepend the installed package's `codex-path`
directory after applying the session environment policy. The live test
verifies that directory against the installed package manifest before
checking that session directories immediately follow it. Other leading
entries fail. Verified with CLI `0.154.0` on 2026-09-15 against the
[native runtime prepend](https://github.com/openai/codex/blob/rust-v0.154.0/codex-rs/core/src/tools/runtimes/mod.rs#L119).

## Session Config Options

| Sibling | Advertised IDs | Value authority and read-back |
|---|---|---|
| claude | `model`, `mode`, `effort`, `output_style` | Model and permission mode use native control requests. Effort and output style use `apply_flag_settings` followed by `get_settings`. Optional selectors require native availability and a known current value. |
| codex | `model`, `mode`, `effort`, `service_tier`, `personality` | Values forward to the next `turn/start`; only `mode`, `effort`, and `personality` reject empty. `mode` is `default` or `plan`, sent as `collaborationMode`. `service_tier` and `personality` appear only while set. |
| pi | `model`, `thought_level` | Model checks `<provider>/<id>`; `get_state` reports the adopted thought level. Menu `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. |
| hermes | `model`, `effort` | Session-scoped `config.set`, followed by `model.options` and `config.get` read-back. Effort: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, `ultra`. |
| opencode | `model`, `mode`, `effort` | Nonempty values forward unchanged on the next prompt. Model IDs must be provider-qualified. `effort` appears only while set. |
| amp | `mode` | Forwarded unchanged as `--mode` on the next prompt process; native `agent_mode` updates the accepted value. Menu `low`, `medium`, `high`, `ultra`, plus the accepted value when outside it. No `model` option is advertised and `configId: "model"` is refused. |

### How each model catalog is built

| Sibling | Source | Metadata |
|---|---|---|
| claude | `initialize.models`, snapshotted once per process | `modelId` and native `supportedEffortLevels`; configured and selected ids append after the native entries. Unknown full ids forward unchanged. |
| codex | `model/list`, the presets the CLI build ships, read once per app-server generation; when the active `model_provider` routes through a gateway publishing `/v1/models`, that list replaces the presets, each id naming its upstream | `modelId`, plus `contextWindow` and `supportedEffortLevels` when the row carries them; the effort menu is the selected model's `supportedReasoningEfforts`, else a fixed menu once an effort is set |
| pi | `get_available_models`, pi's own registry filtered by its configured providers, snapshotted once at native start | `modelId`, plus `contextWindow` and `maxOutputTokens` when the row carries them |
| hermes | `model.options` provider catalogs | Provider-qualified `modelId`. Configured and selected ids append after native entries. |
| opencode | `GET /config/providers` per binding | Provider-qualified IDs, native context window and variant names; configured and selected IDs append after catalog entries. |
| amp | none | No catalog: Amp selects models through modes. `WithDefaultModel` and `WithConfiguredModels` refuse nonempty values. |

Hermes, OpenCode, and Pi refuse a host-listed id with no provider prefix at construction.

## Vendor Process Options

| Sibling | Options | `WithHome` variable |
|---|---|---|
| claude | `WithClaudeSettingSources`, `WithClaudeSettingsFile` | `CLAUDE_CONFIG_DIR` |
| codex | `WithCodexConfigOverrides` (`-c key=value`; the `shell_environment_policy` keyspace fails construction) | `CODEX_HOME` |
| pi | none | `PI_CODING_AGENT_DIR` |
| hermes | none | `HERMES_HOME` |
| opencode | none | `WithHome` maps `data`, `config`, `cache`, and `state` under its root to the corresponding XDG home variables. |
| amp | none | `WithHome` refuses nonempty values; native home selection uses the inherited environment. |

### Ephemeral scratch

| Sibling | What `WithScratchDir` parents |
|---|---|
| claude | temporary native quota-probe conversations and config directories |
| codex | the image-output read root only; the adapter writes no ephemeral files |
| pi | the content-addressed extension directory for the wrapper bridge |
| hermes | nothing; accepted for family uniformity |
| opencode | the native environment-plugin root |
| amp | the lifecycle bridge directory of each native process |

## Slash Commands

| Sibling | Discovery | Advertises | Invoke-denied (ACP alternative) |
|---|---|---|---|
| claude | `initialize.commands` | Native names after sanitizing and excluding session/account/config control commands | none |
| codex | none | Nothing; command silence is test-pinned. `/x args` reaches `turn/start` as text. | n/a |
| pi | `get_commands` at session setup, re-fetched on relaunch | Native list minus names the shared sanitizer rejects | none |
| hermes | none | Nothing; slash text reaches `prompt.submit`. | n/a |
| opencode | `GET /command` at binding setup; re-fetched on relaunch | Native names, descriptions, and hints; exact matches dispatch through the native command endpoint | none |
| amp | none | Nothing; slash text reaches the prompt as text. | n/a |

## Image Input and Output

### Advertised media envelope

Every sibling advertises the effective default limits. Amp clamps the per-image
bound to its native ceiling and declares the native dimension bound; the others
declare neither.

| Sibling | `maxBytes` | `maxDimension` | `documentFormats` |
|---|---:|---:|---|
| claude | default | 0 | `["application/pdf"]` |
| codex, pi, hermes, opencode | default | 0 | `[]` |
| amp | 5,138,022 | 8,000 | `[]` |

### Handoff native form

| Sibling | Native form for a validated image |
|---|---|
| claude | Inline base64 `source` blocks in the stream-json user message |
| codex | a `data:` URL on the turn input |
| pi | inline base64 |
| hermes | inline base64 through `image.attach_bytes` before `prompt.submit` |
| opencode | A `data:` URL file part on native message and command requests |
| amp | Inline base64 image blocks in the stream-json user message |

### Native input ordering

| Sibling | Input shape |
|---|---|
| amp, claude, codex, opencode | Ordered content arrays preserve text/image interleaving. |
| pi, hermes | Separate text and image fields accept text before the image group or images alone. Forwarded text, resource links, or text resources after the first image are refused under the [image input rule](05-behavior.md#image-input). Image blobs remain images; their URI is provenance. |

### Non-raster blobs

| Sibling | Non-`image/` blob | Post-gate disposition |
|---|---|---|
| claude | Gates `application/pdf`; refuses other non-image MIME types | PDF becomes a native document block |
| amp, codex, pi | refuses every non-`image/` MIME before decode | nothing decoded |
| hermes | Gates every MIME | Non-image bytes are dropped; the resource URI remains text |
| opencode | Gates every MIME | Bytes are dropped; the resource URI is retained as text |

### Selected-model gate authority

| Sibling | Source | `unsupported_by_model` |
|---|---|---|
| claude | No authoritative native modality field | never; unknown forwards |
| codex | `model/list` per-model `inputModalities` | yes |
| pi | `get_available_models` per-model `input` list | yes |
| hermes | No authoritative native modality field | never; unknown forwards |
| opencode | Provider catalog `capabilities.input.image` | yes; missing metadata remains unknown |
| amp | No authoritative native modality field | never; unknown forwards |

An absent model, absent field, or empty list resolves to `unknown`. A failed
catalog call fails the app-server generation start (codex) or session
establishment (claude, hermes, opencode, pi).

### Output surfaces

| Sibling | Surfaces |
|---|---|
| claude | Native image source blocks in assistant or tool-result content: inline base64 is validated; remote URL-only sources become resource links without fetching. |
| codex | `imageGeneration` and `imageView` items: inline base64 `result`, else `savedPath` bounded read → tool-call image content |
| pi | tool-call result content images from `tool_execution_update` and `tool_execution_end`; assistant image blocks one per chunk; inline base64 only |
| hermes | None; image output is not advertised. |
| opencode | Native assistant file parts and completed tool attachments: inline data URLs or bounded local reads become images; remote URLs become resource links without fetching. |
| amp | Native inline tool-result images are validated; remote image URLs become resource links without fetching. |

### Output refusal placement

| Sibling | Where a refusal appears |
|---|---|
| claude | Tool-call image refusal marks the tool failed with guidance; assistant image refusal emits guidance as agent text. |
| codex | the image tool call reports `failed` with the guidance as content |
| pi | the tool call reports `failed` with the guidance as content; an assistant image is replaced by the guidance as agent text |
| opencode | Tool attachments report failed tool status with guidance in the refused slot; assistant image refusal becomes agent text. |
| amp | the tool call carries the guidance as text content in the refused slot |

### Local read roots

| Sibling | Output read roots |
|---|---|
| claude | none; native output is inline or remote URL-only |
| codex | `cwd`, `WithScratchDir`, `os.TempDir()`, `$CODEX_HOME/generated_images` |
| pi | none |
| hermes | none |
| opencode | cwd, session additional directories, configured scratch parent, and `os.TempDir()` |
| amp | none |

### Replay source

| Sibling | Source and validation |
|---|---|
| claude | Native transcript entries use their own UUID for deduplication. Entries sharing an API message id retain each content fragment; images pass through the output gate. |
| codex | Replay validates native image rows and captured `config.images` through the output gate. Both generated and viewed images survive deletion of their original files; a missing or corrupt stored artifact fails load. |
| pi | replay decodes image blocks from the mirrored native rows through the output gate |
| hermes | The native export supplies user/assistant text parts, reasoning, tool calls, and tool results. Image output is not projected. |
| opencode | Final native messages and parts are reduced from sync events. Local images are captured in the same generation under `config`; missing or invalid stored artifacts fail load. |
| amp | User and assistant messages of the mirrored export are projected with their tool calls and inline images through the output gate; compaction summaries are not projected. Historic usage comes from the configuration record. |

## Native Verification

OpenCode `1.18.30`, tag commit `3104c1428ec91f809e5ab86631300de41eb6952e`,
verified 2026-09-14: native creation and sync import; race-enabled ACP → native
`opencode run --session` → ACP continuation; fresh-home import followed by a
further prompt; permissions, form questions, raw events, PATH rotation,
running-command cancellation, and deletion. The native CLI fixture passes
`--dir` because the CLI also consults inherited `PWD`. Message IDs follow the
[native timestamp layout](https://github.com/anomalyco/opencode/blob/v1.18.30/packages/opencode/src/id/id.ts).
Snapshot reads use scoped queries through the
[native database command](https://github.com/anomalyco/opencode/blob/v1.18.31/packages/opencode/src/cli/cmd/db.ts);
imports use the
[native sync replay route](https://github.com/anomalyco/opencode/blob/v1.18.31/packages/opencode/src/server/routes/instance/httpapi/handlers/sync.ts).
OpenCode `1.18.31`, verified 2026-09-18: conversation-scoped reads and their
stability fence complete against a populated native database without exporting
unrelated history. Query stdout is captured in a private regular file: the
[CLI's explicit exit](https://github.com/anomalyco/opencode/blob/v1.18.31/packages/opencode/src/index.ts)
truncates larger piped output at 64 KiB in the observed macOS run. Native
creation, fresh-home import, and deletion pass with a carrier larger than
64 KiB and no model calls.

OpenCode server readiness has a two-minute bound, shortened by the caller's
deadline. Health requests have a two-second timeout and retry within that bound.
Startup diagnostics distinguish health readiness from schema loading and report
their durations. On macOS with `1.18.31`, an early health request can remain
unanswered while a second connection receives a healthy response from the same
process. The two-second retry recovers this observed startup stall. Verified
2026-09-18 without model calls, including native creation, import, and deletion.

Hermes `0.21.3`, native source `f5a457ad`, verified 2026-09-15:
no-token creation/close/delete; race-enabled ACP → native
`hermes chat --cli --resume` → ACP continuation; fresh-home import followed by
a further prompt; native permissions, form clarification, raw events, PATH
rotation through an `execute_code` subprocess, running-command cancellation,
and deletion. Explicit model selection precedes the native agent build.
Approvals and clarification use the
[native server-request protocol](https://github.com/NousResearch/hermes-agent/blob/f5a457ad5bebd9d78bbf35ffaaf1c33866a03ca6/tui_gateway/contracts/server_requests.py).

Codex `0.154.0`, verified 2026-09-15: native creation/close/delete;
race-enabled ACP → native `codex exec resume` → ACP continuation; live prompt,
load/resume, and PATH rotation. The installed package's verified prefix is
recorded under [session options](#vendor-session-options). Verified
2026-09-17 without tokens: the account-usage read through the built binary on
an authenticated home. Verified 2026-09-18 without tokens: an empty session
resumes after adapter restart in the same or a fresh native home, retaining
its ACP id while committing a replacement native binding.

Pi `0.85.1`, verified 2026-09-15: native creation/close/delete; live prompt,
load/resume, strict native PATH prefix, PATH rotation, and ACP → native
`pi --print --session` → ACP continuation preserving both earlier turns.

Amp `0.0.1789432613-gd97f0d`, verified 2026-09-16 in `low` mode: no-token
creation, export, close, and delete; a prompt that read a file through a native
tool, native deletion of the thread, load into a private replacement under the
original ACP id with retained usage, native `amp threads continue --execute`
recalling the file, and a fresh resume; ACP → native continuation → ACP load;
session PATH prefix and rotation through a native tool; remote cancellation of a
running command; deletion. Two successive native compactions on one thread
settled their turns and kept the mirror consistent; recovery of a compacted
thread after native deletion is refused because the importer rejects summary
blocks. A failed recovery deleted the destination it created.

Claude Code `2.1.278`, source-verified 2026-09-20 against the integrity-checked
[published native package](https://registry.npmjs.org/@anthropic-ai/claude-code-darwin-arm64/2.1.278):
`get_usage` exposes fixed rate-limit members and `model_scoped` windows. Its
availability flag can be true while the fetched report is null. Adapter tests
cover both forms; this verification did not execute an authenticated read.

Claude Code `2.1.273`, verified 2026-09-17 without tokens: native
initialization and settings controls, account usage on the default home and
its `not_reported` answer under `WithHome`, close, resume, and deletion.

Claude Code `2.1.270`, verified 2026-09-14 with tokens: permission callbacks,
AskUserQuestion elicitation, raw events, PATH changes on resume,
running-command cancellation, and race-enabled ACP → native `claude --resume`
→ ACP load with both earlier turns retained under one conversation id. The
native transcript can contain multiple entries with one API message id.

## Known Deviations

- **Hermes native compression:** a changed durable key at mirror time poisons
  the session with `native_session_identity_drift`. Native approval and clarify
  server requests retain their JSON-RPC ids and are answered in arrival order;
  unresolved host callbacks deny or skip the pending input. Native
  `request.cancel` cancels only its matching callback.
- **Hermes terminal environment:** the native terminal bootstraps a login
  shell; operator and system startup files can reorder `PATH`. The live
  inheritance check uses a direct subprocess through `execute_code`.
  Verified on macOS with `0.21.3` on 2026-09-15 against the
  [native login bootstrap](https://github.com/NousResearch/hermes-agent/blob/f5a457ad5bebd9d78bbf35ffaaf1c33866a03ca6/tools/environments/base.py#L276).
- **Hermes input bridges:** sudo, secret, and terminal-buffer requests receive
  an empty value. Other desktop, vault, and setup request methods are unsupported.
- **Hermes restore:** its native HTTP import creates missing conversations and
  refuses replacement of an existing id, so a shorter native conversation fails
  restore. The gateway process starts before import; session binding waits until
  import and validation finish.

- **Codex publishes the CLI build's presets** whatever provider the home
  routes to, and snapshots them once per app-server generation.
- **Codex stored restore:** restore materializes
  `$CODEX_HOME/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<threadId>.jsonl`
  from the `session_meta` row's timestamp, then resumes by the recorded native
  thread id. That native id accepts letters, digits, `-`, and `_`, at most
  128 bytes; an invalid stored binding fails restore.
- **Codex sandbox:** the native default workspace-write policy can refuse
  writes outside cwd; hosts configure `sandboxPolicy` explicitly. The adapter
  translates the option in both directions: an object policy is reduced to
  its mode string on `thread/start`, and a string policy is expanded to the
  native object on `turn/start`. Additional directories become the thread's
  workspace permission profile and the turn's writable roots.
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
  a seeded `settings.json` must parse.

- **Amp models and permissions:** modes select the model; no model catalog,
  permission, elicitation, or slash-command surface exists. Native tool
  permissions stay native.
- **Amp compaction:** the remote thread actor compacts on its own and inserts an
  `info` summary message the plugin message API does not expose. Verification
  compares the conversation around such messages. A compacted thread cannot be
  recovered after native deletion.
- **Amp recovery cleanup:** a failed recovery deletes the private destination it
  created and never bound. This is the only native state the adapter deletes.

Off-prompt `-32603` reachability where it differs:

| Sibling | `_runtime_unavailable` | `_session_poisoned` `cause` |
|---|---|---|
| claude | never | `native_session_identity_drift` |
| codex | when a replacement app-server cannot start | never |
| pi | never | `native_session_identity_drift` |
| hermes | never | `native_session_identity_drift` |
| opencode | when a replacement server cannot start | `native_session_id_drift` on native deletion |
| amp | never | `native_session_identity_drift` |

Hermes and OpenCode re-hydrate on a lazy relaunch, so `_restore_failed` is
also reachable from `session/prompt` and `session/set_config_option` there.
