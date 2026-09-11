# Getting Started

## Source Order

Read these before implementing:

1. The [ACP v1 protocol](https://agentclientprotocol.com/protocol/v1/overview)
   and its [schema](https://agentclientprotocol.com/protocol/v1/schema).
2. The [family protocol posture](../README.md#protocol-posture) and
   [upstream watch](../tracking/upstream-acp.md). Verify a proposal's status in
   the [RFD feed](https://agentclientprotocol.com/rfds/updates) before relying
   on it.
3. The [upstream schema repository](https://github.com/agentclientprotocol/agent-client-protocol)
   and [Go SDK](https://github.com/coder/acp-go-sdk) at the
   [shared pins](../README.md#shared-pins).
4. This module's packages and the relevant sibling with its
   [registry entry](registry.md).

ACP details MUST be verified against the live docs and pinned schema.

## Researching the Harness

Before writing package code, prove the harness has a programmatic surface you
can drive directly, using the installed binary, its official docs, and small
throwaway probes in a temporary home. Answer:

- How do you start a session, send input, and receive streamed text, tool
  calls, usage, and terminal state?
- How do you interrupt a running turn?
- How do you request and answer tool permissions, and ask non-permission
  questions?
- How do you list, resume, close, and delete native sessions?
- How do you supply model, mode, reasoning, sandbox, approval, env, and
  structured-output settings?
- How do you reconstruct durable state after deleting native state?
- What native IDs does it generate, and can they change on restore?
- What version or endpoint probes prove the required surface is present?

## Picking the Native Surface

Pick the narrowest direct surface that can implement the contract:

| Sibling | Native surface | Why |
|---|---|---|
| Codex | `codex app-server --listen stdio://` | Native app-server protocol with thread, turn, model, and event APIs. |
| Pi | `pi --mode rpc` JSONL over stdin/stdout | One RPC process per session with explicit native state and environment control. |

The selected surface MUST expose the lifecycle, permissions, restore, and ID
mapping this contract requires. A sibling MUST document why its native surface
meets those requirements.

## Claim Identity Constants

Claim the [identity constants](00-overview.md#identity-constants) before
implementation and use the vendor key as the owned `_meta` namespace. Prove the
store format restores after native state deletion **before** writing production
restore code ([04-sessions-and-store.md](04-sessions-and-store.md#proving-the-format)).
