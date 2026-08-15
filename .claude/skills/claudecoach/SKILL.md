---
name: claudecoach
description: >-
  Coach an iRacing driving session from raw telemetry. Parses .ibt files, compares
  the driver's best lap against a fast reference, finds where time and consistency
  are lost, and tracks improvement across sessions via a journal — run through a
  blind-test protocol so the analysis and the driver's own read don't bias each
  other. Use when the user wants to review a lap or stint, find where they're slow
  or inconsistent, check if they're improving, or "look at my telemetry".
---

# claudecoach — iRacing telemetry coaching

A local Go tool (`cmd/ibt`) decodes iRacing `.ibt` telemetry into driving metrics;
you (Claude) interpret them. The tool tabulates — you coach. Run everything from the
repo root with `go run ./cmd/ibt <command> ...` (no build step needed).

## Where the files are

- **Driver sessions:** `/mnt/c/Users/JonSa/Documents/iRacing/telemetry/` — newest first
  with `ls -t`. Filenames encode car, track, and timestamp. (This is a WSL box;
  `~/Documents` and `~/Downloads` do **not** resolve — use the `/mnt/c/...` paths.)
- **Reference laps (a fast pro):** `/mnt/c/Users/JonSa/Downloads/26S3 SF23 <Track> Telemetry Replay Lapfiles/telemetry race.ibt`
  (also `telemetry quali.ibt`). Match the track to the driver's session.
- **Journal (trends):** `claudecoach/journal/<track>/sessions.jsonl` — gitignored, local.
- **Writeups:** `telemetry-notes/<track>-analysis.md` (also gitignored).

## The commands

```
go run ./cmd/ibt summary      "<file.ibt>"                          # lap table, best flying lap, car/track/driver, CONDITIONS
go run ./cmd/ibt channels     "<file.ibt>"                          # list telemetry channels
go run ./cmd/ibt consistency  "<file.ibt>"                          # per-corner consistency, sector variance (single file)
go run ./cmd/ibt compare      "<you>" <lap> "<ref>" <lap> [csv]     # position-aligned delta, segment gain/loss, style, rotation check
go run ./cmd/ibt brake        "<you>" <lap> "<ref>" <lap>           # brake modulation per corner (sat/fine/jerk)
go run ./cmd/ibt section      "<you>" <lap> "<ref>" <lap> <loM> <hiM># deep-dive a window (throttle, steering, gear, line, exit)
go run ./cmd/ibt journal add    "<file.ibt>"                        # append this session's summary to the journal
go run ./cmd/ibt journal trends "<file.ibt | track-name>"          # cross-session trends
```

`compare` takes **any two files** — it does not have to be you-vs-reference. Comparing
your own two sessions (e.g. before/after a setup or hardware change) is often the single
most valuable run: it isolates one variable. Use it that way deliberately.

Corners are auto-detected from speed minima on the fastest lap, named `C1..Cn@dist`. They
are **lap-dependent** (a tire-saving lift or a flat-out kink leaves no dip), so the same
corner can shift number between sessions — always refer to a corner by its **distance**
(`C3@1528m`), never its bare number, when writing anything cross-session. The journal
matches corners across sessions by apex distance for this reason.

## Workflow

1. **Locate.** Find the driver's session (usually the newest, or the one they name) and a
   matching reference for that track.
2. **Establish comparability — BEFORE comparing anything.** Run `summary` on both files and
   read the **conditions block** (fuel, session type, track temp, brake bias). A lap time
   only means something against another lap at the *same* fuel load, session type, and
   roughly the same temps. An 8 L quali lap vs a 55 L race lap is not a valid A/B and will
   invert your conclusion. If the driver's "best lap" was set in a non-comparable session,
   the right reference is a comparable one — often their own earlier lap, not the pro's.
   **Also just ask the driver:** what changed (hardware, setup, aids, tires), and what the
   conditions were. This context-gathering is *not* part of the blind test (see step 4).
3. **Run the analysis and form your read — but hold it.** `consistency` on the driver's
   session; `compare` (comparable driver lap vs reference). Study the corner table, segment
   gains/losses, driving-style split, and the **rotation check**. Deep-dive suspect corners
   with `brake` and `section` (watch steering lock and gear, not just speed/throttle).
