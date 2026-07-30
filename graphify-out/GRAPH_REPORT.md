# Graph Report - .  (2026-07-30)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 1696 nodes · 4352 edges · 93 communities (81 shown, 12 thin omitted)
- Extraction: 87% EXTRACTED · 13% INFERRED · 0% AMBIGUOUS · INFERRED: 585 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `fb6d1951`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- vault_test.go
- .WriteFile
- hooks_test.go
- tui.go
- discovery.go
- Output
- mcp.go
- Render
- memory
- demo
- bootstrap_test.go
- main.go
- Open
- graph_test.go
- scan.go
- usage_test.go
- Store
- cmdReport
- fixture
- run
- spec_test.go
- Scan
- cmdDoctor
- Verdict
- fixture
- brief.go
- cmdScanDiff
- properties
- properties
- BashWriteTargets
- plugin.json
- type
- gate
- Parse
- Spec
- gitx.go
- worktree.go
- declared_test.go
- store.go
- spec.go
- Run
- properties
- prFixture
- batten.schema.json
- on_exceed
- cmdPhase
- normPath
- properties
- TestTheLockIsSharedBetweenWorktrees
- properties
- domain
- evidence
- retryTransient
- matrix-replica.sh
- artifacts
- pattern
- tokens_per_run
- TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn
- specFrom
- unit
- marketplace.json
- Run
- matrix-demo.sh
- enforcement
- phases
- quota_pct_per_run
- anchor
- diff_from
- imputed_usd_per_run
- GateShortfall
- TestEveryEdgeRelationReadHasAProducer
- claude-code/scripts/bootstrap.sh
- scripts/bootstrap.sh
- capabilities
- project
- version
- release-check.sh
- install/install.go
- batten
- build-plugin.sh script
- replica-ui.sh
- Input
- RawMessage
- Reader
- Handler
- Input
- Context
- V
- github.com/ArthurZizumbo/batten
- runCheck

## God Nodes (most connected - your core abstractions)
1. `Store` - 115 edges
2. `Run` - 79 edges
3. `Spec` - 77 edges
4. `Open()` - 57 edges
5. `captureStdout()` - 51 edges
6. `inDir()` - 50 edges
7. `Output` - 34 edges
8. `gateFixture()` - 28 edges
9. `fixture()` - 27 edges
10. `demo` - 27 edges

## Surprising Connections (you probably didn't know these)
- `writeCost()` --calls--> `UnpricedShare()`  [INFERRED]
  cmd/batten/pr.go → internal/render/render.go
- `prFixture()` --calls--> `LoadFrom()`  [INFERRED]
  cmd/batten/pr_test.go → internal/spec/spec.go
- `prFixture()` --calls--> `Open()`  [INFERRED]
  cmd/batten/pr_test.go → internal/store/store.go
- `reportFixture()` --calls--> `Open()`  [INFERRED]
  cmd/batten/report_test.go → internal/store/store.go
- `cmdIterate()` --calls--> `Refusal()`  [INFERRED]
  cmd/batten/unattended.go → internal/hooks/envelope.go

## Import Cycles
- None detected.

## Communities (93 total, 12 thin omitted)

### Community 0 - "vault_test.go"
Cohesion: 0.08
Nodes (57): eq(), Writer, props(), cell(), domainsOf(), Builder, Edge, Node (+49 more)

### Community 1 - ".WriteFile"
Cohesion: 0.08
Nodes (63): chainedCommand(), chainPath(), command(), RawMessage, Install(), Installed(), IsBatten(), newSettings() (+55 more)

### Community 2 - "hooks_test.go"
Cohesion: 0.09
Nodes (63): Handler, fields(), T, TestADegradedBattenIsMarkedRetryable(), TestEveryDenialCarriesACodeAndAWayOut(), TestTheProseStaysFirst(), TestTheWriteSetCollisionOffersNoWayThrough(), reasonOf() (+55 more)

### Community 3 - "tui.go"
Cohesion: 0.08
Nodes (48): amounts(), bar(), binding(), ceilingLine(), contentH(), contentW(), criteriaLine(), firstUnmeasurable() (+40 more)

### Community 4 - "discovery.go"
Cohesion: 0.08
Nodes (54): index, installedPlugin, Item, kind, Problem, Source, Agents(), bare() (+46 more)

