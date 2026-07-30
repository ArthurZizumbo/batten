# Graph Report - .  (2026-07-29)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 1651 nodes · 4212 edges · 71 communities (63 shown, 8 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 658 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `786108b4`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Spec
- guardFixture
- mcp.go
- tui.go
- discovery.go
- Output
- LoadFrom
- Render
- Verdict
- memory
- hooks_test.go
- demo
- renderPR
- .WriteFile
- statusline_test.go
- usage_test.go
- properties
- scan.go
- Store
- main.go
- fixture
- bootstrap_test.go
- run
- vault_test.go
- statusline.go
- Open
- properties
- properties
- type
- cmdPhase
- plugin.json
- properties
- $defs
- properties
- cmdReport
- declared_test.go
- TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn
- store.go
- Run
- contrastDiff
- batten.schema.json
- normPath
- domains
- properties
- MeasureGroup
- phase
- retryTransient
- matrix-replica.sh
- artifacts
- properties
- pattern
- unit
- marketplace.json
- matrix-demo.sh
- phases
- anchor
- diff_from
- TestEveryEdgeRelationReadHasAProducer
- scanNode
- capabilities
- project
- version
- batten
- install_from
- install_from
- build-plugin.sh script
- replica-ui.sh
- github.com/ArthurZizumbo/batten

## God Nodes (most connected - your core abstractions)
1. `Store` - 111 edges
2. `Spec` - 77 edges
3. `Run` - 74 edges
4. `Open()` - 50 edges
5. `captureStdout()` - 41 edges
6. `inDir()` - 40 edges
7. `Output` - 34 edges
8. `fixture()` - 32 edges
9. `main()` - 30 edges
10. `demo` - 27 edges

## Surprising Connections (you probably didn't know these)
- `load()` --calls--> `LoadFrom()`  [INFERRED]
  cmd/batten/main.go → internal/spec/spec.go
- `cmdHook()` --calls--> `ReadInput()`  [INFERRED]
  cmd/batten/main.go → internal/hooks/hooks.go
- `degraded()` --calls--> `AdviseDegraded()`  [INFERRED]
  cmd/batten/main.go → internal/hooks/envelope.go
- `loadForHook()` --calls--> `Find()`  [INFERRED]
  cmd/batten/main.go → internal/plan/plan.go
- `loadForHook()` --calls--> `Load()`  [INFERRED]
  cmd/batten/main.go → internal/plan/plan.go

## Import Cycles
- None detected.

## Communities (71 total, 8 thin omitted)

### Community 0 - "Spec"
Cohesion: 0.06
Nodes (58): branchExists(), cmdWorktree(), currentBranch(), gitRun(), mergeDriverRegistered(), registerWorktree(), specRootIn(), worktreeAdd() (+50 more)

### Community 1 - "guardFixture"
Cohesion: 0.08
Nodes (69): BashTarget, BashWriteTargets(), compact(), hasExplicitScriptFlag(), hasInPlaceFlag(), isAssignment(), nonFlagArgs(), shellSplit() (+61 more)

### Community 2 - "mcp.go"
Cohesion: 0.06
Nodes (52): CallToolRequest, deref(), firstLine(), CallToolResult, budgetOutput, graphOutput, runsOutput, verdictOutput (+44 more)

### Community 3 - "tui.go"
Cohesion: 0.07
Nodes (50): formatCeilings(), exceededSummary(), amounts(), bar(), binding(), ceilingLine(), contentH(), contentW() (+42 more)

### Community 4 - "discovery.go"
Cohesion: 0.08
Nodes (55): index, installedPlugin, Item, kind, Problem, Source, Agents(), bare() (+47 more)

### Community 5 - "Output"
Cohesion: 0.08
Nodes (35): bashInput, envelope, HookSpecific, Input, Output, writeInput, Handler, Input (+27 more)

### Community 6 - "LoadFrom"
Cohesion: 0.09
Nodes (50): Result, expandHome(), Writer, Run(), fixture(), T, TestACheckOnlyRunSaysTheReviewerIsMissing(), TestBattenVaultOverridesTheSpec() (+42 more)

### Community 7 - "Render"
Cohesion: 0.07
Nodes (45): Detail, Edge, HTMLInput, Node, bar(), cmdBudget(), Canvas, Edge (+37 more)

