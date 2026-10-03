# Family Registry

Per-sibling facts that differ between siblings. Uniform rules live in the
contract and are not restated here. Update this page whenever a sibling
changes its public surface.

## Native Surfaces and Process Models

| Sibling | Strategy | Native surface | Process lifetime |
|---|---|---|---|
| claude | session runtime | Claude Code stream-json plus control protocol | One process per session. The adapter passes a UUID with `--session-id`, and relaunches with `--resume` once a transcript exists; an empty conversation retains its UUID with `--session-id`. Transcripts live under `CLAUDE_CONFIG_DIR/projects/<ProjectDirName(cwd)>/<uuid>.jsonl`; cwd is canonicalized before deriving the project directory. |
| codex | multiplexed runtime | `codex app-server --listen stdio:// --disable plugins` | One app-server per Agent serves every thread and holds `.acp-go-codex.lock` in its home until the process is waited on. It starts on the first session-establishing request; after it exits the next explicit operation starts one replacement and rebinds the addressed thread through `thread/resume`. Rollouts live in `$CODEX_HOME/sessions/`. |
| pi | session runtime | `pi --mode rpc` JSONL | One live process per session. A dead process is relaunched against the same native session file on the next prompt. |
| hermes | session runtime | `hermes serve --isolated --host 127.0.0.1 --port <port>` plus the adapter's `acp-go-hermes` plugin | One authenticated gateway per session. The native binding uses the durable conversation key; the transient gateway id is internal. Persistence uses native per-session HTTP export/import. Each launch resolves the home with `hermes config path`, which follows the active profile, and writes the plugin under that home's `plugins/`. While its `config.yaml` lists the plugin neither in `plugins.enabled` nor in `plugins.disabled`, the gateway's `plugins.manage` toggle enables and loads it before any session exists. The plugin registers only under `ACP_GO_HERMES_CALL_REPORTS=1`, which the adapter sets for the `hermes serve` it starts. A launch that cannot resolve the home or write the plugin runs without call reports. |
| opencode | multiplexed runtime | `opencode serve` authenticated loopback HTTP and global SSE; `opencode db` for scoped history reads | One server per Agent serves every session and holds a native-data-directory file lock. Short-lived native database commands read a conversation graph and its stability fence. Close releases a logical binding; a dead server is replaced on the next operation and the addressed session is rebound. |
| opencodev2 | multiplexed runtime | OpenCode v2 `opencode serve`, authenticated loopback HTTP and global SSE | One server per Agent serves every session and holds a native-data-directory file lock. Native session export/import supplies persistence. Close releases a logical binding; the next operation replaces a dead server and rebinds the addressed session. |
| amp | prompt runtime | `amp threads continue <thread> --execute --stream-json-input` stream-json plus a temporary native lifecycle plugin | One process per prompt. `session/new` runs `amp threads new` eagerly. Each prompt attaches to the remote thread, refuses to submit while remote work is active, and is reaped before export and publication; restore and observation attach without input. The plugin is installed under the native plugin directory named for the adapter process that wrote it and removed after its process is reaped; a plugin an earlier, now-dead adapter left behind is swept on the next start. |
| nanocodex | prompt runtime | Bundled `acp-go-nanocodex-native` JSONL helper embedding the native Rust library | One process per prompt. Short observation processes initialize or restore native state without input. Go holds the native UUID lock under `CODEX_HOME/nanocodex/acp-locks/` from before hydration through helper exit and reap; an untouched binding replacement holds both UUID locks. Stopped-file snapshots reacquire the UUID lock through the store commit. The helper also holds a UUID writer lock until shutdown; hydration and stopped snapshots acquire it to exclude surviving helpers after an adapter crash. Rollouts live under `CODEX_HOME/sessions/`. |

