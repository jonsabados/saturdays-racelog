# claudecoach — iRacing telemetry coaching

A personal driving-analysis system: a Go CLI decodes iRacing `.ibt` telemetry into
driving metrics, and Claude (via the `claudecoach` skill) interprets them through a
blind-test coaching protocol, tracking improvement across sessions in a journal.

Not part of the deployed racelog app — but built in the main Go module (`cmd/ibt` +
`telemetry/`) with an eye toward a future "upload telemetry → coaching" feature, so the
analysis lives in reusable, tested packages.

## Layout

| Path | What |
|---|---|
| `telemetry/ibt/` | `.ibt` binary decoder (dependency-free, read-only) |
| `telemetry/analysis/` | pure analysis: resampling, flying-lap filter, corner detection, session summaries — the reusable core, with tests |
| `cmd/ibt/` | the CLI (thin formatting over `telemetry/analysis`) |
| `claudecoach/journal/` | per-track session summaries (`sessions.jsonl`) — **gitignored, local** |
| `.claude/skills/claudecoach/SKILL.md` | the coaching workflow Claude follows |
| `telemetry-notes/` | narrative writeups (e.g. `suzuka-sf23-analysis.md`) |

## Build & run

```bash
make ibt                       # builds dist/ibt
# or just run directly from the repo root:
go run ./cmd/ibt <command> ...
go test ./telemetry/...        # unit tests for the analysis core
```

## Commands

```
summary      <file.ibt>                              lap table, best flying lap, car/track/driver
channels     <file.ibt>                              list telemetry channels
consistency  <file.ibt> [prom] [spacing] [maxApex] [smoothWin] [gridN]
                                                     per-corner consistency + sector variance (single file)
compare      <you.ibt> <lap> <ref.ibt> <lap> [csv]   position-aligned delta, segment gain/loss, driving style
brake        <you.ibt> <lap> <ref.ibt> <lap>         brake modulation per detected corner (sat/fine/jerk)
section      <you.ibt> <lap> <ref.ibt> <lap> <loM> <hiM>
                                                     deep-dive a distance window (throttle, GPS line, exit)
journal add    <session.ibt>                         append this session's summary (dedupes by filename)
journal trends <session.ibt | track-name>            cross-session trends
```

The optional `consistency` args are corner-detection tuning knobs; defaults suit a
high-downforce open-wheeler. Corners are auto-detected from speed minima on the fastest
lap and named `C1..Cn`.

## Design notes

- **Track-agnostic.** Corner apexes, the flying-lap window (relative to the session best),
  and sector splits are all derived from the data — no per-track constants. Tested on
  Suzuka (~1:37) and Mugello (~1:27).
- **Corner detection is lap-dependent.** A corner taken flat or with only a tire-saving
  lift leaves no speed minimum, so it won't be detected. A per-track *overlay* (canonical
  named corner list, stable across sessions) is planned to fix this for trend-tracking;
  see the stub in `telemetry/analysis/overlays.go`.
- **The tool tabulates; Claude coaches.** Interpretation is deliberately not baked in.

## Source data (not committed)

- Driver sessions: `~/Documents/iRacing/telemetry/superformulasf23 *_<track> *.ibt`
- Reference laps: `~/Downloads/26S3 SF23 <Track> Telemetry Replay Lapfiles/telemetry {race,quali}.ibt`