### Community 8 - "Verdict"
Cohesion: 0.09
Nodes (29): scanVerdict(), eq(), Writer, props(), cell(), domainsOf(), Builder, Edge (+21 more)

### Community 9 - "memory"
Cohesion: 0.04
Nodes (49): properties, additionalProperties, description, properties, type, description, items, type (+41 more)

### Community 10 - "hooks_test.go"
Cohesion: 0.13
Nodes (42): fields(), T, TestADegradedBattenIsMarkedRetryable(), TestEveryDenialCarriesACodeAndAWayOut(), TestTheProseStaysFirst(), TestTheWriteSetCollisionOffersNoWayThrough(), GateShortfallAt(), budgetFixture() (+34 more)

### Community 11 - "demo"
Cohesion: 0.12
Nodes (19): checkResult, demo, cmdDemo(), demoDir(), jsonQuote(), meaningfulLine(), mustRun(), onPath() (+11 more)

### Community 12 - "renderPR"
Cohesion: 0.10
Nodes (40): Builder, Edge, Node, mermaidClass(), mermaidEscape(), mermaidLabel(), renderPR(), shortfall() (+32 more)

### Community 13 - ".WriteFile"
Cohesion: 0.18
Nodes (40): cmdInit(), captureStdout(), gitRepoWithSpec(), T, inDir(), jsonString(), runHook(), TestABrokenStoreSaysSoInsteadOfPassingInSilence() (+32 more)

### Community 14 - "statusline_test.go"
Cohesion: 0.15
Nodes (38): installStatusline(), chainedCommand(), chainPath(), command(), RawMessage, Install(), Installed(), IsBatten() (+30 more)

### Community 15 - "usage_test.go"
Cohesion: 0.12
Nodes (38): Time, Price(), priceAt(), rateFor(), cacheSplit(), Reader, Parse(), parseFile() (+30 more)

### Community 16 - "properties"
Cohesion: 0.05
Nodes (41): additionalProperties, description, properties, type, description, examples, minimum, type (+33 more)

### Community 17 - "scan.go"
Cohesion: 0.13
Nodes (38): allChecks(), checksFor(), dedup(), deriveDomains(), deriveHarness(), derivePurpose(), deriveStack(), deriveUnit() (+30 more)

### Community 18 - "Store"
Cohesion: 0.08
Nodes (7): DB, Server, newServer(), Serve(), now(), QuotaSnapshot, Store

### Community 19 - "main.go"
Cohesion: 0.13
Nodes (30): dx, canvasHTML(), checkAnchors(), checkInstall(), checkRunnable(), checkSpecOnly(), checkStore(), cmdDoctor() (+22 more)

### Community 20 - "fixture"
Cohesion: 0.19
Nodes (37): contentOf(), CallToolResult, T, TestAnUnknownPhaseIsReportedRatherThanIgnored(), TestSpecNarrowsToOnePhase(), TestTheModelGetsProseAndTheClientGetsTheJSON(), TestTheSummaryNeverInventsAZero(), TestTheVerdictSummaryKeepsTheAnswerAndTheRemedy() (+29 more)

### Community 21 - "bootstrap_test.go"
Cohesion: 0.19
Nodes (32): buildArchive(), copyFile(), Server, T, mustRun(), readJSON(), realBinary(), releaseArchive() (+24 more)

### Community 22 - "run"
Cohesion: 0.14
Nodes (31): T, TestAnUnmeasuredRunIsNotAZeroSpendRun(), TestARunCarriesItsUnpricedShare(), TestBudgetNeverInventsANumber(), TestMeasureByModelAgreesWithTheRunLedger(), TestMeasureByModelSeparatesUnpricedRequests(), TestMeasureRefusesToConcludeFromTooFewRuns(), TestOverBudgetTripsOnTheMeasurableCeilings() (+23 more)

### Community 23 - "vault_test.go"
Cohesion: 0.25
Nodes (29): New(), checkFilterNode(), fixtureNodes(), fixtureRun(), fixtureUsage(), fixtureVerdict(), Node, T (+21 more)

### Community 24 - "statusline.go"
Cohesion: 0.14
Nodes (26): attach(), budgetSeg(), chain(), firstLine(), Cmd, Context, Model, pctStr() (+18 more)

