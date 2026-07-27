# Graph Report - .  (2026-07-27)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 970 nodes · 1993 edges · 56 communities (48 shown, 8 thin omitted)
- Extraction: 95% EXTRACTED · 5% INFERRED · 0% AMBIGUOUS · INFERRED: 97 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `34b26f56`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Store
- discovery.go
- Spec
- main.go
- memory
- mcp.go
- tui.go
- vault.go
- statusline_test.go
- usage_test.go
- properties
- scan.go
- hooks.go
- mcp_test.go
- properties
- vault_test.go
- plugin.json
- phases
- batten.schema.json
- $defs
- properties
- provenance
- properties
- properties
- type
- properties
- Render
- phase
- properties
- pattern
- artifacts
- enforcement
- unit
- marketplace.json
- Run
- coverage
- domain
- probe
- capabilities
- check
- checks
- models
- path
- project
- skills
- version
- model
- batten
- claude-code/scripts/bootstrap.sh
- scripts/bootstrap.sh
- build-plugin.sh script
- Writer
- CallToolResult
- Context
- github.com/ArthurZizumbo/batten

## God Nodes (most connected - your core abstractions)
1. `Store` - 69 edges
2. `Run` - 38 edges
3. `Spec` - 36 edges
4. `fixture()` - 25 edges
5. `seed()` - 22 edges
6. `ctx()` - 22 edges
7. `Model` - 21 edges
8. `main()` - 21 edges
9. `load()` - 18 edges
10. `Item` - 17 edges

## Surprising Connections (you probably didn't know these)
- `cmdInit()` --calls--> `Scan()`  [INFERRED]
  cmd/batten/main.go → internal/scan/scan.go
- `load()` --calls--> `Open()`  [INFERRED]
  cmd/batten/main.go → internal/store/store.go
- `load()` --references--> `Store`  [EXTRACTED]
  cmd/batten/main.go → internal/store/store.go
- `loadForHook()` --calls--> `Open()`  [INFERRED]
  cmd/batten/main.go → internal/store/store.go
- `loadForHook()` --references--> `Store`  [EXTRACTED]
  cmd/batten/main.go → internal/store/store.go

## Import Cycles
- None detected.

## Communities (56 total, 8 thin omitted)

### Community 0 - "Store"
Cohesion: 0.06
Nodes (19): DB, newServer(), Serve(), Duration, normPath(), now(), nullable(), scanRun() (+11 more)

### Community 1 - "discovery.go"
Cohesion: 0.08
Nodes (53): index, installedPlugin, Item, kind, Problem, Source, Agents(), bare() (+45 more)

### Community 2 - "Spec"
Cohesion: 0.06
Nodes (43): Find(), Load(), LoadFrom(), attach(), budgetSeg(), chain(), firstLine(), Cmd (+35 more)

### Community 3 - "main.go"
Cohesion: 0.09
Nodes (52): bar(), cmdBudget(), cmdCanvas(), cmdCheck(), cmdClaim(), cmdClose(), cmdDoctor(), cmdHook() (+44 more)

### Community 4 - "memory"
Cohesion: 0.04
Nodes (49): properties, additionalProperties, description, properties, type, description, items, type (+41 more)

### Community 5 - "mcp.go"
Cohesion: 0.10
Nodes (36): CallToolRequest, exceededSummary(), CallToolResult, Context, V, joinNotes(), orEmpty(), rfc3339() (+28 more)

### Community 6 - "tui.go"
Cohesion: 0.11
Nodes (30): amounts(), bar(), binding(), ceilingLine(), contentH(), contentW(), fracStyle(), glyph() (+22 more)

### Community 7 - "vault.go"
Cohesion: 0.10
Nodes (27): Builder, eq(), Writer, props(), cell(), domainsOf(), Edge, Node (+19 more)

### Community 8 - "statusline_test.go"
Cohesion: 0.15
Nodes (37): chainedCommand(), chainPath(), command(), RawMessage, Install(), Installed(), IsBatten(), newSettings() (+29 more)

### Community 9 - "usage_test.go"
Cohesion: 0.12
Nodes (37): Time, Price(), priceAt(), rateFor(), cacheSplit(), Reader, Parse(), parseFile() (+29 more)

### Community 10 - "properties"
Cohesion: 0.05
Nodes (41): additionalProperties, description, properties, type, description, examples, minimum, type (+33 more)

### Community 11 - "scan.go"
Cohesion: 0.13
Nodes (33): allChecks(), checksFor(), dedup(), deriveDomains(), deriveHarness(), derivePurpose(), deriveStack(), deriveUnit() (+25 more)

### Community 12 - "hooks.go"
Cohesion: 0.16
Nodes (21): bashInput, Handler, HookSpecific, Input, Output, writeInput, advise(), allow() (+13 more)

### Community 13 - "mcp_test.go"
Cohesion: 0.25
Nodes (30): CallToolResult, Context, ctx(), fixture(), T, seed(), TestBudgetReportsUnmeasurableCeilingAsUnavailable(), TestBudgetWithNoDeclaredCeiling() (+22 more)

### Community 14 - "properties"
Cohesion: 0.07
Nodes (30): description, enum, type, description, enum, type, description, type (+22 more)

### Community 15 - "vault_test.go"
Cohesion: 0.26
Nodes (28): New(), checkFilterNode(), fixtureNodes(), fixtureRun(), fixtureUsage(), fixtureVerdict(), Node, T (+20 more)

