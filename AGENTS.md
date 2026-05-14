# AGENTS.md

## Project Overview

**Zarf** is an air-gapped Kubernetes deployment tool written in Go. It packages applications (Helm charts, manifests, container images, git repos) into a single distributable archive, then deploys them into disconnected clusters. The module path is `github.com/zarf-dev/zarf`.

## Essential Commands

| Task | Command |
|------|---------|
| Build (current OS/arch) | `make build` |
| Build Linux amd64 | `make build-cli-linux-amd` |
| Unit tests (with race + coverage) | `make test-unit` |
| Unit tests (fast, no race) | `make test-unit-quick` |
| Run single package tests | `go test -v ./src/pkg/packager/...` |
| Lint Go code | `make lint-go` (runs `golangci-lint run`) |
| Generate docs & schema | `make docs-and-schema` |
| Build all examplse | `make build-examples` |
| Init package creation | `make init-package` |
| E2E (requires cluster) | `make test-e2e-with-cluster` |
| E2E (no cluster) | `make test-e2e-without-cluster` |
| Pre-commit | `pre-commit run --all-files` |
| Format | `golangci-lint fmt` |

The default built binary goes to `build/zarf` (or `build/zarf-arm` for ARM).

## Code Organization

```
src/
  cmd/              # CLI commands (cobra)
    package.go      # zarf package {create,deploy,inspect,publish,pull,remove,sign,verify,list,mirror-resources}
    initialize.go   # zarf init
    root.go         # root command, Execute entrypoint, logger setup, feature flags
    viper.go        # Viper config key constants
    tools.go         # zarf tools (vendored kubectl, helm, k9s, crane, syft, yq)
    ...
  api/
    v1alpha1/       # Core domain types: ZarfPackage, ZarfComponent, ZarfChart, ZarfManifest
    internal/v1beta1/  # v1alpha1 -> v1beta1 translation layer (wraps v1alpha1 with JSON marshal/unmarshal)
  pkg/
    packager/       # High-level operations: Create, Deploy, Load, Publish, Pull, Remove, Inspect, Mirror
      layout/         # PackageLayout: manages on-disk layout of unpacked package (decompress, verify, assemble, archive)
      filters/        # Component filtering (by OS, flavor, user selection, diff)
      load/           # Package definition loading (imports, validation)
      actions/        # Component action execution
    zoci/           # OCI operations: push/pull/fetch/copy packages and layers
    cluster/        # Kubernetes cluster interaction
    images/         # Container image pull/push/unpack
    logger/         # Structured logging via log/slog (context-stored, format: console/json/dev)
    variables/      # Variable templating (###ZARF_PKG_VAR_*, ###ZARF_PKG_TMPL_*)
    value/          # Values file parsing and merging (dot-path access)
    feature/        # Feature flag system (alpha/beta/GA/deprecated lifecycle)
    state/          # Cluster state tracking (what's deployed)
    utils/          # Shared utilities (exec, YAML, cosign, I/O, network)
    transform/      # Image/repo/artifact reference transformation
    pki/            # PKI/certificate generation
    lint/           # Package linting rules
    ...
  internal/         # Internal packages (not imported externally)
    packager/helm/    # Helm chart operations (install, template, image extraction, post-render)
    packager/template/ # Package-level templating
    packager/requirements/ # Package version requirement checks
    agent/            # In-cluster Zarf agent (webhook, proxy)
      hooks/            # Flux/ArgoCD resource mutation hooks
    git/              # Git operations
    healthchecks/     # Post-deploy health checks
    template/         # Generic Go-template rendering (sprig-based)
    ...
  test/
    e2e/            # E2E tests (numbered .go files, run in order)
    testutil/       # Shared test helpers (TestContext, OCI registry)
    common.go       # E2E test framework (ZarfE2ETest struct)
  types/            # Shared runtime types (RemoteOptions, ZarfCommonOptions)
  config/           # Global config, constants, default values
    lang/           # All user-facing strings
```

## Architecture & Control Flow

### Package Lifecycle
1. **Load**: `packager.LoadPackage` fetches from local file, OCI, or deployed cluster → `layout.PackageLayout`
2. **Create**: Reads `zarf.yaml` definition → pulls images/repos → assembles archive (`.tar.zst`) or pushes to OCI
3. **Deploy**: Loads package → template + push images → install Helm charts / apply manifests → health checks
4. **Publish**: Loads package → pushes to OCI registry (optionally re-signing)
5. **Remove**: Loads package definition → uninstalls Helm releases → removes tracked resources

### CLI Structure
- All commands use `cobra.Command` with `RunE` returning `error`
- `PersistentPreRunE` on root handles logging, feature flags, viper config
- Logger is stored in `context.Context` via `logger.WithContext(ctx, l)` and retrieved via `logger.From(ctx)`
- `config.CLIArch` is set by the `--architecture`/`-a` persistent flag; use `config.GetArch()` to resolve
- Viper config reads from `zarf-config.toml` (or `zarf-config.yaml`) in the working directory, home dir, or `/etc/zarf/`

