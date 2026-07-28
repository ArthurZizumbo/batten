## 1. [BLOCKER/broken] [CONFIRMED] Three ordinary spellings of `git commit` walk straight through the verdict gate

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: All four spellings denied identically. `git -C <dir> commit`, `git -c user.email=... commit` and `git.exe commit` are the same act as `git commit`.

**why**: This is the one denial the product is built around, and the forms that escape it are not exotic: `git -c user.name=... -c user.email=... commit` is the standard non-interactive commit an agent or CI emits (it is what I myself typed to make the sandbox's baseline commit, before I noticed), and `git -C <dir> commit` is what you write the moment you are not at the repo root. `commitRe = (^|[;&|]|\s)git\s+(-[^\s]+\s+)*commit\b` consumes flags but not the separate ARGUMENT git's global flags take, so `-C .` and `-c k=v` break the match. The `.exe` miss is an internal inconsistency: the file's own phaseRe already writes `batten(?:\.exe)?`, so the author knew the form and forgot it 70 lines later — on Windows, batten's stated primary platform.

**fix**: Match the program, then scan the rest of the argv for a bare `commit` token: e.g. `(^|[;&|]|\s)git(\.exe)?\s+(?:-[^\s]+(?:[=\s]\S+)?\s+)*commit\b`, and add table tests for `git -C . commit`, `git -c a=b commit`, `git.exe commit`, `git --git-dir=x commit`. Better still: since a missed commit is a silent hole in the core promise, bias to over-matching — treat any Bash command containing the token `commit` preceded by a `git`-ish program as a gate event, and let a false positive be a warning rather than let a real commit through unseen.

**file**: internal/hooks/hooks.go:267 (commitRe)

**verificador**: Reproduced exactly in a fresh sandbox with my own BATTEN_DB. `git -C . commit -m x`, `git -c user.email=a@b.c commit -m x` and `git.exe commit -m x` are all ALLOWED (empty hook output) under the identical session/cwd/DB where plain `git commit -m x` is DENIED. Root cause is one regex at internal/hooks/hooks.go:267 — `(^|[;&|]|\s)git\s+(-[^\s]+\s+)*commit\b`. The flag group only consumes options wh

---

## 2. [BLOCKER/broken] [UNVERIFIED] A second unit entering the same phase name silently steals the phase row out of the first unit's run record

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: US-001's run record is immutable with respect to anything US-002 does. `batten show US-001` should still list both `plan` and `build`.

**why**: Two units open at once is the headline use case (the README's fan-out story, the multi-session row in ROADMAP.md). The cause is that phase nodes are keyed globally: `NodeID: "p-" + phaseID` (cmd/batten/main.go:368) against `node_id TEXT PRIMARY KEY` with `INSERT OR REPLACE INTO nodes` (internal/store/store.go:404). The row is not duplicated, it is *transplanted* — its run_id is rewritten to the new run. I confirmed in SQLite that US-001's first run ended up with p-research/p-build/p-verify/p-close and no p-plan at all, and after `batten phase FOO-9 build` a still-open US-001 run in phase `build` had literally zero phase nodes, so `batten show US-001` printed a header and nothing else. `FinishNode` has the same bug (`UPDATE nodes SET status=? ... WHERE node_id=?`, store.go:413) — finishing a phase would finish whichever run currently owns that node id. This is silent, permanent corruption of the exact artifact this dimension is about, and it also poisons `batten canvas`, the TUI and the vault export, which all read the same nodes.

**fix**: Make the nodes primary key composite: `PRIMARY KEY (run_id, node_id)`, and change FinishNode/AddEdge/lookups to key on (run_id, node_id). Alternatively prefix the node id with the run id. A migration is needed for existing DBs.

**file**: internal/store/store.go (nodes DDL, StartNode ~line 404, FinishNode ~line 411); cmd/batten/main.go:368

---

## 3. [BLOCKER/broken] [UNVERIFIED] `batten ingest` silently discards every request that predates the run and then reports "0 tokens" as a measured number

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: Either count the requests, or say what was dropped and why — e.g. "+0 requests (3 requests / 291.0k tokens skipped: they predate this run, opened at 06:19:07Z). Re-open the run with `batten phase` before the session starts, or use --since." Reporting `0 tokens` and drawing an empty progress bar for a file batten just read and measured at 291,000 tokens is precisely the "invented number" the package exists to prevent — and here it errs downward, the one direction pricing.go explicitly refuses to err in.

**why**: This is the failure mode that defeats the entire dimension, and it is the DEFAULT path, not an edge case. store.RecordUsage drops any row whose TS is below runs.started_at, and `batten phase <unit> build` is normally typed mid-session — the batten-engine skill is literally triggered by a user saying "Fase 3 US-034" partway through a conversation. Everything the session spent before that keystroke is silently gone. My first, honest attempt at this whole exercise died here: a perfectly valid 8-request/120,092-token transcript ingested as `US-001: +0 requests, 0 tokens total, $0.00 imputed` three times in a row with exit 0, and I only found the cause by reading store.go and querying started_at out of SQLite. A real user gets no such hint. The consequence is not just a wrong report: a run that actually blew a 200k ceiling by 45% shows `0 / 200.0k`, OverBudget returns false, and `on_exceed: block` waves the commit through.

**fix**: Have RecordUsage return the count it fenced out alongside `added`, and make cmdIngest print it: silence is the bug, not the fence. Then reconsider the fence itself — attributing a whole session's transcript to the run that session is working is closer to the truth than dropping 291k measured tokens, so at minimum offer `batten ingest --since-session-start` or back-date started_at to the first record of the adopting session. Whatever the policy, `batten budget` must not render a bar for a run whose ledger it knows is incomplete.

**file**: internal/store/store.go (RecordUsage, the `if u.TS < fenceFor(u.RunID) { continue }` fence, ~line 720) and cmd/batten/main.go (cmdIngest, ~line 1054)

---

## 4. [BLOCKER/broken] [UNVERIFIED] `on_exceed: block` is silently not enforced when the gate declares no `checks:` — the hook returns early and never evaluates the budget

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: The commit must be denied on the budget, exactly as it is when the gate happens to declare a check. The advisory about the missing `checks:` is correct and should still be emitted, but it must not consume the return path that the budget ceiling lives on. At minimum the warning should say the budget was not evaluated, rather than implying the only problem is an unverified approval.

**why**: A/B proven with a single-line spec change and nothing else: I added `checks: ['echo qa-ok']` back to the same gate, ran `batten check US-012`, and replayed the byte-identical commit payload — it flipped to `{"permissionDecision":"deny","permissionDecisionReason":"batten: US-012 is over budget. budget.on_exceed=block.\n  ! tokens       1560000 of 200000\n  ! imputed_usd  $7.03 of $2.00 ..."}`. So the ONLY thing standing between a 7.8x-over-tokens, 3.5x-over-dollars run and a clean commit is whether the gate happens to list a shell command. `gate.checks` is not required by batten.schema.json, and a checkless gate is the most likely shape of a first spec — which means the budget tripwire is off precisely for the users who most need the training wheels. Worse, the two surfaces now contradict each other: MCP `batten_verdict_status` tells the agent `commit_denied: true`, the agent dutifully stops, and had it tried the commit anyway the hook would have let it through. The advisory branch is also the one place in the codebase where the "fail open only with a visible warning" principle fails open on something the warning never mentions.

**fix**: Do not return from the else branch. Record the advisory in a local variable, fall through to the budget check, and merge the two: if the budget is blown, deny (and append the checkless-gate warning to the reason); otherwise return the advisory. Add a regression test that asserts an over-budget run with a checkless gate is denied — the current test suite passes with this hole open.

**file**: internal/hooks/hooks.go — verdictGate: the `} else { return advise(...) }` branch (~line 340-350) returns before the budget block at ~line 353

---

## 5. [BLOCKER/broken] [UNVERIFIED] Phase node ids are global, not per-run: starting the same phase on a second work item deletes the first one's phase node and its canvas collapses to a bare header

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: US-005's run graph is untouched by anything that happens to US-006. Its canvas still shows the build phase and its fan-out. Two units working the same phase name concurrently is the normal case — the README's own multi-session story (US-034/sessA vs US-051/sessB) requires it.

**why**: This is the observability dimension's worst possible failure: the surface the README sells as 'the path the work actually took' silently deletes the work. A user opening US-005.canvas in Obsidian after touching any other unit sees one box saying '# US-005' and nothing else — no phase, no agents, no retry, no edges. Nothing warns them; `batten canvas` prints a cheerful 'wrote ... (1 nodes, 0 edges)'. Worse, the run note and the canvas now contradict each other: the note still lists `| backend | backend | ok |` in the fan-out table while the canvas shows an empty run. It also silently corrupted my real seeded run halfway through this session — I only noticed because the canvas shrank. Any repo with more than one open work item hits this on the first `batten phase`.

**fix**: Namespace the id: `NodeID: run.RunID + "/p-" + phaseID` (and the matching src in hooks.go's AddEdge), or change the schema to `PRIMARY KEY (run_id, node_id)`. The edges table has the same defect — `PRIMARY KEY (src_node, dst_node, rel)` with run_id as a plain column — so it should become `PRIMARY KEY (run_id, src_node, dst_node, rel)` in the same migration.

**file**: internal/store/store.go (schema: `nodes.node_id TEXT PRIMARY KEY` — not run-scoped; `AddNode` uses INSERT OR REPLACE) and cmd/batten/main.go cmdPhase (`NodeID: "p-" + phaseID`), plus internal/hooks/hooks.go:494 which builds the same unscoped id for the spawn edge

---

## 6. [BLOCKER/broken] [UNVERIFIED] The commit gate does not exist until some batten command has created a run — so the FIRST commit after adopting batten is ungated, silently

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: A deny, or at minimum a visible warning. The README's headline scene is 'an agent finishes a work item, feels good about it, and reaches for the commit -> permission denied'. On a branch that correctly names TASK-002, with enforcement: enforce and a close phase carrying requires_verdict: ok, batten allows the commit with zero output because no run row exists for TASK-002 yet.

**why**: This is the first thing a newcomer does after `batten doctor` says the gates block: branch, code, commit. It goes straight through. They conclude batten is installed and working — it is installed and doing nothing. The gate only ever fires for units someone already ran `batten phase` or `batten check` on, which is precisely the disciplined case that needed the gate least. It also means the whole product can be bypassed by never touching the CLI.

**fix**: In verdictGate, when the unit resolves but Store.ActiveRun returns no row, do not `return nil, nil`. Either create the run implicitly, or emit a deny/advise that says so: 'batten: no run recorded for TASK-002 — run `batten check` (or `batten phase TASK-002 <phase>`) before committing.' Silence is the one response that teaches the wrong lesson.

**file**: internal/hooks/hooks.go:284-287 (`run, err := h.Store.ActiveRun(...)` -> `return nil, nil // no run recorded: the gate has nothing to say`)

---

## 7. [MAJOR/broken] [CONFIRMED] `batten init --help` writes batten.yaml instead of printing help; every unknown flag is silently swallowed

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: `--help`/`-h` prints the init usage (the flags are `--scan-json` and `--from <doc>`) and exits 0 without writing anything. An unrecognised flag prints `unknown flag: --bogus-flag` and exits non-zero.

**why**: `--help` is the universally safe first probe of an unfamiliar CLI. Here it is a write. It cannot clobber an existing spec (the overwrite guard fires first), but on first contact with a repo it silently creates a tracked-file-to-be that the user did not ask for and may not notice — and it teaches the user that this binary ignores its own flags, which is exactly the wrong lesson right before they type `--from`.

**fix**: Add a `default:` arm returning `fmt.Errorf("unknown flag %q", args[i])`, and an explicit `case "--help", "-h":` that prints usage and returns nil. Same treatment for the other cmdX(os.Args[2:]) entry points.

**file**: cmd/batten/main.go (func cmdInit, lines ~1429-1440: the arg loop has only `case "--scan-json"` and `case "--from"`, no default)

**verificador**: Reproduced exactly, on a fresh copy of the template repo with my own BATTEN_DB, for all three variants the reporter named (`--help`, `-h`, `--bogus-flag`), plus a fourth I added (`--from` with no value). Every one printed the "wrote batten.yaml" banner, exited 0, and left an untracked batten.yaml in the repo.

The source confirms it is not a deliberate behaviour. `cmdInit` at cmd/batten/main.go:14

---

## 8. [MAJOR/broken] [CONFIRMED] Skill→domain mapping is prose-substring noise: batten's own plugin skill and an unrelated global UI skill are assigned to the `ml` domain

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: Either a match a human can verify at a glance (hyphen-segment match on the skill name), or no suggestion at all. batten mapping its own plugin skill `batten-engine` into a project's ML domain is the clearest tell that the signal is noise.

**why**: batten.schema.json says of `skills`: these are what the domain's agent loads. A wrong entry spends fan-out context arguing for the wrong tool — an ML subagent told to load a UI/UX design skill and batten's own workflow engine. It also poisons trust in the whole draft: this is the first block a reviewer reads after the domains.

**fix**: ALREADY FIXED AT HEAD — commit d99b6ef 'fix: skill suggestions match a skill's name, never its prose' replaces the prose scan with `strings.Split(strings.ToLower(s.Name), "-")` segment equality, whose comment describes this exact terraform-gcp/db failure. The prebuilt testbed binary (built 2026-07-27 23:52) predates internal/scan/scan.go (written 2026-07-28 00:06). Action needed: rebuild the testbed binary. Note the fix leaves portal-echarts-dashboards and portal-sse-streaming unmapped, which the new comment accepts as the deliberate trade ('Suggesting nothing is better').

**file**: internal/scan/scan.go (skill/domain matching loop, ~line 105)

**verificador**: I could not refute it. In a fresh sandbox copy of proyecto_ui with my own BATTEN_DB, the designated testbed binary (batten-testbed/batten.exe, built 2026-07-27 23:52:58) produced the reported skills lines byte-for-byte, including `skills: [portal-cicd, portal-data-connectors, portal-semantic-layer, portal-synthetic-data, batten-engine, ui-ux-pro-max]` under `ml`. Every causal claim also checks out

---

## 9. [MAJOR/broken] [CONFIRMED] The `batten_writeset_owner` MCP tool reports `write_allowed: true` for files the PreToolUse guard denies

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: The advisory tool must never be more permissive than the gate. Case A: `agent_id` was supplied and `NodeByAgent` resolves that agent's run unambiguously, so resolveRun should use it instead of declaring 'no active run … nothing is enforced'. Case B: the tool should run the same WriteSetOwnerAcrossOpenRuns check the hook runs, and return `write_allowed: false` naming US-004. When the run genuinely cannot be resolved, the honest default is `write_allowed: null`/unknown with the reason — not `true`.

**why**: The MCP server's own instructions say 'Before writing a file as a fanned-out subagent call batten_writeset_owner', and the tool description says 'if another agent owns it, the PreToolUse hook will DENY your Write/Edit'. In a multi-session repo the tool tells the agent the opposite of what the hook will do, so the agent burns a turn on a write that gets denied — and worse, the note 'nothing is enforced' actively teaches the agent that the fence is off. It is also the one place batten breaks its own stated principle of never reporting an unmeasurable thing permissively.

**fix**: In writeSetOwner: (1) if AgentID resolves a node, use that node's RunID as the run rather than falling through to resolveRun's ambiguity path; (2) after the in-run WriteSetOwner check, call st.WriteSetOwnerAcrossOpenRuns(project, rel, run.RunID) and set write_allowed=false with the other unit named; (3) initialise WriteAllowed only after a run is resolved, so the unresolved case cannot default to true.

**file**: internal/mcp/mcp.go (queries.writeSetOwner ~line 645, resolveRun ~line 847)

**verificador**: Reproduced exactly, in a fresh sandbox (`.../scratchpad/refute-ws-az9`, own `BATTEN_DB=az9.db`, own copy of proyecto_ui), and the reporter's EXPECTED is right — this is not one of batten's deliberate fail-open designs.

WHAT I REPRODUCED
Case A (agent_id supplied, no run_id) — identical output to the report:
  {"note":"several runs are open (US-003, US-002, US-004): pass unit or run_id. batten wil

---

## 10. [MAJOR/broken] [CONFIRMED] NotebookEdit is wired into the write-set guard but reads the wrong JSON key, so notebooks are never guarded

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: A NotebookEdit on another agent's claimed .ipynb should be denied exactly like Edit/Write. NotebookEdit is listed in `preToolUse`'s switch and in the plugin's `"matcher": "Write|Edit|NotebookEdit"`, so the intent is unambiguous — but `writeInput` only declares `FilePath string \`json:"file_path"\``, and NotebookEdit's parameter is `notebook_path`. The unmarshal succeeds with an empty path and writeSetGuard returns early on `path == ""`.

**why**: It is a silent hole in the one rule the project calls load-bearing, and it fails open with no warning at all — not even the advisory path. It matters most in exactly the domain this template repo has (ml/, EDA notebooks), and the hook config's explicit NotebookEdit matcher means a user reading the plugin config would reasonably believe notebooks are covered. Two agents can shred one notebook and batten will report nothing.

**fix**: Add `NotebookPath string `json:"notebook_path"`` to writeInput and use `cmp.Or(wi.FilePath, wi.NotebookPath)` as the path. A regression test with a verbatim NotebookEdit payload would have caught it — guard_test.go only exercises file_path.

**file**: internal/hooks/hooks.go (writeInput struct ~line 244, preToolUse ~line 256)

**verificador**: Reproduced exactly as described in a fresh sandbox with an isolated BATTEN_DB. A2 sending a real Claude Code NotebookEdit payload (key `notebook_path`) at a notebook claimed by A1 produced empty output = allowed; the same tool with `file_path` produced a full DENY, as did an Edit control. Root cause verified in source: `internal/hooks/hooks.go:244-246` declares `writeInput` with only `FilePath str

---

## 11. [MAJOR/broken] [CONFIRMED] `batten check` exits 0 when the gate is BLOCKED

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: A non-zero exit when the recorded verdict is blocked, so `&&` chains and CI steps stop.

**why**: Exit status is the only thing a script reads. Any pipeline step that runs `batten check $UNIT` reports success while batten has just recorded BLOCKED, and an agent that writes `batten check US-011 && git commit ...` gets past its own guard rail — the hook still catches the commit, but only if the commit is spelled in a form the regex matches (see the first finding), so the two defects compose into a real hole. The human-readable output is correct; only the machine-readable signal is wrong, which is the worst combination because the text looks right in the transcript.

**fix**: Return a sentinel error (or os.Exit(2)) when result != "ok", after the summary is printed, and document the exit codes in printUsage: 0 = gate ok, 2 = gate blocked, 1 = batten could not run. Keep 1 and 2 distinct so CI can tell 'the checks failed' from 'batten is misconfigured'.

**file**: cmd/batten/main.go:717 (cmdCheck returns nil regardless of `result`)

**verificador**: Reproduced exactly in a fresh sandbox. `batten check US-011` printed `US-011: BLOCKED (batten-verified). one or more gate checks failed` and exited 0, and `batten.exe check US-011 >/dev/null 2>&1 && echo 'gate green'` printed `gate green`.

The stronger version of the finding, which the reporter did not state: the exit code carries **zero** information in either direction. Same command, same unit,

---

## 12. [MAJOR/broken] [REFUTED] A commit for an attributable unit with no recorded run is allowed in total silence

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: A warning at minimum. The unit IS attributable here — the branch is feature/US-011-semantic-layer and the spec's own pattern matches US-011 out of it. The only thing missing is that `batten phase US-011 <p>` was never run, which is a setup omission, not an ambiguity.

**why**: Forgetting one command silently disables the product's core denial for that whole work item, and nothing anywhere tells you. This contradicts the codebase's own stated principle #3, which it applies correctly two branches later: the checkless-gate path fails open with a loud advise() even under `enforcement: enforce`, on the reasoning that 'a gate that silently degrades to trusting the model is worse than no gate, because it will be believed'. The same reasoning applies here and is not applied. The comment at the deny site says 'no run recorded: the gate has nothing to say' — but it has something very useful to say: 'this branch names US-011 and no run was ever opened for it; run `batten phase US-011 build`'.

**fix**: When activeUnit() resolved a unit but ActiveRun returns no row, emit advise() rather than nil: "batten: this branch names US-011 but no run was ever opened for it, so the close gate cannot act. Run: batten phase US-011 build". Keep the silent path only for the genuinely unattributable case (activeUnit == "").

**file**: internal/hooks/hooks.go:284 (verdictGate, the `if err != nil { return nil, nil }` after ActiveRun)

**verificador**: The OBSERVED behaviour reproduces byte-for-byte, but the finding's premise ("in total silence") is false at the system level, and the EXPECTED is wrong: this is a deliberate, in-source-documented, unit-tested design with an explicit compensating disclosure.

1) It is deliberate and labelled as such. internal/hooks/hooks.go:284-287:
   run, err := h.Store.ActiveRun(h.Spec.Project, unit)
   if err !

---

## 13. [MAJOR/broken] [UNVERIFIED] `batten init --from <doc>` never reads the doc — it only prints its own argument back

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: Either (a) --from actually mines the prose doc as DESIGN.md §'loom init — el arranque que hace que esto sea usable' promises ('`loom init --from docs/general/prompts_optimizers_v2.local.md` → **lee tu flujo en prosa y propone el `loom.yaml`**'), or at minimum (b) it records the path as `unit.plan:` in the spec — the key exists in batten.schema.json and README line 107 shows `plan: docs/backlog.md` — and (c) it errors out when the file does not exist.

**why**: `batten --help` advertises `batten init [--from <prose-doc>]` and DESIGN.md calls it the migration path, with the note that '`loom init` mediocre = producto muerto ... es la feature más importante'. A user pointing --from at their 1500-line planning doc gets a file that contains not one fact from it and no signal that nothing happened. Silently accepting a nonexistent path removes the last chance to notice.

**fix**: Short term: os.Stat the path and fail loudly if missing; set facts.UnitPlan = from so the spec at least records it; and change the stdout line from an instruction to the user into an honest 'not read by the binary — run /batten-init to mine it'. Long term: hand `from` to the /batten-init skill through --scan-json (add a `from_doc` field to the scan facts).

**file**: cmd/batten/main.go (func cmdInit: `from` is captured at line ~1437 and used only at line ~1475 in a Printf)

---

## 14. [MAJOR/broken] [UNVERIFIED] A one-character typo in a top-level spec key is silently ignored — `enforcment: report` makes doctor report `gates block`

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: batten.schema.json declares `"additionalProperties": false` at the root (line 12). `batten doctor` — whose one-line description is 'validate spec, report live capabilities' — should reject or at least warn: `⚠ unknown key "enforcment" at top level (did you mean "enforcement"?)`.

**why**: This is the safety-critical key. A team that types `enforcment: report` believes gates warn; batten silently applies the default, which is `enforce`, and `git commit` starts getting denied for want of a verdict with nothing in the spec explaining why. The reverse typo pattern applies to `invariants`/`invariant` and `domains`/`domain`, where the failure is open instead of closed: the rules that 'ride VERBATIM into every fanned-out agent's prompt' just vanish, and neither doctor nor the loader says a word. doctor validates skill names and agent names against reality but not the spec's own key set against its own shipped schema.

**fix**: Use a yaml.Decoder with KnownFields(true) so unknown keys are a load error, or validate the parsed document against the embedded batten.schema.json inside `doctor`. If a hard error is too aggressive for existing specs, emit `⚠ unknown key` per offender in doctor and keep Load permissive.

**file**: internal/spec/spec.go (func Load, line ~206: `yaml.Unmarshal(b, &s)` — non-strict)

---

## 15. [MAJOR/missing] [UNVERIFIED] unit came out `TASK-\d+` and no `unit.plan`/`unit.locator`, in a repo whose backlog init itself catalogued

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: `unit: {name: US, pattern: 'US-\d{3}', plan: context/planeacion_proyecto.md, locator: '### {id}'}` — every one of those four values is recoverable from a file the scan already opened.

**why**: unit.pattern is how batten answers 'which unit is this commit for?'. With TASK-\d+ against a US-### backlog, no branch, prompt or commit in this repo will ever match, so the anchor, the diff scoping and the verdict gate never bind to anything — the spec is inert on arrival. And `artifacts.handoff: docs/{id}.md` is a guess made in the same breath as ignoring the real plan doc, so a user who fixes only the pattern still has no route from a unit id to its acceptance criteria. Confirmed the inference machinery is fine, only its input source is: in a copy with `git checkout -b feature/E2-US-015-auth-jwt`, init emitted `unit="US"` / `pattern: 'US-\d{3}'`.

**fix**: ALREADY FIXED AT HEAD — commit 3122d29 'fix: init reads the backlog for the unit, not just branch names' adds unitFromDocs()/pickUnit() and emits `plan:` and `locator:` (scan.go ~583-590). The testbed binary predates it; rebuild. When re-testing, check the US-UX-01..08 headings: pickUnit's regex reads those as prefix 'UX', so 'US' wins on count and the emitted `US-\d{3}` cannot match `US-UX-01` — 8 of 44 backlog items would stay invisible.

**file**: internal/scan/scan.go (deriveUnit / unitFromDocs / pickUnit)

---

## 16. [MAJOR/broken] [UNVERIFIED] `batten claim` does not check other open runs: two sessions are both told they own the same file, then BOTH are denied on it

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: The second `batten claim` should fail the same way an in-run collision does — `write-set collision: backend/app/main.py is already owned by US-010 (run ...)`, rc=1 — so the planning bug surfaces at claim time, where the build skill says to fix the plan. Instead the collision is accepted with a reassuring message and detonates later as a mutual deadlock in which neither declared owner can write the file it was told it owns, and neither denial message mentions that the caller is itself a recorded owner.

**why**: This is the exact scenario the write-set guard exists for, and it is the one ROADMAP.md line 35 claims is 'enforced by a PRIMARY KEY (run_id, path), so disjointness is a database constraint, not advice' and line 38 claims is 'write-sets defended between open runs'. The PK is per-run, so across runs disjointness is neither a constraint nor advice — it is nothing at claim time, and a hard deadlock at write time. Two parallel sessions (the documented multi-session workflow) plus one overlapping file bricks both fan-outs at hour six of an unattended run, and the escape hatch is non-obvious: nothing in the deny message tells you a duplicate claim was recorded.

**fix**: In ClaimWriteSet, before inserting, also run the WriteSetOwnerAcrossOpenRuns check that the hook already uses, and refuse with the owning unit and run named. That makes the claim command agree with the guard, and turns the deadlock into a plan bug caught at claim time.

**file**: internal/store/store.go (ClaimWriteSet, ~line 481) and cmd/batten/main.go (cmdClaim, ~line 305)

---

## 17. [MAJOR/broken] [UNVERIFIED] Opening a second unit steals the first unit's phase node, emptying its run DAG and its exported canvas

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: US-012's DAG should be unaffected by anything US-013 does. Node ids must be namespaced per run. `AddNode` does `INSERT OR REPLACE` on a globally-unique `node_id TEXT PRIMARY KEY`, and `cmdPhase` builds that id as `"p-" + phaseID` with no run in it — so every `batten phase <any-unit> build` in a shared DB moves the single row `p-build` to the newest run. The effect ping-pongs: re-running `batten phase US-002 build` moved the node back to US-002 (canvas 1→4 nodes) and simultaneously emptied US-003 (4→1). It also survived a run being closed: US-001, closed and rolled_back, still exported an empty canvas.

**why**: The canvas/vault export is the human-facing artifact of a run, and it goes from 4 nodes to just a header the moment a colleague (or the same user in a second terminal) starts another unit — silently, with no error. canvas.Render has an explicit 'orphans get their own column so nothing is silently dropped' safety net, but it only catches nodes with NO parent edge; here the subagents still have a `spawn` edge pointing at a `p-build` node that now belongs to a different run, so they are dropped anyway. `batten show` degrades the same way, losing the phase row. Everything downstream that reasons over the DAG (canvas, vault note, TUI) inherits it.

**fix**: Make the phase node id run-scoped — `"p-" + run.RunID + "-" + phaseID` (hooks.go's `AddEdge(run.RunID, "p-"+run.Phase, ...)` and any other constructor must use the same helper). Belt and braces: have canvas.Render treat a child whose parent node is absent from `nodes` as an orphan rather than dropping it, so a stale edge can never make work disappear.

**file**: cmd/batten/main.go (cmdPhase, `NodeID: "p-" + phaseID` ~line 368) and internal/store/store.go (AddNode INSERT OR REPLACE ~line 404)

---

## 18. [MAJOR/missing] [UNVERIFIED] An agent denied an Edit can perform the identical write through Bash, with no denial and no warning

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: At minimum a visible warning on the Bash path, in the spirit of the project's own principle #3 ('fail open only with a warning'). PreToolUse already fires on Bash for the verdict gate (plugin/claude-code/hooks/hooks.json matches Bash), so the payload reaches the binary — batten simply never looks at the command for writes into claimed paths. A cheap heuristic (shell redirects `>`/`>>`/`tee`, plus `sed -i`, `mv`, `cp`, `patch`, `python ... open(...,'w')`) covering the obvious cases would turn a silent bypass into a loud one.

**why**: The write-set guard is the project's headline claim ('Two agents must never write the same file — that is what makes the fan-out safe'), and the deny message tells the agent to fix the plan rather than route around the fence. A model that has just been denied will very plausibly reach for Bash — this is the single most likely real-world escape, it leaves no trace, and no doc I could find (README.md, DESIGN.md, ROADMAP.md, batten-build.md) states that the guard covers only Write/Edit/NotebookEdit. Users will read 'denied by a hook' as 'cannot happen'.

**fix**: In the Bash branch, after verdictGate, scan the command for repo-relative/absolute paths that appear as write targets and run them through the same ownership lookup; emit `advise()` (never deny — a false positive on a shell string must not block a session) naming the file and its owner. Document the scope limit in README/DESIGN either way.

**file**: internal/hooks/hooks.go (preToolUse, the `case "Bash"` branch only calls verdictGate ~line 249)

---

## 19. [MAJOR/confusing] [UNVERIFIED] A directory claim is accepted and reported as protecting files, but fences nothing

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: Either reject a claim on an existing directory ('a write-set is a list of files; frontend/ is a directory — list the files, or claim frontend/** if prefix claims are supported'), or honour it as a prefix claim and deny writes underneath. What must not happen is the current outcome: the command asserts 'any other agent writing them is now denied' about a claim that denies nothing. Related and equally silent: `batten claim agent-fe-4 C:/Windows/System32/drivers/etc/hosts` (outside the repo) was also accepted with 'owns 1 file(s)', and can never match, because cmdClaim keeps the absolute path when filepath.Rel escapes the root while writeSetGuard only ever computes repo-relative paths.

**why**: 'This agent owns frontend/' is the most natural way for a planner to express a write-set, and batten's own plan/build skills describe write-sets in per-domain terms ('domain <D> — 4 agents'). The orchestrator gets an unqualified success message and a false sense of protection, and the failure is invisible until two agents have already written the same file — which is precisely the outcome the guard is sold on preventing. A claim that silently protects nothing is worse than a rejected claim.

**fix**: In cmdClaim, `os.Stat` each argument: reject directories with a message that shows the file-list form, and reject (or at least warn on) paths whose relative form starts with `..`. Print the claimed paths back, not just a count, so 'owns 1 file(s)' can never hide a claim on the wrong thing.

**file**: cmd/batten/main.go (cmdClaim ~line 305)

---

## 20. [MAJOR/missing] [UNVERIFIED] `batten check` alone closes a unit — an empty diff and zero acceptance-criteria judgment commit clean

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: The gate should still deny until something has judged US-013 against its acceptance criteria. `batten check` knows nothing about US-013 — it ran the repo's suite, which was green before US-013 existed.

**why**: The README says the one failure batten exists to kill is 'closing a work item because it LOOKS fine'. This is the mechanised version of that failure: the gate is satisfied by a green suite that has no relationship to the unit. batten itself knows this is wrong — `batten check`'s own safe_next_step reads 'add your acceptance-criteria judgment, then close' — but the hook never requires that judgment. So the shortest path through the gate is `batten phase X build && batten check X && git commit`, which an agent optimising for a green gate will find, and which certifies nothing.

**fix**: Require BOTH halves when the gate declares checks: a source=batten ok (the mechanical half) AND a source=agent ok whose ts is >= the batten verdict's (the judgment half). The messages already exist for each; just add the second condition, e.g. "batten: US-013 passed the gate's checks but nothing has judged it against its acceptance criteria. Emit the verdict envelope for gate qa." Optionally also refuse when `diff_from: anchor` yields an empty diff — closing a unit that changed no files is almost always a mistake.

**file**: internal/hooks/hooks.go:328 (the `if g.Checks > 0` branch checks only LatestVerdictBySource(...,"batten"))

---

## 21. [MAJOR/broken] [UNVERIFIED] An override is invisible to every CLI surface, and `batten show` states the opposite of the truth

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: `batten show US-014` should say the gate is OPEN because of an override, quote the reason, and give its timestamp; `batten runs` should mark the row (e.g. VERDICT `override`); the canvas should carry it.

**why**: `batten override` promises "This override is in the audit log" — but the only reader is sqlite. Worse, the one command a human runs to review a unit prints a statement that is now false: it says the gate will deny while the hook is allowing. A reviewer scanning `batten runs` before a merge sees an un-gated unit as indistinguishable from an ungated one. And because store.HasOverride returns only a bool, the `--reason` string — the thing the CLI refuses to proceed without, on the grounds that 'an override with no stated reason is just a disabled gate' — is write-only: `grep -rn 'overrides' --include=*.go` shows nothing in the repo ever reads the reason column.

**fix**: Add `store.Overrides(runID) []Override` returning gate/reason/ts. In cmdShow, look it up before the verdict block and print e.g. `⚠ OVERRIDDEN 2026-07-28 00:24 (gate qa): hotfix for the Friday demo... — the close gate is OPEN for this run`, and never print 'the close gate will deny a commit' when an override exists. Add an `override` marker to the VERDICT column in cmdRuns and a node/label in cmdCanvas.

**file**: cmd/batten/main.go:483 (cmdShow) and internal/store/store.go:935 (HasOverride returns bool, never the reason)

---

## 22. [MAJOR/confusing] [UNVERIFIED] A blocked gate never names the failing check, and no batten surface can show you the output it captured

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: The denial should name which check failed ("python -m pytest backend/tests -q failed (exit 1)") and point at where the output is; `batten show` should be able to print the evidence it stored, e.g. via `batten show US-011 --full`.

**why**: This is the message the task asked me to look for: technically correct, and it does not tell the user what to do next. 'fix the failures' — which failures? batten ran the command, captured 9 lines including `FAILED backend/tests/test_semantic.py::test_resolve_grain_rejects_unknown`, and stored them; then every surface it ships throws them away. cmdShow truncates each evidence item at the first newline and appends '…', and there is no flag to see more (cmdShow takes only args[0]). So the only ways to learn why the gate is shut are to re-run `batten check` (paying the test-suite cost again) or to open the sqlite file by hand. For an unattended overnight run, where re-running is exactly what you cannot do cheaply and nobody is watching the console, the captured evidence is the whole point and it is unreachable.

**fix**: Two small changes. (1) In verdictGate, when the blocked verdict has source=batten, append the first FAIL evidence item's head to the reason: "failing check: python -m pytest backend/tests -q (exit 1)" and end with "see: batten show US-011 --full". (2) Add `--full` to cmdShow that prints evidence items verbatim, indented, instead of firstLineOf.

**file**: cmd/batten/main.go:509 (firstLineOf) and internal/hooks/hooks.go:318 (the deny message uses v.Why, never the failing evidence item)

---

## 23. [MAJOR/broken] [UNVERIFIED] `batten close --status ok` accepts an agent-asserted verdict that the commit gate refuses — the "checks must be RUN, not asserted" rule has a documented back door

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: Both paths enforce the same rule. If the commit hook demands a verdict with `source='batten'` because the gate declares `checks`, then `batten close --status ok` must demand it too — or refuse and say so.

**why**: The two enforcement paths disagree, and the weaker one wins. The DB after this sequence reads `('US-001','ok','close','70a7179')` with a single verdict row of `('qa','ok','agent')` — the unit is recorded as cleanly closed, its write-set claims are released, and the resolution artifact is stamped, without `make check` or `make test` ever having been executed. The code even claims otherwise: cmd/batten/main.go:678 says "Closing as ok obeys the same rule as the commit gate", but `gateReadyToClose` (main.go:698-713) only calls `LatestVerdict` and never `LatestVerdictBySource(..., "batten")` the way internal/hooks/hooks.go:329 does. An agent that gets denied at the commit can close the run anyway and the run record will say it passed.

**fix**: In `gateReadyToClose`, mirror the hook: when `sp.Gates[gate].Checks` is non-empty, require `LatestVerdictBySource(run.RunID, gate, "batten").Result == "ok"`. Better still, extract one `gateVerdictOK(spec, store, run)` helper and call it from both the hook and cmdClose so they cannot drift again.

**file**: cmd/batten/main.go:698 (gateReadyToClose) vs internal/hooks/hooks.go:326-337

---

## 24. [MAJOR/wrong-docs] [UNVERIFIED] The README's headline flow is wrong: recording a verdict with cited evidence does NOT let the commit through

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: README.md lines 58-66 state: "A verdict that cites three real things gets through, and only then: $ batten verdict --unit US-034 --file verdict.json / verdict US-034-qa: ok (3 evidence) / $ git commit -m ... / [feature/US-034 8f2a1c9] feat: add order rate limiting". Either the commit succeeds as documented, or the README documents the `batten check` step.

**why**: This is the product's central worked example and it does not reproduce. A first-time user follows the README exactly, produces a verdict with three genuine citations, and is still denied — by a message pointing at a command (`batten check`) that appears nowhere in the README, nowhere in `batten --help`, and nowhere in batten.schema.json. The new behaviour is *better* than the documented one, which makes it worse that it is undocumented: the user's reasonable conclusion is that the gate is broken. The same gap exists in plugin/claude-code/skills/batten-verdict/SKILL.md, which teaches the envelope and says nothing about `batten check`.

**fix**: Update the README's worked example to `batten check US-034` -> `batten verdict ...` -> `git commit`, and say explicitly that a gate with declared `checks` requires a batten-produced verdict. Add the same note to the batten-verdict and batten-verify skills and to the `gates.checks` description in batten.schema.json.

**file**: README.md:58-66; plugin/claude-code/skills/batten-verdict/SKILL.md; batten.schema.json ($defs.gate.checks)

---

## 25. [MAJOR/missing] [UNVERIFIED] `batten check` and `batten close` exist but are absent from `batten --help` — including the command the deny message tells you to run

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: Every dispatched subcommand appears in `--help`. `close` is the terminal step of the documented lifecycle and `check` is what every denial tells the user to run.

**why**: The gate denies a commit and says `Run: batten check US-001`. The user runs `batten --help` to find out what that is and it is not there; nor is it in README.md or batten.schema.json. `batten close` — the command that ends a run — is equally invisible. `measure`, `hook-debug` and `version` are also undispatched from help (cmd/batten/main.go:79-87). Compounding it, argument parsing silently swallows unknown flags: `batten runs --help` prints `no runs yet` instead of usage, and `batten show US-001 --json`, `--run <id>` and `-v` all print the same default output with no error, so there is no way to discover that those flags do not exist.

**fix**: Add `close`, `check` (and ideally `measure`, `version`) to the usage block. Make unknown flags an error rather than a silent no-op, and make `<cmd> --help` print that command's usage.

**file**: cmd/batten/main.go (usage string near the dispatch switch at line 49)

---

## 26. [MAJOR/broken] [UNVERIFIED] Phase completion is never recorded — every phase reads `running` forever, including after the run is closed

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: Advancing to the next phase marks the previous one done; `batten close` marks the run's phases terminal. A closed run should not display five phases all `running`.

**why**: The run record cannot answer "where did this actually get to?" — the one question it exists to answer. In SQLite every phase node has `ended_at = NULL`; `FinishNode` is never called for phase nodes anywhere in the non-test code. `batten canvas US-001` inherits it verbatim: all five phase cards render `## <phase>\n`running`` on a run whose header says `status ok`. Anyone reading the canvas or the TUI sees a run that is simultaneously finished and entirely in progress.

**fix**: In cmdPhase, call FinishNode on the outgoing phase node before starting the new one (status ok, or `blocked` if the phase's gate ended blocked). In cmdClose, finish whatever phase node is still open. Both need the composite-key fix from the node-theft finding first, or they will finish another run's node.

**file**: cmd/batten/main.go:368 (cmdPhase node start), cmd/batten/main.go:639 (cmdClose); internal/store/store.go:411 (FinishNode)

---

## 27. [MAJOR/broken] [UNVERIFIED] doctor's stale-run warning can never be cleared by activity — `events.run_id` is always NULL, so the "no activity" clause is dead code and the message is false

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: Either the warning clears when the run is worked on (its own advice is "close or resume them"), or it is worded as "opened >48h ago" so the user is not told something false.

**why**: `StaleRuns` computes activity as `COALESCE((SELECT MAX(ts) FROM events e WHERE e.run_id=r.run_id), r.started_at)` (internal/store/store.go:1087), but the only LogEvent call site in the whole non-test codebase passes an empty run id — `h.Store.LogEvent("", "", event, ...)` (internal/hooks/hooks.go:177) — and `nullable("")` turns that into SQL NULL. So the subquery never matches anything and the check degenerates to `started_at < cutoff`. The warning is really "opened >48h ago" and the only way to silence it is to close the run. On a long-lived unit (a two-week epic under active daily work) doctor nags forever and the user learns to ignore it, which is exactly how a real abandoned run then gets missed. It also means the entire events table is orphaned from the run graph — nothing can ever be joined back to a run.

**fix**: Resolve the run in the hook handler (it already does, via activeUnit/EnsureRun) and pass run.RunID to LogEvent. Also bump the run's activity timestamp on `batten phase`, `batten check` and `batten verdict`, so CLI-driven work counts. Until then, reword the message to "opened >48h ago".

**file**: internal/hooks/hooks.go:177 (LogEvent call), internal/store/store.go:1083 (StaleRuns), cmd/batten/main.go:1321 (doctor message)

---

## 28. [MAJOR/broken] [UNVERIFIED] With two or more open unowned runs the commit gate silently stops enforcing — no deny, no warning, nothing on stdout

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: At minimum an `additionalContext` / advisory saying "batten cannot tell which unit this commit belongs to — the verdict gate is NOT enforcing; bind this session with `batten phase <unit> <phase>`". Silence here is the one thing the README says batten will never do.

**why**: `activeUnit` deliberately returns "" when ambiguous (internal/hooks/hooks.go:665, comment: "ambiguous: refuse to guess, and refuse to block"), and the SessionStart hook does surface it — but only once, at session start, and only if the user reads the injected context. Every subsequent commit in that session sails through ungated with no output at all. This directly contradicts README.md's core principle that batten fails open only *out loud* ("a gate that silently degrades to trusting the model is worse than no gate, because it will be believed" — a comment the codebase itself writes at hooks.go for the no-checks case, but not for this one). Refusing to guess is right; refusing to say anything is not. Note the interaction with the unit-pattern finding below: junk runs from typos accumulate and are what pushes a repo into this state.

**fix**: In the ambiguous branch, emit an `advise()` (the mechanism already used for the gate-with-no-checks case a few lines down) on every gated tool call, not just at SessionStart: name the open units and the exact bind command. Consider also using the commit message's own unit id as a tiebreak before giving up.

**file**: internal/hooks/hooks.go:626-666 (activeUnit), ~line 337 (the advise() precedent)

---

## 29. [MAJOR/broken] [UNVERIFIED] Unit ids are never validated against the declared `unit.pattern` — a typo opens a permanent phantom run

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: `batten: "FOO-9" does not match this project's unit pattern 'US-\d{3}'` and exit 1 — the same courtesy `batten phase US-001 deploy` already gets for an unknown phase (`batten: no phase "deploy" in batten.yaml`).

**why**: `unit.pattern` is described in batten.schema.json as the thing that defines what a unit id is, and `batten phase` — the one command that mints runs — is the only place it is not applied. A lowercase typo (`us-001`) opens a *separate* run that `batten close US-001` will never find, so it stays open forever; each phantom feeds the open-run count that disables the commit gate (previous finding); each also steals the `p-build` phase node from whichever real run held it (first finding). I hit exactly this compound failure by accident: after one stray `batten phase FOO-9 build`, the still-open US-001 run in phase `build` had zero phase nodes and the gate had gone quiet. The strictness the phase-id check already has is the right model.

**fix**: In cmdPhase (and cmdClose/cmdCheck/cmdVerdict), run the id through `sp.MatchUnit` / the compiled pattern and reject a non-match, with the pattern quoted in the error and a `--force` escape if genuinely needed.

**file**: cmd/batten/main.go (cmdPhase, ~line 340-380); internal/spec/spec.go (MatchUnit)

---

## 30. [MAJOR/broken] [UNVERIFIED] `batten measure`'s token column omits all cache buckets, understating by up to 21.9x, and sits beside a dollar column computed on the full basis

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: `  claude-opus-4-5-20260401     15 req, 2.0M tokens, $8.98` — the same five-bucket definition batten.schema.json gives for tokens_per_run ("all five buckets: input, output, 5m cache write, 1h cache write, cache read") and the same one `batten budget`, `batten runs` and `batten ingest` already use. If an input+output-only figure is wanted it needs its own labelled column, not the word "tokens".

**why**: The word "tokens" means two different things in two commands of the same binary, differing by an order of magnitude, with no hint in the output. The row is also internally impossible on its face: $8.98 over "92.1k tokens" implies about $97 per million for a model published at $5/$25 input/output — anyone doing the arithmetic to sanity-check batten's numbers concludes batten is broken. The stated purpose of this table is to prove the cheap tier is carrying the mechanical work; because cache dominates real fan-out traffic and cache ratios differ sharply per model (21.9x for the orchestrating parent vs 9.5x for a short-lived subagent), the omission distorts exactly the comparison the table exists to make. This is a number batten presents that it did not measure the way it says it did.

**fix**: Change the SUM to `SUM(u.input_tokens+u.output_tokens+u.cache_write_5m+u.cache_write_1h+u.cache_read)` to match runs.tokens_spent and every other surface. Add a test asserting MeasureByModel's total equals the sum of runs.tokens_spent over the same rows.

**file**: internal/store/store.go — MeasureByModel, ~line 1135: `SUM(u.input_tokens+u.output_tokens)`

---

## 31. [MAJOR/broken] [UNVERIFIED] `batten measure` prints "$0.00" for an unpriced model, presenting unknown cost as free

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: `  claude-opus-4-9-20260901     2 req, 57.9k tokens, UNPRICED (no published rate in batten's table; tokens are exact)` — or at minimum a `?` marker and a footnote. `$0.00` in a column headed "spend" is a claim that the model was free.

**why**: pricing.go opens with a comment stating the exact rule this output breaks: "A model we have no entry for is UNPRICED, not free ... Guessing a rate for an unrecognized model would corrupt the budget ledger in the one direction that matters — downward — so we refuse to." cmdIngest honours it; cmdMeasure does not. The failure mode is specific and likely: the price table is a hardcoded prefix list, so the first day a new model id ships, every run using it silently reports as costing nothing, and `batten measure` — the command whose whole job is "where did the tokens actually go" — will name the new model as the cheapest thing in the fleet. The ingest warning that would have caught it scrolled past hours earlier.

**fix**: Have MeasureByModel also return the count of rows whose model has no rate (usage.rateFor returning !ok, or simply imputed_usd == 0 with tokens > 0), and render those as UNPRICED instead of $0.00. Same treatment for the `$0.00` that would appear in `batten runs`.

**file**: cmd/batten/main.go — cmdMeasure, the `fmt.Printf("  %-28s %d req, %s tokens, $%.2f\n", ...)` line ~line 583; store.ModelSpend carries no unpriced flag

---

## 32. [MAJOR/confusing] [UNVERIFIED] `batten budget` and `batten runs` present the imputed total as complete when part of the run is unpriced

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: Something that marks the dollar figure as a floor rather than a measurement, e.g. `· imputed_usd  ≥$0.39 / $2.00  [==..........]  (27% of this run's tokens use an unpriced model — the true figure is higher)`. The `·` mark and the 2/12 progress bar assert that the run is comfortably inside its dollar ceiling; batten cannot know that.

**why**: 27.4% of this run's tokens (57,915 of 211,227) carried no price, so $0.39 is a lower bound, not a measurement — but budget draws a bar and marks the ceiling as unbreached exactly as it would for a fully-priced run. This is the same class of statement batten refuses to make for quota_pct one line below, where it correctly prints NOT MEASURABLE rather than 0. The information exists (ingest printed the model name once) and store.Usage already holds imputed_usd == 0 rows with non-zero tokens, so the surface has everything it needs. On a subscription the dollar ceiling is the ceiling users are most likely to reason about loosely, which is exactly why a silent floor is dangerous here.

**fix**: Add a `Partial bool` (or `UnpricedTokens int64`) to store.Ceiling, set it when the run has usage rows with tokens > 0 and imputed_usd == 0, and render a `≥` plus a one-line note. The MCP budgetOutput should carry the same field so the agent sees it too.

**file**: cmd/batten/main.go — cmdBudget (~line 1105 imputed_usd branch) and cmdRuns; store.Ceiling has no "partial" flag

---

## 33. [MAJOR/confusing] [UNVERIFIED] `batten measure` reports a headroom / code-graph mean over runs whose flag was never recorded, and drops the "insufficient" caveat once there are 3 of them

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: The `unknown` bucket should not be rendered as a row under a heading that claims to describe a headroom or code-graph effect — at most a footer such as "4 further runs are excluded: their code-graph state was not recorded". And the whole `headroom effect` section should be suppressed when capabilities.compression is not declared, since batten never probed headroom for any of those runs.

**why**: Both blocks are printed under the heading "<X> effect on THIS project's runs", so `unknown  12 run(s): 190.3k tokens, $0.79 imputed (mean)` reads as a measurement of headroom's effect. It is not: capabilities.compression was never declared, headroomAlive() was never called, and runs.headroom is NULL for all 12. This is a mean over a column that means "we did not look", presented as a result. The caveat logic makes it worse rather than better — it keys off `g.Runs < 3`, so the moment the unknown bucket reaches three runs the "(insufficient)" note disappears and the row stands unqualified. `docs/VERIFICATION.md` claims "con 2 runs dice 'insufficient — need ≥3'; no inventa conclusión", which holds for the with/without rows I proved out, but the unknown row escapes the rule entirely. This is the one command whose stated purpose is to replace vendor claims with the user's own numbers.

**fix**: In printFlagComparison, filter the 'unknown' label out of the rendered rows and report it as an excluded-count footer instead. In cmdMeasure, skip the headroom section unless sp.Capabilities.CompressionEnabled() && sp.Capabilities.Compression.Measure, and skip the code-graph section unless sp.Capabilities.GraphEnabled() — the same conditions that decide whether the flag is ever written.

**file**: cmd/batten/main.go — printFlagComparison (~line 600) renders every group including 'unknown'; internal/store/store.go — measureByFlag CASE returns 'unknown' for NULL (~line 975); cmdMeasure calls MeasureByHeadroom unconditionally (~line 588)

---

## 34. [MAJOR/confusing] [UNVERIFIED] quota_pct stays NOT MEASURABLE with a misleading remedy when more than one run is open, even with the statusline live and sampling

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: The remedy must name the actual cause. Something like `· quota_pct    NOT MEASURABLE — 2 runs are open and none is bound to a session, so no quota baseline could be attributed. Bind this session (commit or edit a file in the repo) or close the other run.` The statusline is plainly installed and working — it printed `5h 12%` and `5h 31%` on the two lines directly above.

**why**: The refusal itself is correct and admirable — internal/statusline/statusline.go's `pick` deliberately declines to baseline an ambiguous repo rather than mis-attribute another session's quota, and that is the right call. But the message blames the one thing that is demonstrably not wrong, and it is unfalsifiable from the user's side: they run `batten statusline --install`, nothing changes, and there is no further hint. Multiple open runs is the normal state for the fan-out workflow batten is built for, so a user can easily conclude the quota ceiling simply does not work. I only found the real cause by reading `pick`; after closing the other run the identical statusline payloads produced `· quota_pct 0.0% / 15.0%` and then `! quota_pct 19.0% / 15.0%` with a correct commit denial. Note the MCP surface already words this far better ("It is not installed, or it has not sampled since this run opened, or the window rolled over mid-run") — the CLI should borrow that, plus the ambiguity case neither mentions.

**fix**: Have QuotaBurned return a reason enum (not-installed / no-baseline / ambiguous-run / window-rolled-over / no-sample-since-open) alongside ok, and render it in cmdBudget and in hooks.formatCeilings. The MCP unavailable_reason string is already 90% of the copy needed; add the ambiguous-run case to it.

**file**: cmd/batten/main.go — cmdBudget's `!c.Available` branch (~line 1100) hardcodes the install hint; internal/store/store.go — QuotaBurned (~line 830) collapses four distinct causes into one bare `ok == false`

---

## 35. [MAJOR/broken] [UNVERIFIED] The canvas silently drops every subagent whose spawning phase node cannot be resolved, despite a comment promising nothing is silently dropped

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: Either the four subagents appear in the 'unattributed' column the code already builds for orphans, or the canvas refuses to write and says which nodes it could not place. canvas.go's own comment says: 'Orphans (no spawning phase recorded) get their own column so nothing is silently dropped.'

**why**: This is what escalates the id collision from 'a missing box' to 'the fan-out disappeared'. The orphan column is guarded on `byPhase[""]` — a subagent with NO spawn edge — but a subagent whose spawn edge points at a node id that no longer exists lands in `byPhase["p-build"]`, a bucket the render loop never iterates. It is a data-loss path that produces a plausible-looking file, which is the failure mode batten's own principles single out as the dangerous one. It will also fire for any future cause of a missing phase node, not just the collision.

**fix**: Build a set of real phase node ids first; when `parent[n.NodeID]` is empty OR not in that set, bucket the node under "" so it lands in the existing 'unattributed' column. One line, and it makes the invariant the comment already claims actually true.

**file**: internal/canvas/canvas.go — Render(), the `byPhase[parent[n.NodeID]]` bucketing and the `if orphans := byPhase[""]` guard

---

## 36. [MAJOR/broken] [UNVERIFIED] `batten budget` reports `0 tokens, $0.00` for a run whose usage was never measured — the exact fabricated zero the README calls worse than no budget

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: The same wording the other three surfaces already use for this identical fact. `batten runs` prints `TOKENS —  IMPUTED —`; the run note prints 'Usage **not measured** for this run (no transcript ingested). Not zero — unknown.'; MCP per-node usage returns `null`. `batten budget` should print `NOT MEASURED — no transcript ingested for this run` and MCP should return `available:false, spent:null`, exactly as it already does for quota_pct.

**why**: README: 'batten never invents a number. A budget that quietly reports zero for what it failed to measure is worse than no budget at all, because it will be believed.' ROADMAP principle #1: 'Never invent a number.' The MCP tool's own description says 'A ceiling that cannot be measured is reported as unavailable with the reason — never as zero', and its own output schema documents `spent` as 'null when this ceiling cannot be measured — NOT zero'. All three are violated by the same two lines. An unattended `/batten-night` run reading this sees two ceilings with full headroom and 12 empty bar segments and concludes it can keep going, when in fact nothing has been counted. Four surfaces now disagree about one fact, which is the strongest possible signal that the honest ones are accidents rather than policy.

**fix**: Set Available from whether the run has any usage rows at all (`SELECT COUNT(*) FROM usage WHERE run_id=?`), not from whether a cap was declared. The rendering branches in cmd/batten/main.go cmdBudget, internal/hooks/hooks.go formatCeilings and internal/mcp already handle `!Available` correctly — they just never see it.

**file**: internal/store/store.go — Budget(), which hardcodes `true` for the Available field on the tokens and imputed_usd ceilings regardless of whether any usage row exists

---

## 37. [MAJOR/missing] [UNVERIFIED] Retries can never be recorded: `retry_of` has four consumers and zero producers, so no surface can show a retry the README, the vault index and two MCP tools all promise

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: A retry is visible as a retry. The README says the canvas draws 'the phases, the fan-out, the retries, the blocked verdict that got fixed and re-verified'; the vault index note batten itself writes says 'as it actually happened: the phases, the fan-out, the retries'; batten_run_graph's description says 'the TYPED edges between them (spawn, depends_on, retry_of, rollback) ... including retries and rollbacks'; canvas.go has a `relColor("retry_of") -> orange` branch.

**why**: This is the single most-advertised property of the run graph and it cannot happen. Four consumers exist — canvas.go relColor, vault.go renderRelations, mcp.go's `retries` counter, tui.go's cross-edge rendering — and no code path in the binary, the hooks or the plugin's skills ever writes anything but a `spawn` edge. `depends_on`, `supersedes` and `rollback` are in the same position. I proved the renderers are fine by injecting one row by hand into a copy of the DB: the run note immediately produced '## Relations / - `retry_of`: ml → ml'. So the whole gap is a missing producer. Until then, a user looking at two red-and-green `ml` cards has no way to know the second is the retry of the first rather than a second independent ml task, and `retries: 0` is an actively wrong answer given to the agent.

**fix**: Cheapest honest fix: add `batten retry <new-agent-id> --of <old-agent-id>` calling AddEdge(run, new, old, "retry_of"), and have /batten-build's fix loop call it. Better: have SubagentStart accept an optional `retry_of` agent id, or infer it — a new subagent with the same agent_type in the same phase as a node whose status is `failed` is a retry with high confidence. Also give the two cards distinguishable text (append the agent_id or an attempt number; the `attempt` column already exists in the nodes schema and is never used).

**file**: internal/hooks/hooks.go subagentStart (the natural place to emit it) and cmd/batten/main.go (no command exists)

---

## 38. [MAJOR/missing] [UNVERIFIED] A retry cannot claim the files it must fix — the failed agent still owns them and there is no way to release a write-set

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: A retry of a failed agent inherits (or can take over) that agent's write-set. Failing that, a clear error explaining how to hand the fence over — `batten claim --release`, `batten claim --supersede <old-agent>`, or an automatic transfer when the previous owner's node status is `failed`.

**why**: The retry is the whole point of the run graph, and the product cannot execute one without either abandoning the files that failed or hand-editing SQLite. I only got a retry into the graph at all by giving the second ml agent a different file. The guard is right to be strict — that fence is the feature — but a failed, finished node holding a permanent lock is not the disjointness rule, it is a leak: the owner is dead and nothing reclaims it. This is also what makes finding #4 unfixable in practice even if the edge existed.

**fix**: Allow a claim to take over a path whose current owner node has status `failed` (or `ended_at IS NOT NULL`), recording a `retry_of`/`supersedes` edge as the audit trail — which would produce the missing edge from finding #4 for free. Alternatively add an explicit `batten claim --from <old-agent-id>` transfer that is refused unless the old node is finished.

**file**: internal/store/store.go ClaimWriteSet (rejects any second owner unconditionally) and cmd/batten/main.go cmdClaim

---

## 39. [MAJOR/broken] [UNVERIFIED] Write-set paths are case-folded on Windows and the folded form is reported to both the human and the agent as the file that was touched

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: `frontend/composables/useTrace.ts` everywhere it is displayed. Case-folding is correct and necessary for the collision comparison on NTFS/APFS; it must not become the canonical spelling that gets reported back.

**why**: Three observability surfaces report a path that does not exist in the repository. On the human side the run note's 'Files touched' is the audit trail of what the fan-out did, and it names a file git has never heard of — on a case-sensitive CI checkout `usetrace.ts` is simply a different file. On the agent side it is worse: a fanned-out subagent told 'your write-set is frontend/composables/usetrace.ts' and instructed to stay inside its fence will create a second, wrongly-cased file. The dimension the user cares about is 'what gets generated is what was intended', and here the record of what was touched is wrong in a way that silently diverges between Windows and Linux.

**fix**: Store the original path and add a generated/duplicate `path_key` column carrying normPath() for the PRIMARY KEY and the owner lookups. WriteSet/WriteSetsByRun then return what the user actually typed while the guard keeps folding. If a schema change is too invasive right now, at minimum stop folding in the read paths' display and note the limitation.

**file**: internal/store/store.go — ClaimWriteSet stores `normPath(p)` (which lowercases on windows/darwin) rather than storing the original and folding only for comparison

---

## 40. [MAJOR/broken] [UNVERIFIED] The unit is resolved from the branch name only — the commit message is never read, so trunk-based repos get no gate and cross-unit commits are gated against the wrong unit

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: batten.schema.json documents unit.pattern as 'a Go regexp that matches a unit id anywhere in free text — a branch name, a prompt, a commit message. This is how batten answers "which unit is this commit for?" without being told.' The README repeats it: 'found in branch names, prompts, commits'. The commit message is right there in tool_input.command and is never matched against the pattern. Case (b) should at minimum warn; hooks.go:624-625 even claims 'the caller surfaces this so the silence is visible, not a mystery', but SessionStart printed nothing.

**why**: Two distinct failures. (1) Once TASK-001 is approved, every subsequent commit made on that branch — for any unit — inherits its ok verdict and is never checked. (2) Any team practising trunk-based development, or anyone who forgets to put the id in the branch name, has batten silently disabled. There is no doctor line, no session banner, nothing that says 'this branch has no unit, the gate is off'. The product's own README calls the alternative failure mode out by name: a guard that 'simply isn't there'.

**fix**: In verdictGate, when the branch yields no unit, run sp.MatchUnit(cmd) over the commit command before giving up — and prefer the commit message's id over the branch's when they disagree (the commit message is the more specific statement of intent). When neither yields a unit and the spec is in enforce mode, emit an advise() rather than silence.

**file**: internal/hooks/hooks.go:271-283 (verdictGate has `cmd` in hand and never matches it) and internal/hooks/hooks.go:626-668 (activeUnit)

---

## 41. [MAJOR/missing] [UNVERIFIED] `batten check` and `batten close` — the two commands the adoption path actually needs — are missing from `batten --help` and from the README

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: `main()` dispatches `check`, `close`, `measure` and `hook-debug`; none appear in printUsage() and none appear in README.md. `batten check` is the command that runs the gate's checks, generates the machine evidence, and turns a blocked verdict into ok — it is the single highest-value verb in the binary. `batten close` is the only way to move a run out of `status=running`.

**why**: A newcomer who follows the README gets as far as a denied commit and then has no documented way forward except `batten override`. The literal instruction in the deny message is 'fix the failures, then run batten check again' — a command that does not exist as far as `--help` and the README are concerned. I only found it by probing the binary with a bad subcommand and then reading cmd/batten/main.go.

**fix**: Add `batten check [<unit>] [--gate <name>]  run the gate's checks and record the evidence` and `batten close <unit> [--status ok|blocked]  finish the unit and release its write-set claims` to printUsage(), and put `batten check` into the README's five-step path — it is the step between 'work a unit' and 'commit'.

**file**: cmd/batten/main.go:99-118 (printUsage) vs cmd/batten/main.go:79-86 (the dispatch cases)

---

## 42. [MAJOR/broken] [UNVERIFIED] `batten check` exits 0 when the unit is BLOCKED

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: A non-zero exit when the result is `blocked`. The message even says 'the commit gate will deny until this passes' — the process then reports success to its caller.

**why**: `batten check && git commit` commits a blocked unit. So does any CI step, Makefile target, pre-commit hook or shell one-liner that chains on the exit status — which is the natural way anyone would wire a check command. The hook gate would still catch it inside Claude Code, but outside Claude Code (a terminal, CI) `batten check` is the only enforcement available and it reports pass on fail.

**fix**: In cmdCheck, after printing the summary, `if result != "ok" { return errors.New(...) }` — or add an explicit sentinel that main() maps to exit 1 without double-printing.

**file**: cmd/batten/main.go:786-803 (cmdCheck returns nil regardless of `result`)

---

## 43. [MAJOR/broken] [UNVERIFIED] `batten doctor` exits 0 on an invalid spec

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: Non-zero. A validator that prints ✗ and returns success cannot be used as a validator. The same holds for `✗ no batten.yaml found. Run: batten init` (also exit 0).

**why**: `batten doctor` is the natural CI guard — 'the spec still parses' is exactly the thing you want to break a build. As shipped, a `batten doctor` step in CI passes on a spec that the loader will refuse at runtime, so a broken batten.yaml lands on main and the failure surfaces at 3am in the hook instead.

**fix**: cmdDoctor should return the error instead of `fmt.Printf` + `return nil` in the two `✗` branches. If a zero exit is wanted for the 'no spec here' case specifically, keep that one at 0 and make 'invalid spec' non-zero — but do not print ✗ and succeed.

**file**: cmd/batten/main.go:1187-1198 (both `✗` branches `return nil`)

---

## 44. [MAJOR/broken] [UNVERIFIED] `batten init --help` writes batten.yaml instead of printing help — unknown flags are silently ignored on every subcommand

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: Usage text for `init`, exit 0 or 2. Asking a command for help should never mutate the working tree. cmdInit's arg loop recognises only `--scan-json` and `--from` and drops everything else on the floor; the same pattern is in cmdCheck, cmdClose, cmdVerdict.

**why**: `--help` on a subcommand is the first thing anyone types. Here it creates a file. In a repo that already had a batten.yaml it merely errors, but in a fresh repo — exactly the from-zero case — you get an unrequested spec you did not read. It also means a typo'd flag (`--form docs/workflow.md`) is silently ignored rather than reported, so `batten init --form ...` writes a spec that migrated nothing and says nothing about it. I hit this myself on my very first command against the binary.

**fix**: Give each subcommand a real flag.FlagSet (which handles -h/--help and rejects unknown flags), or at minimum add a `default:` arm in each hand-rolled loop that errors on any argument starting with `--` that it does not recognise.

**file**: cmd/batten/main.go:1429-1440 (cmdInit's arg loop has no default arm); same shape at 646-659 (cmdClose) and 724-737 (cmdCheck)

---

## 45. [MINOR/missing] [CONFIRMED] `batten doctor` validates a domain's rules file, skills and agent, but never that its `path` exists

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: `⚠ domain "backend": path does not exist: does-not-exist/` alongside the rules warning — the same class of check, one line away in the same loop.

**why**: `path` is the fan-out axis and the write-set boundary. A domain pointing at a directory that does not exist (renamed dir, typo, monorepo reshuffle) means a subagent is fenced to nothing and the verify phase diffs nothing — and it fails silently mid-run, which is exactly the failure mode the schema says doctor exists to prevent ('a renamed skill should be caught there, not fail silently in the middle of an overnight run'). The rules/skills/agent checks prove the pattern is already there.

**fix**: Add an os.Stat on filepath.Join(spec.Root, d.Path) in the same loop and warn when it is missing or not a directory.

**file**: cmd/batten/main.go (doctor's domain loop, near line 1292 `⚠ domain %q: rules file missing`)

**verificador**: Reproduced exactly in a fresh sandbox, and confirmed in source as unconditional. cmd/batten/main.go:1289-1295 is the entire per-domain check and it stats only d.Rules; `grep -rn "os.Stat" internal/ cmd/` returns 12 hits (rules, obsidian vault, graph.json, install paths) and none touch Domain.Path. spec.Validate (internal/spec/spec.go:283-292) only rejects an empty path. Nothing in the binary ever 

---

## 46. [MINOR/missing] [CONFIRMED] `batten close` and `batten check` are absent from the usage output — including the command that releases write-set claims

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: `close`, `check` and `measure` are all implemented and dispatched in main() but never listed. `batten close` is the ONLY way a user can release a run's write-set claims, and `batten check` is what the close gate's own error message tells you to run ('Run: batten check <unit>' / 'run `batten check US-001`, or the verify phase'). Both should appear, e.g. `batten check <unit>  run the gate's checks and record a batten-verified verdict` and `batten close <unit> [--status ...]  end a run and release its write-set claims`.

**why**: A user whose second session is blocked by a stale claim ('is being worked by US-001 in another open session') has no discoverable way out: the deny message says 'Coordinate, or use a worktree', the usage text offers nothing, and the command that actually fixes it is invisible. `batten doctor` surfaces stale runs but the remedy still is not in the help. This turns a recoverable state into a support question.

**fix**: Add the three missing lines to printUsage. Since the switch and the usage text are maintained separately, consider driving both from one table so a new command cannot be added without a help line.

**file**: cmd/batten/main.go (printUsage ~line 97; the dispatch switch already handles close/check/measure ~line 78-86)

**verificador**: Reproduced exactly, byte-for-byte, in a fresh sandbox. `batten` with no args prints the 15-line usage and exits 2; `close`, `check` and `measure` appear nowhere in it. The source confirms the discrepancy is real and not an artifact of the build: cmd/batten/main.go lines 79-84 dispatch `case "measure"`, `case "close"`, `case "check"`, while printUsage() at lines 99-118 lists 15 commands and omits a

---

## 47. [MINOR/broken] [CONFIRMED] A relative `file_path` in the tool payload silently bypasses the write-set guard

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: The same decision for the same file. `writeSetGuard` calls `filepath.Rel(h.Spec.Root, path)` with an absolute root and a relative path; on Windows that returns an error, and the guard returns nil (allow) rather than resolving the path against the payload's `cwd`, which it already has.

**why**: Lower severity than the others because Claude Code's Edit/Write tools require absolute paths today, so I could not trigger this through the real tool contract — I fabricated the payload. But the guard's fail-open on a path it merely failed to parse is the wrong default for the one check the project calls load-bearing, and it is one upstream payload change away from mattering. It is also a two-line fix.

**fix**: If `!filepath.IsAbs(path)`, join it onto `in.CWD` (falling back to h.Spec.Root) before computing Rel. Keep the fail-open, but only for paths that genuinely resolve outside the repo.

**file**: internal/hooks/hooks.go (writeSetGuard ~line 408)

**verificador**: Reproduced exactly as described in a fresh sandbox, and the reporter's root-cause diagnosis is correct.

CONFIRMED MECHANISM. `writeSetGuard` (internal/hooks/hooks.go:404-415) does `rel, err := filepath.Rel(h.Spec.Root, path)` and on `err != nil` returns `nil, nil` — allow, with no output at all. `filepath.Rel` errors whenever the base is absolute and the target is relative. Go probe (go1.26.3):
 

---

## 48. [MINOR/confusing] [CONFIRMED] "has no batten-verified pass" hides that batten check ran and FAILED

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: Something like: "batten: US-011 — the gate's checks were run and FAILED (python -m pytest backend/tests -q, exit 1). Your ok verdict does not override that."

**why**: The denial is correct and the enforcement is right — this is exactly the case the gate exists for, and it held. But the wording describes the wrong world. 'has no batten-verified pass' is what you would also see if `batten check` had never been run, so an agent reads it as 'I forgot a step', re-runs `batten check`, gets BLOCKED again, and has learned nothing about the fact that its own ok verdict directly contradicts a failing test. The one moment the tool has caught the model overclaiming is the moment it declines to say so.

**fix**: Branch on whether LatestVerdictBySource returned a row: err != nil -> the current 'no batten-verified pass, run batten check' message; row with Result != "ok" -> a distinct message that names the batten result, its why, and the disagreement with the agent's claim.

**file**: internal/hooks/hooks.go:331

**verificador**: Reproduced byte-for-byte in a fresh sandbox, and the reporter's diagnosis of the cause is correct — but the severity should be read as a diagnostics/message defect, not an enforcement defect.

WHAT IS TRUE. At $HOME/Proyectos/Public/LoopWorkFlow/internal/hooks/hooks.go:328-336 the gate does:

    bv, err := h.Store.LatestVerdictBySource(run.RunID, gateName, "batten")
    if err != nil || 

---

## 49. [MINOR/broken] [CONFIRMED] `evidence: [""]` satisfies the evidence rule — the guard counts items, not content

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: Refused with ErrNoEvidence, exactly as `"evidence":[]` is. An empty string cites nothing.

**why**: SaveVerdict is described in its own comment as 'the single thing batten exists to make impossible', and the README calls the evidence line 'the reason the file exists — do not soften it'. `len(v.Evidence) == 0` is a one-character-away-from-correct test: `[""]`, `[" "]` and `["-"]` all pass. The blast radius is small in a well-formed spec, because a gate with checks separately demands a batten-sourced pass — but in the checkless-gate configuration (which `batten doctor` merely warns about, and which is where a new adopter starts) this IS the whole gate, and `batten show` then renders a bare `-` bullet under a green 'verdict qa=ok' as though something had been cited.

**fix**: Count only non-blank items: `n := 0; for _, e := range v.Evidence { if strings.TrimSpace(e) != "" { n++ } }; if evidenceRequired && v.Result == "ok" && n == 0 { return ErrNoEvidence }`. Consider a minimum length too — a one-character evidence item is not a citation.

**file**: internal/store/store.go:598 (SaveVerdict)

**verificador**: Reproduced verbatim, and the source confirms it: `internal/store/store.go:598` is `if evidenceRequired && v.Result == "ok" && len(v.Evidence) == 0`. There is no `TrimSpace`, no empty-item filter, anywhere between `json.Unmarshal` in `cmd/batten/main.go:250` and the INSERT. `[""]` has len 1, so it passes. Same count-only test is repeated at `cmd/batten/main.go:707` (the `close` path), `internal/hoo

---

## 50. [MINOR/missing] [REFUTED] The 9 custom subagents are discovered but never wired to a domain, and doctor cannot flag the omission

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: At minimum `domains.backend.agent: backend-engineer` and `domains.frontend.agent: frontend-engineer` — the two cases where the agent name is the domain name plus '-engineer' — or a note[] saying '9 custom agents found but not assigned; set domains.<d>.agent'.

**why**: batten.schema.json says of `agent`: 'The fan-out orchestrator uses it as the subagent type, so the DAG, the canvas and the hook matchers all show YOUR agent's name.' Without it the whole point of the run graph on a repo that already invested in 9 tuned agents — seeing your own agents in the DAG — is lost, and nothing in the draft or in doctor tells the user the key exists. This is arguably by design (the /batten-init skill is meant to supply the judgment from scan.json), but the binary-only path is the one users hit first and it produces a spec that silently ignores the repo's agents.

**fix**: Emit `agent:` when a discovered agent's name starts with the domain name (backend-engineer -> backend), otherwise add a note[] listing the unassigned agents so the user knows the key exists.

**file**: internal/scan/scan.go (Facts.ToYAML — Agents is populated but never rendered)

**verificador**: The command as filed does not run, and the second half of the claim ("doctor cannot flag the omission") is factually wrong. What survives is a small, honest gap — not the defect described.

1) THE COMMAND NEVER RAN. `batten init --scan-json` prints JSON and returns without writing anything (cmd/batten/main.go:1448-1454: `if scanJSON { ...; return nil }`). Run verbatim in a fresh sandbox the chain 

---

## 51. [MINOR/confusing] [REFUTED] init says 'the stack is unknown' and 'NO check command was found' about a repo whose AGENTS.md — which init lists in harness[] — declares the stack and the exact QA gate commands

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: The two notes should be scoped to what was actually searched — e.g. 'no build files (Makefile/pyproject.toml/package.json) found, so no check could be auto-detected; AGENTS.md documents commands (`make check`) — confirm they exist before filling gates.qa.checks'. As written they read as a claim about the repo, and they are false about the repo.

**why**: The scan opens the file, lists it in harness[], and puts 'the spec must agree with it, not replace it' at the top of the generated yaml — then reports the opposite of what that file says about the two most consequential empty fields (gates.qa.checks and the stack). A user reading only the notes concludes batten looked at their harness and found nothing there. It also makes `coverage: 70` / `coverage: 50` (a real schema key, stated verbatim in the QA Gate section) look undiscoverable. The warning itself is honest and correct in substance — the replica genuinely has no Makefile — so this is phrasing, not a wrong gate.

**fix**: Name the evidence in the note: say which build files were looked for and not found, and cross-reference the harness files as the place to mine the commands from. Optionally have --scan-json return the `## Comandos`/`## QA Gate` blocks verbatim as a `harness_hints` field so /batten-init has something to reconcile against.

**file**: internal/scan/scan.go (Notes construction, ~line 122)

**verificador**: The output reproduces verbatim, but the notes are literally true about this repo and the EXPECTED behaviour is the one batten deliberately refuses.

FACT THE REPORTER MISSED: the testbed repo contains ZERO build files and ZERO source files. `find . -not -path "./.git/*" \( -name Makefile -o -name pyproject.toml -o -name package.json -o -name go.mod -o -name requirements.txt -o -name justfile -o -n

---

## 52. [MINOR/wrong-docs] [REFUTED] `--scan-json` is documented nowhere, yet init's own output tells users to use it

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: `batten init [--scan-json] [--from <prose-doc>]` in the top-level help, and one line in README's CLI block. The flag is the documented handoff between the binary and the /batten-init skill, and it is the richer of the two paths.

**why**: The binary points users at a flag that neither `--help` nor any doc will confirm exists, and (see finding 1) typing `batten init --scan-json --help` to find out writes a file instead. For a plugin whose DESIGN.md calls init 'la feature más importante', the discoverable surface should not be the smaller half.

**fix**: Add --scan-json to the usage line and to README's `$ batten init` block, with one clause on what it is for ('machine-readable repo facts for /batten-init').

**file**: cmd/batten/main.go (usage string) and README.md (CLI block, ~line 227)

**verificador**: The three surface observations reproduce exactly, but the finding rests on two false premises and a wrong expectation.

1) "Documented nowhere" is false. The reporter grepped four files (README/ROADMAP/DESIGN/schema), found zero hits, and generalized to "nowhere" — without grepping the one file that both consumes the flag and is named in the very message they quote. A repo-wide grep finds it immed

---

## 53. [MINOR/confusing] [REFUTED] report mode still tells you to run an override for something it did not block

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: In report mode the escape-hatch line should be dropped, or replaced with something true of report mode (e.g. "gates are in report mode; flip enforcement: enforce in batten.yaml to make this a denial").

**why**: 'To proceed anyway' is advice for a wall that is not there — the commit already proceeded. An agent that follows instructions literally will run `batten override US-015 --reason ...`, writing a permanent override record for a run that was never blocked, which then quietly disarms that unit's gate for real if the project later flips to enforce. report mode is explicitly the adoption ramp, so it is precisely the mode a new user is reading these messages in for the first time.

**fix**: Have gate() strip or rewrite a trailing 'To proceed anyway' line when ReportOnly(), or build the reason in two parts (violation, remedy) and let gate() choose the remedy line per mode.

**file**: internal/hooks/hooks.go:98 (gate) and 108 (advise) — the reason string is built for the deny case and reused verbatim

**verificador**: Reproducible verbatim, but it is not a defect — it is the documented design, and the reporter's EXPECTED is wrong on the merits.

1. The output does NOT mislead the user about blocking. The same payload's user-visible line is `"batten (warning — not blocking): ..."`. The escape-hatch sentence lives only in `additionalContext` (the model's context), not in the `systemMessage` banner — `advise()` in

---

## 54. [MINOR/broken] [UNVERIFIED] `batten check` on a closed unit silently forks a second, anchorless run and hides the closed one

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: Either reuse the closed run, or refuse with something like "US-011 was closed at <sha>; run `batten phase US-011 build` to reopen it". A run with no phase and no anchor should not be creatable as a side effect.

**why**: cmdCheck calls EnsureRun, which happily opens a fresh run when none is active. `batten runs` then shows US-011 twice with contradictory rows (running/blocked and ok/close) and no way to tell which is current; `batten show` follows the newer one, so the closed run — whose comment in cmdShow explicitly says it is 'the run you just closed is the one you most want to inspect' — becomes unreachable. The phantom run also has base_sha NULL, so a `diff_from: anchor` phase on it has nothing to diff from, and the anchor guarantee the spec advertises is quietly void for that run.

**fix**: In cmdCheck (and cmdVerdict), use ActiveRun and error out when there is none — 'no open run for US-011; start one with batten phase US-011 <phase>' — rather than EnsureRun. Reserve EnsureRun for cmdPhase, which is the one command whose job is to open a run.

**file**: cmd/batten/main.go:764 (EnsureRun inside cmdCheck)

---

## 55. [MINOR/confusing] [UNVERIFIED] The commit-gate denial names a different unit than the commit message does — the commit text is never consulted

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: Either resolve the unit from the commit message when it names one, or say so: "this commit names US-999 but this session is bound to US-001".

**why**: batten.schema.json's description of `unit.pattern` says it is "how batten answers 'which unit is this commit for?' without being told" and lists commit messages as one of the places it is matched. In practice `activeUnit` looks only at session binding, then branch name, then the single-unowned-run fallback (internal/hooks/hooks.go:626) — `tool_input.command` is never matched against the pattern. Being told "US-001 has no batten-verified pass" while staring at a commit message that says US-999 reads like a bug in batten, and sends the user off to run `batten check US-001` for a unit they were not working on.

**fix**: Match the pattern against the commit message and, on a mismatch with the bound unit, deny with a message that names both. At minimum fix the schema description so it does not promise commit-message resolution.

**file**: internal/hooks/hooks.go:626 (activeUnit); batten.schema.json (unit.pattern description)

---

## 56. [MINOR/missing] [UNVERIFIED] Closed runs are recorded but unreachable — `batten runs` shows two rows for a unit and nothing can inspect the older one

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: `batten show <unit> --run <run-id>` (or `batten show <run-id>`) opens the historical run; `batten runs` carries a run id / started-at / age column so two rows for one unit are distinguishable; an unrecognised flag is an error.

**why**: The whole value proposition of the run record is that it is durable procedural memory — "the path the work actually took". Once a unit is reopened, the closed run's anchor, verdicts and evidence are permanently invisible from the CLI: `show` and `canvas` both hard-code `LatestRun` (internal/store/store.go:329). `batten runs` gives you two indistinguishable `US-001` rows and no handle to either. `batten budget US-001` is the same problem with a sharper edge: it prints one identical, unlabelled 3-line block per run of that unit, so a unit with two runs prints the same `US-001  0 tokens, $0.00 imputed` block twice with nothing to tell them apart.

**fix**: Add `--run <id>` to show/canvas/budget (and accept a bare run id as the positional arg), add a run-id-or-age column to `batten runs`, label each budget block with the run id and status, and reject unknown flags instead of ignoring them.

**file**: cmd/batten/main.go (cmdShow ~line 450, cmdRuns ~line 410, cmdBudget); internal/store/store.go:329 (LatestRun)

---

## 57. [MINOR/missing] [UNVERIFIED] Entering a phase that declares `diff_from: anchor` with no anchor recorded is completely silent

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: `⚠ this phase diffs from the anchor, but no anchor was recorded (the "build" phase was never entered). Run `batten phase US-008 build` first, or the review scope will be wrong.`

**why**: The anchor is the load-bearing idea of the whole scoping story — README and the batten-verify command both insist "Never `HEAD~N` — the anchor is what keeps the scope right across rebases". When it is missing the verify agent has nothing to diff from and is told nothing, so it falls back to exactly the `HEAD~N` guess the design exists to prevent. Related ergonomics: even when the anchor *does* exist, `batten phase <unit> verify` prints only the gate reminder and never echoes the anchor or the diff range, so a human running the phase by hand has to go find it with `batten show`. Phase order is not enforced either (`batten phase US-007 close` straight from nothing works, and going backwards to `research` works), which makes skipping the anchor phase easy to do by accident.

**fix**: In cmdPhase, when `ph.DiffFrom == "anchor"` and `run.BaseSHA == ""`, print a warning naming the anchor-bearing phase. When the anchor does exist, print `diff scope: <base>..HEAD` so the range is copy-pasteable.

**file**: cmd/batten/main.go:372-382 (cmdPhase); internal/spec/spec.go:77 (DiffFrom, currently parsed and never read by any Go code)

---

## 58. [MINOR/broken] [UNVERIFIED] The anchor is stored as a 7-character abbreviation, never the full SHA

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: The full 40-character SHA is persisted; the 7-char form is produced only for display (the code already has a `shortSHA` helper for exactly that, used at internal/tui/tui.go:403 and internal/vault/vault.go:204 — where it is currently a no-op because the stored value is already short).

**why**: The anchor is the one durable, reproducible fact a closed run keeps — provenance.format stamps it into the resolution artifact and every later diff is computed from it. A 7-char prefix is only unambiguous relative to the repo *at the moment it was taken*; as the repo grows, `git diff 7a332be..HEAD` on a months-old closed run can start failing with `ambiguous argument`. Git itself defaults to `core.abbrev=auto` and grows past 7 for exactly this reason. Storing lossy data when the full value is free is an avoidable way to lose a run record's only anchor to history.

**fix**: Change gitSHA to `git rev-parse HEAD` and abbreviate at every display site via the existing shortSHA helper. Existing short values keep working since git resolves prefixes.

**file**: cmd/batten/main.go:1486 (gitSHA); internal/store/store.go:370 (SetBaseSHA)

---

## 59. [MINOR/broken] [UNVERIFIED] `batten canvas` emits zero edges for any run without a fan-out — the run "DAG" is a pile of disconnected cards

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: Consecutive phases are joined by edges so the canvas reads as a path, and the phase whose gate produced the verdict points at the verdict card.

**why**: README.md sells the canvas as the picture of "the path the work **actually took**, not the one the plan hoped for" — but `AddEdge` has exactly one non-test call site in the codebase (internal/hooks/hooks.go:494, `rel="spawn"`, only on subagent launch), so a run without a fan-out gets no edges at all. Opened in Obsidian this is six floating boxes with no arrows and no reading order, every one of them labelled `running` (see the phase-completion finding). Someone opening this to review a finished unit learns nothing from it.

**fix**: When cmdPhase starts a new phase node, add a `depends_on` (or `follows`) edge from the previous phase node of the same run; edge the gate phase to its verdict node; edge a re-entered phase to its prior attempt with `retry_of`, which the canvas renderer already understands.

**file**: cmd/batten/main.go:368 (cmdPhase); internal/canvas/canvas.go:99 (Render); internal/store/store.go:453 (AddEdge)

---

## 60. [MINOR/confusing] [UNVERIFIED] The `evidence[]` item shape is undocumented outside one skill file, and getting it wrong leaks a Go type error

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: `batten: evidence[] items must be plain strings, e.g. "make test: 142 passed, 0 failed" — got a JSON object at evidence[0]`, and a documented shape in the README and/or the JSON Schema.

**why**: README.md shows the envelope only as `"evidence":[]` — empty — so a first-time user writing their first verdict by hand has nothing to pattern-match on and reaches for structured objects, which is the natural guess for something called "evidence". The string form is documented only in plugin/claude-code/skills/batten-verdict/SKILL.md, which a CLI user has no reason to open, and batten.schema.json describes gates but not the verdict envelope at all. The error that comes back is a raw Go unmarshal message naming an internal struct field — the only place in the tool where an implementation detail leaks into a user-facing message, and it sits directly on the product's most important path.

**fix**: Show a populated `evidence[]` in the README's verdict example; add a `verdict.schema.json` (or a `$defs.verdict` block) so editors validate it; and translate the unmarshal error into a message about the expected item type and index.

**file**: cmd/batten/main.go (cmdVerdict, ~line 227); README.md:46 and :61; batten.schema.json

---

## 61. [MINOR/confusing] [UNVERIFIED] The SessionStart resume brief renders tokens as %.1fM, so a run at 21% of its ceiling reads as "0.0M tokens", and the ceiling is never shown

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: `, 42.6k / 200.0k tokens · $0.25 / $2.00 imputed · quota not measurable` — reuse humanTokens(), which every other surface already uses and which renders these correctly as 42.6k / 68.9k / 188.0k, and include the declared ceilings.

**why**: This brief is the one budget surface an agent reads unprompted on every resume, and it is the answer to the "¿dónde quedó?" question the code comment names as its purpose. Rounding 42,600 tokens to "0.0M" tells the resuming agent nothing was spent when a fifth of the ceiling is already gone, and there is a real behavioural consequence: an agent that reads "0.0M tokens" has no reason to consider downgrading effort or wrapping up. Omitting the ceiling compounds it — even the correctly-rendered "0.2M tokens" for the 188,000-token run gives no signal that it is at 94% of its limit and one fan-out away from a denied commit. Every other surface in the binary formats tokens well; this one does not.

**fix**: Swap the %.1fM for the shared humanTokens() helper, and append the declared ceilings by calling Store.Budget with sp.Budget — including the NOT MEASURABLE line, so the agent learns which ceilings are actually live before it starts spending.

**file**: internal/hooks/hooks.go — sessionStart, `fmt.Fprintf(&b, ", %.1fM tokens / $%.2f imputed", float64(r.TokensSpent)/1e6, r.ImputedUSD)` ~line 580

---

## 62. [MINOR/wrong-docs] [UNVERIFIED] `batten --help` omits `check`, `close` and `measure`, including the one command the gate's own denial tells the user to run

**dim**: BUDGET, USAGE ACCOUNTING and "

**command**:


**observed**:


**expected**: `batten check <unit>`, `batten close <unit> [--status ...]` and `batten measure` listed alongside the rest. `hook-debug` can stay hidden if it is deliberately internal.

**why**: The gate's own denial text instructs the user to run a command the help does not document: "batten: US-001 has no batten-verified pass. The gate's checks must be RUN, not asserted. Run: batten check US-001". A user who types `batten --help` to check the spelling finds nothing and has no way to discover it short of reading the Go source — which is what I had to do. `close` matters just as much for this dimension: without it runs never leave `running`, and `batten measure` silently compares nothing because measureByFlag filters on `status IN ('ok','blocked','failed','rolled_back')`. `measure` itself is the headline "prove it on your own numbers" command in CHANGELOG.md and DESIGN.md and is invisible from the CLI. Running `batten measure` with no runs also exits 0 printing absolutely nothing, which is indistinguishable from the command not existing.

**fix**: Add the three user-facing commands to printUsage. Separately, give cmdMeasure a fallback line when it has nothing to report ("no finished runs yet — close a run with `batten close <unit>` and measure will have something to compare") so an empty result is distinguishable from a missing command.

**file**: cmd/batten/main.go — printUsage (~line 98) versus the dispatch switch (~line 48-90)

---

## 63. [MINOR/broken] [UNVERIFIED] Phase nodes are never finished, so the canvas and TUI paint every phase as `running` forever

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: `## build` / `ok` in green once the run has moved past it, matching how subagent nodes already transition from `running` to `ok`/`failed` via FinishNode.

**why**: Colour is the fastest signal on the canvas, and it says nothing: every phase is purple regardless of what happened. A user scanning a finished run cannot tell which phases completed, and a run that ended three days ago still reads as in-flight. `FinishNode` has exactly one caller in the whole codebase (SubagentStop), and neither `SetPhase` nor `CloseRun` touches the nodes table, so a phase node's status is written once at creation and never again.

**fix**: In cmdPhase, before adding the new phase node, call FinishNode on the run's previous phase node with status `ok`. In CloseRun, finish any still-running phase node of that run with the run's final status.

**file**: cmd/batten/main.go cmdPhase (creates the new phase node without closing the previous one) and internal/store/store.go CloseRun (updates runs only)

---

## 64. [MINOR/broken] [UNVERIFIED] Every canvas overlaps the phase card with its first subagent card by exactly half a card

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: No two cards overlap. Obsidian draws them stacked, so the bottom half of every phase card — which carries its status line — is covered by the first subagent card.

**why**: It is deterministic: every phase with at least one subagent in every canvas batten will ever write. The README's pitch for this format is 'Zero lines of graph-layout code on our side. Obsidian already renders it', which makes the small amount of layout arithmetic that does exist worth getting right. It is the first thing a user sees when they open the file.

**fix**: Make padTop >= nodeH + a gutter (e.g. `padTop = 160`), or offset children by `nodeH + 40`. The group height formula `padTop + len(kids)*gapY + 40` follows automatically.

**file**: internal/canvas/canvas.go — `padTop = 60` is used as the first child's Y offset while `nodeH = 120` is the phase card's height

---

## 65. [MINOR/confusing] [UNVERIFIED] `batten tui` with a non-terminal stdout enters the alt-screen, hides the cursor, renders nothing, ignores `q` and never exits

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: Either a one-line refusal — `batten tui needs an interactive terminal; try batten show <unit>` with a non-zero exit — or a plain-text fallback dump. The 104 bytes it does emit are pure terminal setup: enable alt-screen (`\e[?1049h`), hide cursor (`\e[?25l`), set the window title, clear.

**why**: To be precise about what would be needed: the TUI cannot be exercised headlessly at all — it requires a real pty/ConPTY, and I had to drive it through pywinpty to see a frame. That is a fair requirement for a TUI. The defect is that it neither says so nor gives up: it hangs indefinitely with no output, and because it has already entered the alt-screen and hidden the cursor, a user who pipes it (`batten tui | less`, a wrapper script, an agent running it) and then Ctrl-Cs is left with a hidden cursor in the alt-screen buffer. `internal/tui` is the one package at 0% coverage per ROADMAP.md, and this is exactly the class of thing that gap hides.

**fix**: Guard cmdTUI with a terminal check on os.Stdout (golang.org/x/term.IsTerminal, already an indirect dependency via bubbletea) and return a plain error naming `batten show`/`batten runs` as the non-interactive alternatives.

**file**: cmd/batten/main.go cmdTUI — no isatty check before tui.Run

---

## 66. [MINOR/confusing] [UNVERIFIED] The TUI's run-list bar labels percent-of-cap as a quota percentage, producing an impossible '113% quota'

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: Something that cannot be read as a share of the 5-hour window — e.g. `▓▓▓▓▓▓▓▓ 17.0/15.0% quota` or `▓▓▓▓▓▓▓▓ quota OVER`. 113% of a rolling window is not a possible quantity.

**why**: The whole argument for the quota ceiling in the README is that 'a percentage is the only trustworthy quota metric'. The list pane prints a percentage next to the word `quota` that is not the quota — it is the fraction of the cap — and the detail pane one column to the right prints the real number. The two panes of the same screen disagree. For the `tokens` and `imputed_usd` ceilings percent-of-cap reads fine; only the ceiling that is itself denominated in percent is ambiguous.

**fix**: Special-case `quota_pct` in bindingLine to render `%.0f/%.0f%%` from Spent and Cap directly, keeping the bar driven by frac.

**file**: internal/tui/tui.go — bindingLine(): `frac := c.Spent / c.Cap; ... fmt.Sprintf("%s %3.0f%% %s", bar(frac, barW), frac*100, kindLabel(c.Kind))`

---

## 67. [MINOR/broken] [UNVERIFIED] Phases never reach a terminal state — every phase still reads `running` after the unit is closed ok

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: `plan`, `build` and `verify` should be done/ok once the run advanced past them, and all four should be terminal once `batten close` set `status=ok`. `batten phase <unit> <next>` advances the pointer but never closes out the phase it left.

**why**: `batten show` is the human's window onto what actually happened, and it reports a finished, approved, committed unit as four phases still in flight. The same state feeds `batten canvas`, so the Obsidian view is wrong in the same way. A reviewer cannot tell a run that stalled in `verify` from one that sailed through all four.

**fix**: In cmdPhase, mark the previous phase node done when advancing; in cmdClose, mark every remaining phase node terminal with the run's status.

**file**: cmd/batten/main.go cmdPhase / cmdClose, and the phase node status column in internal/store

---

## 68. [MINOR/broken] [UNVERIFIED] `batten canvas` draws no edges between phases — the 'run DAG' is a row of disconnected boxes

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: Edges for `plan -> build -> verify -> close` at minimum. The README says the canvas 'draws the path the work actually took, not the one the plan hoped for' — with no phase-to-phase edges there is no path drawn at all, only x-coordinates. (Before the subagents existed it was literally `10 nodes, 0 edges`.)

**why**: The canvas is the project's only visual artefact and the reason JSON Canvas was chosen ('zero lines of graph-layout code on our side. Obsidian already renders it'). Obsidian renders four unconnected group boxes, all labelled `running`. The retries, the blocked-then-fixed verdict, the ordering — none of it is expressed as graph structure.

**fix**: Emit an edge between consecutive phase nodes in spec order, and an edge from the verdict node to the phase whose gate it satisfies. Colour the edge by the phase's terminal status once that lands (previous finding).

**file**: internal/canvas/

---

## 69. [MINOR/confusing] [UNVERIFIED] `batten budget` prints `0 tokens, $0.00` for a run it never measured — the exact failure the README says it refuses to commit

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: Something that distinguishes 'measured, and it was zero' from 'never measured'. The README is emphatic about this: 'batten never invents a number. A budget that quietly reports zero for what it failed to measure is worse than no budget at all, because it will be believed.' It even shows the right pattern for quota: `NOT MEASURABLE — install the statusline`. The token ledger gets no such treatment.

**why**: The whole point of the budget section is that an unattended overnight run needs a tripwire you can trust. Empty progress bars at 0/3.0M read as 'you have used nothing, keep going' — which is what an unattended run would conclude — when the truth is 'nothing was ever counted'. `batten runs` gets this right in the same session, printing `—` for TOKENS and IMPUTED; `batten budget` should agree with it.

**fix**: When the ledger has no ingested transcript for the run, print `tokens  NOT MEASURED — run: batten ingest <unit> --transcript <path>` instead of `0 / 3.0M`, matching the quota_pct line's existing honesty.

**file**: cmd/batten/main.go cmdBudget

---

## 70. [MINOR/missing] [UNVERIFIED] `batten doctor` never checks that the `check:` / `gates.checks` commands can actually run

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: A warning that the check command is not resolvable, in the same family as the two warnings it does emit. batten.schema.json makes this the governing rule for the field: 'Take them verbatim from the Makefile / package.json — do not invent a command that does not exist in the repo.' doctor verifies the rules file and the skill name, which are cheaper mistakes, but not the commands the gate's entire credibility rests on.

**why**: `batten init` guesses these commands heuristically, and the newcomer's job in step 3 is to confirm them. A typo (`go tst ./...`, `make check` in a repo with no Makefile) produces a clean doctor and then a permanently-blocked unit at commit time, or worse, a green run in a repo where `make` silently no-ops. Resolving argv[0] is a two-line check.

**fix**: For each domain check and gate check, split off argv[0] and `exec.LookPath` it (plus a `./`-relative file check), warning `⚠ gate "qa": check "..." — command "go tst" not found on PATH`. Do not run the command, just resolve it.

**file**: cmd/batten/main.go:1187+ (cmdDoctor)

---

## 71. [MINOR/missing] [UNVERIFIED] `batten init` neither creates a .gitignore nor adds `.batten/`, which `batten canvas` then writes into the repo

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: `batten init`, which already writes a file into the repo, should also ensure `.batten/` is ignored — or `batten canvas` should default somewhere outside the tree. The canvas is a rendered view of a database, not source.

**why**: The default `batten canvas <unit>` path drops a generated artefact into the working tree with no mention of it. On a team, everyone's canvas lands in `git status` and eventually in a commit, then conflicts on regeneration. It is a one-line fix at the exact moment batten already has the repo open for writing.

**fix**: In cmdInit, append `.batten/` to .gitignore (creating it if absent) and say so in the 'Next:' block. Mention the canvas output path in `--help`.

**file**: cmd/batten/main.go:1456+ (cmdInit, right after writing spec.Filename)

---

## 72. [POLISH/confusing] [REFUTED] `artifacts.handoff` is written into every spec but no phase reads it

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: Either a phase that reads it (README's example wires `reads: [planning]` into build), or drop the orphan and let the note[] say 'declare artifacts when you decide where handoffs live'.

**why**: batten.schema.json describes artifacts as 'Where each phase writes its output. Keys are artifact kinds (referenced from phases[].reads)'. Emitting a key nothing references, with a TODO asking the user where it should live, invites them to carefully set a path that will never be used — and doctor will not tell them.

**fix**: Add `reads: [handoff]` to the build phase in the emitted draft, or warn in doctor when an artifacts key is referenced by no phase.

**file**: internal/scan/scan.go line 593 (`w("  handoff: docs/{id}.md   # TODO: ...")`)

**verificador**: The text reproduces exactly, but the premise behind it is wrong: in batten an artifact is an OUTPUT, and `reads:` is optional consumption. `batten.schema.json` defines `artifacts` as "Where each phase writes its output" — there is no `writes:` key on a phase, so the artifacts map IS the write-target table. The loader enforces the relation in exactly one direction, and I proved both halves in my sa

---

## 73. [POLISH/broken] [UNVERIFIED] `batten init --from` prints step 2 twice

**dim**: INSTALL + INIT on a repo that 

**command**:


**observed**:


**expected**: 1, 2, 3, 4 — the inserted line should renumber the ones after it.

**why**: Cosmetic, but it is on the very first screen a new user sees, in the output of the flag the docs call the migration path. It reads as if the tool lost track of its own instructions.

**fix**: Build the list into a []string and number it on print.

**file**: cmd/batten/main.go lines ~1473-1477 (two hardcoded `"  2. "` prefixes)

---

## 74. [POLISH/confusing] [UNVERIFIED] The deny message prints an empty 'Your write-set:' when the trespasser has declared none

**dim**: THE WRITE-SET GUARD and multi-

**command**:


**observed**:


**expected**: 'You have not claimed any files — run `batten claim A2 <file>...` with the write-set from the plan.' The blank bullet comes from `strings.Join(mine, "\n  ")` on an empty slice.

**why**: Minor on its own, but it lands in the message an agent reads to decide what to do next, and it hides the single most likely actual cause: the agent skipped its `batten claim` step, so it owns nothing and thinks it should. The blank line reads like a rendering bug and gives the model nothing to act on. It is also the only message in the whole surface that degrades to whitespace — everything else batten prints is careful about the empty case.

**fix**: Branch on `len(mine) == 0` and substitute the 'you have not claimed anything yet' sentence plus the exact `batten claim` invocation, instead of joining an empty slice.

**file**: internal/hooks/hooks.go (writeSetGuard, ~line 448-454)

---

## 75. [POLISH/confusing] [UNVERIFIED] Windows exit codes render as unsigned garbage in check output

**dim**: THE VERDICT GATE — batten's co

**command**:


**observed**:


**expected**: `✗ npm test (exit -4058)` — or, since a negative code means the process died abnormally rather than returning a status, `✗ npm test (failed: ENOENT / exit -4058)`.

**why**: 4294963238 is -4058 read as a uint32. It appears in the console AND is baked verbatim into the stored evidence string, so the number a reviewer later reads in the verdict is nonsense. batten is explicitly Windows-first (the whole hooks package is justified by engram's hooks being 'fragile on Windows'), and negative exit codes are the normal way Windows reports a process that could not start or was killed — exactly the case a gate most needs to describe accurately, because 'the check failed' and 'the check could not run' are different facts. (I confirmed this is only a display issue: a probe check showed `batten check` correctly runs commands with cwd = the repo root, so the npm ENOENT itself was npm's own upward search for package.json, not a batten bug.)

**fix**: Render via int32: `code := int(int32(ee.ExitCode()))`, and when code < 0 label it 'terminated abnormally' rather than printing it as a plain status.

**file**: cmd/batten/main.go:808 (runCheck, ee.ExitCode())

---

## 76. [POLISH/confusing] [UNVERIFIED] `batten check` overwrites the displayed verdict, hiding the agent's criterion-level evidence

**dim**: PHASES, ANCHOR, CLOSE, and the

**command**:


**observed**:


**expected**: `batten show` displays both verdicts for the gate, labelled by source, since they carry complementary evidence: batten's says the commands ran, the agent's says the acceptance criteria were met.

**why**: The two verdict sources answer different questions and the mechanical one silently masks the semantic one. The verdicts table correctly keeps all three rows I produced (agent/ok, batten/blocked, batten/ok) with a `source` column, so the data is there — `batten show` just calls `LatestVerdict` and prints one. The consequence is that the reviewer of a closed run sees "make test passed" and never sees which acceptance criteria were actually checked against the diff, which is the more valuable half.

**fix**: Have `batten show` print the latest verdict per (gate, source), each tagged `(batten)` / `(agent)`, and render both on the canvas. The verdict card layout already carries the source label.

**file**: cmd/batten/main.go (cmdShow, ~line 450); internal/store/store.go (LatestVerdict / LatestVerdictBySource)

---

## 77. [POLISH/confusing] [UNVERIFIED] TUI header says '1 runs'

**dim**: OBSERVABILITY — the TUI, canva

**command**:


**observed**:


**expected**: `1 run`

**why**: Cosmetic, but it is the first line of the review surface and the product's whole register is precision about what it does and does not know.

**fix**: Pluralize on len(m.runs) == 1.

**file**: internal/tui/tui.go:294 — fmt.Sprintf("  %s · %d runs", m.spec.Project, len(m.runs))

---

## 78. [POLISH/confusing] [UNVERIFIED] `batten doctor` stops at the first fatal validation error and never reaches its warnings, so fixing a spec is one round-trip per problem

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: One report listing everything doctor knows about the file. The two warnings were true on the first run and were withheld. The loader already accumulates its own errors into a list ('invalid spec:' with a bullet per problem), so this is only about doctor abandoning the run when Load fails.

**why**: Step 3 of the adoption path is 'edit the draft init wrote' — the one step where a newcomer makes several mistakes at once. Getting them one at a time, each costing a full read-edit-run cycle, is the difference between a five-minute setup and a twenty-minute one. Minor, but it lands squarely on the first impression.

**fix**: When spec.Load fails, print the fatal errors and then continue with whatever file-level checks do not need a loaded Spec (rules files, skill names, check-command resolution) parsed straight from the YAML.

**file**: cmd/batten/main.go:1194-1198

---

## 79. [POLISH/confusing] [UNVERIFIED] The internal `n-` node prefix leaks into the write-set denial message the agent reads

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: `(node-1a/store); you are node-2b/api` — the ids the user typed into `batten claim` and the ones the plugin passes as `agent_id`. The README's own example shows `(node-7f3a/ml-C); you are node-2b91/ml-A`, with no prefix.

**why**: The denial message is one of exactly two user-facing outputs the whole product is built around, and it prints an id that matches nothing the user can grep for — not the claim they made, not the agent_id in the hook payload, not the README. It reads like a second, mangled identifier and invites the reader to wonder which one is real.

**fix**: Strip the node-table prefix when formatting the collision message (or store the display id alongside the node id).

**file**: internal/hooks/hooks.go writeSetGuard, message formatting

---

## 80. [POLISH/confusing] [UNVERIFIED] `batten claim` cannot be used from a terminal at all, and its usage line does not say why

**dim**: THE FROM-ZERO DEMO REPO — buil

**command**:


**observed**:


**expected**: Either it works, or `batten --help` says it is not a standalone command. The usage line reads `batten claim <agent-id> <file>...  declare a subagent's write-set`, with nothing to suggest the agent must already have been registered by a SubagentStart hook that only Claude Code fires.

**why**: The error message is good — it names the cause — but it arrives after the user has already been told, by --help, that this is a command they can run. To demo or debug the write-set guard outside a live fan-out you have to hand-craft a SubagentStart payload and pipe it into `batten hook SubagentStart` first, which requires reading internal/hooks/hooks.go to learn the field names. I did exactly that to produce the demo.

**fix**: Annotate the usage line: `batten claim <agent-id> <file>...  declare a write-set (the agent must exist: fan-out only)`. Optionally let `claim` create the subagent row itself when given `--register`, which would make the guard demonstrable from a terminal.

**file**: cmd/batten/main.go:107 (printUsage) and cmdClaim

---

