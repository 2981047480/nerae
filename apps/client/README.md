# Client

The uni-app Vue 3 / TypeScript client has an App authentication entry, a
device-approval panel and a local training-draft entry. Reliable server-backed
synchronization is not wired, so drafts are explicitly marked unsynchronized.
Weixin login is not implemented. There is no training or rest countdown.

From the repository root, install with the committed workspace lock, then run:

- `pnpm --filter @nerae/client typecheck`
- `pnpm --filter @nerae/client build:h5`
- `pnpm --filter @nerae/client build:mp-weixin`
- `pnpm --filter @nerae/client build:app`

`build:app` runs `uni build -p app`, then exits non-zero when the compiler log
contains `Cannot find module: @dcloudio/uni-uts-v1` or when this run did not
leave a nonempty `app-service.js`. The default path is
`apps/client/dist/build/app/app-service.js`. `UNI_OUTPUT_DIR`, when set, is
that file's directory instead. A file left by an older run is removed first, so
it cannot make a failed compile look successful. The DCloud compiler still
exits 0 when it cannot load UTS; this repository script is the gate, and the
compiler itself is unchanged.

On Linux glibc, `@dcloudio/uts-linux-x64-gnu` is an optional package tagged
`libc: gnu`. pnpm does not add it to an existing `node_modules` tree. Delete
`node_modules` and run `pnpm install --frozen-lockfile` from the repository
root before building.

For an App build, `VITE_NERAE_API_BASE` must be set to the verified HTTPS API
base including its path prefix. It is embedded in the client as public
configuration: never put credentials or secrets in it. Missing or invalid
configuration leaves login unavailable and does not select a fallback server.
The App target produces uni-app resources only; it is not a signed installable
package and has not established real-device login or synchronization.
