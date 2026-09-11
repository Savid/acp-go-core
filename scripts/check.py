#!/usr/bin/env python3
"""Validate this repository without sibling checkouts or network."""

import ast
import collections
import json
import pathlib
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

import yaml



class UniqueLoader(yaml.SafeLoader):
    pass


def yaml_object(loader, node):
    return unique_object(loader.construct_pairs(node))


UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, yaml_object)


def prose_lines(source):
    """Retain source line numbers while excluding fenced code blocks."""
    fence = None
    for number, line in enumerate(source.splitlines(), 1):
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            value = marker[1]
            if fence is None:
                fence = value
            elif value[0] == fence[0] and len(value) >= len(fence):
                fence = None
            yield number, ""
        else:
            yield number, line if fence is None else ""


def anchors(source):
    result = set()
    counts = collections.Counter()
    for _, line in prose_lines(source):
        heading = re.match(r"^#{1,6}\s+(.+?)(?:\s+#+)?$", line)
        if heading:
            slug = "".join(
                c for c in heading[1].lower() if c.isalnum() or c in " _-"
            ).replace(" ", "-")
            count = counts[slug]
            counts[slug] += 1
            result.add(slug + (f"-{count}" if count else ""))
        result.update(re.findall(r'\b(?:id|name)=[\'"]([^\'"]+)', line))
    return result


def check_links(root, pages):
    root = root.resolve()
    errors = []
    cached = {}
    for page in pages:
        page = page.resolve()
        # Joining lines allows wrapped Markdown link labels and destinations.
        source = "\n".join(line for _, line in prose_lines(page.read_text()))
        links = list(re.finditer(r"\]\(\s*(<[^>]+>|[^\s)]+)(?:\s+\"[^\"]*\")?\s*\)", source))
        links += list(re.finditer(r"(?m)^\s{0,3}\[[^\]]+\]:\s*(<[^>]+>|\S+)", source))
        for match in links:
            destination = match[1].strip("<>")
            parsed = urlsplit(destination)
            if parsed.scheme or parsed.netloc:
                continue
            target = (page.parent / unquote(parsed.path)).resolve() if parsed.path else page
            if not target.is_relative_to(root):
                continue  # Optional sibling checkouts are outside this repo's checks.
            number = source.count("\n", 0, match.start()) + 1
            location = f"{page.relative_to(root)}:{number}"
            if not target.exists():
                errors.append(f"{location}: missing link target {destination}")
            elif parsed.fragment and target.suffix == ".md":
                if target not in cached:
                    cached[target] = anchors(target.read_text())
                if unquote(parsed.fragment) not in cached[target]:
                    errors.append(f"{location}: missing anchor {destination}")
    return errors


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate key: {key}")
        result[key] = value
    return result


def read_json(path):
    return json.loads(path.read_text(), object_pairs_hook=unique_object)


