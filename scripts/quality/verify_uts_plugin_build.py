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
import DeviceApprovalPanel from '@/features/auth/DeviceApprovalPanel.vue'
import TrainingEntry from '@/features/training/TrainingEntry.vue'
import type { AppAuthRuntime } from '@/platform/app/nativeAuthRuntime'
import { generateSecureRandomRequestKey } from '@/uni_modules/nerae-random'
import { createConfiguredAppHttpsPorts } from '@/platform/app'
import { createAppSessionCoordinator } from '@/platform/app/nativeSession'
import { createAppDeviceIdentityCoordinator, signAppEmailLoginChallenge } from '@/platform/app/nativeDeviceIdentity'
import { createAppEmailLoginFlow } from '@/platform/app/nativeEmailLogin'
import { createAppAuthRuntime } from '@/platform/app/nativeAuthRuntime'
import { createNativeTrainingJournalStorage } from '@/platform/app/nativeTrainingJournalStorage'
import { createNativeTrainingDraftStorage } from '@/platform/app/nativeTrainingDraftStorage'
import { createAppTrainingEditor, attachAppTrainingEditor, discoverAppTrainingDrafts } from '@/platform/app/nativeTrainingEditor'
import { TrainingOperationJournal } from '@/features/training/operationJournal'
import { discoverTrainingDrafts, readTrainingDraft, createTrainingDraft, writeTrainingDraft, readVersionedTrainingDraft, compareAndSetVersionedTrainingDraft, readTrainingOperationJournal, compareAndSetTrainingOperationJournal, readVersionedTrainingOperationJournal, compareAndSetVersionedTrainingOperationJournal, claimTrainingOperationDispatch } from '@/uni_modules/nerae-secure-vault'

const result = ref('')
const authRuntime = shallowRef<AppAuthRuntime | null>(null)
function probe() {
  result.value = generateSecureRandomRequestKey().errorCode + String(createConfiguredAppHttpsPorts) + String(createAppSessionCoordinator) +
    String(createAppDeviceIdentityCoordinator) + String(signAppEmailLoginChallenge) + String(createAppEmailLoginFlow) + String(createAppAuthRuntime) +
    String(createNativeTrainingJournalStorage) + String(createNativeTrainingDraftStorage) + String(createAppTrainingEditor) + String(attachAppTrainingEditor) + String(discoverAppTrainingDrafts) + String(TrainingOperationJournal) +
    String(createConfiguredAppHttpsPorts('https://example.invalid').revokeCurrentSession) +
    String(discoverTrainingDrafts) + String(readTrainingDraft) + String(createTrainingDraft) + String(writeTrainingDraft) +
    String(readVersionedTrainingDraft) + String(compareAndSetVersionedTrainingDraft) +
    String(readTrainingOperationJournal) + String(compareAndSetTrainingOperationJournal) +
    String(readVersionedTrainingOperationJournal) + String(compareAndSetVersionedTrainingOperationJournal) + String(claimTrainingOperationDispatch)
}
</script>
<template><view @tap="probe">{{ result }}</view><AuthEntry v-if="authRuntime" :runtime="authRuntime" />
  <DeviceApprovalPanel v-if="authRuntime" :approvals="authRuntime.approvals" :session="authRuntime.session" />
  <TrainingEntry v-if="authRuntime" :session="authRuntime.session" time-zone="Asia/Shanghai" /></template>
"""

PLUGINS = {
    "nerae-device-key": ("neraeDeviceKey", ("readExistingDevicePublicKeyByJs", "createDeviceKeyAtReservedAliasByJs", "signExistingDeviceProofByJs")),
    "nerae-random": ("neraeRandom", ("generateSecureRandomRequestKeyByJs",)),
    "nerae-secure-vault": ("neraeSecureVault", ("readAuthenticationStateByJs", "writeAuthenticationStateByJs", "clearAuthenticationStateByJs", "readDeviceIdentityRegistryByJs", "compareAndSetDeviceIdentityRegistryByJs", "discoverTrainingDraftsByJs", "readTrainingDraftByJs", "createTrainingDraftByJs", "writeTrainingDraftByJs", "readVersionedTrainingDraftByJs", "compareAndSetVersionedTrainingDraftByJs", "readTrainingOperationJournalByJs", "compareAndSetTrainingOperationJournalByJs", "readVersionedTrainingOperationJournalByJs", "compareAndSetVersionedTrainingOperationJournalByJs", "claimTrainingOperationDispatchByJs")),
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
    require("AuthEntry" in service and "请查看此邮箱" in service and "检查确认状态" in service and "匹配一致，允许登录" in service,
            "App output is missing the referenced authentication entry")
    require("本机草稿" in service and "重试本机保存" in service,
            "App output is missing the referenced training entry")
    require("revokeCurrentSession" in service and "TrainingOperationJournal" in service,
            "App output is missing a referenced revocation or training journal entry")
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
    require(weixin_entry.is_file() and "AuthEntry" in weixin_entry.read_text(encoding="utf-8") and
            "检查确认状态" in weixin_entry.read_text(encoding="utf-8"),
            "Weixin output is missing the referenced authentication entry")
    weixin_approval = work / "dist/build/mp-weixin/features/auth/DeviceApprovalPanel.wxml"
    require(weixin_approval.is_file() and "匹配一致，允许登录" in weixin_approval.read_text(encoding="utf-8"),
            "Weixin output is missing the referenced device approval panel")
    weixin_training = work / "dist/build/mp-weixin/features/training/TrainingEntry.wxml"
    require(weixin_training.is_file() and "重试本机保存" in weixin_training.read_text(encoding="utf-8"),
            "Weixin output is missing the referenced training entry")
    for plugin in PLUGINS:
        fallback = weixin / plugin / "utssdk/index.js"
        require(fallback.is_file() and "unsupported_platform" in fallback.read_text(encoding="utf-8"),
                f"Weixin output is missing the unsupported-platform fallback for {plugin}")
    print("App native proxy/source and Weixin fallback checks passed; device execution was not tested.")


if __name__ == "__main__":
    main()
