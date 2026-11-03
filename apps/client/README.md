# Client

The uni-app Vue 3 / TypeScript client has an App authentication entry and a
device-approval panel. Its training page and reliable synchronization are not
wired; the route reports that training records are unavailable. Weixin login
is not implemented. There is no training or rest countdown.

From the repository root, install with the committed workspace lock, then run:

- `pnpm --filter @nerae/client typecheck`
- `pnpm --filter @nerae/client build:h5`
- `pnpm --filter @nerae/client build:mp-weixin`
- `pnpm --filter @nerae/client build:app`

For an App build, `VITE_NERAE_API_BASE` must be set to the verified HTTPS API
base including its path prefix. It is embedded in the client as public
configuration: never put credentials or secrets in it. Missing or invalid
configuration leaves login unavailable and does not select a fallback server.
The App target produces uni-app resources only; it is not a signed installable
package and has not established real-device login or synchronization.
