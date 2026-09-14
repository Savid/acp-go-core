#!/usr/bin/env bash
# Verifies the structural rules in docs/07 against local sibling checkouts.
set -euo pipefail
export LC_ALL=C

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
family_root=${ACP_GO_FAMILY_ROOT:-$(dirname "$repo_root")}
status=0

fail() { printf 'FAIL %s\n' "$*"; status=1; }
skip() { printf 'SKIP %s\n' "$*"; }
pass() { printf 'PASS %s\n' "$*"; }

pin() { rg -o --no-line-number "^\| $1 \| \`([^\`]+)\`" -r '$1' "$repo_root/README.md" | head -1; }
sdk_pin=$(pin 'ACP SDK'); go_pin=$(pin 'Go directive'); core_pin=$(pin 'Core module')
core_released=1; rg -q '^\| Core module \|.*unreleased' "$repo_root/README.md" && core_released=0
[[ -n "$sdk_pin" && -n "$go_pin" ]] || { fail "README shared pins are missing"; exit 1; }
sdk_module=${sdk_pin%@*}; sdk_version=${sdk_pin#*@}; go_version=${go_pin#go }
core_module=${core_pin%% *}

siblings=()
while IFS= read -r vendor; do
  siblings+=("$vendor")
done < <(rg -o --no-line-number '^\| \[acp-go-([a-z]+)\]' -r '$1' "$repo_root/README.md")
(( ${#siblings[@]} > 0 )) || { fail "README family table lists no sibling"; exit 1; }

forbidden_literals=('HostAuthority' 'acp-go.dev/route' '/rateLimits' '/auth/' '/session/fork' 'McpServerStdio{' 'WithSessionMCPServers' 'WithAmbientEnvironment' 'ProviderAuth')
forbidden_names=('acp-go' 'coordination repo')

rg -q "^module $core_module\$" "$repo_root/go.mod" || fail "acp-go-core: module path differs from README pin"
rg -q "^go $go_version\$" "$repo_root/go.mod" || fail "acp-go-core: go directive differs from README pin"
rg -q '^toolchain ' "$repo_root/go.mod" && fail "acp-go-core: toolchain line present"
rg -q "^\s*$sdk_module $sdk_version\$" "$repo_root/go.mod" || fail "acp-go-core: ACP SDK pin differs from README"
[[ -f "$repo_root/lifecycle/testdata/fixtures/manifest.json" ]] || fail "acp-go-core: lifecycle fixture battery missing"

check_sibling() {
  local vendor=$1 repo=$2 name="acp-go-$1" f
  for f in AGENTS.md CLAUDE.md LICENSE Makefile README.md doc.go example_test.go contract_test.go helpers_test.go agent.go options.go request_builders.go session.go session_meta.go session_prompt.go scratch.go go.mod .golangci.yml .github/workflows/check.yml "cmd/$name/main.go" "cmd/$name/otel.go" "cmd/$name/signals_unix.go" "cmd/$name/version.go" integration/doc.go integration/binary_test.go integration/helpers_test.go; do
    [[ -e "$repo/$f" ]] || fail "$name: missing $f"
  done
  for f in examples/minimal-client examples/interactive-chat examples/resume-from-file; do
    [[ -d "$repo/$f" ]] || fail "$name: missing $f"
  done
  compgen -G "$repo/fake*_test.go" >/dev/null || fail "$name: scripted fake native binary test file missing"
  rg -q "^module github.com/savid/$name\$" "$repo/go.mod" || fail "$name: module path is not github.com/savid/$name"
  rg -q "^go $go_version\$" "$repo/go.mod" || fail "$name: go directive differs from README pin"
  rg -q '^toolchain ' "$repo/go.mod" && fail "$name: toolchain line present"
  rg -q "^\s*$sdk_module $sdk_version\$" "$repo/go.mod" || fail "$name: ACP SDK pin differs from README"
  if (( core_released == 0 )); then skip "$name: core module pin not yet released"; else
    rg -q "^\s*$core_module " "$repo/go.mod" || fail "$name: core module dependency missing"; fi
  rg -q "^package ${vendor}acp\$" "$repo/agent.go" || fail "$name: root package is not ${vendor}acp"
  rg -q "SessionStoreFormat = \"$vendor-[a-z-]+-v1\"" "$repo"/*.go || fail "$name: SessionStoreFormat is not <vendor>-<kind>-v1"
  rg -q "RawEventMethod = \"_$vendor/rawEvent\"" "$repo"/*.go || fail "$name: RawEventMethod is not canonical"
  rg -q 'InputHandoffRoot +string' "$repo/options.go" && rg -q 'func WithInputHandoffRoot\(dir string\) Option' "$repo/options.go" || fail "$name: WithInputHandoffRoot surface missing"
  rg -q 'ConfiguredModels +\[\]string' "$repo/options.go" && rg -q 'func WithConfiguredModels\(ids \[\]string\) Option' "$repo/options.go" || fail "$name: WithConfiguredModels surface missing"
  for f in 'wire.MediaEnvelopeKey|acp-go.dev/mediaEnvelope' 'wire.HandoffKey|acp-go.dev/handoff' 'wire.LifecycleKey|acp-go.dev/lifecycle'; do
    rg -q --type go -g '!*_test.go' -e "$f" "$repo" || fail "$name: reserved literal ${f#*|} unused in production Go"
  done
  for f in "${forbidden_literals[@]}"; do
    if rg -q --type go -F "$f" "$repo"; then fail "$name: forbidden literal $f present"; fi
  done
  for f in README.md AGENTS.md doc.go; do
    for n in "${forbidden_names[@]}"; do
      rg -q -F "$n" "$repo/$f" && rg -F "$n" "$repo/$f" | rg -qv "$name|acp-go-core" && fail "$name: $f names $n"
    done
    for other in "${siblings[@]}"; do
      [[ $other == "$vendor" ]] && continue
      rg -q -F "acp-go-$other" "$repo/$f" && fail "$name: $f names acp-go-$other"
    done
  done
  for f in -path -home -scratch-dir -model -seed-file -debug -version; do
    rg -q -- "\"${f#-}\"" "$repo/cmd/$name/main.go" || fail "$name: flag $f missing"
  done
  rg -q 'var buildVersion = "dev"' "$repo/cmd/$name/version.go" || fail "$name: buildVersion default is not dev"
  rg -q '^GO_TEST_TIMEOUT \?= 40m$' "$repo/Makefile" || fail "$name: GO_TEST_TIMEOUT not declared once as 40m"
  rg -q '^audit: fmt-check lint build coverage-check tidy vuln modernize-check$' "$repo/Makefile" || fail "$name: audit prerequisites are not canonical"
  rg -q 'go test -race -shuffle=on -timeout=\$\(GO_TEST_TIMEOUT\) \./\.\.\.' "$repo/Makefile" || fail "$name: test recipe is not canonical"
  rg -q 'go fix -diff \./\.\.\.' "$repo/Makefile" || fail "$name: modernize-check recipe is not canonical"
  rg -q '@latest' "$repo/Makefile" && fail "$name: @latest in Makefile"
  rg -q '^ *docs/' "$repo/.gitignore" && fail "$name: docs site remnants in .gitignore"
  [[ -d "$repo/docs" ]] && fail "$name: docs site directory present"
  [[ -e "$repo/docs.json" ]] && fail "$name: docs.json present"
  [[ -d "$repo/testdata/lifecycle" || -d "$repo/fixtures/lifecycle" ]] && fail "$name: sibling carries a lifecycle fixture copy"
  [[ -f "$repo/host_authority.go" ]] && fail "$name: host_authority.go present"
  [[ -f "$repo/auth.go" ]] && fail "$name: auth.go present"

  return 0
}

present=()
for vendor in "${siblings[@]}"; do
  repo="$family_root/acp-go-$vendor"
  if [[ ! -d $repo ]]; then skip "acp-go-$vendor: checkout not found"; continue; fi
  present+=("$vendor"); check_sibling "$vendor" "$repo"
done

if (( ${#present[@]} > 1 )); then
  first="$family_root/acp-go-${present[0]}"
  for vendor in "${present[@]:1}"; do
    repo="$family_root/acp-go-$vendor"
    for f in LICENSE .gitignore .golangci.yml; do
      [[ -f "$first/$f" && -f "$repo/$f" ]] || continue
      cmp -s "$first/$f" "$repo/$f" || fail "acp-go-$vendor: $f differs from acp-go-${present[0]}"
    done
  done
else
  skip "shared-file comparison needs at least two checkouts"
fi

if ! python3 - "$repo_root" "$family_root" <<'PY_CHECK'
import pathlib
import re
import sys

core, family = map(pathlib.Path, sys.argv[1:])
vendors = re.findall(r"^\| \[acp-go-([a-z]+)\]", (core / "README.md").read_text(), re.M)
repos = [core] + [family / f"acp-go-{vendor}" for vendor in vendors if (family / f"acp-go-{vendor}").is_dir()]
failed = False

def fail(message):
    global failed
    print(f"FAIL {message}")
    failed = True

versions = {}
for repo in repos:
    for line in (repo / "go.mod").read_text().splitlines():
        match = re.fullmatch(r"\s+([^ ]+) (v[^ ]+)", line)
        if match:
            module, version = match.groups()
            if module in versions and versions[module][1] != version:
                fail(f"{repo.name}: {module} {version} differs from {versions[module]}")
            versions[module] = (repo.name, version)

recipes = {}
for repo in repos:
    makefile = (repo / "Makefile").read_text()
    for target in ("test", "coverage-check", "lint", "fmt", "fmt-check", "tidy", "vuln", "modernize-check"):
        match = re.search(rf"^{target}:[^\n]*\n((?:\t[^\n]*\n)+)", makefile, re.M)
        if not match:
            fail(f"{repo.name}: missing {target} recipe")
            continue
        recipe = match[1]
        if target in recipes and recipes[target] != recipe:
            fail(f"{repo.name}: {target} recipe differs from core")
        recipes.setdefault(target, recipe)

sys.exit(1 if failed else 0)
PY_CHECK
then
  status=1
fi

(( status == 0 )) && pass "drift-check"
exit $status