### Community 25 - "Open"
Cohesion: 0.14
Nodes (25): cmdCanvas(), cmdClaim(), cmdClose(), cmdIngest(), cmdMCP(), cmdRuns(), cmdShow(), cmdStatus() (+17 more)

### Community 26 - "properties"
Cohesion: 0.10
Nodes (20): description, type, description, type, description, minLength, type, description (+12 more)

### Community 27 - "properties"
Cohesion: 0.11
Nodes (19): description, type, description, maximum, minimum, type, properties, description (+11 more)

### Community 28 - "type"
Cohesion: 0.11
Nodes (19): description, examples, items, type, description, items, type, description (+11 more)

### Community 29 - "cmdPhase"
Cohesion: 0.19
Nodes (16): cmdOverride(), cmdPhase(), gitSHA(), headroomAlive(), cmdScanDiff(), TestScanDiffRefusesWithoutAnAnchor(), TestScanDiffSeesWhatNoShellParserCan(), cmdIterate() (+8 more)

### Community 30 - "plugin.json"
Cohesion: 0.11
Nodes (18): author, name, defaultEnabled, description, displayName, keywords, license, name (+10 more)

### Community 31 - "properties"
Cohesion: 0.12
Nodes (18): description, items, type, description, enum, type, properties, checks (+10 more)

### Community 32 - "$defs"
Cohesion: 0.12
Nodes (17): $defs, domain, gate, resource, verdict, additionalProperties, required, type (+9 more)

### Community 33 - "properties"
Cohesion: 0.12
Nodes (16): description, enum, type, description, items, type, description, examples (+8 more)

### Community 34 - "cmdReport"
Cohesion: 0.40
Nodes (14): cmdReport(), parseSince(), T, reportFixture(), TestAQuietWindowIsReportedAsAResult(), TestNoUnblockedWarningWhenEnforcing(), TestParseSinceAcceptsTheSpellingPeopleReachFor(), TestReportCountsWhatBattenActuallyStopped() (+6 more)

### Community 35 - "declared_test.go"
Cohesion: 0.32
Nodes (14): declaredFields(), declaredFieldsFrom(), T, holdsDeclaredFields(), productionIdentUses(), productionSelectors(), repoRoot(), ruleStated() (+6 more)

### Community 36 - "TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn"
Cohesion: 0.23
Nodes (15): cmdCheck(), gateReadyToClose(), Duration, indent(), narrowExit(), runCheck(), TestGateReadyToCloseAgreesWithTheHook(), verdictTreeOf() (+7 more)

### Community 37 - "store.go"
Cohesion: 0.11
Nodes (9): firstLineStr(), newRunID(), nullable(), DecisionCount, Edge, Event, Fenced, ModelSpend (+1 more)

### Community 38 - "Run"
Cohesion: 0.27
Nodes (3): Duration, scanRun(), Run

### Community 39 - "contrastDiff"
Cohesion: 0.30
Nodes (9): scanReport, contrastDiff(), normalizeScanPath(), printScanDiff(), equalStrings(), T, TestContrastDiffSeparatesTheThreeCases(), TestNoClaimsIsNotACleanResult() (+1 more)

### Community 40 - "batten.schema.json"
Cohesion: 0.17
Nodes (11): additionalProperties, description, $id, required, $schema, title, type, phases (+3 more)

### Community 41 - "normPath"
Cohesion: 0.22
Nodes (8): normPath(), T, TestMigrationAddsVerdictSource(), TestNormPathCaseFold(), TestProbeWriteLockIsHonestAndLeavesNothingBehind(), TestSaveVerdictRejectsOkWithoutEvidence(), TestWriteSetsByRunKeepsNilDistinct(), CrossOwner

### Community 42 - "domains"
Cohesion: 0.20
Nodes (10): $ref, additionalProperties, description, type, additionalProperties, description, type, domains (+2 more)

### Community 43 - "properties"
Cohesion: 0.20
Nodes (10): description, enum, type, properties, enforcement, resources, description, type (+2 more)

### Community 45 - "phase"
Cohesion: 0.22
Nodes (9): phase, description, examples, type, additionalProperties, required, type, locator (+1 more)

### Community 46 - "retryTransient"
Cohesion: 0.42
Nodes (7): IsTransient(), retryTransient(), T, TestRetryGivesUpOnRealFailuresImmediately(), TestRetryStopsRatherThanHanging(), TestRetrySucceedsOnceTheContentionClears(), TestTransientIsToldApartFromBroken()

