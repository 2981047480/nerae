"""Download only screenshot references returned for this Nerae research task."""
import concurrent.futures, hashlib, json, urllib.parse, urllib.request
from pathlib import Path
from PIL import Image
ROOT = Path(__file__).resolve().parent
source = json.loads((ROOT / "sources.json").read_text())
screens = {}
for query in source["queries"]:
    for screen in query["response"].get("screens", []):
        if screen.get("kind") == "image":
            key = screen["app"]["app_id"] + "/" + screen["screen_id"]
            screens.setdefault(key, dict(screen, queries=[]))["queries"].append(query["key"])
def fetch(item):
    key, screen = item
    if urllib.parse.urlsplit(screen["media_url"]).hostname != "mcp.appllama.io":
        raise ValueError("Unexpected source domain")
    path = ROOT / "references" / (key + ".webp")
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        req = urllib.request.Request(screen["media_url"], headers={"User-Agent": "Nerae-UI-reference-study/1.0"})
        with urllib.request.urlopen(req, timeout=25) as response:
            path.write_bytes(response.read())
    with Image.open(path) as image:
        width, height = image.size
    return {"screen_ref": key, "name": screen["name"], "flow": screen.get("flow"),
            "app": screen["app"], "queries": screen["queries"],
            "path": str(path.relative_to(ROOT)), "width": width, "height": height,
            "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "bytes": path.stat().st_size}
results, errors = [], []
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    futures = {pool.submit(fetch, item): item[0] for item in screens.items()}
    for future in concurrent.futures.as_completed(futures):
        try:
            results.append(future.result())
        except Exception as error:
            errors.append({"screen_ref": futures[future], "error": str(error)})
results.sort(key=lambda row: row["screen_ref"])
manifest = {"date": source["date"], "source": source["source"],
            "requested_unique": len(screens), "downloaded": len(results),
            "screens": results, "errors": errors,
            "note": "Reference screenshots retain the Appllama watermark; never product assets."}
(ROOT / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"requested": len(screens), "downloaded": len(results), "errors": errors}, ensure_ascii=False))
raise SystemExit(bool(errors))