### Community 5 - "Output"
Cohesion: 0.09
Nodes (31): bashInput, envelope, HookSpecific, Input, Output, writeInput, Handler, AdviseDegraded() (+23 more)

### Community 6 - "mcp.go"
Cohesion: 0.09
Nodes (37): CallToolRequest, Context, exceededSummary(), CallToolResult, joinNotes(), narrowToPhase(), orEmpty(), orEmptyGates() (+29 more)

### Community 7 - "Render"
Cohesion: 0.08
Nodes (42): Detail, Edge, HTMLInput, Node, Canvas, Edge, Node, relColor() (+34 more)

### Community 8 - "memory"
Cohesion: 0.04
Nodes (49): properties, additionalProperties, description, properties, type, description, items, type (+41 more)

### Community 9 - "demo"
Cohesion: 0.10
Nodes (22): checkResult, demo, cmdDemo(), demoDir(), jsonQuote(), meaningfulLine(), mustRun(), onPath() (+14 more)

### Community 10 - "bootstrap_test.go"
Cohesion: 0.16
Nodes (46): buildArchive(), checksumsFor(), copyFile(), gateDenies(), Server, T, mustRun(), readJSON() (+38 more)

### Community 11 - "main.go"
Cohesion: 0.09
Nodes (49): aOrAn(), bar(), checkRunnable(), cmdBudget(), cmdCanvas(), cmdCheck(), cmdClaim(), cmdClose() (+41 more)

### Community 12 - "Open"
Cohesion: 0.16
Nodes (47): cmdInit(), captureStdout(), gitRepoWithSpec(), T, inDir(), jsonString(), runHook(), TestABrokenStoreSaysSoInsteadOfPassingInSilence() (+39 more)

### Community 13 - "graph_test.go"
Cohesion: 0.15
Nodes (40): attemptOf(), domainFixture(), Handler, Input, T, hookInput(), retriesIn(), startAgent() (+32 more)

### Community 14 - "scan.go"
Cohesion: 0.12
Nodes (39): allChecks(), checksFor(), dedup(), deriveDomains(), deriveHarness(), derivePurpose(), deriveStack(), deriveUnit() (+31 more)

### Community 15 - "usage_test.go"
Cohesion: 0.12
Nodes (38): Time, Price(), priceAt(), rateFor(), cacheSplit(), Reader, Parse(), parseFile() (+30 more)

### Community 16 - "Store"
Cohesion: 0.09
Nodes (10): canvasHTML(), DB, Server, newServer(), Serve(), now(), scanNode(), Node (+2 more)

### Community 17 - "cmdReport"
Cohesion: 0.17
Nodes (27): cmdRecover(), commitExists(), gitAt(), isAncestor(), cmdReport(), filesPhrase(), firstPhase(), Builder (+19 more)

### Community 18 - "fixture"
Cohesion: 0.26
Nodes (29): ctx(), fixture(), CallToolResult, Context, T, seed(), TestBudgetReportsUnmeasurableCeilingAsUnavailable(), TestBudgetWithNoDeclaredCeiling() (+21 more)

### Community 19 - "run"
Cohesion: 0.17
Nodes (28): T, TestAnUnmeasuredRunIsNotAZeroSpendRun(), TestARunCarriesItsUnpricedShare(), TestBudgetNeverInventsANumber(), TestMeasureByModelAgreesWithTheRunLedger(), TestMeasureByModelSeparatesUnpricedRequests(), TestMeasureRefusesToConcludeFromTooFewRuns(), TestOverBudgetTripsOnTheMeasurableCeilings() (+20 more)

### Community 20 - "spec_test.go"
Cohesion: 0.16
Nodes (27): T, TestLoadResolvesUnitPlanFromTheSpec(), TestParseCriteria(), TestParseHonorsTheLocatorShape(), TestParseSplitsTheBacklogIntoUnits(), TestParseSurvivesACapturingPattern(), Find(), Load() (+19 more)

### Community 21 - "Scan"
Cohesion: 0.13
Nodes (5): DecisionCount, Edge, MeasureGroup, ModelSpend, Scan

### Community 22 - "cmdDoctor"
Cohesion: 0.22
Nodes (14): dx, checkAnchors(), checkInstall(), checkSpecOnly(), checkStore(), cmdDoctor(), codeGraphFresh(), graphHooks() (+6 more)

