# Graph Report - .  (2026-07-23)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 942 nodes · 1960 edges · 58 communities (53 shown, 5 thin omitted)
- Extraction: 94% EXTRACTED · 6% INFERRED · 0% AMBIGUOUS · INFERRED: 120 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `305d6ddf`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Store
- discovery.go
- Spec
- main.go
- mcp.go
- Usage
- tui.go
- statusline_test.go
- properties
- usage_test.go
- hooks.go
- properties
- fixture
- vault_test.go
- scan.go
- plugin.json
- Render
- properties
- properties
- phases
- batten.schema.json
- provenance
- type
- properties
- gate
- phase
- evidence
- compression
- properties
- properties
- properties
- obsidian
- pattern
- artifacts
- unit
- marketplace.json
- $defs
- enforcement
- kind
- enum
- coverage
- export
- memory
- probe
- check
- checks
- provider
- models
- project
- reads
- skills
- version
- batten
- claude-code/scripts/bootstrap.sh
- scripts/bootstrap.sh
- build-plugin.sh script
- github.com/arthu/batten

## God Nodes (most connected - your core abstractions)
1. `Store` - 65 edges
2. `Spec` - 42 edges
3. `Run` - 38 edges
4. `fixture()` - 27 edges
5. `seed()` - 22 edges
6. `ctx()` - 22 edges
7. `main()` - 21 edges
8. `Model` - 21 edges
9. `load()` - 19 edges
10. `Item` - 18 edges

## Surprising Connections (you probably didn't know these)
- `cmdCanvas()` --calls--> `Render()`  [INFERRED]
  cmd/batten/main.go → internal/canvas/canvas.go
- `cmdMCP()` --calls--> `Serve()`  [INFERRED]
  cmd/batten/main.go → internal/mcp/mcp.go
- `cmdIngest()` --calls--> `Parse()`  [INFERRED]
  cmd/batten/main.go → internal/usage/usage.go
- `cmdDoctor()` --calls--> `Validate()`  [INFERRED]
  cmd/batten/main.go → internal/discovery/discovery.go
- `cmdDoctor()` --calls--> `Installed()`  [INFERRED]
  cmd/batten/main.go → internal/statusline/install.go

## Import Cycles
- None detected.

## Communities (58 total, 5 thin omitted)

### Community 0 - "Store"
Cohesion: 0.07
Nodes (19): DB, newServer(), Serve(), Duration, normPath(), now(), nullable(), scanRun() (+11 more)

### Community 1 - "discovery.go"
Cohesion: 0.08
Nodes (53): index, installedPlugin, Item, kind, Problem, Source, Agents(), bare() (+45 more)

### Community 2 - "Spec"
Cohesion: 0.07
Nodes (40): attach(), budgetSeg(), chain(), firstLine(), Cmd, Context, pctStr(), pick() (+32 more)

### Community 3 - "main.go"
Cohesion: 0.09
Nodes (52): bar(), cmdBudget(), cmdCanvas(), cmdCheck(), cmdClaim(), cmdClose(), cmdDoctor(), cmdHook() (+44 more)

### Community 4 - "mcp.go"
Cohesion: 0.10
Nodes (36): CallToolRequest, exceededSummary(), CallToolResult, Context, V, joinNotes(), orEmpty(), rfc3339() (+28 more)

### Community 5 - "Usage"
Cohesion: 0.09
Nodes (28): Builder, eq(), Writer, props(), cell(), domainsOf(), Edge, Node (+20 more)

### Community 6 - "tui.go"
Cohesion: 0.11
Nodes (30): amounts(), bar(), binding(), ceilingLine(), contentH(), contentW(), fracStyle(), glyph() (+22 more)

### Community 7 - "statusline_test.go"
Cohesion: 0.15
Nodes (38): installStatusline(), chainedCommand(), chainPath(), command(), RawMessage, Install(), Installed(), IsBatten() (+30 more)

