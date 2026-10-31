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
import { ref, shallowRef } from 'vue'
import AuthEntry from '@/features/auth/AuthEntry.vue'
import type { AppAuthRuntime } from '@/platform/app/nativeAuthRuntime'
import { generateSecureRandomRequestKey } from '@/uni_modules/nerae-random'
import { createConfiguredAppHttpsPorts } from '@/platform/app'
import { createAppSessionCoordinator } from '@/platform/app/nativeSession'
import { createAppDeviceIdentityCoordinator, signAppEmailLoginChallenge } from '@/platform/app/nativeDeviceIdentity'
import { createAppEmailLoginFlow } from '@/platform/app/nativeEmailLogin'
import { createAppAuthRuntime } from '@/platform/app/nativeAuthRuntime'
import { readTrainingOperationJournal, compareAndSetTrainingOperationJournal } from '@/uni_modules/nerae-secure-vault'

const result = ref('')
const authRuntime = shallowRef<AppAuthRuntime | null>(null)
function probe() {
  result.value = generateSecureRandomRequestKey().errorCode + String(createConfiguredAppHttpsPorts) + String(createAppSessionCoordinator) +
    String(createAppDeviceIdentityCoordinator) + String(signAppEmailLoginChallenge) + String(createAppEmailLoginFlow) + String(createAppAuthRuntime) +
    String(readTrainingOperationJournal) + String(compareAndSetTrainingOperationJournal)
}
</script>
<template><view @tap="probe">{{ result }}</view><AuthEntry v-if="authRuntime" :runtime="authRuntime" /></template>
"""

PLUGINS = {
    "nerae-device-key": ("neraeDeviceKey", ("readExistingDevicePublicKeyByJs", "createDeviceKeyAtReservedAliasByJs", "signExistingDeviceProofByJs")),
    "nerae-random": ("neraeRandom", ("generateSecureRandomRequestKeyByJs",)),
    "nerae-secure-vault": ("neraeSecureVault", ("readAuthenticationStateByJs", "writeAuthenticationStateByJs", "clearAuthenticationStateByJs", "readDeviceIdentityRegistryByJs", "compareAndSetDeviceIdentityRegistryByJs", "readTrainingOperationJournalByJs", "compareAndSetTrainingOperationJournalByJs")),
    "nerae-https": ("neraeHttps", ("requestByJs", "cancelByJs")),
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
    require("AuthEntry" in service and "请查看此邮箱" in service,
            "App output is missing the referenced authentication entry")
    for plugin, (package, functions) in PLUGINS.items():
        require(package in service and all(function in service for function in functions),
                f"App output is missing the native proxy for {plugin}")
        for platform, index in (("app-ios", "index.swift"), ("app-android", "index.kt")):
            path = app / "uni_modules" / plugin / "utssdk" / platform / "src" / index
            require(path.is_file() and all(function in path.read_text(encoding="utf-8") for function in functions),
                    f"App output is missing the native wrapper for {plugin} on {platform}")

    # Weixin must fail closed for these App-only bridges, not ship App proxies.
    weixin = work / "dist/build/mp-weixin/uni_modules"
    weixin_entry = work / "dist/build/mp-weixin/features/auth/AuthEntry.js"
    require(weixin_entry.is_file() and "AuthEntry" in weixin_entry.read_text(encoding="utf-8"),
            "Weixin output is missing the referenced authentication entry")
    for plugin in PLUGINS:
        fallback = weixin / plugin / "utssdk/index.js"
        require(fallback.is_file() and "unsupported_platform" in fallback.read_text(encoding="utf-8"),
                f"Weixin output is missing the unsupported-platform fallback for {plugin}")
    print("App native proxy/source and Weixin fallback checks passed; device execution was not tested.")


if __name__ == "__main__":
    main()
