# acp-go-core

Shared code for the `acp-go-*` family: the behavior every sibling has in
common, held once.

| Package | Contents |
|---|---|
| `acpcore` | `SessionStore` and its types, `InMemorySessionStore` |
| `acpcore/storetest` | The store contract battery a host store runs against itself |
| `acpcore/lifecycle` | The `acp-go.dev/lifecycle` extension: capability, envelope, events, reducer, emitter, and the embedded fixture battery |
| `acpcore/process` | Environment merge, executable resolution, child launch with its own process group and pipes, shutdown, and the epoch fence |
| `acpcore/wire` | Uniform error shapes, raw-event framing and sequencing, and the reserved `_meta` literals |
| `acpcore/image` | Decoded-byte limits, the media envelope, prompt image validation in both forms, output normalization, and the artifact store |

## Use

```go
import (
    acpcore "github.com/savid/acp-go-core"
    "github.com/savid/acp-go-core/lifecycle"
    "github.com/savid/acp-go-core/process"
)
```

A sibling imports what it needs and re-exports nothing. A host imports the
root package for the store types it implements.

## Lifecycle

The lifecycle extension carries, in ACP v1 `_meta`, the shape of the ACP v2
[prompt lifecycle RFD](https://agentclientprotocol.com/rfds/v2/prompt): a
prompt is answered on acceptance and turns, activities, and actions are
reported through ordered session updates. `lifecycle.Fixtures` embeds the
canonical reducer battery; `go test ./lifecycle` runs every vector.

## Process model

A harness is a plain child. `process.Environment` merges the sibling's own
environment, the host overlay, the session overlay, and the keys the sibling
owns, later wins, and composes `PATH` from the session's extra directories.
`process.Start` launches with three dedicated pipes in its own process group;
`Shutdown` signals the group and waits for the root. Nothing here isolates
anything; that belongs to whoever executes the sibling.

## Checks

```sh
make test
make audit
```
