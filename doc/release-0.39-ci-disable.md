# Release 0.39 CI Disablement Recommendation

## Summary

This document provides an assessment and recommendation regarding the Prow CI jobs for the release-0.39 branch, which have been experiencing persistent failures.

## Background

The release-0.39 branch Prow CI jobs (`pull-kubemacpool-unit-test-v0.39` and `pull-kubemacpool-e2e-k8s-v0.39`) have been consistently failing since June 2026 due to infrastructure compatibility issues:

1. **Unit test failures**: Docker/Podman daemon fails to start within the Prow container environment
2. **E2E test failures**: 3-hour timeouts during kubevirtci cluster image download or the same Docker daemon failure

## Assessment

### Release Branch Status

- **Current release**: 0.51 (as of July 2026)
- **Release 0.39 age**: Significantly outdated with 12 newer release versions
- **Maintenance activity**: Only receiving automated Renovate security dependency updates
- **No active development**: No feature work or bug fixes being applied to this branch

### Infrastructure Compatibility

The release-0.39 branch uses:
- `KUBEVIRT_PROVIDER=k8s-1.23`
- `KUBEVIRTCI_TAG=2207242237-3a5c590` (from July 2022)

This 4-year-old kubevirtci image has compatibility issues with current Prow infrastructure:
- Podman socket initialization failures
- Image download timeouts
- Potential kernel/cgroup incompatibilities with modern CI nodes

### Cost-Benefit Analysis

**Costs of maintaining CI**:
- Significant engineering effort to update kubevirtci images on a release branch
- Ongoing maintenance burden as infrastructure continues to evolve
- CI resource consumption for a branch receiving minimal updates

**Benefits of maintaining CI**:
- Validates security dependency updates (which are often low-risk)
- Limited value given no active development on this branch

## Recommendation

**Disable Prow CI jobs for the release-0.39 branch.**

### Rationale

1. **Branch lifecycle**: With 12 newer releases available, users should migrate to supported versions rather than rely on 0.39
2. **Minimal activity**: Only automated security updates are being applied, which can be validated through other means if needed
3. **Infrastructure compatibility**: Fixing the CI would require substantial effort to update the kubevirtci environment on a release branch
4. **Resource efficiency**: Freeing CI resources for actively maintained branches provides better value

### Implementation

The Prow job configurations are maintained in the [kubevirt/project-infra](https://github.com/kubevirt/project-infra) repository. To disable these jobs:

1. Locate the job definitions in `kubevirt/project-infra` repository (typically under `github/ci/prow-deploy/files/jobs/`)
2. Remove or comment out the `pull-kubemacpool-unit-test-v0.39` and `pull-kubemacpool-e2e-k8s-v0.39` job configurations
3. Submit a PR to project-infra with a reference to this assessment

### Alternative Approach

If there is a strong need to continue receiving security updates on release-0.39:

1. **Manual validation**: Security updates can be reviewed and tested manually when needed
2. **Selective CI**: Consider running CI only on-demand rather than for every PR
3. **Infrastructure update**: Upgrade the kubevirtci version for release-0.39, though this adds maintenance risk to a stable branch

## Conclusion

Given the age of the release-0.39 branch, the availability of 12 newer releases, and the minimal development activity, disabling the Prow CI jobs is the most practical and cost-effective solution. Users requiring security updates should migrate to a supported release branch.

## References

- GitHub Issue: #645
- Prow instance: https://prow.ci.kubevirt.io
- Failed job examples:
  - [PR #639 unit-test](https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/pr-logs/pull/k8snetworkplumbingwg_kubemacpool/639/pull-kubemacpool-unit-test-v0.39/2064026453862780928)
  - [PR #639 e2e](https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/pr-logs/pull/k8snetworkplumbingwg_kubemacpool/639/pull-kubemacpool-e2e-k8s-v0.39/2064026453976027136)