### Community 23 - "Verdict"
Cohesion: 0.20
Nodes (18): cmdPR(), Builder, Edge, Node, mermaidClass(), mermaidEscape(), mermaidLabel(), renderPR() (+10 more)

### Community 24 - "fixture"
Cohesion: 0.22
Nodes (20): fixture(), T, TestACheckOnlyRunSaysTheReviewerIsMissing(), TestBattenVaultOverridesTheSpec(), TestExpandHomeLeavesAbsolutePathsAlone(), TestExportHonorsTheDeclaredList(), TestExportWorksForAClosedRun(), TestRunNoteReportsTheWriteSetsThatWereActuallyClaimed() (+12 more)

### Community 25 - "brief.go"
Cohesion: 0.13
Nodes (16): deref(), firstLine(), CallToolResult, budgetOutput, graphOutput, runsOutput, verdictOutput, writeSetOutput (+8 more)

### Community 26 - "cmdScanDiff"
Cohesion: 0.24
Nodes (17): scanReport, cmdScanDiff(), contrastDiff(), countPaths(), normalizeScanPath(), printScanDiff(), equalStrings(), T (+9 more)

### Community 27 - "properties"
Cohesion: 0.10
Nodes (20): description, type, description, type, description, minLength, type, description (+12 more)

### Community 28 - "properties"
Cohesion: 0.11
Nodes (19): description, type, description, maximum, minimum, type, properties, description (+11 more)

### Community 29 - "BashWriteTargets"
Cohesion: 0.20
Nodes (17): BashTarget, BashWriteTargets(), compact(), hasExplicitScriptFlag(), hasInPlaceFlag(), isAssignment(), nonFlagArgs(), shellSplit() (+9 more)

### Community 30 - "plugin.json"
Cohesion: 0.11
Nodes (18): author, name, defaultEnabled, description, displayName, keywords, license, name (+10 more)

### Community 31 - "type"
Cohesion: 0.11
Nodes (18): description, examples, items, type, description, items, type, description (+10 more)

### Community 32 - "gate"
Cohesion: 0.12
Nodes (17): description, items, type, gate, verdict, additionalProperties, dependentRequired, description (+9 more)

### Community 33 - "Parse"
Cohesion: 0.46
Nodes (6): loadForHook(), cleanTitle(), Find(), Load(), Parse(), Unit

### Community 34 - "Spec"
Cohesion: 0.19
Nodes (9): gateReadyToClose(), verdictTreeOf(), coverageFloors(), DiffScope(), phaseBriefing(), sortedDomains(), Phase, Spec (+1 more)

### Community 35 - "gitx.go"
Cohesion: 0.28
Nodes (15): Worktree, Canonical(), ChangedFiles(), CommonDir(), isBattenState(), IsDirty(), IsRepo(), Output() (+7 more)

### Community 36 - "worktree.go"
Cohesion: 0.33
Nodes (15): branchExists(), cmdWorktree(), currentBranch(), gitRun(), mergeDriverRegistered(), registerWorktree(), specRootIn(), worktreeAdd() (+7 more)

### Community 37 - "declared_test.go"
Cohesion: 0.31
Nodes (15): declaredFields(), declaredFieldsFrom(), T, holdsDeclaredFields(), productionIdentUses(), productionSelectors(), repoRoot(), ruleStated() (+7 more)

### Community 38 - "store.go"
Cohesion: 0.16
Nodes (8): firstLineStr(), median(), newRunID(), nullable(), Event, Fenced, OverrideDetail, WriteSetStats

### Community 39 - "spec.go"
Cohesion: 0.17
Nodes (10): Regexp, Budget, Capabilities, CompressionCap, Domain, Gate, GraphCap, MemoryCap (+2 more)

### Community 40 - "Run"
Cohesion: 0.23
Nodes (3): Duration, scanRun(), Run

### Community 41 - "properties"
Cohesion: 0.14
Nodes (14): description, examples, type, description, minLength, type, required, description (+6 more)

### Community 42 - "prFixture"
Cohesion: 0.42
Nodes (12): addBothVerdicts(), between(), T, prFixture(), saveVerdict(), TestAnApprovalCitingNothingIsCalledOut(), TestLabelsWithMermaidSyntaxAreNeutralised(), TestMermaidBlockIsInternallyConsistent() (+4 more)

### Community 43 - "batten.schema.json"
Cohesion: 0.17
Nodes (11): additionalProperties, description, $id, required, $schema, title, type, phases (+3 more)

