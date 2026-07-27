# Examples

Three specs, deliberately at different depths. Read them in this order — the useful lesson is not
what a full spec looks like, it is **how little you need to start.**

| | Depth | What it shows |
|---|---|---|
| [`minimal/`](minimal/batten.yaml) | ~30 lines | The smallest spec that still buys you the gate. Start here |
| [`webapp-ts/`](webapp-ts/batten.yaml) | ~70 lines | A two-domain TypeScript app: fan-out axes, invariants, a budget |
| [`agrosat/`](agrosat/batten.yaml) | ~220 lines | A real ML project mid-development: 7 phases, a shared H100, per-domain models |

`agrosat/` is in Spanish because it is the genuine article — the spec of a project actually being
built this way, not something written to be read. That is the point of shipping it.

## How to fill your own

Do not copy one of these. Run the interview:

```console
$ batten init                          # inspects the repo, writes a working draft
$ batten init --from docs/workflow.md  # ...or migrates a process you already wrote in prose
$ batten doctor                        # validates it, reports which capabilities are live
```

`batten init` derives what a heuristic can honestly derive — your work-item pattern from branch
names, your domains from the directory layout, your `check` commands **verbatim** from your
Makefile or `package.json`. It never invents a check. It refuses to overwrite a `batten.yaml` that
already exists.

What it leaves for you is what a heuristic has no business guessing, and it says so in the file:

```yaml
    invariants: []  # TODO
```

### The invariants are the point

They are the highest-value lines in the spec, and the only ones nothing can derive for you. They
ride **character-for-character** into every fanned-out agent's prompt.

An invariant is a rule **a reviewer would catch and a distracted agent would break**:

```yaml
invariants:
  - session_id in every query and endpoint       # good: checkable, specific, breakable
  - logic in the service, never in the router    # good: an architectural rule with a clear violation
  - write good code                              # useless: nothing to violate
```

Mine them from your `AGENTS.md` files, from review comments you have written more than once, and
from the bugs that keep coming back.

### What does not belong

> **If a hook cannot enforce it or a command cannot run it, it does not go in `batten.yaml`.**

`make test` goes in the spec. "Two agents never write the same file" goes in the spec — it becomes a
constraint. "Think carefully about the architecture" stays in prose, and that is fine.

Over-declaring turns the spec into a DSL nobody maintains. Under-declaring turns it into a comment.

## Adopt without blocking the sprint

Every example starts at `enforcement: report`. Gates warn instead of denying, so batten can go into
a repo mid-sprint without becoming the reason a release slipped. Flip it to `enforce` when the team
trusts the gates — that is a decision, and it should be made deliberately rather than defaulted into.

There is a JSON Schema at [`../batten.schema.json`](../batten.schema.json); point your editor at it
and the file completes and validates as you type.
