# kubemacpool Code Review Style Guide

This guide captures the review standards established by the project's maintainers. All
code review feedback should align with these principles.

## PR Structure and Scope

- **Keep PRs focused**: Each PR should address a single concern. Defer tangential
  improvements, refactors, and unrelated module updates to follow-up PRs.
- **Commit discipline**: Each commit should be independently reviewable and should not
  leave the codebase in a broken intermediate state.
- **Commit ordering matters**: Order commits so reviewers can understand the
  progression. Preparatory refactors come first, the core change comes last.
- **Commit messages**: Use conventional commit format. Titles should be concise,
  correctly spelled, and properly spaced. If a commit prepares for a later change
  in the same PR, say so in the message.

## Go Idioms

- **`context.Background()` over `context.TODO()`**: `context.TODO` is deprecated;
  always use `context.Background()`.
- **Error wrapping**: Always wrap errors with `fmt.Errorf("context: %w", err)`.
  Never swallow errors silently. If an error is intentionally ignored, document why.
- **Use standard library and apimachinery utilities**: Prefer `bytes.Compare`,
  `net.ParseMAC`, `k8s.io/apimachinery/pkg/util/sets`, and similar existing
  packages over custom implementations. Do not write helper functions that
  duplicate stdlib functionality.
- **Inline single-use functions**: If a function has exactly one caller and is
  short, inline it. Small helper functions that only add indirection hurt
  readability.
- **Avoid boolean function parameters**: Passing a bool to control function
  behavior is a code smell. Pass concrete parameters or use options instead.
- **Value types over pointers when mutation is not needed**: If a struct is not
  expected to be mutated, pass it by value. This eliminates nil-pointer concerns.
- **Package naming**: Follow Go conventions. Avoid stuttering
  (e.g., `tlsconfig.NewConfig()`). Package names should be short, lowercase, and
  meaningful.
- **Godoc conventions**: Follow <https://go.dev/blog/godoc>. Comments explain
  "why", not "what".
- **Named constants**: Replace repeated string literals with named constants.
- **Remove unused code**: Do not leave unused flags, env vars, imports, or dead
  code. If something is not used, remove it.
- **Go module hygiene**: Do not mix module replacement/addition with feature work.
  If replacing one utility package with another (e.g., `k8s.io/utils/pointer` with
  `k8s.io/utils/ptr`), do it in a dedicated PR to keep the codebase consistent.
- **go.mod version**: Follow the Go version that imported `k8s.io` modules support.
  Understand the distinction between `go` directive (minimum version) and
  `toolchain` directive (build toolchain version) in go.mod.

## Error Handling and Defensive Programming

- **Fail explicitly**: Prefer failing loudly over silent fallback behavior. If an
  operation is unsupported, return a clear error.
- **Detect invalid states**: If a value (e.g., pool size) is negative or otherwise
  invalid, report a configuration error rather than silently proceeding.
- **Nil guards**: Add nil checks where pointer dereferences could panic, but prefer
  value types when possible to avoid this class of bug entirely.
- **Fail-open decisions must be documented**: If a code path intentionally continues
  on error (fail-open), add a comment explaining the rationale and consequences.

## Naming and Clarity

- **Meaningful names**: No meaningless enumerations (e.g., `IMAGE_1`, `IMAGE_2`).
  Names must convey intent.
- **Avoid package name collisions**: Do not name a package the same as a standard
  library package (e.g., naming a package `tls` when `crypto/tls` is imported).
- **Accurate comments**: Comments must match the code. If code uses OR logic, do not
  write "BOTH" in the comment. Remove stale comments.

## Testing

- **Test behavior, not implementation**: Test through public APIs. Test what
  kubemacpool does, not what Kubernetes does.
- **Tests must be fast**: Do not activate VMs or trigger heavy operations when
  lighter alternatives exist. MAC allocation happens on VM create/update, not
  on VM start.
- **Use Gomega matchers**: Use `Eventually`, `Consistently`, `WithPolling`,
  `WithTimeout`, and proper matchers instead of manual polling loops or
  traditional Go error checking.
- **Ginkgo assertions exclusively**: Do not mix Ginkgo assertions with traditional
  `if err != nil` error checking. Use Ginkgo/Gomega consistently.
- **Assert final state, not intermediate state**: Wait for the actual desired end
  state rather than asserting intermediate states that may be transient.
- **Precise test setup**: Ensure the test environment matches exactly what the test
  expects. For pool init tests, the VMI list must be precisely controlled.
- **Cleanup with defer**: Use `defer func() { delete... }()` for test resource
  cleanup.

## Logging and Observability

- **Log levels matter**: Use `debug` for routine operations. Keep `info` level
  meaningful and low-noise.
- **Add diagnostic logging**: When adding new controllers or features, include
  logging of relevant state (e.g., NetworkPolicies, endpoints) to aid future
  debugging.
- **Suppress noisy output**: Use `--quiet` or equivalent flags in scripts and
  helpers to reduce log noise.
- **Prometheus alerts**: Add a `for` duration (e.g., `5m`) to alerts to prevent
  flapping and give the system time to self-heal.

## Architecture and Safety

- **Do not change unrelated behavior**: If a PR is about collision detection, do
  not change webhook rejection behavior in the same PR. Match the scope of changes
  to the PR's stated purpose. Same for comment reviews. Keep it in the change code
  context unless especially needed.
- **Consider maintenance burden**: Before adding formatting, parsing, or
  transformation code, ask whether existing tooling can handle it and whether the
  team will need to maintain it long-term.

## Documentation

- **Avoid implementation details in user-facing docs**: Use high-level descriptions
  (e.g., "MAC addresses specified in spec") instead of exposing internal field
  paths.
- **Accurate and clear wording**: Describe features in terms of what users need to
  know (e.g., "Prometheus monitoring support") rather than internal mechanisms.

## Consistency

- **Follow existing patterns**: Use the same namespace strategy, comment alignment,
  kustomize conventions, and error-handling patterns as the rest of the codebase.
- **License headers**: All new Go source files must include the Apache 2.0 license
  header.