### Community 16 - "plugin.json"
Cohesion: 0.11
Nodes (18): author, name, defaultEnabled, description, displayName, keywords, license, name (+10 more)

### Community 17 - "phases"
Cohesion: 0.17
Nodes (13): type, $ref, properties, additionalProperties, description, items, minItems, type (+5 more)

### Community 18 - "batten.schema.json"
Cohesion: 0.17
Nodes (11): additionalProperties, description, $id, required, $schema, title, type, phases (+3 more)

### Community 19 - "$defs"
Cohesion: 0.17
Nodes (12): $defs, gate, resource, verdict, additionalProperties, dependentRequired, description, type (+4 more)

### Community 20 - "properties"
Cohesion: 0.18
Nodes (11): description, type, properties, description, items, type, agent, invariants (+3 more)

### Community 21 - "provenance"
Cohesion: 0.18
Nodes (11): description, examples, type, format, provenance, additionalProperties, description, properties (+3 more)

### Community 22 - "properties"
Cohesion: 0.18
Nodes (11): description, enum, type, description, items, type, kind, priority (+3 more)

### Community 23 - "properties"
Cohesion: 0.15
Nodes (15): $ref, additionalProperties, description, type, additionalProperties, description, type, properties (+7 more)

### Community 24 - "type"
Cohesion: 0.20
Nodes (10): description, items, type, type, exclude, reads, description, items (+2 more)

### Community 25 - "properties"
Cohesion: 0.22
Nodes (10): description, enum, type, properties, evidence, verdict, description, enum (+2 more)

### Community 26 - "Render"
Cohesion: 0.33
Nodes (9): Canvas, Edge, Node, Edge, Node, humanTokens(), relColor(), Render() (+1 more)

### Community 27 - "phase"
Cohesion: 0.22
Nodes (9): phase, description, examples, type, additionalProperties, required, type, locator (+1 more)

### Community 28 - "properties"
Cohesion: 0.25
Nodes (8): description, minLength, type, description, type, name, plan, properties

### Community 29 - "pattern"
Cohesion: 0.25
Nodes (8): description, examples, minLength, type, pattern, #\\d+, TICKET-\\d+, US-\\d{3}

### Community 30 - "artifacts"
Cohesion: 0.29
Nodes (7): description, pattern, additionalProperties, description, examples, type, artifacts

### Community 31 - "enforcement"
Cohesion: 0.33
Nodes (6): description, enum, type, enforcement, enforce, report

### Community 32 - "unit"
Cohesion: 0.29
Nodes (7): unit, additionalProperties, description, required, type, name, pattern

### Community 33 - "marketplace.json"
Cohesion: 0.29
Nodes (6): description, name, owner, name, plugins, $schema

### Community 34 - "Run"
Cohesion: 0.48
Nodes (6): Result, expandHome(), Spec, Run(), VaultWriter(), Writer

### Community 35 - "coverage"
Cohesion: 0.40
Nodes (5): description, maximum, minimum, type, coverage

### Community 36 - "domain"
Cohesion: 0.40
Nodes (5): domain, additionalProperties, required, type, path

### Community 37 - "probe"
Cohesion: 0.40
Nodes (5): description, examples, type, probe, nvidia-smi --query-gpu=memory.free --format=csv,noheader,nounits

### Community 38 - "capabilities"
Cohesion: 0.50
Nodes (4): additionalProperties, description, type, capabilities

### Community 39 - "check"
Cohesion: 0.40
Nodes (5): description, examples, items, type, check

### Community 40 - "checks"
Cohesion: 0.50
Nodes (4): description, items, type, checks

### Community 41 - "models"
Cohesion: 0.50
Nodes (4): additionalProperties, description, type, models

### Community 42 - "path"
Cohesion: 0.50
Nodes (4): description, minLength, type, path

### Community 43 - "project"
Cohesion: 0.50
Nodes (4): description, minLength, type, project

### Community 45 - "skills"
Cohesion: 0.67
Nodes (3): skills, description, type

### Community 46 - "version"
Cohesion: 0.50
Nodes (4): version, const, description, type

### Community 47 - "model"
Cohesion: 0.67
Nodes (3): description, type, model

## Knowledge Gaps
- **229 isolated node(s):** `$schema`, `name`, `description`, `name`, `plugins` (+224 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **8 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Run` connect `Store` to `Spec`, `main.go`, `mcp.go`, `tui.go`, `vault.go`, `mcp_test.go`, `vault_test.go`, `Render`?**
  _High betweenness centrality (0.109) - this node is a cross-community bridge._
- **Why does `Store` connect `Store` to `Spec`, `Run`, `main.go`, `mcp.go`, `tui.go`, `statusline_test.go`, `hooks.go`?**
  _High betweenness centrality (0.103) - this node is a cross-community bridge._
- **Why does `Spec` connect `Spec` to `Store`, `discovery.go`, `mcp.go`, `tui.go`, `statusline_test.go`, `hooks.go`?**
  _High betweenness centrality (0.081) - this node is a cross-community bridge._
- **What connects `$schema`, `name`, `description` to the rest of the system?**
  _229 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Store` be split into smaller, more focused modules?**
  _Cohesion score 0.06196291270918137 - nodes in this community are weakly interconnected._
- **Should `discovery.go` be split into smaller, more focused modules?**
  _Cohesion score 0.08360655737704918 - nodes in this community are weakly interconnected._
- **Should `Spec` be split into smaller, more focused modules?**
  _Cohesion score 0.06487434248977206 - nodes in this community are weakly interconnected._