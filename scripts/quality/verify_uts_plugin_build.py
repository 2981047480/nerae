#!/usr/bin/env python3
"""Compile a disposable import probe and check that App UTS bridges are emitted.

This tests compiler output, not native framework linking or device execution.
The temporary project is retained to make failures inspectable.
"""

from pathlib import Path
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[2]
CLIENT = ROOT / "apps/client"
PROBE = """<script setup lang="ts">
import { ref } from 'vue'
import { readExistingDevicePublicKey } from '@/uni_modules/nerae-device-key'
import { generateSecureRandomRequestKey } from '@/uni_modules/nerae-random'
import { readAuthenticationState } from '@/uni_modules/nerae-secure-vault'
import { createConfiguredAppHttpsPorts } from '@/platform/app'

const result = ref('')
function probe() {
  result.value = readExistingDevicePublicKey('probe').errorCode +
    generateSecureRandomRequestKey().errorCode + readAuthenticationState().errorCode +
    String(createConfiguredAppHttpsPorts)
}
</script>
<template><view @tap="probe">{{ result }}</view></template>
"""

PLUGINS = {
    "nerae-device-key": ("neraeDeviceKey", "readExistingDevicePublicKeyByJs"),
    "nerae-random": ("neraeRandom", "generateSecureRandomRequestKeyByJs"),
    "nerae-secure-vault": ("neraeSecureVault", "readAuthenticationStateByJs"),
    "nerae-https": ("neraeHttps", "requestByJs"),
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def main() -> None:
    work = Path(tempfile.mkdtemp(prefix="nerae-uts-build-"))
    print(f"UTS probe project: {work}", flush=True)
    shutil.copytree(CLIENT / "src", work / "src")
    for name in ("package.json", "tsconfig.json", "vite.config.ts"):
        shutil.copy2(CLIENT / name, work / name)
    (work / "node_modules").symlink_to(CLIENT / "node_modules", target_is_directory=True)
    (work / "src/pages/index/index.vue").write_text(PROBE, encoding="utf-8")
    uni = CLIENT / "node_modules/.bin/uni"
    require(uni.exists(), "Install the locked client dependencies before running this check")

    for platform in ("app", "mp-weixin"):
        print(f"Building {platform} import probe", flush=True)
        subprocess.run([str(uni), "build", "-p", platform], cwd=work, check=True)

    app = work / "dist/build/app"
    service = (app / "app-service.js").read_text(encoding="utf-8")
    for plugin, (package, function) in PLUGINS.items():
        require(package in service and function in service,
                f"App output is missing the native proxy for {plugin}")
        for platform, index in (("app-ios", "index.swift"), ("app-android", "index.kt")):
            path = app / "uni_modules" / plugin / "utssdk" / platform / "src" / index
            require(path.is_file() and function in path.read_text(encoding="utf-8"),
                    f"App output is missing the native wrapper for {plugin} on {platform}")

    # Weixin must fail closed for these App-only bridges, not ship App proxies.
    weixin = work / "dist/build/mp-weixin/uni_modules"
    for plugin in PLUGINS:
        fallback = weixin / plugin / "utssdk/index.js"
        require(fallback.is_file() and "unsupported_platform" in fallback.read_text(encoding="utf-8"),
                f"Weixin output is missing the unsupported-platform fallback for {plugin}")
    print("App native proxy/source and Weixin fallback checks passed; device execution was not tested.")


if __name__ == "__main__":
    main()
