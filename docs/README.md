# FlexInfer Documentation

This directory is the canonical documentation for `services/flexinfer`.

## Getting Started

| Guide | Description |
|-------|-------------|
| [Installation](INSTALL.md) | Install FlexInfer on your cluster |
| [Quickstart](user/quickstart.md) | Deploy your first model in 5 minutes |
| [Configuration](CONFIGURATION.md) | Environment variables and settings |

## User Guides

| Guide | Description |
|-------|-------------|
| [Models (v1alpha2)](user/models-v1alpha2.md) | Creating and managing Model resources |
| [Proxy & Requests](user/proxy.md) | Sending inference requests |
| [API Compatibility](user/api-compatibility.md) | OpenAI API compatibility |
| [Routing](user/routing.md) | Session affinity, prefix routing, load balancing |
| [GPU Sharing](user/gpu-sharing.md) | Time-sharing GPUs between models |
| [Caching](user/caching.md) | Model weight caching strategies |
| [Operations](user/operations.md) | Day-2 operations and troubleshooting |

## Developer Guides

| Guide | Description |
|-------|-------------|
| [Local Development](dev/local-dev.md) | Setting up a dev environment |
| [Architecture](dev/architecture.md) | System design and components |
| [Backends](dev/backends.md) | Supported inference backends |
| [Testing](dev/testing.md) | Running tests |
| [Release & Images](dev/release.md) | Building and releasing |

## Specifications

| Spec | Description |
|------|-------------|
| [CRDs](specs/crds.md) | Custom Resource Definitions |
| [Proxy API](specs/proxy-api.md) | Proxy HTTP endpoints |
| [Scheduler Extender](specs/scheduler-extender.md) | Kubernetes scheduler integration |
| [Labels & Annotations](specs/labels-and-annotations.md) | Resource metadata conventions |
| [Metrics](specs/metrics.md) | Prometheus metrics reference |

## Planning

| Document | Description |
|----------|-------------|
| [Feature Inventory](planning/feature-inventory.md) | Current feature status |
| [Next Roadmap](planning/next-roadmap.md) | Upcoming work |
| [Phase 1](planning/phase-1-controller-api-hardening.md) | Controller & API hardening |
| [Phase 2](planning/phase-2-serverless-activator-hardening.md) | Serverless hardening |
| [Phase 3](planning/phase-3-routing-performance.md) | Routing & performance |
| [Phase 4](planning/phase-4-operational-polish.md) | Operational polish |
| [Phase 5](planning/phase-5-multi-cluster.md) | Multi-cluster (future) |

## Quick Links

- **Need help?** Start with [Quickstart](user/quickstart.md) then [Operations](user/operations.md)
- **Debugging issues?** See troubleshooting in [Operations](user/operations.md#troubleshooting-decision-tree)
- **API reference?** See [CRDs](specs/crds.md) and [Proxy API](specs/proxy-api.md)

## Site Integration

These docs are intentionally written to be "site-syncable" (plain Markdown, optional YAML frontmatter).
`services/flexinfer-site` can copy and render them as part of the playground/docs experience.

Navigation is defined in `docs/nav.yaml`.
