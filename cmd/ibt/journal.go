package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jonsabados/saturdaysspinout/telemetry/analysis"
	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

// journalRoot is relative to the repo root (run ibt from there). Gitignored.
const journalRoot = "claudecoach/journal"

// journalRecord is one appended line: a session summary plus provenance.
type journalRecord struct {
	Date   string `json:"date"`   // "YYYY-MM-DD HH-MM-SS" from the filename, else file mtime
	Source string `json:"source"` // .ibt basename
	analysis.SessionSummary
}

func runJournal(args []string) {
	if len(args) < 2 {
		fmt.Println("usage: ibt journal <add|trends> <session.ibt | track-name>")
		return
	}
	switch args[0] {
	case "add":
		journalAdd(args[1])
	case "trends":
		journalTrends(args[1])
	default:
		fmt.Printf("unknown journal subcommand %q (want add|trends)\n", args[0])
	}
}

func journalAdd(path string) {
	h, err := ibt.Open(path)
	if err != nil {
		panic(err)
	}
	sum := analysis.SummarizeSession(h, analysis.DefaultCornerOpts())
	if sum.FlyingLaps == 0 {
		fmt.Println("no flying laps in this session — nothing to journal")
		return
	}
	rec := journalRecord{Date: parseSessionDate(path), Source: filepath.Base(path), SessionSummary: sum}

	dir := filepath.Join(journalRoot, slug(sum.Track))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	file := filepath.Join(dir, "sessions.jsonl")

	// dedupe by source so the skill can re-run safely
	for _, r := range readRecords(file) {
		if r.Source == rec.Source {
			fmt.Printf("already journaled %s (%s) — skipping\n", rec.Source, file)
			return
		}
	}

	line, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		panic(err)
	}

	fmt.Printf("journaled %s -> %s\n", rec.Source, file)
	fmt.Printf("  %s / %s / %s\n", sum.Track, sum.Car, sum.Driver)
	fmt.Printf("  %d flying laps, best %s, mean %s ±%.2fs, %d corners\n",
		sum.FlyingLaps, fmtTime(sum.BestLapSec), fmtTime(sum.MeanLapSec), sum.StdLapSec, len(sum.Corners))
}

func journalTrends(arg string) {
	// resolve the track: a file path -> read its TrackName; otherwise a track name
	track := arg
	if strings.HasSuffix(strings.ToLower(arg), ".ibt") {
		if h, err := ibt.Open(arg); err == nil {
			track = analysis.TrackName(h)
		}
	}
	file := filepath.Join(journalRoot, slug(track), "sessions.jsonl")
	recs := readRecords(file)
	if len(recs) == 0 {
		fmt.Printf("no journal entries for %q (%s)\n", track, file)
		return
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Date < recs[j].Date })

	latest := recs[len(recs)-1]
	fmt.Printf("=== TRENDS: %s (%s) — %d sessions ===\n\n", latest.Track, latest.Car, len(recs))
	fmt.Printf("%-19s %5s  %-9s  %-13s %-8s %s\n", "date", "laps", "best", "mean±std", "fuel", "session")
	for _, r := range recs {
		fmt.Printf("%-19s %5d  %-9s  %s ±%.2fs  %-8s %s\n",
			r.Date, r.FlyingLaps, fmtTime(r.BestLapSec), fmtTime(r.MeanLapSec), r.StdLapSec, r.Fuel, r.SessionType)
	}
	fmt.Println("(compare like-for-like: same fuel + session type, or the trend is an artifact.)")

	// per-corner min-speed across sessions, matched by nearest apex distance to
	// the latest session's corners (robust to detection C-numbering shifts).
	fmt.Printf("\nCORNER MIN-SPEED (km/h) BY SESSION  [matched by apex distance]\n")
	fmt.Printf("%-16s", "corner")
	for _, r := range recs {
		fmt.Printf(" %-10s", shortDate(r.Date))
	}
	fmt.Println()
	for _, lc := range latest.Corners {
		fmt.Printf("%-16s", fmt.Sprintf("%s@%.0f", lc.Name, lc.ApexDist))
		for _, r := range recs {
			val := "   -"
			bestD := 121.0
			for _, c := range r.Corners {
				if d := math.Abs(c.ApexDist - lc.ApexDist); d < bestD {
					bestD = d
					val = fmt.Sprintf("%.1f", c.MinSpeedMean)
				}
			}
			fmt.Printf(" %-10s", val)
		}
		fmt.Println()
	}
	fmt.Println("\n(auto-detected corners are lap-dependent; a per-track overlay will stabilize this.)")
}

func parseSessionDate(path string) string {
	base := filepath.Base(path)
	if m := regexp.MustCompile(`(\d{4}-\d{2}-\d{2}) (\d{2}-\d{2}-\d{2})`).FindStringSubmatch(base); m != nil {
		return m[1] + " " + m[2]
	}
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().Format("2006-01-02 15-04-05")
	}
	return "unknown"
}

func shortDate(d string) string {
	if len(d) >= 10 {
		return d[:10]
	}
	return d
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func readRecords(file string) []journalRecord {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []journalRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r journalRecord
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			out = append(out, r)
		}
	}
	return out
}
