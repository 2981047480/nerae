#!/usr/bin/env python3
"""Build an isolated import probe with HBuilderX's local 5.33 toolchain.

This verifies resource and UTS source generation, not an App link or install.
The script does not explicitly modify the project or HBuilderX installation;
the bundled compiler runs with the caller's permissions and environment.
"""

from __future__ import annotations

import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
CLIENT = ROOT / "apps/client"
DEFAULT_HBUILDERX = "/private/tmp/nerae-hbuilder-download/HBuilderX.app"


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def main() -> int:
    app = Path(os.environ.get("NERAE_HBUILDERX_APP", DEFAULT_HBUILDERX)).resolve()
    hbx = app / "Contents/HBuilderX"
    node = hbx / "plugins/node/node"
    modules = hbx / "plugins/uniapp-cli-vite/node_modules"
    cli = modules / "@dcloudio/vite-plugin-uni/bin/uni.js"
    typescript = hbx / "plugins/compile-typescript/node_modules/typescript"
    require(node.is_file() and os.access(node, os.X_OK), "HBuilderX Node is unavailable")
    require(cli.is_file() and modules.is_dir(), "HBuilderX bundled uni-app CLI is unavailable")
    require((typescript / "package.json").is_file(), "local HBuilderX TypeScript plugin is unavailable")

    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("nerae_uts_probe", ROOT / "scripts/quality/verify_uts_plugin_build.py")
    require(spec is not None and spec.loader is not None, "UTS probe configuration is unavailable")
    probe = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(probe)

    work = Path(tempfile.mkdtemp(prefix="nerae-hbuilder-533-check-", dir="/private/tmp"))
    succeeded = False
    try:
        shutil.copytree(CLIENT / "src", work / "src")
        for name in ("package.json", "tsconfig.json", "vite.config.ts"):
            shutil.copy2(CLIENT / name, work / name)
        (work / "src/pages/index/index.vue").write_text(probe.PROBE, encoding="utf-8")
        (work / "node_modules").symlink_to(modules, target_is_directory=True)

        env = os.environ.copy()
        for name in ("HX_PLUGIN_PATHS", "HX_Version", "UNI_INPUT_DIR", "UNI_OUTPUT_DIR",
                     "UNI_PLATFORM", "UNI_APP_PLATFORM", "UNI_UTS_PLATFORM", "UNI_COMPILE_TARGET", "UNI_CLI_CONTEXT"):
            env.pop(name, None)
        env["HX_APP_ROOT"] = str(hbx)
        env["npm_config_offline"] = "true"
        env["COREPACK_ENABLE_NETWORK"] = "0"
        for platform in ("app", "mp-weixin"):
            log = work / f"compiler-{platform}.log"
            try:
                result = subprocess.run([str(node), str(cli), "build", "-p", platform], cwd=work, env=env,
                                        text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90, check=False)
            except subprocess.TimeoutExpired as error:
                output = error.output or ""
                if isinstance(output, bytes):
                    output = output.decode("utf-8", errors="replace")
                log.write_text(output, encoding="utf-8")
                raise
            log.write_text(result.stdout, encoding="utf-8")
            require(result.returncode == 0 and "Compiler version: 5.33" in result.stdout and "Build complete" in result.stdout,
                    f"5.33 {platform} probe failed (exit {result.returncode}); see {log}")

        app_output = work / "dist/build/app"
        service = (app_output / "app-service.js").read_text(encoding="utf-8")
        require("AuthEntry" in service and "请查看此邮箱" in service, "App authentication import is missing")
        weixin_auth = work / "dist/build/mp-weixin/features/auth/AuthEntry.js"
        require(weixin_auth.is_file() and "AuthEntry" in weixin_auth.read_text(encoding="utf-8"),
                "Weixin authentication import is missing")
        for plugin, (package, functions) in probe.PLUGINS.items():
            require(package in service and all(name in service for name in functions), f"missing JS proxy: {plugin}")
            for platform, filename, suffix in (("app-ios", "index.swift", "*.swift"),
                                               ("app-android", "index.kt", "*.kt")):
                emitted = app_output / "uni_modules" / plugin / "utssdk" / platform / "src"
                wrapper = emitted / filename
                require(wrapper.is_file() and all(name in wrapper.read_text(encoding="utf-8") for name in functions),
                        f"missing native wrapper: {plugin}/{platform}")
                sources = list((CLIENT / "src/uni_modules" / plugin / "utssdk" / platform).glob(suffix))
                require(bool(sources), f"missing native source: {plugin}/{platform}")
                for source in sources:
                    target = emitted / source.name
                    require(target.is_file() and target.read_bytes() == source.read_bytes(),
                            f"native source changed during build: {plugin}/{platform}/{source.name}")
            fallback = work / "dist/build/mp-weixin/uni_modules" / plugin / "utssdk/index.js"
            require(fallback.is_file() and "unsupported_platform" in fallback.read_text(encoding="utf-8"),
                    f"missing Weixin fail-closed fallback: {plugin}")
        print("PASS: 5.33 App and Weixin import probes, iOS/Android wrappers, Weixin fallbacks")
        print("No App was linked, signed, installed, or run on a device.")
        succeeded = True
        return 0
    finally:
        if succeeded and os.environ.get("NERAE_KEEP_HBUILDER_533_CHECK") != "1":
            shutil.rmtree(work)
        else:
            print(f"Probe directory retained: {work}", file=sys.stderr)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
