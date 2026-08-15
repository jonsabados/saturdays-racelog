// Command ibt is a local iRacing telemetry coaching tool. It decodes .ibt files
// (via the telemetry/ibt package) and prints lap tables, channel listings, and
// position-aligned driver-vs-reference analyses (compare/brake/consistency/section).
//
// Not part of the deployed app — a personal analysis CLI driven by the
// claudecoach skill.
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

func fmtTime(s float64) string {
	if s <= 0 {
		return "--"
	}
	m := int(s) / 60
	sec := s - float64(m*60)
	return fmt.Sprintf("%d:%06.3f", m, sec)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: ibt <summary|channels|compare|brake|consistency|section|journal> <file> ...")
		fmt.Println("  summary      <file>")
		fmt.Println("  channels     <file>")
		fmt.Println("  consistency  <file> [prominenceKmh] [spacingM] [maxApexKmh] [smoothWin] [gridN]")
		fmt.Println("  compare      <you.ibt> <lap> <ref.ibt> <lap> [csv]")
		fmt.Println("  brake        <you.ibt> <lap> <ref.ibt> <lap>")
		fmt.Println("  section      <you.ibt> <lap> <ref.ibt> <lap> <loM> <hiM>")
		fmt.Println("  journal add    <session.ibt>          (append session summary to the journal)")
		fmt.Println("  journal trends <session.ibt|track>     (show cross-session trends)")
		os.Exit(1)
	}
	mode := os.Args[1]
	if mode == "compare" {
		runCompare(os.Args[2:])
		return
	}
	if mode == "brake" {
		runBrake(os.Args[2:])
		return
	}
	if mode == "consistency" {
		runConsistency(os.Args[2:])
		return
	}
	if mode == "section" {
		runSection(os.Args[2:])
		return
	}
	if mode == "journal" {
		runJournal(os.Args[2:])
		return
	}
	h, err := ibt.Open(os.Args[2])
	if err != nil {
		panic(err)
	}
	fmt.Printf("ver=%d tickRate=%dHz numVars=%d bufLen=%d samples=%d (~%.1f min)\n",
		h.Ver, h.TickRate, h.NumVars, h.BufLen, h.NumSamples, float64(h.NumSamples)/float64(h.TickRate)/60)

	yaml := h.SessionYAML()
	for _, key := range []string{"TrackName", "TrackDisplayName", "TrackConfigName", "DriverSetupName"} {
		if m := regexp.MustCompile(key + `: ?(.*)`).FindStringSubmatch(yaml); m != nil {
			fmt.Printf("  %s: %s\n", key, strings.TrimSpace(m[1]))
		}
	}
	// driver / car
	if m := regexp.MustCompile(`UserName: ?(.*)`).FindStringSubmatch(yaml); m != nil {
		fmt.Printf("  Driver: %s\n", strings.TrimSpace(m[1]))
	}
	if m := regexp.MustCompile(`CarScreenName: ?(.*)`).FindStringSubmatch(yaml); m != nil {
		fmt.Printf("  Car: %s\n", strings.TrimSpace(m[1]))
	}
	// Conditions — establish comparability BEFORE comparing laps. Fuel load,
	// track temp, and session type each change what a lap time means; comparing
	// across them (e.g. an 8L quali lap vs a 55L race lap) silently misleads.
	fmt.Println("  --- conditions (must match to compare laps) ---")
	for _, key := range []string{"SessionType", "SessionName", "FuelLevel", "BrakePressureBias", "TrackSurfaceTemp", "TrackAirTemp"} {
		if m := regexp.MustCompile(key + `: ?(.*)`).FindStringSubmatch(yaml); m != nil {
			fmt.Printf("  %-17s %s\n", key+":", strings.TrimSpace(m[1]))
		}
	}

	if mode == "channels" {
		names := make([]string, 0, len(h.VarList))
		for _, v := range h.VarList {
			names = append(names, fmt.Sprintf("%-28s cnt=%d %-8s %s", v.Name, v.Count, v.Unit, v.Desc))
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}

	// summary: lap table via Lap channel transitions
	lapV, ok := h.Vars["Lap"]
	stV, ok2 := h.Vars["SessionTime"]
	pctV := h.Vars["LapDistPct"]
	spdV := h.Vars["Speed"]
	onTrackV, hasOT := h.Vars["IsOnTrack"]
	if !ok || !ok2 {
		fmt.Println("missing Lap/SessionTime channels")
		return
	}
	type lapRow struct {
		lap        int32
		start, end float64
		minPct     float64
		maxSpeed   float64
	}
	var laps []lapRow
	curLap := h.Int(lapV, 0)
	lapStart := h.Float(stV, 0)
	maxSpd := 0.0
	for i := 1; i < h.NumSamples; i++ {
		if hasOT && h.Int(onTrackV, i) == 0 {
			// still track transitions, but note pit/off
		}
		s := h.Float(spdV, i)
		if s > maxSpd {
			maxSpd = s
		}
		l := h.Int(lapV, i)
		if l != curLap {
			t := h.Float(stV, i)
			laps = append(laps, lapRow{curLap, lapStart, t, 0, maxSpd})
			curLap = l
			lapStart = t
			maxSpd = 0
		}
	}
	_ = pctV
	fmt.Printf("\n%-5s %-12s %s\n", "Lap", "LapTime", "MaxSpeed(km/h)")
	best := lapRow{lap: -1}
	bestTime := 1e9
	for _, lr := range laps {
		dur := lr.end - lr.start
		mark := ""
		if dur > 60 && dur < 300 && dur < bestTime {
			bestTime = dur
			best = lr
			_ = best
		}
		if dur > 60 && dur < 300 {
			mark = ""
		} else {
			mark = "  (out/in/pit or partial)"
		}
		fmt.Printf("%-5d %-12s %6.1f%s\n", lr.lap, fmtTime(dur), lr.maxSpeed*3.6, mark)
	}
	if bestTime < 1e9 {
		fmt.Printf("\nBEST FLYING LAP: lap %d  %s\n", best.lap, fmtTime(bestTime))
	}
}
