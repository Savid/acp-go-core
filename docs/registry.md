# Family Registry

Per-sibling facts that differ between siblings. Uniform rules live in the
contract and are not restated here. Update this page whenever a sibling
changes its public surface.

## Identity Summary

| Repo | Package | Vendor key | Extension prefix | Store format | Strategy |
|---|---|---|---|---|---|
| `acp-go-pi` | `piacp` | `pi` | `_pi/` | `pi-session-jsonl-v1` | session runtime |

All import paths are `github.com/savid/acp-go-<vendor>`; all binaries are
`acp-go-<vendor>`.

## Native Surfaces and Process Models

| Sibling | Native surface | Process lifetime |
|---|---|---|
| pi | `pi --mode rpc` JSONL | One live process per session. A dead process is relaunched against the same native session file on the next prompt. |

Teardown signals the process group and waits for the root process on every
sibling.

## Native Version Probes

| Sibling | Minimum native version |
|---|---|
| pi | `0.80.6` |

## Session Stores

| Sibling | Kind | Carrier record |
|---|---|---|
| pi | Append-only session JSONL rows plus a `config` subpath | Each commit appends the rows pi wrote since the last commit, then one session record naming pi's session file, the accepted session environment, and the ordered paths. |

Adapter-authored records use closed current schemas and reject unknown or
duplicate fields, malformed input, regressed counters, and mismatched
identity.

### Mirror-Commit Ordering

| Sibling | Settlement and close ordering |
|---|---|
| pi | Settlement reads pi's session file after `agent_settled` and commits the new rows before the terminal idle and the response. Close aborts any turn, stops the process, commits, then fences. |

## Turn Failure and Turn Timeout

| Sibling | Causes | Native mapping and disclosed detail |
|---|---|---|
| pi | `process_exit`, `transport`, `provider`, `timeout`, `extension` | Command rejection preserves native text; process death reports status plus the last stderr line. Wrapper extension failure uses fixed `a pi extension failed`. |

Malformed or empty records are skipped or produce a typed failure.

## Raw Events

Pi emits every admitted record on its permanent channel and logs and
continues on emit failure.

## Lifecycle Extension

| Sibling | `updatesOutsidePrompt` | `activityKinds` |
|---|---|---|
| pi | yes | `[]` |

A permanent-channel sibling delivers between-prompt native work as ordinary
updates under agent-origin turns.

### What each permanent channel delivers

| Sibling | Open / delivery / settle |
|---|---|
| pi | An `agent_start` with no prompt in flight opens an agent-origin turn on the event pump; `agent_settled` drives usage → mirror → idle. |

### What each channel tolerates

| Sibling | Tolerance and remaining fences |
|---|---|
| pi | Queue reports, compaction and retry pairs, custom messages, and unmodelled types are session-scoped and open nothing. A wrapper extension error fails the cycle with cause `extension`; an operator extension error is pi's own. |

### Opening publication

| Sibling | Order after the establishing response |
|---|---|
| pi | catalog, then snapshot; non-stdio connections publish inline |

### Close and delete boundaries

| Sibling | `session/close` | `session/delete` |
|---|---|---|
| pi | Cancels the turn and its dialogs, signals and waits the process, commits native rows and the session record, terminalizes an open agent-origin cycle, and fences. | Tombstones first, then the same close. |

## Usage `size`

- **pi:** `get_session_stats.contextUsage.contextWindow`, else the selected
  model's catalog `contextWindow`, else `0`.

## Delegated Agents

- **Not applicable (pi).** No subscribed provenance channel; nothing is
  synthesized.

## Vendor Session Options

| Sibling | Extra fields | Structured output |
|---|---|---|
| pi | `thinkingLevel`, `permission` (`ask`\|`allow`), `autoRetry` | Not advertised; `outputSchema` fails at session start. |

Session `env` and `extraPathDirs` reach the native boundary as the session's
pi process environment.

## Session Config Options

| Sibling | Advertised IDs | Value authority and read-back |
|---|---|---|
| pi | `model`, `thought_level` | Model checks `<provider>/<id>`; `get_state` reports the adopted thought level. Menu `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. |

### How each model catalog is built

| Sibling | Source | Metadata |
|---|---|---|
| pi | `get_available_models`, pi's own registry filtered by its configured providers, snapshotted once at native start | `modelId`, plus `contextWindow` and `maxOutputTokens` when the row carries them |

Pi refuses a host-listed id with no provider prefix at construction.

## Vendor Process Options

| Sibling | Options | `WithHome` variable |
|---|---|---|
| pi | none | `PI_CODING_AGENT_DIR` |

## Slash Commands

| Sibling | Discovery | Advertises | Invoke-denied (ACP alternative) |
|---|---|---|---|
| pi | `get_commands` at session setup, re-fetched on relaunch | Native list minus names the shared sanitizer rejects | none |

## Image Input and Output

### Advertised media envelope

| Sibling | `maxBytes` | `maxPromptBytes` | `maxDimension` | `documentFormats` |
|---|---:|---:|---:|---|
| pi | 6,291,456 | 6,291,456 | 0 | `[]` |

### Handoff native form

| Sibling | Native form for a validated image |
|---|---|
| pi | inline base64 |

Pi retains one root handle per prompt.

### Non-raster blobs

| Sibling | Non-`image/` blob | Post-gate disposition |
|---|---|---|
| pi | refuses every non-`image/` MIME before decode | nothing decoded |

Text resources share the aggregate: Pi charges raw text before XML expansion.

### Selected-model gate authority

| Sibling | Source | `unsupported_by_model` |
|---|---|---|
| pi | `get_available_models` per-model `input` list | yes |

An absent model, absent field, or empty list resolves to `unknown`. A failed
catalog call fails establishment.

### Output surfaces

| Sibling | Surfaces |
|---|---|
| pi | tool-call content images via the wrapper-owned bridge; assistant image blocks one per chunk; inline base64 only |

### Output refusal placement

| Sibling | Where a refusal appears |
|---|---|
| pi | the tool call reports `failed` with the guidance as content; an assistant image is replaced by the guidance as agent text |

### Local read roots

| Sibling | Output read roots |
|---|---|
| pi | none |

### Replay source

| Sibling | Source and validation |
|---|---|
| pi | replay decodes image blocks from the mirrored native rows through the output gate; nothing is swept |

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

- **Pi snapshots its catalog once** at native start; a mid-session credential
  change does not move the menu.
- **Pi identity:** ACP ID is the native session UUID.
- **Pi extensions:** the wrapper-owned bridge and PATH extensions load with
  `-e` from a content-addressed scratch directory; pi's own discovery of the
  operator's extensions, skills, and prompt templates stays on.
- **Pi retry and settings:** native retry defaults off; `autoRetry` opts in
  per session. Seed files are written into pi's config root under a manifest;
  a seeded `settings.json` must parse. Turn process-exit detail retains one
  last stderr line.

Off-prompt `-32603` reachability:

| Sibling | `_invalid_options` `field` | `_restore_failed` | `_session_poisoned` `cause` | `_internal_failure` `class` |
|---|---|---|---|---|
| pi | the refused option name | load, resume | `native_session_identity_drift` | `native_start`; bare token on close, delete, and list failures |
