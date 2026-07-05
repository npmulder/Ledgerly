# Ledgerly Web

React SPA scaffold for Ledgerly, built with Vite, TypeScript, React Router, and npm scripts delegated through go-task.

## Commands

- `npm run dev` starts the Vite development server.
- `npm run build` type-checks and builds the production bundle.
- `npm run test` runs Vitest once.
- `npm run lint` runs ESLint.
- `npm run typecheck` runs TypeScript strict checks.

From the repository root, the same checks are exposed through the included `web` namespace:

- `task web:dev`
- `task web:build`
- `task web:test`
- `task web:lint`
- `task web:typecheck`

## Source Layout

`@/` resolves to `web/src`.

- `src/app`: application shell, routing, and app-level constants.
- `src/components`: reusable React components.
- `src/screens`: route-level screen components.
- `src/api`: generated or handwritten API client code when added later.
- `src/styles`: hand-rolled design tokens and global styles when FE-2 adds them.

TanStack Query is installed for later configuration but is intentionally not wired up in this scaffold. No API calls, UI libraries, or CSS frameworks are included.
