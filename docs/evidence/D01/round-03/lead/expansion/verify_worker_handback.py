"""Verify an idle worker handback before Lead freezes it. Does not edit source."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("workspace", type=Path)
    p.add_argument("fix", help="fix04, fix05, ...")
    p.add_argument("output", type=Path)
    a = p.parse_args()
    root = a.workspace.resolve()
    out = a.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    target = out / "handback-verification.json"
    if target.exists():
        p.error("preserve the previous verification; choose a new output")
    worker = "docs/evidence/D01/round-03/worker"
    report_path = root / worker / f"D01-03-{a.fix}-report.json"
    report = json.loads(report_path.read_text())
    digest = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
    result = {"scope": "file hashes and write boundary; not UI acceptance", "accepted": False,
              "observed_utc": datetime.now(timezone.utc).isoformat(), "manifests": {}}
    passed = True
    all_paths = set()
    for kind in ("source", "evidence", "report"):
        manifest = root / worker / "logs" / f"d01-03-{a.fix}-{kind}-sha256.txt"
        entries = {}
        for line in manifest.read_text().splitlines():
            if not line.strip() or line.startswith("#"):
                continue
            match = re.fullmatch(r"([a-f0-9]{64})\s+(.+)", line)
            if not match:
                raise ValueError(f"invalid manifest line: {line}")
            checksum, name = match.groups()
            file = (root / name).resolve()
            if not file.is_relative_to(root):
                raise ValueError("manifest escaped workspace")
            entries[name] = checksum
        bad = [name for name, checksum in entries.items()
               if not (root / name).is_file() or digest(root / name) != checksum]
        expected = report.get(kind + "_hashes", {}).get("manifest_sha256")
        valid = not bad and (expected is None or expected == digest(manifest))
        result["manifests"][kind] = {"count": len(entries), "manifest_sha256": digest(manifest),
                                            "mismatches": bad, "valid": valid}
        result[kind + "_files"] = entries
        all_paths.update(entries)
        all_paths.add(str(manifest.relative_to(root)))
        passed &= valid
    git = lambda *args: subprocess.check_output(["git", "-C", str(root), *args], text=True)
    changed = git("status", "--porcelain=v1", "--untracked-files=all").splitlines()
    allowed_prefixes = ("design/prototype/", worker + "/")
    outside = [line for line in changed if not line[3:].startswith(allowed_prefixes)]
    tracked = git("ls-files", "design/prototype").splitlines()
    unlisted = [name for name in tracked if name not in result["source_files"]]
    passed &= not outside and not unlisted
    result.update(parent_revision=git("rev-parse", "HEAD").strip(), changed=changed,
                  outside_scope=outside, unlisted_tracked_source=unlisted, passed=passed,
                  dev_log_excluded=True)
    target.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"passed": passed, "counts": {k: v["count"] for k, v in result["manifests"].items()},
                      "outside": outside, "output": str(target)}))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
