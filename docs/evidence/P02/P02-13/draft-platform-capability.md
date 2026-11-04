# Training draft platform capability

The secure-vault UTS module now identifies the compiled draft implementation:
iOS returns `ios-versioned-v1`, Android returns `android-legacy-v1`, and the
generic fallback returns `unsupported`. The capability does not authorize a
write and is not yet consumed by the editor. A future factory must reject an
unknown or unavailable value instead of defaulting to either platform.

DCloud's [platform documentation](https://uniapp.dcloud.net.cn/tutorial/platform.html)
states that ordinary uni-app JavaScript cannot select Android versus iOS with
`APP-ANDROID` / `APP-IOS` conditionals. A separate 5.33 App resource probe
confirmed those branches were absent from the ordinary TypeScript output.
The same probe generated distinct Swift and Kotlin native UTS implementations.

On 2026-11-04, the matching npm compiler generated the real secure-vault Swift
and Kotlin wrappers with their respective capability values. A separate 5.33
App and Weixin resource probe included the real module; the App proxy named
the capability export, and the generated Weixin fallback returned `unsupported`
in an isolated Node VM. The App/Weixin import probe also passed. Finally,
all four native iPhoneOS arm64 plugin libraries linked against the 5.33 offline
SDK, including the modified secure-vault library.

The separate App/Weixin probe and its generated files are retained at
`/private/tmp/nerae-533-real-platform-probe.fljAuPsH`; it added test-only calls
to keep the capability export in both builds. The independent conditional
compilation probe is `/private/tmp/nerae-533-platform-probe.7o9PlA0l`.
The probe's initial pnpm launcher invocation failed through a symlinked path;
the subsequent direct invocation of the matching `uni.js` compiler passed.

These are source conversion, resource and unsigned plugin-link checks only.
They do not establish plugin loading or return values on a device, an APK,
a signed App, an editor integration, or cross-device synchronization.