4. **BLIND TEST (do not skip).** Before you reveal anything:
   - Write your full read to a scratch file (a locked diagnosis).
   - Ask the driver what *they* think their main issues are — their gut, unprompted by your
     findings. **Only your findings are sealed; gathering context is not.** Fuel, hardware,
     conditions, aids, "which corner felt bad" — ask freely; those make the analysis correct.
   - Then reveal, and **score their read against the data** (right / partly / inverted),
     calling out agreements and surprises. This is the core of the exercise.
5. **When the driver pushes back on a finding, treat it as a lead, not resistance.** A driver
   saying "the car won't do that" or "if I pin it I'm in the wall" is *data* — usually the
   best error-correction signal you have. Their physical intuition often exposes that your
   finding was a symptom, not a cause (e.g. "get to full throttle" was shallow; the real
   issue was carrying too much steering lock — the car wasn't rotated). Re-probe with the
   tool before defending your read. "Keep the driver's dignity" means *don't lecture* — it
   does **not** mean don't argue back.
6. **Trend check.** Run `journal trends` for the track. If the journal is empty or sparse
   for this track, first offer to `journal add` the comparable prior sessions (same fuel /
   session type) so the trend is real — then read it. Frame repeat-track findings as
   "better / same / worse than last time."
7. **Journal it.** `journal add` the session (safe to re-run — it dedupes by filename).
8. **Writeup (offer).** Update `telemetry-notes/<track>-analysis.md` with a dated section:
   the blind-test scorecard, key deltas, and a short "practice priorities" list.

## Reading the output (what the metrics mean)

- **summary conditions** — fuel / session type / temps / brake bias. Comparability gate;
  read first.
- **consistency** — `min-speed STDDEV` = apex-speed spread lap-to-lap. `brakeSTD(m)` =
  braking-*point* spread; `n/a` means no braking there, or the onset fell outside the search
  window (it is *not* "perfectly consistent" — never report `n/a` as a win). The
  slowest-corner brake-saturation block shows whether the driver mashes to 100%.
- **compare** — segment `LOSS/gain` = where lap time goes. Driving-style %. The **rotation
  check**: at points where the ref is flat and the driver isn't, if the driver carries more
  steering lock the car isn't rotated — the throttle gap is a *symptom*; coach "unwind the
  lock sooner," never "just be flat" (that's how you crash on a light rear over a kerb).
- **brake** — `sat%` = share pinned >95% (mashing). `fine%` = share in the 15–85% band.
  `jerk` = steppiness. Low sat% + high fine% = clean, modulated braking.
- **section** — steering `|lock|` (deg), `gear` at each sub-apex (nearest-sampled, discrete),
  `line wander` = lap-to-lap GPS spread, `throttle-resume` = exit-commitment distance
  (earlier = better). A driver can be limited by rotation *or* running a shorter gear than
  the reference through an exit — both make "be flatter" wrong advice.

## Interpreting, not just reporting

Numbers are the input to coaching, not the output. Tie findings to mechanism (brake feel,
throttle commitment, rotation, line, gearing, tire management) and to what changed (hardware,
setup, conditions). A single number is a symptom — chase the cause before you prescribe.

## Gotchas

- **Comparability first.** The most common way to be confidently wrong is comparing across
  fuel loads or session types. Always read the conditions block.
- **Corners by distance, not number** (`C3@1528m`). Bare `C1..Cn` shift between sessions.
- **Ad-hoc probes:** to answer a question no command covers, you can write a throwaway Go
  program against the `analysis` package. `go run <dir>/` fails ("directory outside main
  module") — list the `.go` files explicitly, or add it under `cmd/`. Use `analysis.Nearest`
  (not `Interp`) for discrete channels like gear.

## Worked example — the loop this exists to close

Suzuka round 1 flagged the driver mashing 100% brake on every lap and concluded stiffer
brake hardware was justified. He changed his pedal elastomers; at Mugello he now runs 0%
brake saturation, never exceeding ~0.87. A prior round's prescription → a hardware change →
measurable confirmation a round later is exactly what the journal is for. Look for these
loops and name them.