Native bindings name the Claude conversation UUID, Codex thread id, Hermes stored
session key, OpenCode session ID, Pi or Nanocodex session UUID, or Amp thread id. The
[identity contract](03-sessions-and-store.md#lifecycle-stream-and-incarnation-identity)
defines storage and wire publication.

## Session Stores

| Sibling | Kind | Carrier record |
|---|---|---|
| claude | Native transcript rows plus a `config` subpath | The current record holds cwd, transcript location, accepted environment and paths, model, permission mode, effort, system prompt, bare mode, output schema, and output style. Empty conversations commit configuration with an empty main record. |
| codex | Append-only rollout rows plus a `config` subpath | A generation contains the rollout rows and one session record naming the rollout path, the accepted session environment, the ordered paths, and the session's model, mode, effort, tier, personality, policies, output schema, and admitted image bytes keyed by native tool identity. Empty conversations commit configuration with an empty main record. If native resume reports no rollout and neither the store nor the recorded native file has rows, restore creates a new native thread and commits its binding under the unchanged ACP session id. |
| pi | Append-only session JSONL rows plus a `config` subpath | A generation contains the native rows and one session record naming pi's session file, the accepted session environment, and the ordered paths. Empty conversations commit configuration with an empty main record; restore then resumes the recorded session file without a native header. |
| hermes | Native per-conversation JSON export plus a `config` subpath | The configuration holds cwd, additional directories, environment, ordered paths, model, and effort. An empty conversation receives a native row before establishment succeeds. Missing state imports before binding a native session. Existing shorter or divergent history fails restore. |
| opencode | Online native sync-event graph plus a `config` subpath | The graph contains the root conversation and its descendants; it always carries the root creation event, so an empty main record never occurs and fails restore. The configuration holds cwd, additional directories, environment, ordered paths, model, mode, permission, variant, output schema, and captured local image bytes or refusal records. |
| opencodev2 | Native session exports plus a `config` subpath | The main record contains one raw export per conversation in a parent-first graph, including the root even when empty. Configuration holds cwd, additional directories, environment, ordered paths, model, mode, permission, variant, captured local image bytes or refusals, and deferred synthetic inbox entries. Import creates missing sessions and re-enqueues saved synthetic entries without starting execution; shorter or conflicting native messages fail restore. |
| amp | Raw native thread export plus a `config` subpath | The main record is one raw `threads export` document, so an empty conversation is an export with no messages. The configuration holds cwd, additional directories, ACP and native session ids, service origin, mode, environment, ordered paths, update time, and historic usage keyed by native protocol message id. A confirmed missing thread is imported into a private replacement under the same ACP id; compaction summaries stay in the export and cannot be imported. |
| nanocodex | Raw native rollout rows plus a `config` subpath | The carrier holds ACP/native IDs, a home-relative rollout path, cwd, model, effort, endpoint, `modelIdPrefix`, transport, credential-variable name and auth-file path, environment, ordered paths, title, started state, and update time. After a prompted helper shuts down, it atomically replaces an optional checkpoint sidecar containing the snapshot head, request prefix, and context accounting; `config.checkpoint` mirrors the latest copy. Restore uses it only when its boundary, history length, identity, and supported shape match, otherwise rebuilding from native history. Optional checkpoint failures do not fail committed turns. A verified untouched header-only rollout receives a fresh native binding during observation or pre-prompt launch; the prior file remains. |

### Mirror-Commit Ordering

| Sibling | Settlement and close ordering |
|---|---|
| claude | A native `result` settles a prompt. Context usage and transcript rows commit before idle and the response. Close interrupts the turn, signals and waits the process, commits the final transcript, then fences. |
| codex | Settlement reads the rollout after `turn/completed` and commits the new rows before the terminal idle and the response. Close interrupts any turn, unsubscribes the thread, commits, then fences; the shared app-server stays up for its peers. |
| pi | Settlement reads pi's session file after `agent_settled` and commits the new rows before the terminal idle and the response. Close aborts any turn, stops the process, commits, then fences. |
| hermes | `message.complete` settles the turn; callbacks finish before HTTP export, atomic mirror, idle, and response. Close interrupts pending work, exports while the gateway is available, then stops it. |
| opencode | Native prompt HTTP completion refetches every owned assistant step; callbacks join before the stable sync snapshot, atomic mirror, idle, and response. Close snapshots while HTTP remains available, then releases the binding. |
| opencodev2 | `session.execution.succeeded`, `failed`, or non-shutdown `interrupted` settles execution; HTTP prompt acceptance does not. Callbacks join, then the root export ends at the terminal event’s durable idle marker and each descendant export ends at its latest idle marker, or has empty history before its first completed execution. A second read verifies retained messages, configuration, and deferred synthetic inputs before commit, idle, and response. Later executions may continue during verification. Restore preserves saved message identity, order, and content while accepting newly completed assistant, shell, and compaction records omitted by earlier exports. A shutdown interrupt ends the binding. Operations without a terminal boundary require native wait and matching idle graph snapshots. Close snapshots while HTTP remains available, then releases the binding. |
| amp | The plugin's `agent.end` receipt and a quiet thread view settle the turn; the process is reaped, then the export must match the observed conversation, retried with backoff under a 30-second deadline, before the atomic mirror, idle, and response. Close joins the prompt process, commits, then fences; the remote thread stays. |
| nanocodex | Launch commits the reconciled native prefix and current binding before prompt admission. The helper drains events and flushes the rollout before its prompt reply. The adapter shuts down and reaps the helper, then commits the raw rows and configuration before terminal idle and the ACP response. |

## Turn Failure

Only pi adds a vendor cause: `extension`.

| Sibling | Native mapping and disclosed detail |
|---|---|
| claude | Native error results supply `errors`, `error`, or `result`. |
| codex | A `turn/completed` outside the `completed` and `interrupted` statuses is provider, carrying the native error message, `httpStatusCode` as `statusCode`, and `codexErrorInfo.code` as `providerCode`; a refused `turn/start` and a non-retried `error` notification are provider with the native message. |
| pi | Command rejection preserves native text. Wrapper extension failure is `extension` carrying the extension's own error text. |
| hermes | Native result status or RPC rejection supplies provider detail. |
| opencode | Prompt HTTP rejection or assistant error records supply provider detail. Failed native history reads are transport errors, preserving native HTTP status as `statusCode`. |
| opencodev2 | Native execution errors preserve `message`, `status` as `statusCode`, and `type` as `providerCode`. Prompt HTTP rejection supplies provider detail. Failed native usage reads are transport errors, preserving native HTTP status as `statusCode`. |
| amp | A native receipt status `error` or an error stream record supplies provider detail. A refused frame, a missing receipt, or a failed mirror commit is `transport` with the adapter's cause. |
| nanocodex | Typed provider and transport errors use fixed summaries without response bodies or credentials; native HTTP and WebSocket handshake rejection codes become `statusCode`. `providerCode` carries the provider's classification even when unrecognized: the first terminal candidate, else the first, among the canonical `error_type`, the error `code` (a string or an integer), the error object's `type`, and `incomplete_details.reason`, each limited to 128 ASCII letters, digits, dots, underscores, or hyphens. Process exits retain the exit status and stderr tail. Malformed helper frames fail as transport errors. |

## Raw Events

The siblings emit every admitted record on their permanent channel and log
and continue on emit failure. Codex forwards the notification's params with
the method added as `method`; image `result` payloads are replaced by their
size. OpenCode replaces image data URLs with their encoded size. Pi empties
image `data` members and adds their decoded size as `sizeBytes`. Amp has no
permanent channel: it emits the admitted stream-json records of the current
prompt process and removes image payloads. Nanocodex forwards typed native events
inside the current prompt and replaces inline image URLs with an omission marker.

## Lifecycle Extension

### What each permanent channel delivers

| Sibling | Open / delivery / settle |
|---|---|
| claude | Assistant, user, or stream work outside a prompt opens an agent-origin cycle. Native `result` or an agent-origin `task_notification` settles it. Delegated records retain their parent tool-use provenance. |
| codex | The channel is permanent, but no supported path starts thread work outside a client turn: nothing follows `turn/completed`, a native `codex exec resume` on the app-server's thread is refused by the thread-store writer lock, and `thread/resume` emits only status, token-usage, goal, and MCP records, which are session-scoped and open nothing. A record that arrives with no prompt in flight is session-scoped and opens nothing. |
| pi | An `agent_start` with no prompt in flight opens an agent-origin turn on the event pump; `agent_settled` drives usage from `get_session_stats` → mirror → idle, unless the turn was cancelled, which reports no usage. Native records that arrive while the statistics are read are held and delivered after the idle. |
| hermes | Native message, thought, tool, dialog, or error events outside a prompt open an agent-origin cycle. `message.complete` drives mirror → idle. |
| opencode | Native user or assistant message work outside a prompt opens an agent-origin cycle. Native idle drives mirror → idle. Todo updates are session-scoped plans. |
| opencodev2 | `session.execution.started` outside a prompt opens an agent-origin cycle. Text, reasoning, tools, permissions, and forms belong to that execution; its terminal event drives mirror → idle. Todo updates are session-scoped plans. |
| amp | None. `updatesOutsidePrompt` is `false`; each prompt process opens its own incarnation with a snapshot and ends it after the terminal idle. |
| nanocodex | None. `updatesOutsidePrompt` is `false`; the accepted prompt opens a fresh incarnation and the reaped prompt process ends it. |

### What each channel tolerates

| Sibling | Tolerance and remaining fences |
|---|---|
| claude | Unmodelled system records remain session-scoped. Invalid JSON fails the transport. A changed native session id poisons the session. Native control cancellation resolves its matching callback. |
| codex | Usage reports, retried `error` notifications, and unmodelled methods are session-scoped and open nothing. Notifications for threads this Agent does not hold are dropped. Records naming a turn other than the cycle's are ignored. Repeated text completion frames contribute only their new suffix. Each session has a bounded 256-event queue; overflow contains that session, fails its work with a transport cause, and fences it while the shared app-server and its peers keep running. A native turn that ignores its interrupt for five seconds is contained the same way and its prompt answers cancelled. |
| pi | Queue reports, compaction and retry pairs, custom messages, and unmodelled types are session-scoped and open nothing. A wrapper extension error fails the cycle with cause `extension`; an operator extension error is pi's own. |
| hermes | `streaming` owns the current turn; `queued` waits for the next native start after its response. `redirected` and `steered` keep work with the running native turn and return a non-retry failure. Unknown dispositions fail the generation. Events use one bounded 256-record queue; unbound live ids are dropped. |
| opencode | Only held native session IDs reach a binding. Message parent IDs correlate prompt ownership; completed submissions reject late frames. Unknown event kinds are inert. Each binding has a bounded 256-event queue; overflow ends that binding. |
| opencodev2 | Only held native session IDs reach a binding. Prompt inbox acceptance and execution start admit the turn; execution terminal events settle it. Unknown event kinds are inert. Each binding has a bounded 256-event queue; overflow ends that binding. |
| amp | A frame naming another thread poisons the session; a frame without identity fails the turn. Unmodelled stream records open nothing. Compaction summaries are info messages the plugin view does not expose; verification compares the conversation around them. |
| nanocodex | Helper request IDs fence prompt events. Native sequence regressions and malformed frames fail the turn; an unexpected native binding change poisons the session. Unprojected typed events remain raw telemetry. |

### Opening publication

| Sibling | Order after the establishing response |
|---|---|
| claude | snapshot, then catalog |
| codex | snapshot only |
| pi | catalog, then snapshot |
| hermes | snapshot only |
| opencode | catalog, then snapshot |
| opencodev2 | catalog, then snapshot |
| amp | none; the snapshot is the first notification inside each prompt |
| nanocodex | none; the snapshot is the first notification inside each accepted prompt |

### Close boundaries

| Sibling | `session/close` |
|---|---|
| claude | Cancels callbacks and the turn, signals and waits the process, commits final transcript rows, terminalizes an agent-origin cycle, and fences. |
| codex | Cancels the turn and its dialogs, interrupts the native turn, unsubscribes the thread, commits rollout rows and the session record, and fences. The app-server and its other threads continue. |
| pi | Cancels the turn and its dialogs, signals and waits the process, commits native rows and the session record, terminalizes an open agent-origin cycle, and fences. |
| hermes | Cancels and joins callbacks, interrupts pending work, commits the native export, stops and waits the gateway, then fences. |
| opencode | Cancels callbacks and turns, snapshots the native graph, releases the logical binding, and fences. The shared server continues for peers. |
| opencodev2 | Cancels callbacks and turns, interrupts native execution, snapshots the native graph, releases the binding, and fences. The shared server continues for peers. |
| amp | Cancels the native turn through the thread API, waits for the acknowledgement and a settled thread, joins the process, commits the captured export, and fences. The remote thread stays. |
| nanocodex | Cancels and joins the addressed prompt, reaps its helper, commits its rollout and configuration, then fences. |

## Usage Updates

Each sibling's native source for the [usage rules](04-behavior.md#usage-updates)
and the [call breakdown](04-behavior.md#call-breakdown):

- **claude:** each top-level model call reports at its `message_start` stream
  event `used` as `input_tokens + cache_read_input_tokens +
  cache_creation_input_tokens`, and at its `message_delta` that request plus
  `output_tokens`. A provider that opens the call with an empty report
  reports only at `message_delta`. The `message_delta` update carries the
  breakdown: `input_tokens` (which excludes cache), `cache_read_input_tokens`,
  `cache_creation_input_tokens`, and `output_tokens`; request members come
  from `message_delta` where it restates the request, else from
  `message_start`, and a null figure is absent. A call no stream event
  announced reports its request at its first assistant record, whose
  `output_tokens` repeats the opening figure, with a breakdown without
  output. Subagent calls report nothing. Settlement reports the last call's
  figure with `result.total_cost_usd`, the session's cumulative cost, and the
  structured output. A `compact_boundary` discards the figure. `size` is
  `get_context_usage.maxTokens`, read at every process start and after a
  model change, then the `result.modelUsage` context window of the turn's
  model, else `0`. The prompt response is `result.usage`.
- **codex:** a thread the adapter starts opts into `rawResponse/completed`
  (`thread/start` `experimentalRawEvents`, with `rawResponseItem/completed`
  opted out at initialize), which reports each model request as its response
  completes, before its tools run. A resumed thread cannot opt in and reports
  from `thread/tokenUsage/updated`, sent after the request's tools finished,
  when its cumulative `total` moved; on an opted-in thread that report only
  moves the baseline. `used` is the request's `totalTokens`; the breakdown is
  `inputTokens` minus `cachedInputTokens` and `cacheWriteInputTokens`,
  `cachedInputTokens`, `cacheWriteInputTokens`, and `outputTokens`. After a
  compaction codex's estimate, an unchanged `total` with a new `last`,
  replaces the figure without a breakdown. `size` is the gateway model list's
  `contextWindow`, else the window codex last reported, else `0`. No
  settlement report and no cost. The prompt response sums the requests.
- **hermes:** the plugin's `llm_execution` middleware reports each Chat
  Completions response of the session's own conversation: the gateway's
  usage members and response id, broadcast as `plugin.acp-go-hermes.call`
  after the response streams and before Hermes records it. Each report
  yields one update. Each token figure is the first non-zero of the members
  Hermes's usage normalization reads, else a reported 0, else absent:
  - prompt: `prompt_tokens`, then `input_tokens`;
  - cache reads: `prompt_tokens_details.cached_tokens`, then
    `cache_read_input_tokens`, `prompt_cache_hit_tokens`, `cached_tokens`;
  - cache writes: `prompt_tokens_details.cache_write_tokens`, then
    `prompt_tokens_details.cache_creation_input_tokens`,
    `cache_creation_input_tokens`, `cache_write_tokens`;
  - output: `completion_tokens`, then `output_tokens`.

  `used` is the prompt Hermes records, the larger of the prompt and the cache
  reads plus writes. The breakdown is `inputTokens`, the prompt minus the
  cache reads and, where sent, the cache writes, present only when the prompt
  and cache reads were sent and the difference is not negative; then
  `cachedReadTokens`, `cachedWriteTokens`, and `outputTokens`, the matching
  figures. `size` is the `context_max` of the latest reading that stated one,
  when that reading names the call's model. A report without one waits for
  the first reading that states its model's window; one whose window no
  reading of its cycle states reports nothing. Retry attempts report
  separately. Calls on other wires, auxiliary calls, review forks, and
  delegated children report nothing. The gateway's `session.usage` tick and
  `message.complete` carry cumulative counters. A reading whose `prompt`
  counter moved by exactly the prompts of a run of consecutive reported calls
  reports nothing more. Any other reading that moved the counter reports
  `used` as `context_used` and `size` as `context_max`, without a breakdown.
  Ticks stop before `message.complete`, so the closing frame reports a turn's
  last unreported response inside the turn. A response with empty usage moves
  no counter, and Hermes drops `context_used`, as it does after a compaction
  until a response follows. No cost. The prompt response is the counters'
  difference across the readings the turn owned.
- **opencode:** each model call's `step-finish` part reports `used` as its
  `input + output + reasoning + cache.read + cache.write`, with the breakdown
  `input` (opencode subtracts cache reads and writes), `cache.read`,
  `cache.write`, and `output + reasoning`. `size` is the catalog
  `limit.context` of the model the call ran on, else `0`. Calls answering a
  message a native client added to the run report inside the turn. When the
  prompt answers before the stream delivered every call, settlement reports
  the remaining calls from native history and nothing more. A compaction
  summary call reports no context and no breakdown. No cost. The prompt
  response sums the calls.
- **opencodev2:** each `session.step.ended` or usage-bearing `step.failed`
  reports `used` as `input + output + reasoning + cache.read + cache.write`.
  The breakdown is `input`, `cache.read`, `cache.write`, and `output + reasoning`.
  `size` is the call model’s catalog `limit.context`, else `0`; `cost` comes
  from the native session’s cumulative USD cost at publication. Compaction
  consumes tokens but reports no context or breakdown. The prompt response
  sums the execution’s calls and compaction usage.
- **pi:** each assistant `message_end` reports `used` as its `totalTokens`,
  else its input, output, and cache tokens, with the breakdown `input` (pi
  stores it without cache tokens), `cacheRead`, `cacheWrite`, and `output`
  (reasoning included); aborted and failed responses report nothing. A
  response whose first `message_update` already carries input usage also
  reports its input and cache tokens there. `size` is the last
  `get_session_stats.contextUsage.contextWindow` read, else the selected
  model's catalog `contextWindow`, else `0`. Settlement of a prompt or
  agent-origin turn reports a non-zero `contextUsage.tokens`, else the
  cycle's last response's figure, with `get_session_stats.cost`, the
  session's cumulative cost in USD. A completed `compaction_end` discards the
  figure. The prompt response sums the responses' usage.
- **amp:** the native assistant message `usage.maxInputTokens` is `size`;
  `used` is that message's input, output, cache-read, and cache-creation
  tokens, replaced by the result frame's usage when the prompt settles.

- **nanocodex:** each native `model.call.completed` supplies its Responses usage.
  `used` is that call's total tokens; `size` is the configured native context
  window when reported for the selected model, otherwise zero. The breakdown
  subtracts cache reads and writes from input only when both figures are
  reported. Native cache-write zeros are omitted because the native type loses
  whether the provider supplied them; positive cache writes are retained.
  Compaction usage contributes to the terminal turn total but has no per-call
  usage update. The terminal prompt result carries native turn totals; no
  settlement usage update is emitted.

### Response ids

Each sibling's source for the gateway's response id that chunks carry as
`messageId` and the [call breakdown](04-behavior.md#call-breakdown) as
`responseId`:

- **claude:** exact. The `message.id` claude records from the gateway's
  response: the `message_start` stream event for live chunks and the
  `message_delta` breakdown, the assistant record for a call no stream event
  announced, and transcript rows for replay. Omitted when a response has no
  id and for claude's own `<synthetic>` records, whose id is a harness UUID.
- **codex:** the breakdown is exact on a thread the adapter starts, from
  `rawResponse/completed` `responseId`; a resumed thread's
  `thread/tokenUsage/updated` names no response, so its breakdown carries
  none. Chunks carry no `messageId`: streamed deltas name only their item and
  turn, the id arrives only at completion, and the rollout cannot tie a replayed row to its response with
  certainty, since a failed or usage-less response's items precede the next
  `token_usage_record` exactly as that record's own items do.
- **hermes:** exact for the breakdown. The plugin reads the completed
  response's `id` from the OpenAI SDK's parse of the gateway's body. It
  drops Hermes's `stream-<uuid4>` fallback and its partial-stream stub id, so
  a response whose gateway sent no id carries none. Chunks carry no
  `messageId`: the middleware returns only after the response streamed, and
  neither the stream callbacks nor the persisted messages carry the id.
  Replayed chunks carry none.
- **opencode, opencodev2:** omitted. Native messages, call events, and persisted
  history carry no gateway response id.
- **pi:** exact. The assistant message's `responseId`, which pi holds on
  `message_end` and in the session file. pi's RPC `message_update` omits the
  streaming message, so the adapter's extension relays the id from the first
  update that holds it as a `setStatus` under `acp-go-pi:response`, ahead of
  that update's frame; streamed, terminal, and replayed chunks and the
  `message_end` breakdown carry it. A response pi holds no id for, such as one
  that failed before the gateway answered, carries neither.
- **amp:** not recorded.
- **nanocodex:** the breakdown uses native `model.call.completed.response_id`.
  Gateway chunks carry a response id once the provider exposes it. A key built
  from the native call number and output position controls deduplication.
  Native default-route and replayed chunks carry none because their source
  exposes no attributable response id at publication.

## Account Usage

| Sibling | Scope | Native source and mapping | Native `plan` | Native `usageAllowed` |
|---|---|---|---|---|
| claude | `session` | `get_usage` control request with `skip_behaviors: true` on the session's process. `rate_limits.five_hour` is the `session` limit, `rate_limits.seven_day` is `weekly_all`; `seven_day_oauth_apps`, `seven_day_opus`, and `seven_day_sonnet` retain their native keys as distinct ids without labels; each `rate_limits.model_scoped[]` entry is `weekly_scoped/<display_name>` with that name as `label`; a window without `utilization` is left out; a `get_usage` window carries no `windowSeconds`. When supplied, enabled native `spend` uses the shared Anthropic projection, with currency and unit exponent supplied by the source. `rate_limits_available: false` or an observation without windows or spending is `not_reported`; the CLI reports a logged-out home the same way. `rate_limits_available: true` with a null `rate_limits` is `not_reported` before the process completes a turn, since claude fills the report from its first API response, and afterwards a report claude could not fetch: the `account_usage` internal failure, unless an effective setup token supplies windows through the probe. `CLAUDE_CONFIG_DIR` selects the native credential location; that location must have its own login. Effective setup tokens use the [bounded probe exception](02-wire-contract.md#claude-setup-token-probes): Haiku supplies `session` and `weekly_all`; a Fable request supplies `weekly_scoped/Fable`; these carry `windowSeconds` of 18000 and 604800. Native turn quota events update or invalidate those cached windows. `providers`: `anthropic` natively; `openai-codex`, `opencode-go`, and `openrouter` only through the gateway `ANTHROPIC_BASE_URL` names, which also answers `anthropic` when neither the native report nor a probe supplies windows. | `subscription_type` | absent |
| codex | `agent` | `account/read`, then `account/rateLimits/read` with `excludeResetCreditDetails: true`, on the shared app-server. A null account is `not_authenticated`; an account whose `type` is not `chatgpt` is `not_reported` without the second read. Each `rateLimitsByLimitId` key yields `<key>/primary` and `<key>/secondary` for each window present, with `limitName` as `label`, `windowDurationMins × 60` as `windowSeconds`, and Unix `resetsAt`; the bare `rateLimits` snapshot is not read. No window at all is `not_reported`. A read on an idle agent starts the app-server and takes the native-home lock as session establishment would. `providers`: `openai-codex` natively, and every provider through the routes `config.toml` `model_providers` declares with a `base_url`, keyed by `env_key`, in name order. | `account.planType`, always present; an unrecognized tier is the literal `unknown` | `ordinaryUsageAllowed`; absent when the app-server nulls it, which includes an identity that does not match the active account |
| pi | `session` | `providers`: `opencode-go`, `openrouter`, `openai-codex`, `anthropic`. An authenticated loopback extension reads the addressed process’s native model registry, resolving API keys, OAuth tokens, account IDs, endpoints, and authentication headers. Custom provider implementations and unverified routes are refused. Shared readers supply subscription windows, monetary balances and spending, and request counts. A provider pi holds no native account for is read through the routes of extension-registered providers in registration order; the first gateway reporting the provider answers. | ChatGPT `plan_type`; absent for other providers | absent account-wide; Go and ChatGPT report each window’s status |
| hermes | `agent` | `providers`: `anthropic`, `openai-codex`, `opencode-go`, `openrouter`, each only through the routes `config.yaml` `providers` declares with an `api` base, keyed by `key_env`, in name order; hermes exposes no provider credentials natively. | absent | absent account-wide; gateway windows carry their status |
| opencode | `session` | `providers`: `anthropic`, `openai-codex`, `opencode-go`, `openrouter`. Directory-scoped `GET /provider/auth` and `GET /config/providers` supply effective API keys and routes. Authentication plugins for the requested provider, whose effective credentials the native catalog does not expose, and unverified overrides are refused. `anthropic` and `openai-codex` are read only through the gateways the catalog routes to: providers with their own `baseURL`, keyed by the catalog's key or an `{env:NAME}` reference resolved from the session environment; `opencode-go` and `openrouter` fall back to those gateways when no native account holds them. | absent | absent account-wide; Go reports each window’s status |
| opencodev2 | `session` | `providers`: `anthropic`, `openai-codex`, `opencode-go`, `openrouter`. Location-scoped `/api/provider`, `/api/model`, and `/api/integration` identify routes and active connections; `/api/credential` supplies native API keys, and environment connections use the shared runtime’s environment. Model settings override provider settings. OAuth, OpenCode Console organization-scoped inference routes, and unverified authentication overrides are refused. Gateway fallback considers endpoints explicitly configured in native `/api/config` documents and revalidates the effective catalog route and credential after the read. | absent | absent account-wide; Go reports each window’s status |
| amp | `none` | | | |
| nanocodex | `none` | Not advertised. | none | none |

Every sibling's provider and gateway reads follow the
[shared provider readers](02-wire-contract.md#shared-provider-readers).

## Delegated Agents

- **Tagged (claude, amp).** The native parent tool-use id is retained as
  `_meta.<vendor>.parentToolUseId` on derived updates.

- **Not applicable (codex, hermes, nanocodex, opencode, opencodev2, pi).** No subscribed provenance channel; nothing is
  synthesized.

## Vendor Session Options

| Sibling | Extra fields | Structured output |
|---|---|---|
| claude | `permissionMode`, `systemPrompt`, `bare` (an explicit `false` travels so a resume can override a stored `true`), `effort` | Native `--json-schema`; the result is `_meta.claude.structuredOutput` on `usage_update`. An empty schema is refused. |
| codex | `effort`, `serviceTier`, `personality`, `approvalPolicy`, `sandboxPolicy` | Native: `outputSchema` rides `turn/start`; the parsed final answer is `_meta.codex.structuredOutput` on the prompt response. Non-JSON output omits the key and the turn still succeeds. An empty `outputSchema` object is refused at parse time. |
| pi | `thinkingLevel`, `permission` (`ask`\|`allow`), `autoRetry` (an explicit `false` travels so a resume can override a stored `true`) | Not advertised; `outputSchema` fails at session start. |
| hermes | `effort` | Not advertised; `outputSchema` is refused. |
| opencode | `mode`, `permission` (`ask`\|`allow`\|`deny`), `effort` | Native `format: json_schema`; startup requires the native `OutputFormatJsonSchema` schema. The result is `_meta.opencode.structuredOutput` on the prompt response. Empty schemas are refused. |
| opencodev2 | `mode`, `permission` (`ask`\|`allow`\|`deny`), `effort` | Not advertised: OpenCode v2 has no schema-enforced prompt API. `outputSchema` is refused; no `WithSessionOutputSchema` helper is exported. |
| amp | `mode` | Not advertised; `outputSchema` and `model` are refused. |
| nanocodex | `thinking`, `apiBaseUrl`, `websocketUrl`, `modelIdPrefix`, `transport`, `apiKeyEnv`, `authFile` | Not advertised; `outputSchema` is refused. |

Session `env` and `extraPathDirs` reach the native boundary as: the addressed
thread's `config.shell_environment_policy.set` on `thread/start` and
`thread/resume`, with ordered extra directories prepended to the session-selected `PATH`,
falling back to the app-server's `PATH` when omitted (codex);
the session's native process environment (amp, claude, hermes, nanocodex, pi); or a native
`shell.env` plugin reading the addressed session’s metadata, following parent
IDs for child sessions (opencode); or an `execute.before` plugin that resolves
that carrier through native parent IDs and sets the addressed session’s
`/environment` before each tool execution (opencodev2).

Codex's local execution tools prepend the installed package's `codex-path`
directory after applying the session environment policy. The live test
verifies that directory against the installed package manifest before
checking that session directories immediately follow it. Other leading
entries fail.

## Session Config Options

| Sibling | Advertised IDs | Value authority and read-back |
|---|---|---|
| claude | `model`, `mode`, `effort`, `output_style` | Model and permission mode use native control requests. Effort and output style use `apply_flag_settings` followed by `get_settings`. Optional selectors require native availability and a known current value. |
| codex | `model`, `mode`, `effort`, `service_tier`, `personality` | Values forward to the next `turn/start`. `mode` is `default` or `plan`, sent as `collaborationMode`. `service_tier` and `personality` appear only while set. |
| pi | `model`, `thought_level` | Model checks `<provider>/<id>`; `get_state` reports the adopted thought level. Menu `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. |
| hermes | `model`, `effort` | Session-scoped `config.set`, followed by `model.options` and `config.get` read-back. Effort: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, `ultra`. |
| opencode | `model`, `mode`, `effort` | Nonempty values forward unchanged on the next prompt. Model IDs must be provider-qualified. `effort` appears only while set. |
| opencodev2 | `model`, `mode`, `effort` | Native session model and agent endpoints apply selections. Model IDs must be provider-qualified. The model variant carries effort; `effort` appears only while set. Unspecified selections resolve from native model and agent defaults. |
| amp | `mode` | Forwarded unchanged as `--mode` on the next prompt process; native `agent_mode` updates the accepted value. Menu `low`, `medium`, `high`, `ultra`, plus the accepted value when outside it. No `model` option is advertised and `configId: "model"` is refused. |
| nanocodex | `model`, `thought_level` | Short observation processes validate selections and report effective values. Model changes are accepted only before the first committed turn; effort changes apply to subsequent prompts. |

### How each model catalog is built

| Sibling | Source | Metadata |
|---|---|---|
| claude | `initialize.models`, snapshotted once per process | `modelId` and native `supportedEffortLevels`; configured and selected ids append after the native entries. Unknown full ids forward unchanged. |
| codex | `model/list`, the presets the CLI build ships, read once per app-server generation; when the active `model_provider` routes through a gateway publishing `/v1/models`, that list replaces the presets, each id naming its upstream; an id whose last path segment is a preset carries that preset's efforts and default | `modelId`, plus `contextWindow` and `supportedEffortLevels` when the row carries them; the effort menu is the selected model's `supportedReasoningEfforts`, else a fixed menu once an effort is set |
| pi | `get_available_models`, pi's own registry filtered by its configured providers, snapshotted once at native start | `modelId`, plus `contextWindow` and `maxOutputTokens` when the row carries them |
| hermes | `model.options` provider catalogs | Provider-qualified `modelId`. Configured and selected ids append after native entries. |
| opencode | `GET /config/providers` per binding | Provider-qualified IDs, native context window and variant names; configured and selected IDs append after catalog entries. |
| opencodev2 | Location-scoped `GET /api/model` per binding | Provider-qualified IDs, `limit.context`, `limit.output`, and native variant names; configured and selected IDs append after catalog entries. |
| amp | none | No catalog: Amp selects models through modes. `WithDefaultModel` and `WithConfiguredModels` refuse nonempty values. |
| nanocodex | The helper enumerates native `Model::ALL`, appends the selected native model if absent, and reports native effort support during initialization | `modelId`, configured native context window, and supported effort levels; configured and default IDs append after native entries. The native model parser accepts selections outside its default picker; gateway namespaces are separate from native model IDs. |

Hermes, OpenCode, and Pi refuse a host-listed id with no provider prefix at construction.

## Vendor Process Options

| Sibling | Options | `WithHome` variable |
|---|---|---|
| claude | `WithClaudeSettingSources`, `WithClaudeSettingsFile` | `CLAUDE_CONFIG_DIR` |
| codex | `WithCodexConfigOverrides` (`-c key=value`; the `shell_environment_policy` keyspace fails construction) | `CODEX_HOME` |
| pi | none | `PI_CODING_AGENT_DIR` |
| hermes | none | `HERMES_HOME` |
| opencode | none | `WithHome` maps `data`, `config`, `cache`, and `state` under its root to the corresponding XDG home variables. |
| opencodev2 | none | `WithHome` maps `data`, `config`, `cache`, and `state` under its root to the corresponding XDG home variables. |
| amp | none | `WithHome` refuses nonempty values; native home selection uses the inherited environment. |
| nanocodex | none | `CODEX_HOME` |

### Ephemeral scratch

| Sibling | What `WithScratchDir` parents |
|---|---|
| claude | temporary native quota-probe conversations and config directories |
| codex | the image-output read root only; the adapter writes no ephemeral files |
| pi | the content-addressed extension directory for the wrapper bridge |
| hermes | nothing; accepted for family uniformity |
| opencode | the native environment-plugin root |
| opencodev2 | the native environment-plugin root |
| amp | the lifecycle bridge directory of each native process |
| nanocodex | nothing; accepted for uniform options |

## Slash Commands

| Sibling | Discovery | Advertises | Invoke-denied (ACP alternative) |
|---|---|---|---|
| claude | `initialize.commands` | Native names after sanitizing and excluding session/account/config control commands | none |
| codex | none | Nothing; command silence is test-pinned. `/x args` reaches `turn/start` as text. | n/a |
| pi | `get_commands` at session setup, re-fetched on relaunch | Native list minus names the shared sanitizer rejects | none |
| hermes | none | Nothing; slash text reaches `prompt.submit`. | n/a |
| opencode | `GET /command` at binding setup; re-fetched on relaunch | Native names, descriptions, and hints; exact matches dispatch through the native command endpoint | none |
| opencodev2 | Location-scoped `GET /api/command` at binding setup and relaunch | Native names and descriptions; exact matches dispatch through the native command endpoint | none |
| amp | none | Nothing; slash text reaches the prompt as text. | n/a |
| nanocodex | none | Nothing; slash-prefixed text is ordinary prompt input. | n/a |

## Image Input and Output

### Advertised media envelope

Every sibling advertises the effective default limits. Amp clamps the per-image
bound to its native ceiling and declares the native dimension bound; the others
declare neither.

| Sibling | `maxBytes` | `maxDimension` | `documentFormats` |
|---|---:|---:|---|
| claude | default | 0 | `["application/pdf"]` |
| codex, pi, hermes, nanocodex, opencode, opencodev2 | default | 0 | `[]` |
| amp | 5,138,022 | 8,000 | `[]` |

### Handoff native form

| Sibling | Native form for a validated image |
|---|---|
| claude | Inline base64 `source` blocks in the stream-json user message |
| codex | a `data:` URL on the turn input |
| pi | inline base64 |
| hermes | inline base64 through `image.attach_bytes` before `prompt.submit` |
| opencode | A `data:` URL file part on native message and command requests |
| opencodev2 | A `data:` URI in the native prompt or command’s `files` array |
| amp | Inline base64 image blocks in the stream-json user message |
| nanocodex | Inline image data URLs in the ordered native content array |

### Native input ordering

| Sibling | Input shape |
|---|---|
| amp, claude, codex, nanocodex, opencode | Ordered content arrays preserve text/image interleaving. |
| pi, hermes, opencodev2 | Separate text and image fields accept text before the image group or images alone. Forwarded text, resource links, or text resources after the first image are refused under the [image input rule](04-behavior.md#image-input). Image blobs remain images; their URI is provenance. |

### Non-raster blobs

| Sibling | Non-`image/` blob | Post-gate disposition |
|---|---|---|
| claude | Gates `application/pdf`; refuses other non-image MIME types | PDF becomes a native document block |
| amp, codex, nanocodex, pi | refuses every non-`image/` MIME before decode | nothing decoded |
| hermes | Gates every MIME | Non-image bytes are dropped; the resource URI remains text |
| opencode | Gates every MIME | Bytes are dropped; the resource URI is retained as text |
| opencodev2 | Gates every MIME | Bytes are dropped; the resource URI is retained as text |

### Selected-model gate authority

| Sibling | Source | `unsupported_by_model` |
|---|---|---|
| claude | No authoritative native modality field | never; unknown forwards |
| codex | `model/list` per-model `inputModalities` | yes |
| pi | `get_available_models` per-model `input` list | yes |
| hermes | No authoritative native modality field | never; unknown forwards |
| opencode | Provider catalog `capabilities.input.image` | yes; missing metadata remains unknown |
| opencodev2 | Model catalog `capabilities.input` | yes; missing metadata remains unknown |
| amp | No authoritative native modality field | never; unknown forwards |
| nanocodex | No authoritative native modality field | never; unknown forwards |

A failed
catalog call fails the app-server generation start (codex) or session
establishment (claude, hermes, opencode, opencodev2, pi).

### Output surfaces

| Sibling | Surfaces |
|---|---|
| claude | Native image source blocks in assistant or tool-result content: inline base64 is validated; remote URL-only sources become resource links without fetching. |
| codex | `imageGeneration` and `imageView` items: inline base64 `result`, else `savedPath` bounded read → tool-call image content |
| pi | tool-call result content images from `tool_execution_update` and `tool_execution_end`; assistant image blocks one per chunk; inline base64 only |
| hermes | None; image output is not advertised. |
| opencode | Native assistant file parts and completed tool attachments: inline data URLs or bounded local reads become images; remote URLs become resource links without fetching. |
| opencodev2 | Native file content and completed tool content: inline data URLs or bounded local reads become images; remote URLs become resource links without fetching. |
| amp | Native inline tool-result images are validated; remote image URLs become resource links without fetching. |
| nanocodex | None; native image tool output contributes text parts only, with binary data omitted from structured output and raw events. |

### Output refusal placement

| Sibling | Where a refusal appears |
|---|---|
| claude | Tool-call image refusal marks the tool failed with guidance; assistant image refusal emits guidance as agent text. |
| codex | the image tool call reports `failed` with the guidance as content |
| pi | the tool call reports `failed` with the guidance as content; an assistant image is replaced by the guidance as agent text |
| opencode | Tool attachments report failed tool status with guidance in the refused slot; assistant image refusal becomes agent text. |
| opencodev2 | Tool files report failed tool status with guidance in the refused slot; assistant image refusal becomes agent text. |
| amp | the tool call carries the guidance as text content in the refused slot |
| nanocodex | No native output-image projection. A stored user image that fails the output gate fails load with `nanocodex_restore_failed`. |

### Local read roots

| Sibling | Output read roots |
|---|---|
| claude | none; native output is inline or remote URL-only |
| codex | `cwd`, `WithScratchDir`, `os.TempDir()`, `$CODEX_HOME/generated_images` |
| pi | none |
| hermes | none |
| opencode | cwd, session additional directories, configured scratch parent, and `os.TempDir()` |
| opencodev2 | cwd, session additional directories, configured scratch parent, and `os.TempDir()` |
| amp | none |
| nanocodex | none |

### Replay source

| Sibling | Source and validation |
|---|---|
| claude | Native transcript entries use their own UUID for deduplication. Entries sharing an API message id retain each content fragment; images pass through the output gate. |
| codex | Replay validates native image rows and captured `config.images` through the output gate. Both generated and viewed images survive deletion of their original files; a missing or corrupt stored artifact fails load. |
| pi | replay decodes image blocks from the mirrored native rows through the output gate |
| hermes | The native export supplies user/assistant text parts, reasoning, tool calls, and tool results. Image output is not projected. |
| opencode | Final native messages and parts are reduced from sync events. Local images are captured in the same generation under `config`; missing or invalid stored artifacts fail load. |
| opencodev2 | Native exports provide projected messages with text, reasoning, tool content, and user files. Local image bytes or refusals commit with configuration; missing or invalid stored artifacts fail load. |
| amp | User and assistant messages of the mirrored export are projected with their tool calls and inline images through the output gate; compaction summaries are not projected. Historic usage comes from the configuration record. |
| nanocodex | Native input-acceptance events provide user content; response items and the suffix after the latest user item in compacted replacement history provide assistant text, visible reasoning summaries, function/custom calls, and textual tool results. Items removed by native mid-turn compaction before persistence cannot replay. Inline user images pass through the output gate. |

## Known Deviations

- **Nanocodex gateway routes:** API-key HTTPS endpoints use an application-owned
  standard Responses transport with full history replay and function tools.
  Requests identify the adapter in `User-Agent`; only the
  `https://opencode.ai/zen/go/v1` route carries the native conversation in
  `x-opencode-session`. HTTP and WebSocket endpoints without TLS are restricted
  to localhost and loopback IP addresses. Native ChatGPT authentication refuses
  custom provider endpoints.
  Gateway requests omit `prompt_cache_key`.
  Freeform patch tools are excluded on gateway routes; shell tools remain available.
  Automatic compaction uses the native model threshold. The helper requests a
  plain-text summary through an ordinary `/responses` call that repeats the
  generation request with `tool_choice:"none"`, stores it as a marked native
  `compaction` item, and sends it to the provider as a prefixed user message, so
  no provider compaction support is required. A session holding such an item
  must stay on a gateway route. Compacted context persists in the native rollout.
  Gateway generation and compaction retry connection failures, HTTP 408, 409,
  429, and 5xx responses without `x-should-retry: false`, and `response.failed`
  and error events, plus `response.incomplete` during generation, up to five
  total attempts with exponential backoff and random jitter, stopping after
  assistant or reasoning output is delivered. A failure retries unless its
  `providerCode` is terminal: a three-digit code follows the same status rule,
  and quota, authorization, model, invalid-request, context-window, image-input,
  and content-policy codes and the `max_output_tokens` and `content_filter`
  incomplete reasons are terminal. Valid `Retry-After` and `retry-after-ms`
  delays up to 60 seconds are honored with normal backoff as a minimum; longer
  delays fail without retrying early. Cancellation interrupts requests and retry
  delays.
  The native model parser bounds supported models; gateway namespaces only
  change provider wire identifiers. Additional directories, approvals,
  and elicitation are not exposed. The forced-compaction flag after provider
  context overflow is not retained across helper restarts.

- **Nanocodex tools and limits:** Code Mode is disabled on every route. Prompt
  parameters are limited to 12 MiB of encoded JSON before admission. Gateway
  responses require SSE and limit each event and accumulated response event data
  to 4 MiB. Native rollout rows containing whole histories are not protocol frames
  and are not subject to the helper's 32 MiB frame bound.

- **Nanocodex persistence:** full native-file reads preserve a complete final
  JSON row without a newline and discard only a bounded invalid unterminated
  tail; committed-byte reads require newline-terminated rows. Locked hydration
  atomically normalizes final framing before the helper opens the rollout.
  Listing skips malformed stored generations; store backend failures
  return the bare internal-failure token. Its optional internal-failure classes
  are `native_start`, `native_state`, and `lifecycle`.

- **OpenCode v2 native API:** persistence uses experimental session
  export/import endpoints. Import
  cannot replace an existing session. Structured output is unavailable in the
  native prompt schema. External, hidden, or conditional native forms are
  cancelled because the negotiated ACP form cannot represent them. Permission
  and form requests from unbound descendants are refused; their history is
  mirrored with the root. Directory changes return backpressure while native
  inbox entries remain pending.

- **Hermes native compression:** a changed durable key at mirror time poisons
  the session with `native_session_identity_drift`. Native approval and clarify
  server requests retain their JSON-RPC ids and are answered in arrival order;
  unresolved host callbacks deny or skip the pending input. Native
  `request.cancel` cancels only its matching callback.
- **Hermes terminal environment:** the native terminal bootstraps a login
  shell; operator and system startup files can reorder `PATH`. The live
  inheritance check uses a direct subprocess through `execute_code`.
- **Hermes input bridges:** sudo, secret, and terminal-buffer requests receive
  an empty value. Other desktop, vault, and setup request methods are unsupported.
- **Hermes restore:** its native HTTP import creates missing conversations and
  refuses replacement of an existing id, so a shorter native conversation fails
  restore. The gateway process starts before import; session binding waits until
  import and validation finish.
- **Hermes gateway authentication and port:** the loopback WebSocket at
  `/api/ws` authenticates only through the `?token=` query parameter; a
  request carrying the session token as a header alone is refused `403`.
  The HTTP persistence endpoints take the token as the
  `X-Hermes-Session-Token` header. `hermes serve` binds a
  concrete `--port`, with no pre-bound-socket or port-0 handshake a caller can
  read back, so the adapter selects the port by bind-then-close before launch.

- **Claude canonicalises `cwd`:** the transcript project directory derives
  from the resolved path, so `session/new`, `session/load`, and
  `session/resume` refuse a `cwd` that cannot be resolved as `unsupported`
  naming `cwd`. A `session/list` filter that cannot be resolved matches no
  session.

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
  permissions stay native, so amp makes no client calls and
  `MaxConcurrentClientCalls` is validated and never consumed.
- **Amp compaction:** the remote thread actor compacts on its own and inserts an
  `info` summary message the plugin message API does not expose. A compacted
  thread cannot be recovered after native deletion.
- **Amp usage cadence:** amp reports usage once, when the prompt settles,
  rather than after every model call. An empty report still replaces the
  figure, and no update carries a call breakdown.
- **Amp recovery cleanup:** a failed recovery deletes the private destination it
  created and never bound. This is the only native state the adapter deletes.

Off-prompt `-32603` reachability where it differs:

| Sibling | `_runtime_unavailable` | `_session_poisoned` `cause` |
|---|---|---|
| claude | never | `native_session_identity_drift` |
| codex | when a replacement app-server cannot start | never |
| pi | never | `native_session_identity_drift` |
| hermes | never | `native_session_identity_drift` |
| opencode | when a replacement server cannot start | `native_session_identity_drift` on native deletion |
| opencodev2 | when a replacement server cannot start | `native_session_identity_drift` on native deletion |
| amp | never | `native_session_identity_drift` |
| nanocodex | never | `native_session_identity_drift` |

Hermes, Nanocodex, and OpenCode re-hydrate on a lazy relaunch, so `_restore_failed` is
also reachable from `session/prompt` and `session/set_config_option` there.
