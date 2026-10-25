"""Copy an explicitly frozen UI build for independent localhost-only QA."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("workspace", type=Path)
    p.add_argument("revision")
    p.add_argument("port", type=int)
    p.add_argument("output", type=Path)
    a = p.parse_args()
    w = a.workspace.resolve()
    git = lambda *args: subprocess.check_output(["git", "-C", str(w), *args], text=True).strip()
    if git("rev-parse", "HEAD") != a.revision or git("status", "--porcelain", "--", "design/prototype"):
        p.error("explicit clean source freeze required")
    output = a.output.resolve()
    if output.exists():
        p.error("preserve previous preview proof")
    digest = lambda file: hashlib.sha256(file.read_bytes()).hexdigest()
    source_paths = git("ls-files", "design/prototype").splitlines()
    source_before = {name: digest(w / name) for name in source_paths}
    dist = w / "design/prototype/dist"
    built = {str(f.relative_to(dist)): digest(f) for f in sorted(dist.rglob("*")) if f.is_file()}
    if not built or "index.html" not in built:
        p.error("saved build unavailable; run frozen build first")
    target = Path(tempfile.mkdtemp(prefix=f"nerae-frozen-{a.revision[:4]}-", dir="/private/tmp")) / "dist"
    shutil.copytree(dist, target)
    copied = {str(f.relative_to(target)): digest(f) for f in sorted(target.rglob("*")) if f.is_file()}
    source_after = {name: digest(w / name) for name in source_paths}
    if built != copied or source_before != source_after:
        raise RuntimeError("build copy or frozen source changed; do not serve as frozen evidence")
    result = {"source_revision": a.revision, "scope": "byte-identical saved build; server not yet verified",
              "dist": str(target), "files": copied, "file_count": len(copied), "source_files": source_after,
              "port": a.port, "bind": "127.0.0.1", "accepted": False,
              "observed_utc": datetime.now(timezone.utc).isoformat(),
              "serve_command": ["python3", "-m", "http.server", str(a.port), "--bind", "127.0.0.1", "--directory", str(target)]}
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"dist": str(target), "files": len(copied), "port": a.port}))


if __name__ == "__main__":
    main()
