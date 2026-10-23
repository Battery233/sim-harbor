"""Small, dependency-free repository checks shared by CI and local maintenance."""

import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote


def git(root, *args):
    return subprocess.check_output(["git", *args], cwd=root).decode()


def changed_scopes(paths):
    backend = frontend = False
    for path in paths:
        if path.endswith(".md") or path.startswith("docs/images/"):
            continue
        if path.startswith(("internal/", "cmd/")) or path in ("go.mod", "go.sum"):
            backend = True
        elif path.startswith("web/"):
            frontend = True
        else:
            backend = frontend = True
    return backend, frontend


def versions(root):
    docker = (root / "Dockerfile").read_text()
    version = re.search(r'const Version = "([^"]+)"', (root / "internal/version/version.go").read_text())[1]
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-rc\.[1-9]\d*)?", version):
        raise ValueError("Application version is not a supported release or RC version")
    package = json.loads((root / "web/package.json").read_text())
    lock = json.loads((root / "web/package-lock.json").read_text())
    label = re.search(r'org.opencontainers.image.version="([^"]+)"', docker)[1]
    if not (version == package["version"] == lock["version"] == lock["packages"][""]["version"] == label):
        raise ValueError("Application, frontend, lockfile and Docker versions differ")
    compose = re.search(
        r'''^[ \t]+image:[ \t]*["']?\$\{SIM_IMAGE:-sim-harbor:([^}\s]+)\}["']?[ \t]*(?:#.*)?$''',
        (root / "compose.yaml").read_text(), re.M,
    )
    if not compose or compose[1] != version:
        raise ValueError("Compose default image must use SIM_IMAGE with the current application version")
    go = re.search(r"^go (\S+)$", (root / "go.mod").read_text(), re.M)[1]
    if re.search(r"^FROM golang:([^ -]+)", docker, re.M)[1] != go:
        raise ValueError("Docker Go version differs from go.mod")
    return re.search(r"^FROM node:(\d+\.\d+\.\d+)-", docker, re.M)[1]


def anchors(text):
    found, counts = set(), {}
    fenced = False
    for line in text.splitlines():
        if re.match(r"^\s*(```|~~~)", line):
            fenced = not fenced
        if fenced:
            continue
        heading = re.match(r"^#{1,6}\s+(.+?)\s*#*$", line)
        if not heading:
            continue
        value = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", heading[1])
        value = re.sub(r"<[^>]+>", "", value).strip().lower()
        value = re.sub(r"[^\w\- ]", "", value).replace(" ", "-")
        index = counts.get(value, 0)
        counts[value] = index + 1
        found.add(value + (f"-{index}" if index else ""))
    return found


def check_links(root, files):
    checked = 0
    for name in files:
        if not name.endswith(".md"):
            continue
        source = root / name
        for target in re.findall(r"!?\[[^\]]*\]\(([^)]+)\)", source.read_text()):
            target = target.strip().split(' "')[0].strip("<>")
            if re.match(r"^[\w+.-]+:", target):
                continue
            path, _, anchor = unquote(target).partition("#")
            dest = (source.parent / path).resolve() if path else source
            if not dest.exists():
                raise ValueError(f"Missing local link in {name}: {target}")
            if anchor and dest.suffix == ".md" and anchor not in anchors(dest.read_text()):
                raise ValueError(f"Missing local anchor in {name}: {target}")
            checked += 1
    return checked


def main():
    root = Path(__file__).resolve().parent.parent
    files = [p for p in git(root, "ls-files", "-z").split("\0") if p]
    node = versions(root)
    count = check_links(root, files)
    base = os.environ.get("CI_BASE_SHA", "")
    backend = frontend = True
    if re.fullmatch(r"[a-f0-9]{40}", base) and base != "0" * 40:
        try:
            changed = [p for p in git(root, "diff", "--name-only", "-z", base, "HEAD").split("\0") if p]
            backend, frontend = changed_scopes(changed)
        except subprocess.CalledProcessError:
            # Missing history must not silently skip tests.
            backend = frontend = True
        else:
            subprocess.run(["git", "diff", "--check", base, "HEAD"], cwd=root, check=True)
    subprocess.run(["git", "diff", "--check"], cwd=root, check=True)
    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with open(output, "a") as file:
            file.write(f"backend={str(backend).lower()}\nfrontend={str(frontend).lower()}\nnode={node}\n")
    print(f"Versions agree; {count} local links checked. Backend={backend}, frontend={frontend}")


if __name__ == "__main__":
    main()
