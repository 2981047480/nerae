# Client shell

This is the uni-app Vue 3 / TypeScript build shell. The single route is intentionally blank; it does not implement sign-in, business data, product UI, or training/休息计时.

From the repository root, install with the committed workspace lock, then run:

- `pnpm --filter @nerae/client typecheck`
- `pnpm --filter @nerae/client build:h5`
- `pnpm --filter @nerae/client build:mp-weixin`
- `pnpm --filter @nerae/client build:app`

The App target produces uni-app resources only. Native signing, install packages, developer tools, devices, and business behavior are handled by later tasks.