### Community 47 - "matrix-replica.sh"
Cohesion: 0.36
Nodes (7): bad(), BATTEN_DB, check(), hook(), ok(), pay(), matrix-replica.sh script

### Community 48 - "artifacts"
Cohesion: 0.25
Nodes (8): description, pattern, type, additionalProperties, description, examples, type, artifacts

### Community 49 - "properties"
Cohesion: 0.25
Nodes (8): description, minLength, type, description, type, name, plan, properties

### Community 50 - "pattern"
Cohesion: 0.25
Nodes (8): description, examples, minLength, type, pattern, #\\d+, TICKET-\\d+, US-\\d{3}

### Community 51 - "unit"
Cohesion: 0.29
Nodes (7): unit, additionalProperties, description, required, type, name, pattern

### Community 52 - "marketplace.json"
Cohesion: 0.29
Nodes (6): description, name, owner, name, plugins, $schema

### Community 53 - "matrix-demo.sh"
Cohesion: 0.67
Nodes (6): bad(), BATTEN_DB, deny(), ok(), matrix-demo.sh script, want()

### Community 54 - "phases"
Cohesion: 0.33
Nodes (6): $ref, description, items, minItems, type, phases

### Community 55 - "anchor"
Cohesion: 0.40
Nodes (5): description, enum, type, anchor, git_sha

### Community 56 - "diff_from"
Cohesion: 0.40
Nodes (5): description, enum, type, diff_from, anchor

### Community 57 - "TestEveryEdgeRelationReadHasAProducer"
Cohesion: 0.80
Nodes (4): edgeRelations(), T, repoRoot(), TestEveryEdgeRelationReadHasAProducer()

### Community 59 - "capabilities"
Cohesion: 0.50
Nodes (4): additionalProperties, description, type, capabilities

### Community 60 - "project"
Cohesion: 0.50
Nodes (4): description, minLength, type, project

### Community 61 - "version"
Cohesion: 0.50
Nodes (4): version, const, description, type

## Knowledge Gaps
- **226 isolated node(s):** `$schema`, `name`, `description`, `name`, `plugins` (+221 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **8 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Store` connect `Store` to `Spec`, `mcp.go`, `tui.go`, `Output`, `LoadFrom`, `Render`, `Verdict`, `hooks_test.go`, `demo`, `renderPR`, `statusline_test.go`, `main.go`, `run`, `statusline.go`, `Open`, `cmdReport`, `TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn`, `store.go`, `Run`, `normPath`, `MeasureGroup`, `scanNode`?**
  _High betweenness centrality (0.128) - this node is a cross-community bridge._
- **Why does `Open()` connect `Open` to `guardFixture`, `tui.go`, `discovery.go`, `LoadFrom`, `hooks_test.go`, `demo`, `renderPR`, `.WriteFile`, `statusline_test.go`, `usage_test.go`, `Store`, `main.go`, `fixture`, `bootstrap_test.go`, `run`, `cmdPhase`, `cmdReport`, `TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn`, `store.go`, `normPath`, `retryTransient`?**
  _High betweenness centrality (0.113) - this node is a cross-community bridge._
- **Why does `Run` connect `Run` to `Spec`, `guardFixture`, `mcp.go`, `tui.go`, `Output`, `Render`, `Verdict`, `hooks_test.go`, `demo`, `renderPR`, `Store`, `main.go`, `fixture`, `run`, `vault_test.go`, `statusline.go`, `Open`, `cmdPhase`, `cmdReport`, `TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn`, `store.go`, `contrastDiff`?**
  _High betweenness centrality (0.105) - this node is a cross-community bridge._
- **Are the 47 inferred relationships involving `Open()` (e.g. with `.stepInit()` and `checkStore()`) actually correct?**
  _`Open()` has 47 INFERRED edges - model-reasoned connections that need verification._
- **What connects `$schema`, `name`, `description` to the rest of the system?**
  _226 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Spec` be split into smaller, more focused modules?**
  _Cohesion score 0.059466848940533154 - nodes in this community are weakly interconnected._
- **Should `guardFixture` be split into smaller, more focused modules?**
  _Cohesion score 0.07700851536467974 - nodes in this community are weakly interconnected._