### Community 44 - "on_exceed"
Cohesion: 0.17
Nodes (12): default, description, enum, type, on_exceed, requires_verdict, description, enum (+4 more)

### Community 45 - "cmdPhase"
Cohesion: 0.33
Nodes (10): cmdPhase(), gitSHA(), headroomAlive(), cmdIterate(), cmdUnattended(), T, nightFixture(), TestNoDeclaredCeilingIsSaidOutLoud() (+2 more)

### Community 46 - "normPath"
Cohesion: 0.26
Nodes (9): normPath(), T, TestEveryMigrationIsAdditive(), TestMigrationAddsVerdictSource(), TestNormPathCaseFold(), TestProbeWriteLockIsHonestAndLeavesNothingBehind(), TestSaveVerdictRejectsOkWithoutEvidence(), TestWriteSetsByRunKeepsNilDistinct() (+1 more)

### Community 47 - "properties"
Cohesion: 0.22
Nodes (10): $ref, additionalProperties, description, type, additionalProperties, description, type, properties (+2 more)

### Community 48 - "TestTheLockIsSharedBetweenWorktrees"
Cohesion: 0.38
Nodes (8): Duration, holderOf(), Lock(), lockFor(), T, TestTheLockIsSharedBetweenWorktrees(), TestTheLockNamesItsHolder(), worktreeOf()

### Community 49 - "properties"
Cohesion: 0.22
Nodes (9): additionalProperties, description, properties, type, description, minimum, type, budget (+1 more)

### Community 50 - "domain"
Cohesion: 0.22
Nodes (9): $defs, domain, phase, additionalProperties, required, type, additionalProperties, type (+1 more)

### Community 51 - "evidence"
Cohesion: 0.22
Nodes (9): description, enum, type, evidence, verdict, description, enum, type (+1 more)

### Community 52 - "retryTransient"
Cohesion: 0.42
Nodes (7): IsTransient(), retryTransient(), T, TestRetryGivesUpOnRealFailuresImmediately(), TestRetryStopsRatherThanHanging(), TestRetrySucceedsOnceTheContentionClears(), TestTransientIsToldApartFromBroken()

### Community 53 - "matrix-replica.sh"
Cohesion: 0.36
Nodes (7): bad(), BATTEN_DB, check(), hook(), ok(), pay(), matrix-replica.sh script

### Community 54 - "artifacts"
Cohesion: 0.25
Nodes (8): description, pattern, type, additionalProperties, description, examples, type, artifacts

### Community 55 - "pattern"
Cohesion: 0.25
Nodes (8): description, examples, minLength, type, pattern, #\\d+, TICKET-\\d+, US-\\d{3}

### Community 56 - "tokens_per_run"
Cohesion: 0.25
Nodes (8): tokens_per_run, description, examples, minimum, pattern, type, integer, string

### Community 57 - "TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn"
Cohesion: 0.64
Nodes (7): T, headOf(), readFile(), runOK(), TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn(), TestTheMergeBackIsGatedLikeACommit(), worktreeFixture()

### Community 58 - "specFrom"
Cohesion: 0.64
Nodes (7): OrientBeforeReading(), T, specFrom(), TestAskingForAGraphThatIsNotConfiguredSaysSo(), TestOrientBeforeReadingIsTheChainThatWasNeverWired(), TestOrientHandlesAPhaseTheSpecDoesNotDeclare(), TestThePhaseFlagAloneTurnsItOn()

### Community 59 - "unit"
Cohesion: 0.29
Nodes (7): unit, additionalProperties, description, required, type, name, pattern

### Community 60 - "marketplace.json"
Cohesion: 0.29
Nodes (6): description, name, owner, name, plugins, $schema

### Community 61 - "Run"
Cohesion: 0.52
Nodes (6): Result, expandHome(), Writer, Run(), VaultPath(), VaultWriter()

### Community 62 - "matrix-demo.sh"
Cohesion: 0.67
Nodes (6): bad(), BATTEN_DB, deny(), ok(), matrix-demo.sh script, want()

### Community 63 - "enforcement"
Cohesion: 0.33
Nodes (6): description, enum, type, enforcement, enforce, report

### Community 64 - "phases"
Cohesion: 0.33
Nodes (6): $ref, description, items, minItems, type, phases

