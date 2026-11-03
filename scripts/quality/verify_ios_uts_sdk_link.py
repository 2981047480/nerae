#!/usr/bin/env python3
"""Link generated 5.33 iOS UTS plugin sources against the local 5.33 SDK.

This checks individual unsigned plugin libraries, not an App or a device.
The source directory must come from verify_uts_plugin_build.py on this tree.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
CLIENT = ROOT / "apps/client"
EXPECTED_PACKAGE = "3.0.0-5030320261023001"
PLUGINS = ("nerae-device-key", "nerae-random", "nerae-https", "nerae-secure-vault")


def require(ok: bool, message: str) -> None:
    if not ok:
        raise RuntimeError(message)


def digest(path: Path) -> bytes:
    return hashlib.sha256(path.read_bytes()).digest()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sdk-root", required=True, type=Path, help="SDK directory containing uniapp.podspec and SDK/Libs")
    parser.add_argument("--app-resource-dir", type=Path, help="optional prebuilt probe dist/build/app; skips fresh compilation")
    args = parser.parse_args()
    sdk_root = args.sdk_root.resolve(strict=True)
    if args.app_resource_dir:
        app = args.app_resource_dir.resolve(strict=True)
        print("Using prebuilt probe: source linking only; build provenance is not established", flush=True)
    else:
        for name in ("uni-app", "uni-uts-v1", "vite-plugin-uni"):
            installed = json.loads((CLIENT / "node_modules/@dcloudio" / name / "package.json").read_text(encoding="utf-8"))
            require(installed.get("version") == EXPECTED_PACKAGE, f"installed {name} is not the locked version")
        built = subprocess.run([sys.executable, "-B", str(ROOT / "scripts/quality/verify_uts_plugin_build.py")],
                               text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
        print(built.stdout, end="", flush=True)
        require(built.returncode == 0, "fresh App/Weixin UTS probe failed")
        locations = [line.removeprefix("UTS probe project: ") for line in built.stdout.splitlines()
                     if line.startswith("UTS probe project: ")]
        require(len(locations) == 1, "could not locate the fresh probe project")
        app = (Path(locations[0]) / "dist/build/app").resolve(strict=True)
    require(app.name == "app" and app.parent.name == "build" and app.parent.parent.name == "dist",
            "app resources must be from a fresh verify_uts_plugin_build.py fixture")
    fixture_src = app.parents[2] / "src"
    require(fixture_src.is_dir(), "probe fixture sources are missing")
    manifest = json.loads((app / "manifest.json").read_text(encoding="utf-8"))
    package = json.loads((CLIENT / "package.json").read_text(encoding="utf-8"))
    require(package["dependencies"]["@dcloudio/uni-app"] == EXPECTED_PACKAGE, "unexpected project compiler version")
    require(manifest.get("plus", {}).get("uni-app", {}).get("compilerVersion") == "5.33", "probe resources are not compiler 5.33")
    podspec = (sdk_root / "uniapp.podspec").read_text(encoding="utf-8")
    require(re.search(r"\bs\.version\s*=\s*['\"]5\.33\.0['\"]", podspec) is not None, "unexpected offline SDK version")
    frameworks = sdk_root / "SDK/Libs"
    headers = sdk_root / "SDK/inc"
    for name in ("DCloudUTSFoundation.framework", "DCUniBase.framework"):
        require((frameworks / name).is_dir(), f"missing SDK framework: {name}")
    require(headers.is_dir(), "missing SDK headers")
    ios_sdk = subprocess.check_output(["xcrun", "--sdk", "iphoneos", "--show-sdk-path"], text=True).strip()
    work = Path(tempfile.mkdtemp(prefix="nerae-533-uts-link-", dir="/private/tmp"))
    print(f"Native link work directory: {work}", flush=True)
    success = False
    try:
        for plugin in PLUGINS:
            source = CLIENT / "src/uni_modules" / plugin / "utssdk/app-ios"
            generated = app / "uni_modules" / plugin / "utssdk/app-ios/src"
            for uts in (source / "index.uts", source.parent / "interface.uts"):
                copied = fixture_src / uts.relative_to(CLIENT / "src")
                require(copied.is_file() and digest(uts) == digest(copied), f"stale probe UTS input: {plugin}/{uts.name}")
            originals = sorted(source.glob("*.swift"))
            generated_files = sorted(generated.glob("*.swift"))
            expected_names = {path.name for path in originals} | {"index.swift"}
            require(bool(originals) and {path.name for path in generated_files} == expected_names, f"missing or extra generated source: {plugin}")
            for original in originals:
                require(digest(original) == digest(generated / original.name), f"stale native source: {plugin}/{original.name}")
            module = work / plugin
            cache = module / "cache"
            cache.mkdir(parents=True)
            output = module / "libProbe.dylib"
            command = ["xcrun", "swiftc", "-swift-version", "5", "-sdk", ios_sdk, "-target", "arm64-apple-ios15.0",
                       "-module-cache-path", str(cache), "-F", str(frameworks), "-I", str(headers),
                       "-framework", "DCloudUTSFoundation", "-framework", "DCUniBase", "-emit-library",
                       *map(str, generated_files), "-o", str(output)]
            built = subprocess.run(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
            (module / "build.log").write_text(built.stdout, encoding="utf-8")
            require(built.returncode == 0, f"{plugin} link failed; inspect {module / 'build.log'}")
            description = subprocess.check_output(["file", "-b", str(output)], text=True)
            metadata = subprocess.check_output(["xcrun", "vtool", "-show-build", str(output)], text=True)
            require("arm64" in description and "dynamically linked shared library" in description and "platform IOS" in metadata,
                    f"{plugin} did not produce an iPhoneOS arm64 library")
            print(f"{plugin}: iPhoneOS arm64 library linked", flush=True)
        success = True
    finally:
        if success:
            shutil.rmtree(work)
        else:
            print(f"Failed native link evidence retained: {work}", flush=True)
    print("PASS: four isolated native plugin libraries; App linking, signing and device execution were not tested")


if __name__ == "__main__":
    main()
