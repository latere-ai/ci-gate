# Security

## Reporting a vulnerability

Report one privately, through
[GitHub's private vulnerability reporting](https://github.com/latere-ai/ci-gate/security/advisories/new)
or by email to security@latere.ai. Do not open a public issue for it. The
[organization's security policy](https://github.com/latere-ai/.github/blob/main/SECURITY.md)
sets the response times, how disclosure works, and the safe harbor for
research done in good faith.

ci-gate is before v1.0.0, so there is one release line: a fix ships in the
next release, and the [changelog](CHANGELOG.md) says which release has it.

## What is protected

`lateregate` runs on a developer's machine and on CI runners, with the
permissions of whoever runs it. It reads the repository it checks, talks
to the GitHub API with the caller's token, downloads pinned tools, and
starts and removes containers. It commits to the following, and the named
tests check each property. Each test is named as `package.Test`, and the command's own tests as `lateregate.Test`.

| Property | Tests |
|---|---|
| A release tag cannot reach the remote without its changelog section, whatever form the push takes: the pre-push hook reads the remote ref and the pushed commit, not the local name. | `changelog.TestPrepushRefusesAReleaseTagWithoutASection`, `changelog.TestPrepushChecksATagPushWhateverTheLocalRef`, `changelog.TestCutsPushHandsTheHookTheTag` |
| `release` cuts only from the tip of origin's default branch, only a valid version newer than every tag on origin, and pushes the release commit and the tag together or not at all. A rejected push leaves the checkout as it was. | `changelog.TestCutRefusesACheckoutThatIsNotOriginsTip`, `changelog.TestCutComparesOriginsTagsBySemver`, `changelog.TestReleaseRefusesAVersionThatIsNotSemver`, `changelog.TestCutPushesTheBranchAndTheTagAtomically`, `changelog.TestCutUndoesARejectedPush`, `changelog.TestARaceLostAtThePushLeavesNothing` |
| Before a cut, `release` refuses while a CI run in the tag's window is red or a run at `HEAD` has not finished, and `-force-red` prints every finding it overrides. | `greencut.TestEveryRedConclusionRefusesTheCut`, `greencut.TestAnUnfinishedRunAtHeadRefusesOverAnOlderGreenRun`, `greencut.TestForceRedPrintsWhatItOverrides` |
| The GitHub token is sent only to the API host and is not followed to the host a job log redirects to. | `greencut.TestTheJobLogIsReadThroughARedirect` |
| A tool `lateregate` downloads (kubectl, kind, helm, promtool, elan) is checked against a SHA-256 the binary carries before it is installed, and a download over its size limit is refused. golangci-lint, govulncheck and actionlint run through `go run` at pinned versions, so the Go checksum database verifies them. | `e2e.TestAToolIsFetchedCheckedAndCached`, `e2e.TestAToolThatCannotBeFetchedIsRefused`, `e2e.TestADownloadOverTheLimitIsRefused`, `gates.TestRulesRefusesWhatItCannotTrust`, `gates.TestLeanNeedsAPinnedArchiveAndACache`, `golangci.TestLintRendersThenRunsThePinnedLinter`, `gates.TestVulnRunsThePinnedScanner` |
| A container sweep removes only containers and networks whose owner process, on this machine, has exited. A container whose owner is alive is never touched, however old it is, an unlabeled or malformed one is left alone, and one that carries the current run's label is left to that run's own check. | `containers.TestSweepRemovesOnlyWhatAnExitedOwnerOnThisHostLeft`, `containers.TestOwnerReadsOnlyAWellFormedLabel`, `reap.TestSweepRemovesWhatAnExitedProcessLeft`, `reap.TestSweepLeavesWhatThisRunLabelledToTheRunsCheck` |
| The suite's check after a run removes only what carries that run's own label, and reports each one, an interrupted run included. | `gates.TestSuiteRemovesAndNamesWhatTheRunLeft`, `containers.TestLeftoversRemovesOnlyWhatCarriesTheRunLabel`, `gates.TestSuiteNamesWhatAHelperSweepFoundOfThisRun`, `gates.TestSuiteReleasesTheSandboxAndChecksContainersWhenInterrupted` |
| The `tempdir` sandbox never follows a link out of itself, belongs to one checkout, is released when the run is interrupted, and a leak under it is reported on every run until a run leaves nothing. | `gates.TestTempDirNeverFollowsALink`, `gates.TestSandboxNeverLandsOnAnotherCheckoutsLock`, `lateregate.TestInterruptedTempDirGateReleasesItsSandbox`, `gates.TestTempDirReportsALeakOnEveryRunOverTheSameTree` |
| The end-to-end services `lateregate` starts listen on 127.0.0.1 only, and are removed when the suite ends, passes or fails. Only a container the run itself started is removed. | `e2e.TestPostgresStartsAndGivesItsURL`, `e2e.TestServicesReachTheSuiteAndAreRemoved`, `e2e.TestAServiceThatFailsToStartIsRemoved`, `e2e.TestARealPostgresSuiteRunsAndLeavesNoContainer`, `e2e.TestStopLeavesAContainerItDidNotStart` |
| `deploy` rolls out the tag's own tree, taken from git, pinned to the image digest the registry reports. A tag name is checked against the version pattern before it reaches any command, and the kubeconfig it writes is removed on every exit, an interrupt included. An archive entry that would land outside the tree, or pass through a link, is refused. | `ship.TestDeploySetsTheDigestPodmanPulled`, `ship.TestDeployRefusesAnImageNotInTheRegistry`, `ship.TestDeployRefusals`, `ship.TestDeployRemovesTheKubeconfigOnASignal`, `ship.TestExtractRefusesAPathOutsideTheTree`, `ship.TestExtractRefusesAnEntryUnderASymlinkItCreated` |
| `deploy-credential` never prints the rollout token and never puts it on a command line. | `ship.TestDeployCredentialStoresTheIdentitysKubeconfig` |
| `push` moves the `latest` image tag only for the newest release on origin. | `ship.TestPushLeavesLatestWhereItIsForAnOlderTag`, `ship.TestPushMovesLatestForTheNewestTagOnOrigin` |
| `action-pins` holds every third-party action to one reviewed commit. | `actionpins.TestCheck` |

## What is not defended

- **The code being checked.** The bar runs the repository's own code:
  `go test`, `go vet`, the package scripts the `frontend` gate runs
  through Bun, Lake builds, and the steps a `ship` block names. Running
  `lateregate` on a checkout runs that checkout's code with your
  permissions. Do not run it on a repository you do not trust outside a
  sandbox.
- **The repository's own decisions.** `.lateregate.yaml` can waive any
  gate until a date it writes, and `release.require_green: false` turns
  the CI guard off. The bar trusts the configuration of the repository it
  checks.
- **Skipping the hooks.** `git push --no-verify` skips the pre-push, and a
  checkout without `core.hooksPath` set runs no hook. CI is where the bar
  holds.
- **`-force-red` and repositories that ship from a workstation.**
  `-force-red` cuts over a red CI, and a repository with a `ship` block
  reads no CI before a cut.
- **The token's destination.** The CI guard sends the token from
  `GH_TOKEN`, `GITHUB_TOKEN` or `gh auth token` to `GITHUB_API_URL`. Point
  that at a host you trust, over HTTPS.
- **The live check's environment.** The `ship` live check runs with the
  operator's environment, without `SERVICE_TOKEN`. Any other credential in
  that shell reaches the check's script.
- **Container ownership across machines.** A container's owner is a
  process id and a host name. When a Mac's host name changes, a container
  started under the old name is never swept. Two CI jobs that share a
  container engine and a host name but not a process namespace can see
  each other's live containers as abandoned; give such jobs distinct host
  names.
- **Secrets in process listings.** The end-to-end services get their
  generated passwords on the container engine's command line, which other
  users of the machine can read while that command runs.
- **A tool already in the cache.** A downloaded tool is checked once,
  when it is installed. Anyone who can write to your user cache directory
  can replace it.
- **What a laptop run reads.** On a laptop the bar checks what changed and
  what imports it. A test that reads a file of another package by path
  can pass there and fail in CI; `-all` reads the whole tree.

## Audit

ci-gate has had no external security audit.

## Supply chain

ci-gate ships as source: a repository runs it through the `tool` line in
its `go.mod`, and the Go tools check the module against the
[checksum database](https://sum.golang.org), so a tag cannot change after
it is published. A release is a version tag on `main` with release notes.
The module depends on `github.com/goccy/go-yaml`, `golang.org/x/mod`,
`golang.org/x/tools` and `golang.org/x/sync`. The tools it runs are pinned
in the binary: golangci-lint, govulncheck and actionlint by module
version, the downloaded tools by SHA-256, and the end-to-end service
images by digest. The repository runs its own bar, the vulnerability scan
included, on every push. To check what you have:

```sh
go mod verify
go tool lateregate vuln
```
