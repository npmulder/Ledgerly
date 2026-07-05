# API Client Conventions

## Query Keys

TanStack Query keys use this tuple shape:

```ts
[module, resource, params]
```

- `module`: platform or domain module name, for example `platform`, `ledger`, or `invoicing`.
- `resource`: stable resource family or endpoint noun, for example `healthz` or `invoices`.
- `params`: a JSON-serializable object containing filters, route params, pagination, and feature flags. Use `{}` when there are no params.

Keep params objects stable and explicit. Do not use positional params or omit the third tuple element, because consistent key shapes make invalidation predictable across modules.
