"""Lead-only checks of a clean, explicit UI freeze; never edits UI source."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("workspace", type=Path)
    parser.add_argument("revision")
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-f0-9]{40}", args.revision):
        parser.error("revision must be the actual full frozen commit")
    prototype = args.workspace.resolve() / "design/prototype"
    output = args.output.resolve()

    def git(*parts):
        return subprocess.check_output(
            ["git", "-C", str(prototype), *parts], text=True
        ).strip()

    if git("rev-parse", "HEAD") != args.revision:
        parser.error("workspace HEAD is not the requested freeze")
    if git("status", "--porcelain", "--", "."):
        parser.error("prototype is dirty; wait for handback and freeze first")
    if output.exists() and any(output.iterdir()):
        parser.error("output must be new or empty; preserve prior checks")
    output.mkdir(parents=True, exist_ok=True)
    paths = git("ls-files", "-z", "--", ".").split("\0")

    def manifest():
        return {
            p: hashlib.sha256((prototype / p).read_bytes()).hexdigest()
            for p in paths if p
        }

    helper = Path(__file__).with_name("check_frozen_identity_seams.mjs").resolve()
    snapshot = output / "identity-check-executed.mjs"
    snapshot.write_bytes(helper.read_bytes())
    before = manifest()
    node = "/opt/homebrew/bin/node"
    commands = [
        ("typecheck", [node, "node_modules/vue-tsc/bin/vue-tsc.js", "--noEmit"]),
        ("build", [node, "node_modules/vite/bin/vite.js", "build"]),
        ("test", [node, "node_modules/vitest/vitest.mjs", "run"]),
        ("identity-seams", [node, str(snapshot), args.revision, str(output / "identity-seams.json")]),
    ]
    report = {
        "scope": "Lead frozen code checks; module identity checks use fake window/storage; no browser/backend acceptance",
        "source_revision": args.revision,
        "workspace": str(args.workspace.resolve()),
        "before": before,
        "identity_check_snapshot": str(snapshot),
        "identity_check_sha256": hashlib.sha256(snapshot.read_bytes()).hexdigest(),
        "results": [],
        "accepted": False,
    }
    try:
        for name, command in commands:
            started = time.monotonic()
            log = output / f"{name}.log"
            with log.open("xb") as stream:
                result = subprocess.run(command, cwd=prototype, stdout=stream, stderr=subprocess.STDOUT)
            report["results"].append({
                "name": name, "command": command, "exit_code": result.returncode,
                "seconds": round(time.monotonic() - started, 3),
                "log": str(log), "sha256": hashlib.sha256(log.read_bytes()).hexdigest(),
            })
            print(f"{name}: exit {result.returncode}", flush=True)
    finally:
        report["after"] = manifest()
        report["changedDuringCheck"] = [p for p in before if before[p] != report["after"][p]]
        report["headAfter"] = git("rev-parse", "HEAD")
        report["sourceStatusAfter"] = git("status", "--porcelain", "--", ".")
        report["observedUtc"] = datetime.now(timezone.utc).isoformat()
        report["checksPassed"] = (
            len(report["results"]) == len(commands)
            and all(r["exit_code"] == 0 for r in report["results"])
            and not report["changedDuringCheck"]
            and report["headAfter"] == args.revision
            and not report["sourceStatusAfter"]
        )
        (output / "code-checks.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    return 0 if report["checksPassed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
