# Upstream ACP Watchlist

Outstanding adoption decisions. Current behavior belongs in the contract and
registry; resolved items belong in Git. Recheck the linked sources before
acting and replace this snapshot when they change.

Protocol status verified **2026-09-08** against the published
[RFD updates](https://agentclientprotocol.com/rfds/updates) and
[v2 proposal](https://agentclientprotocol.com/rfds/v2/overview): ACP v1
remains the family's protocol; v2 is an active draft.

| Watch | Family impact | Action trigger |
|---|---|---|
| [ACP v2](https://agentclientprotocol.com/rfds/v2/overview) | Prompt lifecycle, updates, restore, permissions, content, and capabilities across every sibling and host | Stable protocol and usable Go SDK support; scope one coordinated cutover. |
| [Go SDK](https://github.com/coder/acp-go-sdk) | Generated types and transport behavior | A release implements a needed stable surface or fixes a relevant defect; plan a family update. |
| [Go SDK content marshaling](https://github.com/coder/acp-go-sdk/blob/v0.13.5/types_gen.go#L1473) | Verified 2026-09-14: `ContentBlock.MarshalJSON` drops `_meta` and `annotations` for text and image variants. A Go SDK client therefore loses the image handoff envelope before the request reaches the adapter; embedded calls and correctly encoded ACP frames retain it. | A corrected generated marshaler; verify content metadata round trips, then scope the shared SDK pin update across every sibling and host. |
| [Go SDK capability marshaling](https://github.com/coder/acp-go-sdk/blob/v0.13.5/types_gen.go#L42) | Verified 2026-09-15: `AgentCapabilities.Auth` and `.McpCapabilities` are struct-valued `omitempty` fields and `MarshalJSON` is a plain alias marshal, so `"auth":{}` and `"mcpCapabilities":{}` are on every initialize frame and cannot be omitted. The advertised meaning is unaffected: both decode to the ACP default with every transport false. | Pointer-typed or custom-marshalled capability fields; then scope the shared SDK pin update across every sibling and host. |
| [Go SDK prompt cancellation](https://github.com/coder/acp-go-sdk/blob/v0.13.5/agent_gen.go#L411) | Verified 2026-09-15: `AgentSideConnection` keeps one cancel func per session id and calls the previous one before dispatching a new `session/prompt`, then deletes the entry when that prompt returns. A second prompt therefore cancels the handler context of the in-flight one even when the sibling refuses it with backpressure, and a following `session/cancel` finds no registered context. Every sibling drives its native turn from session-owned cancellation. The high-level client `Prompt` sends both `$/cancel_request` and `session/cancel` when its caller context is cancelled; raw request cancellation alone leaves the native turn running. | A release that refuses or ignores a concurrent `session/prompt` per session rather than superseding it; re-check whether the session-owned cancellation in each prompt handler can be simplified, then scope the shared SDK pin update. |
| [Subagents proposal](https://github.com/agentclientprotocol/agent-client-protocol/pull/855) | Delegated-agent identity and lifecycle | An accepted protocol shape or a native structured stream supplies facts the adapter can prove. |
| [Mid-turn input](https://github.com/agentclientprotocol/agent-client-protocol/pull/1261) | Host queueing and steering | A stable shape is supported by the native harness and Go SDK. |
| [Image capability proposal](https://github.com/agentclientprotocol/agent-client-protocol/issues/1559) | The family media-envelope advertisement | A stable standard represents the same enforced limits and formats. |
| [Tool call name](https://agentclientprotocol.com/rfds/tool-call-name), [compaction](https://agentclientprotocol.com/rfds/session-compaction), and [notices](https://agentclientprotocol.com/rfds/session-notices) | Typed native-event mapping | A stable shape carries an existing native fact more accurately. |

These are triggers to investigate, not claims that upstream has shipped them.
