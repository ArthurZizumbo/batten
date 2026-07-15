# HANDOFF — batten v2 (mejoras del PLAN.md)

> Estado vivo de la ejecución del PLAN.md. Si una sesión se corta, retomar desde "Próximo paso".
> Última actualización: 2026-07-14, inicio de la ejecución v2.

## Dónde retomar
**Próximo paso**: P2.5 — multi-sesión (binding sesión↔run, write-set guard entre runs, ambigüedad visible).

## Fases y estado
- [x] E0 — artefactos de dogfooding LISTOS. Arthur debe correr `docs/E0-DOGFOOD.md` (5 pasos) e
      pegar resultados. `batten hook-debug --tap/--show` captura el `agent_id` real. batten.yaml
      propio creado (dogfood, enforcement: report). Build scripts en scripts/.
      **Pendiente de Arthur**: los 5 resultados del spike (solo #1 .exe y #2 agent_id cambian diseño).
- [x] P1 — `batten init` real (internal/scan) + `enforcement: report`. Verificado en repo TS.
      Commit incluido. init deriva unidad/dominios/checks reales; report mode advierte en vez de bloquear.
- [x] P2 — vault AUTO (internal/export, dispara en Stop hook + tras veredicto — verificado),
      engram reinyectado en commands, graphify staleness en doctor, headroom measure real
      (migración user_version + `batten measure`). Commit incluido.
- [ ] P2.5 — multi-sesión (binding sesión↔run, guard entre runs)
- [ ] P3 — distribución del binario
- [ ] P4 — verificación end-to-end

## Contexto imprescindible
- Repo: `c:/Users/arthu/Proyectos/Public/LoopWorkFlow`, module `github.com/arthu/batten`, rama `feat/batten-mvp`.
- Binario: `go build -o plugin/claude-code/bin/batten.exe ./cmd/batten`. DB por `BATTEN_DB` env o `${CLAUDE_PLUGIN_DATA}/batten.db`.
- MVP ya commiteado (`1c1be26`): gates, contabilidad, canvas, neutralidad — todo verificado en sandbox.
- Paquetes: internal/{spec,store,hooks,canvas,usage,statusline,mcp,vault,discovery,tui}.
- Principios NO negociables: (1) nunca inventar un número; (2) degradar, no romper; (3) fallar-abierto solo con aviso; (4) override queda en el log.

## Notas de decisiones tomadas durante la ejecución
(se llenan conforme avanzo)

## Verificación de cada fase
(se llena conforme avanzo)