### Community 8 - "properties"
Cohesion: 0.05
Nodes (41): additionalProperties, description, properties, type, description, examples, minimum, type (+33 more)

### Community 9 - "usage_test.go"
Cohesion: 0.13
Nodes (36): Time, Price(), priceAt(), rateFor(), cacheSplit(), Reader, Parse(), parseFile() (+28 more)

### Community 10 - "hooks.go"
Cohesion: 0.18
Nodes (19): bashInput, Handler, HookSpecific, Input, Output, writeInput, advise(), allow() (+11 more)

### Community 11 - "properties"
Cohesion: 0.07
Nodes (30): description, enum, type, description, enum, type, description, type (+22 more)

### Community 12 - "fixture"
Cohesion: 0.26
Nodes (29): ctx(), fixture(), CallToolResult, Context, T, seed(), TestBudgetReportsUnmeasurableCeilingAsUnavailable(), TestBudgetWithNoDeclaredCeiling() (+21 more)

### Community 13 - "vault_test.go"
Cohesion: 0.26
Nodes (28): New(), checkFilterNode(), fixtureNodes(), fixtureRun(), fixtureUsage(), fixtureVerdict(), Node, T (+20 more)

### Community 14 - "scan.go"
Cohesion: 0.20
Nodes (19): allChecks(), checksFor(), dedup(), deriveDomains(), deriveUnit(), exists(), gitOut(), globAny() (+11 more)

### Community 15 - "plugin.json"
Cohesion: 0.11
Nodes (18): author, name, defaultEnabled, description, displayName, keywords, license, name (+10 more)

### Community 16 - "Render"
Cohesion: 0.20
Nodes (14): Canvas, Edge, Node, Result, Edge, Node, humanTokens(), relColor() (+6 more)

### Community 17 - "properties"
Cohesion: 0.15
Nodes (15): $ref, additionalProperties, description, type, additionalProperties, description, type, properties (+7 more)

### Community 18 - "properties"
Cohesion: 0.14
Nodes (14): description, type, properties, description, type, description, minLength, type (+6 more)

### Community 19 - "phases"
Cohesion: 0.17
Nodes (13): type, $ref, properties, additionalProperties, description, items, minItems, type (+5 more)

### Community 20 - "batten.schema.json"
Cohesion: 0.17
Nodes (11): additionalProperties, description, $id, required, $schema, title, type, phases (+3 more)

### Community 21 - "provenance"
Cohesion: 0.18
Nodes (11): description, examples, type, format, provenance, additionalProperties, description, properties (+3 more)

### Community 22 - "type"
Cohesion: 0.20
Nodes (10): items, description, items, type, description, items, type, type (+2 more)

### Community 23 - "properties"
Cohesion: 0.22
Nodes (9): additionalProperties, description, properties, type, additionalProperties, description, type, capabilities (+1 more)

### Community 24 - "gate"
Cohesion: 0.22
Nodes (9): gate, verdict, additionalProperties, dependentRequired, description, properties, type, gate (+1 more)

### Community 25 - "phase"
Cohesion: 0.22
Nodes (9): phase, description, examples, type, additionalProperties, required, type, locator (+1 more)

### Community 26 - "evidence"
Cohesion: 0.22
Nodes (9): description, enum, type, evidence, verdict, description, enum, type (+1 more)

### Community 27 - "compression"
Cohesion: 0.25
Nodes (8): additionalProperties, description, properties, type, description, type, compression, measure

### Community 28 - "properties"
Cohesion: 0.25
Nodes (8): resource, description, items, type, priority, additionalProperties, properties, type

### Community 29 - "properties"
Cohesion: 0.25
Nodes (8): properties, default, description, type, lessons, query_before_read, description, type

### Community 30 - "properties"
Cohesion: 0.25
Nodes (8): description, minLength, type, description, type, name, plan, properties

### Community 31 - "obsidian"
Cohesion: 0.25
Nodes (8): additionalProperties, description, properties, type, obsidian, vault, description, type

