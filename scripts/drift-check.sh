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

forbidden_names=('acp-go' 'coordination repo')
account_usage_rows=$(awk '/^## Account Usage$/{f=1;next} /^## /{f=0} f' "$repo_root/docs/registry.md")

rg -q "^module $core_module\$" "$repo_root/go.mod" || fail "acp-go-core: module path differs from README pin"
rg -q "^go $go_version\$" "$repo_root/go.mod" || fail "acp-go-core: go directive differs from README pin"
rg -q '^toolchain ' "$repo_root/go.mod" && fail "acp-go-core: toolchain line present"
rg -q "^\s*$sdk_module $sdk_version\$" "$repo_root/go.mod" || fail "acp-go-core: ACP SDK pin differs from README"
[[ -f "$repo_root/lifecycle/testdata/fixtures/manifest.json" ]] || fail "acp-go-core: lifecycle fixture battery missing"

check_sibling() {
  local vendor=$1 repo=$2 name="acp-go-$1" f
  for f in AGENTS.md CLAUDE.md LICENSE Makefile README.md doc.go example_test.go contract_test.go helpers_test.go agent.go options.go request_builders.go session.go session_meta.go session_prompt.go go.mod .golangci.yml .github/workflows/check.yml "cmd/$name/main.go" "cmd/$name/otel.go" "cmd/$name/signals_unix.go" "cmd/$name/version.go" integration/doc.go integration/binary_test.go integration/helpers_test.go; do
    [[ -e "$repo/$f" ]] || fail "$name: missing $f"
  done
  if rg -q --type go -g '!*_test.go' '\.scratchDir\(' "$repo"; then
    [[ -f "$repo/scratch.go" ]] || fail "$name: scratch allocator has no scratch.go owner"
  fi
  [[ -f "$repo/fake${vendor}_test.go" ]] || fail "$name: fake${vendor}_test.go missing"
  for f in "$repo"/*.go; do
    case $(basename "$f") in
      *_test.go|agent.go|options.go|request_builders.go|session.go|session_meta.go|session_prompt.go|doc.go|scratch.go) ;;
      agent_*.go|session_*.go|image_*.go|"$vendor"_*.go) ;;
      *) fail "$name: root file $(basename "$f") is not a permitted domain split" ;;
    esac
  done
  rg -q "^module github.com/savid/$name\$" "$repo/go.mod" || fail "$name: module path is not github.com/savid/$name"
  rg -q "^go $go_version\$" "$repo/go.mod" || fail "$name: go directive differs from README pin"
  rg -q '^toolchain ' "$repo/go.mod" && fail "$name: toolchain line present"
  rg -q "^\s*$sdk_module $sdk_version\$" "$repo/go.mod" || fail "$name: ACP SDK pin differs from README"
  rg -q "^\s*$core_module " "$repo/go.mod" || fail "$name: core module dependency missing"
  if (( core_released == 0 )); then
    rg -q "^replace $core_module => \.\./acp-go-core\$" "$repo/go.mod" || fail "$name: unreleased core module is not resolved through replace => ../acp-go-core"
  fi
  rg -q "^package ${vendor}acp\$" "$repo/agent.go" || fail "$name: root package is not ${vendor}acp"
  rg -q "SessionStoreFormat = \"$vendor-[a-z-]+-v1\"" "$repo"/*.go || fail "$name: SessionStoreFormat is not <vendor>-<kind>-v1"
  rg -q "RawEventMethod = \"_$vendor/rawEvent\"" "$repo"/*.go || fail "$name: RawEventMethod is not canonical"
  if rg -q --type go -g '!*_test.go' 'AccountUsageMethod += ' "$repo"; then
    rg -q --type go -g '!*_test.go' "AccountUsageMethod += \"_$vendor/accountUsage\"" "$repo" || fail "$name: AccountUsageMethod is not canonical"
    rg -q --type go -g '!*_test.go' 'wire\.DecodeAccountUsageRequest\(' "$repo" || fail "$name: account usage request is not decoded through core"
    rg -q --type go -g '!*_test.go' 'wire\.AccountUsageCapabilityKey' "$repo" || fail "$name: account usage is not advertised through core's key"
    assembling=$(rg -l --type go -g '!*_test.go' 'wire\.AccountUsageResponse\{[^}]' "$repo" || true)
    [[ -n "$assembling" ]] || fail "$name: no non-test file assembles a wire.AccountUsageResponse"
    while IFS= read -r f; do
      [[ -z "$f" ]] || rg -q '\.Validate\(\)' "$f" || fail "$name: $(basename "$f") assembles an account-usage response without Validate"
    done <<< "$assembling"
    printf '%s\n' "$account_usage_rows" | rg -q "^\| $vendor \| \`(session|agent)\` \|" || fail "$name: registry Account Usage row does not record a scope"
  else
    rg -q --type go -g '!*_test.go' -e 'accountUsage' -e 'AccountUsage' "$repo" && fail "$name: account usage code without AccountUsageMethod"
    printf '%s\n' "$account_usage_rows" | rg -q "^\| $vendor \| \`none\` \|" || fail "$name: registry Account Usage row is not none"
  fi
  rg -q 'exporters\.Configure\(' "$repo/cmd/$name/otel.go" || fail "$name: telemetry bootstrap is not core's"
  resolving=$(rg -l --type go -g '!*_test.go' 'process\.ResolveExecutable\(cmp\.Or\(a\.options\.ExecutablePath, vendor\), base\)' "$repo" || true)
  [[ -n "$resolving" ]] || fail "$name: executable resolution is not core's on the vendor default"
  while IFS= read -r f; do
    [[ -z "$f" ]] || rg -q '\.Base\(\)' "$f" || fail "$name: $(basename "$f") resolves the executable off the base environment"
  done <<< "$resolving"
  rg -q 'wire\.SessionRequestOption' "$repo/request_builders.go" || fail "$name: request builders are not core's"
  rg -q --type go -g '!*_test.go' 'StderrLastLine\(' "$repo" || fail "$name: process death does not report core's stderr tail"
  rg -q 'InputHandoffRoot +string' "$repo/options.go" && rg -q 'func WithInputHandoffRoot\(dir string\) Option' "$repo/options.go" || fail "$name: WithInputHandoffRoot surface missing"
  rg -q 'ConfiguredModels +\[\]string' "$repo/options.go" && rg -q 'func WithConfiguredModels\(ids \[\]string\) Option' "$repo/options.go" || fail "$name: WithConfiguredModels surface missing"
  for f in 'wire.MediaEnvelopeKey|acp-go.dev/mediaEnvelope' 'wire.HandoffKey|acp-go.dev/handoff' 'wire.LifecycleKey|acp-go.dev/lifecycle'; do
    rg -q --type go -g '!*_test.go' -e "$f" "$repo" || fail "$name: reserved literal ${f#*|} unused in production Go"
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
  rg -A1 '^audit: ' "$repo/Makefile" | rg -q '^\tgo mod verify$' || fail "$name: audit recipe does not end with go mod verify"
  rg -q 'go test -race -shuffle=on -timeout=\$\(GO_TEST_TIMEOUT\) \./\.\.\.' "$repo/Makefile" || fail "$name: test recipe is not canonical"
  rg -q 'go fix -diff \./\.\.\.' "$repo/Makefile" || fail "$name: modernize-check recipe is not canonical"
  rg -q '@latest' "$repo/Makefile" && fail "$name: @latest in Makefile"
  rg -q '^ *docs/' "$repo/.gitignore" && fail "$name: docs site remnants in .gitignore"
  [[ -d "$repo/docs" ]] && fail "$name: docs site directory present"
  [[ -e "$repo/docs.json" ]] && fail "$name: docs.json present"
  [[ -d "$repo/testdata/lifecycle" || -d "$repo/fixtures/lifecycle" ]] && fail "$name: sibling carries a lifecycle fixture copy"

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
import subprocess
import sys

core, family = map(pathlib.Path, sys.argv[1:])
vendors = re.findall(r"^\| \[acp-go-([a-z]+)\]", (core / "README.md").read_text(), re.M)
repos = [core] + [family / f"acp-go-{vendor}" for vendor in vendors if (family / f"acp-go-{vendor}").is_dir()]
failed = False

def fail(message):
    global failed
    print(f"FAIL {message}")
    failed = True

for repo in repos[1:]:
    vendor = repo.name.removeprefix("acp-go-")
    readme = (repo / "README.md").read_text()
    if (repo / "CLAUDE.md").read_text().strip() != "# CLAUDE.md\n\n@AGENTS.md":
        fail(f"{repo.name}: CLAUDE.md must contain only its heading and AGENTS.md import")
    headings = re.findall(r"^## (.+)$", (repo / "AGENTS.md").read_text(), re.M)
    if headings != ["Purpose", "Project Map", "Commands", "Coding Rules", "Verification", "Boundaries"]:
        fail(f"{repo.name}: AGENTS.md sections are not in the required order")
    for option in re.findall(r"^func (With\w+)\(", (repo / "options.go").read_text(), re.M):
        if option not in readme:
            fail(f"{repo.name}: README omits process option {option}")
    for token in ("Serve", "go install"):
        if token not in readme:
            fail(f"{repo.name}: README omits {token}")
    formats = re.findall(r'SessionStoreFormat = "([^"]+)"', "\n".join(p.read_text() for p in repo.glob("*.go") if not p.name.endswith("_test.go")))
    if len(formats) != 1 or formats[0] not in readme:
        fail(f"{repo.name}: README omits its store format")
    if "Serve" not in (repo / "doc.go").read_text():
        fail(f"{repo.name}: doc.go omits Serve embedding")
    main = (repo / f"cmd/{repo.name}/main.go").read_text()
    common_flags = {"path", "home", "scratch-dir", "model", "seed-file", "debug", "version"}
    for flag in re.findall(r'flags\.(?:String|Bool|Int|Duration|Var)\([^\n]*?"([a-z][a-z-]*)"', main):
        if flag not in common_flags and not flag.startswith(vendor + "-"):
            fail(f"{repo.name}: flag -{flag} lacks vendor prefix")
        if "-" + flag not in readme:
            fail(f"{repo.name}: README omits flag -{flag}")

    extras = {"example_test.go", "contract_test.go", "helpers_test.go"}
    for test in repo.rglob("*_test.go"):
        relative = test.relative_to(repo)
        if test.name in extras or test.name == f"fake{vendor}_test.go" or relative == pathlib.Path("integration/binary_test.go"):
            continue
        if not test.with_name(test.name.replace("_test.go", ".go")).is_file():
            fail(f"{repo.name}: {relative} has no production stem")
    for source in repo.rglob("*.go"):
        if source.name.endswith("_test.go") or source.name == "scratch.go":
            continue
        if re.search(r'os\.(?:MkdirTemp|CreateTemp)\(\s*""\s*,', source.read_text()):
            fail(f"{repo.name}: {source.relative_to(repo)} allocates system scratch outside scratch.go")
    tracked = subprocess.check_output(["git", "ls-files", "-z"], cwd=repo, text=True).split("\0")
    for path in tracked:
        if path.startswith(".") and path.split("/")[0] not in {".github", ".gitignore", ".golangci.yml"}:
            fail(f"{repo.name}: unsupported tracked dot file {path}")

    workflows = sorted((repo / ".github/workflows").glob("*"))
    if [path.name for path in workflows] != ["check.yml"]:
        fail(f"{repo.name}: check.yml must be the only workflow")
    workflow = (repo / ".github/workflows/check.yml").read_text()
    for pattern, description in (
        (r"^  push:\s*$", "push trigger"),
        (r"^  pull_request:\s*$", "pull request trigger"),
        (r"^  contents: read$", "read-only contents permission"),
        (r"^  group:.*github\.ref", "concurrency grouped by ref"),
        (r"^  cancel-in-progress: true$", "concurrency cancellation"),
        (r"^        os: \[ubuntu-latest, macos-latest\]$", "Linux and macOS matrix"),
        (r"^    runs-on: \$\{\{ matrix\.os \}\}$", "matrix runner"),
        (r"^      - run: make audit$", "audit step"),
    ):
        if not re.search(pattern, workflow, re.M):
            fail(f"{repo.name}: workflow missing {description}")
    for action in re.findall(r"uses: ([^\s]+)", workflow):
        if not re.fullmatch(r"[^@]+@[0-9a-f]{40}", action):
            fail(f"{repo.name}: action {action} is not pinned to a full commit SHA")

    makefile = (repo / "Makefile").read_text()
    if len(re.findall(r"^GO_TEST_TIMEOUT \?= 40m$", makefile, re.M)) != 1:
        fail(f"{repo.name}: GO_TEST_TIMEOUT must be declared exactly once")
    for target in ("build", "test-integration-smoke", "test-integration-live", "clean", "help"):
        if not re.search(rf"^{target}:", makefile, re.M):
            fail(f"{repo.name}: missing {target} target")
    for tier, tokens in (("smoke", "0"), ("live", "1")):
        match = re.search(rf"^test-integration-{tier}:[^\n]*\n((?:\t[^\n]*\n)+)", makefile, re.M)
        recipe = match[1] if match else ""
        for token in ("-tags=integration", f"ACP_GO_{vendor.upper()}_RUN_INTEGRATION=1", f"ACP_GO_{vendor.upper()}_RUN_LIVE_TOKENS={tokens}"):
            if token not in recipe:
                fail(f"{repo.name}: integration {tier} recipe omits {token}")

versions = {}
for repo in repos:
    for line in (repo / "go.mod").read_text().splitlines():
        match = re.fullmatch(r"\s+([^ ]+) (v[^ ]+)(?: // indirect)?", line)
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