### Key Interfaces / Contracts
- `filters.ComponentFilterStrategy` — filters components during load
- `layout.VerificationStrategy` — `VerifyIfPossible`, `VerifyAlways`, `VerifyNever`
- `types.RemoteOptions` — `PlainHTTP`, `InsecureSkipTLSVerify` for OCI/HTTP operations

## Logging

- Use `logger.From(ctx)` to get the logger from context
- Use `logger.Default()` only as a fallback when context is unavailable
- Supported formats: `console` (default), `json`, `dev`
- Log levels: `debug`, `info`, `warn`, `error` (also accepts `trace` → debug for backwards compat)
- Without a logger in context, logs are discarded — always pass a context with a logger

## Testing

### Unit Tests
- Use `testutil.TestContext(t)` to create a context with logger attached
- Table-driven tests are the standard pattern
- Use `require` (testify) for assertions
- Mock Kubernetes with `k8s.io/client-go/kubernetes/fake`
- Test OCI packages with `testutil.OCIRegistry(t)` for in-process OCI registries
- Test files are in `testdata/` directories alongside the test files

### E2E Tests
- Located in `src/test/e2e/`, numbered for execution order (`00_*.go`, `01_*.go`, etc.)
- Run the compiled binary as a subprocess via `e2e.Zarf(t, args...)` / `e2e.ZarfInDir(t, dir, args...)`
- Always pass `--log-format=console --no-color` (automatically added)
- Require the init package pre-built via `make init-package`
- For individual test files: `cd src/test/e2e && go test -v -run TestFoo ./main_test.go ./XX_foo_test.go`

## Conventions & Gotchas

### File Headers
Every `.go` file must start with the SPDX header. The linter (`goheader`) enforces this:
```go
// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2021-Present The Zarf Authors
```

### **Critical: Logger in Context**
The `logger.From(ctx)` function returns a **discarding logger** if no logger is stored in the context. This is a common source of "no output" bugs. Always ensure a logger is placed in context via the root command's `PersistentPreRunE`.

### **Tools Command Order**
Tools commands must be added to rootCmd **first** (before other subcommands). This prevents viper config defaulting from injecting values into `zarf tools update-creds`. See `NewZarfCommand()` in `src/cmd/root.go:196`.

### **Architecture Handling**
- `config.CLIArch` is overridable via the `--architecture`/`-a` flag
- `config.GetArch()` resolves with priority: CLI flag → package metadata → `runtime.GOARCH`
- Special architecture `skeleton` is used for skeleton packages (`v1alpha1.SkeletonArch`)

### **Deprecated Flags**
Deprecated flags use `cmd.Flags().MarkDeprecated(...)` and backward-compat handling via `preRun` methods that check `cmd.Flags().Changed("old-flag")`. The pattern:
```go
func (o *options) preRun(cmd *cobra.Command, _ []string) {
    if cmd.Flags().Changed("skip-signature-validation") {
        logger.Default().Warn("--skip-signature-validation is deprecated ...")
        if cmd.Flags().Changed("verify") {
            return
        }
        o.verify = !o.skipSignatureValidation
    }
}
```

### **Linter Exclusions**
- `src/cmd/helm/` is excluded from all linting (it's vendored Helm CLI code)
- `third_party$`, `builtin$`, `examples$` are excluded paths

### **Component Filtering**
Components can be filtered out pre-deploy by OS (`filters.ByLocalOS`), by user selection (`filters.ForDeploy` / `filters.BySelectState`), or by differential packaging (`filters.ByDifference`). Always combine filters via `filters.Combine(...)`.

### **Signature Verification**
- Signature verification always runs, but by default uses `VerifyIfPossible` — it logs warnings but doesn't fail
- Use `--verify` to enforce (`VerifyAlways`)
- Use `layout.VerifyNever` only internally (e.g., when signing)

### **Go Module Replace Directives**
Two `replace` directives in `go.mod` are intentional and should not be removed:
- `github.com/xeipuuv/gojsonschema` → a fork pending upstream merge
- `modernc.org/sqlite` → pinned to avoid breaking changes in v1.33.0

### **Build Flags**
The Makefile injects version info via `-ldflags` using `BUILD_ARGS`. These include Zarf version, Kubernetes component version, and vendored tool versions (k9s, crane, syft, archives, helm). The `CLIVersion` defaults to `git describe --tags`.

### **Vipper Config Keys**
Config keys follow a flat pattern (e.g., `log_level` in TOML) but Go constants are PascalCase prefixed with `V` (e.g., `VLogLevel = "log_level"`). All viper config key constants are in `src/cmd/viper.go`.

### **Vendored Tools**
Zarf embeds several CLI tools accessible via `zarf tools ...`:
- `kubectl`, `helm`, `k9s`, `crane` (container image tool), `syft` (SBOM), `yq` (YAML processor)
- These are excluded from normal flag parsing to avoid interference with Zarf's own flags