### Community 32 - "pattern"
Cohesion: 0.25
Nodes (8): description, examples, minLength, type, pattern, #\\d+, TICKET-\\d+, US-\\d{3}

### Community 33 - "artifacts"
Cohesion: 0.29
Nodes (7): description, pattern, additionalProperties, description, examples, type, artifacts

### Community 34 - "unit"
Cohesion: 0.29
Nodes (7): unit, additionalProperties, description, required, type, name, pattern

### Community 35 - "marketplace.json"
Cohesion: 0.29
Nodes (6): description, name, owner, name, plugins, $schema

### Community 36 - "$defs"
Cohesion: 0.33
Nodes (6): $defs, domain, additionalProperties, required, type, path

### Community 37 - "enforcement"
Cohesion: 0.33
Nodes (6): description, enum, type, enforcement, enforce, report

### Community 38 - "kind"
Cohesion: 0.33
Nodes (6): description, enum, type, kind, exclusive_pool, mutex

### Community 39 - "enum"
Cohesion: 0.33
Nodes (6): enum, claude-mem, engram, graphify, headroom, none

### Community 40 - "coverage"
Cohesion: 0.40
Nodes (5): description, maximum, minimum, type, coverage

### Community 41 - "export"
Cohesion: 0.40
Nodes (5): description, items, type, uniqueItems, export

### Community 42 - "memory"
Cohesion: 0.40
Nodes (5): additionalProperties, default, description, type, memory

### Community 43 - "probe"
Cohesion: 0.40
Nodes (5): description, examples, type, probe, nvidia-smi --query-gpu=memory.free --format=csv,noheader,nounits

### Community 44 - "check"
Cohesion: 0.50
Nodes (4): description, examples, type, check

### Community 45 - "checks"
Cohesion: 0.50
Nodes (4): description, items, type, checks

### Community 46 - "provider"
Cohesion: 0.50
Nodes (4): properties, provider, description, type

### Community 47 - "models"
Cohesion: 0.50
Nodes (4): additionalProperties, description, type, models

### Community 48 - "project"
Cohesion: 0.50
Nodes (4): description, minLength, type, project

### Community 49 - "reads"
Cohesion: 0.50
Nodes (4): reads, description, items, type

### Community 50 - "skills"
Cohesion: 0.50
Nodes (4): skills, description, items, type

### Community 51 - "version"
Cohesion: 0.50
Nodes (4): version, const, description, type

## Knowledge Gaps
- **229 isolated node(s):** `$schema`, `name`, `description`, `name`, `plugins` (+224 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **5 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Spec` connect `Spec` to `Store`, `discovery.go`, `main.go`, `mcp.go`, `tui.go`, `statusline_test.go`, `hooks.go`, `Render`?**
  _High betweenness centrality (0.082) - this node is a cross-community bridge._
- **Why does `properties` connect `properties` to `artifacts`, `unit`, `enforcement`, `properties`, `models`, `project`, `phases`, `batten.schema.json`, `provenance`, `version`, `properties`?**
  _High betweenness centrality (0.080) - this node is a cross-community bridge._
- **Why does `Run` connect `Store` to `Spec`, `main.go`, `mcp.go`, `Usage`, `tui.go`, `fixture`, `vault_test.go`, `Render`?**
  _High betweenness centrality (0.078) - this node is a cross-community bridge._
- **Are the 3 inferred relationships involving `fixture()` (e.g. with `.WriteFile()` and `LoadFrom()`) actually correct?**
  _`fixture()` has 3 INFERRED edges - model-reasoned connections that need verification._
- **What connects `$schema`, `name`, `description` to the rest of the system?**
  _229 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Store` be split into smaller, more focused modules?**
  _Cohesion score 0.06554019457245264 - nodes in this community are weakly interconnected._
- **Should `discovery.go` be split into smaller, more focused modules?**
  _Cohesion score 0.08360655737704918 - nodes in this community are weakly interconnected._