def check_fixtures(root):
    errors = []
    folder = root / "lifecycle/testdata/fixtures"
    manifest = read_json(folder / "manifest.json")
    names = [entry["file"] for entry in manifest["fixtures"]]
    if len(names) != len(set(names)):
        errors.append("lifecycle manifest repeats a fixture")
    if set(names) | {"manifest.json"} != {p.name for p in folder.glob("*.json")}:
        errors.append("lifecycle JSON inventory differs from its manifest")
    members = set(manifest["expectShape"]["stateShape"]["members"])
    section = (root / "docs/03-wire-contract.md").read_text().split(
        "### Sequencing and Fail-Closed Rules", 1
    )[1].split("\n## ", 1)[0]
    vocabulary = set(re.findall(r"^\| `([a-z_]+)` \|", section, re.MULTILINE))
    covered = set()
    if not vocabulary:
        errors.append("wire contract has no lifecycle violation vocabulary")
    for name in names:
        if pathlib.Path(name).name != name or not name.endswith(".json"):
            errors.append(f"invalid lifecycle fixture filename: {name}")
            continue
        try:
            fixture = read_json(folder / name)
            expected = fixture["expect"]
            if fixture["name"] != pathlib.Path(name).stem:
                errors.append(f"{name}: fixture name differs from filename")
            if set(expected["state"]) != members:
                errors.append(f"{name}: expected state members differ from manifest")
            if not isinstance(fixture["input"], list) or not fixture["input"]:
                errors.append(f"{name}: input must be a nonempty array")
            if expected["verdict"] == "fail_closed":
                covered.add(expected["violation"])
                index = expected["atInput"]
                if type(index) is not int or not 0 <= index < len(fixture["input"]):
                    errors.append(f"{name}: atInput must index the input array")
            elif expected["verdict"] == "accepted":
                if {"violation", "atInput"} & set(expected) or "postRefusal" in fixture:
                    errors.append(f"{name}: accepted fixture carries refusal fields")
            else:
                errors.append(f"{name}: unknown expected verdict")
            if "postRefusal" in fixture and not isinstance(fixture["postRefusal"], list):
                errors.append(f"{name}: postRefusal must be an array")
        except (OSError, ValueError, KeyError, TypeError) as error:
            errors.append(f"{name}: {error}")
    if vocabulary - covered:
        errors.append("uncovered lifecycle violations: " + ", ".join(sorted(vocabulary - covered)))
    if covered - vocabulary:
        errors.append("unknown fixture violations: " + ", ".join(sorted(covered - vocabulary)))
    return errors


def check_skills(root):
    errors = []
    folder = root / ".agents/skills"
    skills = {p.parent.name: p for p in folder.glob("*/SKILL.md")}
    for directory in folder.iterdir():
        if directory.is_dir() and directory.name not in skills:
            errors.append(f"{directory.relative_to(root)}: missing SKILL.md")
    for name, path in skills.items():
        try:
            source = path.read_text()
            frontmatter = re.match(r"\A---\n(.*?)\n---(?:\n|$)", source, re.DOTALL)
            if frontmatter is None:
                raise ValueError("missing or unterminated YAML frontmatter")
            metadata = yaml.load(frontmatter[1], Loader=UniqueLoader)
            if metadata.get("name") != name or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name):
                raise ValueError("skill name must match its directory")
            if not isinstance(metadata.get("description"), str) or not metadata["description"].strip():
                raise ValueError("missing skill description")
            link = root / ".claude/skills" / name
            if not link.is_symlink() or link.resolve() != path.parent.resolve():
                raise ValueError("missing or incorrect .claude/skills symlink")
        except (OSError, ValueError, AttributeError, yaml.YAMLError) as error:
            errors.append(f"{path.relative_to(root)}: {error}")
    for link in (root / ".claude/skills").iterdir():
        if link.name not in skills:
            errors.append(f"{link.relative_to(root)}: no corresponding skill")
    return errors


def check_scripts(root):
    errors = []
    for path in (root / "scripts").glob("*.py"):
        try:
            ast.parse(path.read_text(), filename=str(path))
        except SyntaxError as error:
            errors.append(f"{path.relative_to(root)}: {error}")
    for path in (root / "scripts").glob("*.sh"):
        result = subprocess.run(["bash", "-n", str(path)], capture_output=True, text=True)
        if result.returncode:
            errors.append(f"{path.relative_to(root)}: {result.stderr.strip()}")
    return errors


def main():
    root = pathlib.Path(__file__).resolve().parent.parent
    pages = sorted({*root.glob("*.md"), *root.glob("docs/*.md"),
                    *root.glob("tracking/*.md"), *root.glob(".agents/skills/**/*.md")})
    errors = []
    for label, check in (
        ("local links", lambda: check_links(root, pages)),
        ("lifecycle fixtures", lambda: check_fixtures(root)),
        ("skills", lambda: check_skills(root)),
        ("script syntax", lambda: check_scripts(root)),
    ):
        try:
            findings = check()
        except (OSError, ValueError, KeyError, IndexError, TypeError) as error:
            findings = [f"{label}: {error}"]
        errors.extend(findings)
        print(f"{'FAIL' if findings else 'PASS'} {label}")
    for error in errors:
        print(error)
    return bool(errors)


if __name__ == "__main__":
    sys.exit(main())