### Community 65 - "quota_pct_per_run"
Cohesion: 0.33
Nodes (6): quota_pct_per_run, description, examples, maximum, minimum, type

### Community 66 - "anchor"
Cohesion: 0.40
Nodes (5): description, enum, type, anchor, git_sha

### Community 67 - "diff_from"
Cohesion: 0.40
Nodes (5): description, enum, type, diff_from, anchor

### Community 68 - "imputed_usd_per_run"
Cohesion: 0.40
Nodes (5): description, examples, minimum, type, imputed_usd_per_run

### Community 69 - "GateShortfall"
Cohesion: 0.50
Nodes (5): envelope, GateShortfall(), shortUnit(), staleTarget(), SplitDigest()

### Community 70 - "TestEveryEdgeRelationReadHasAProducer"
Cohesion: 0.80
Nodes (4): edgeRelations(), T, repoRoot(), TestEveryEdgeRelationReadHasAProducer()

### Community 71 - "claude-code/scripts/bootstrap.sh"
Cohesion: 0.60
Nodes (3): install_from(), bootstrap.sh script, verify_archive()

### Community 72 - "scripts/bootstrap.sh"
Cohesion: 0.60
Nodes (3): install_from(), bootstrap.sh script, verify_archive()

### Community 73 - "capabilities"
Cohesion: 0.50
Nodes (4): additionalProperties, description, type, capabilities

### Community 74 - "project"
Cohesion: 0.50
Nodes (4): description, minLength, type, project

### Community 75 - "version"
Cohesion: 0.50
Nodes (4): version, const, description, type

### Community 78 - "release-check.sh"
Cohesion: 0.83
Nodes (3): bad(), note(), release-check.sh script

### Community 92 - "runCheck"
Cohesion: 0.40
Nodes (5): Duration, narrowExit(), runCheck(), TestNarrowExitFoldsWindowsWraparound(), TestRunCheckReportsTheRealExitCode()

## Knowledge Gaps
- **212 isolated node(s):** `$schema`, `name`, `description`, `name`, `plugins` (+207 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **12 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Open()` connect `Open` to `.WriteFile`, `hooks_test.go`, `tui.go`, `discovery.go`, `demo`, `bootstrap_test.go`, `main.go`, `graph_test.go`, `usage_test.go`, `Store`, `cmdReport`, `fixture`, `run`, `Scan`, `cmdDoctor`, `fixture`, `cmdScanDiff`, `Parse`, `store.go`, `prFixture`, `cmdPhase`, `normPath`, `retryTransient`, `TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn`?**
  _High betweenness centrality (0.163) - this node is a cross-community bridge._
- **Why does `Spec` connect `Spec` to `.WriteFile`, `hooks_test.go`, `tui.go`, `discovery.go`, `Output`, `mcp.go`, `demo`, `main.go`, `Store`, `cmdReport`, `spec_test.go`, `cmdDoctor`, `Verdict`, `fixture`, `Parse`, `worktree.go`, `spec.go`, `prFixture`, `specFrom`, `Run`?**
  _High betweenness centrality (0.138) - this node is a cross-community bridge._
- **Why does `Run` connect `Run` to `vault_test.go`, `.WriteFile`, `hooks_test.go`, `tui.go`, `Output`, `mcp.go`, `Render`, `demo`, `main.go`, `Store`, `cmdReport`, `fixture`, `run`, `cmdDoctor`, `Verdict`, `cmdScanDiff`, `Spec`, `worktree.go`, `store.go`, `prFixture`, `GateShortfall`?**
  _High betweenness centrality (0.113) - this node is a cross-community bridge._
- **Are the 54 inferred relationships involving `Open()` (e.g. with `.stepInit()` and `checkStore()`) actually correct?**
  _`Open()` has 54 INFERRED edges - model-reasoned connections that need verification._
- **Are the 23 inferred relationships involving `captureStdout()` (e.g. with `TestDemoDirReusesItsOwnSandbox()` and `TestDemoIsTheEndToEndTestThisProjectDidNotHave()`) actually correct?**
  _`captureStdout()` has 23 INFERRED edges - model-reasoned connections that need verification._
- **What connects `$schema`, `name`, `description` to the rest of the system?**
  _212 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `vault_test.go` be split into smaller, more focused modules?**
  _Cohesion score 0.07578947368421053 - nodes in this community are weakly interconnected._