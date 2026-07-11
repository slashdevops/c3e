# c3e documentation

`c3e` (**C**ache with **C**ascading **E**xpiration **E**ngine) is a
dependency-aware caching layer for Go built on top of
[Valkey](https://valkey.io/) (Redis-compatible).

The root [README](../README.md) covers installation and a first example. These
pages go deeper:

| Page | What it covers |
| --- | --- |
| [Architecture](architecture.md) | The three-layer design, the stale-while-revalidate read path, the key scheme, and the dependency reverse index — with diagrams. |
| [Usage](usage.md) | Worked examples: basic, type-safe, dependency tracking, `GetOrFetch`, and advanced composition patterns. |
| [Configuration](configuration.md) | Every `SafeCacheManagerConfig` field, its validation, and how to choose values. |
| [Observability](observability.md) | The injectable logger and the metrics/tracing hooks. |
| [Best practices](best-practices.md) | Do / don't guidance and a troubleshooting guide. |
| [Limitations & semantics](limitations.md) | The invalidation contract, consistency model, error handling, and backend constraints — read this before relying on it in production. |

The Go API reference lives on
[pkg.go.dev/github.com/slashdevops/c3e](https://pkg.go.dev/github.com/slashdevops/c3e)
(generated from the package doc in `docs.go`).
