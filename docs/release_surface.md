# Release Surface Contract

This document explains how the service release surface is tracked. The
machine-readable contract is `docs/openapi.yaml`; the focused behavioral
contracts in `docs/contracts/` remain the source for detailed job, workflow,
schema, dataset lifecycle, sidecar, and worker semantics.

## Stable API Groups

The OpenAPI contract covers these external HTTP groups:

- health and readiness: `/healthz`, `/readyz`, `/v1/version`
- dataset registry and lifecycle: `/v1/datasets`,
  `/v1/datasets/import`, `/v1/datasets/{dataset}`,
  `/v1/datasets/{dataset}/artifacts`, delete, rebuild
- schema management: `/v1/schemas`, `/v1/schemas/validate`,
  `/v1/datasets/{dataset}/schema`
- operation history: `/v1/dataset-operations`,
  `/v1/datasets/{dataset}/operations`
- sidecar status: `/v1/sidecars`, `/v1/sidecars/vector/health`
- retrieval: `/v1/retrieve`
- jobs: `/v1/jobs`, job detail/events/cancel
- workflows: `/v1/workflows`, workflow detail/steps/events/cancel

## Contract Rules

- New public endpoints should be added to `docs/openapi.yaml` in the same
  change that exposes them.
- Response envelopes with `schema_version` should remain compatible with their
  matching detailed contract files.
- API errors should use the shared `{ "error": { "code", "message" } }`
  envelope unless an endpoint-specific contract explicitly says otherwise.
- OpenAPI schemas may intentionally use `additionalProperties: true` for
  typed job specs and workflow specs while those worker boundaries evolve.
- `docs/openapi.yaml` is not a substitute for golden/service smoke gates; it is
  the release-surface inventory and SDK/frontend input.

## Validation Gate

Phase 24 release checks should run `scripts/check_release_surface.py`. The
checker intentionally uses only the Python standard library so clean clones do
not need PyYAML just to validate the release inventory. It verifies that
`docs/openapi.yaml` declares every public route registered by
`internal/svc.Service.Routes`, that key versioned response contracts appear in
the OpenAPI file, and that `.env.example` plus local/smoke scripts expose the
expected operational entrypoints. End-to-end behavior remains covered by
service smoke and regression tests.
