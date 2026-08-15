package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jonsabados/saturdaysspinout/telemetry/analysis"
	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

type brakeZoneStat struct {
	name            string
	secsOnBrake     float64
	meanBrake       float64
	peakBrake       float64
	reversals       int
	reversalsPerSec float64
	rmsRate         float64 // RMS of per-sample brake delta (smoothness; higher=jerkier)
	satFrac         float64 // fraction of on-brake time pinned >95% (saturation)
	fineFrac        float64 // fraction of on-brake time in fine mid-range 15-85%
	riseTime        float64 // seconds from brake-on to first reaching 95% (0 if never)
}

// analyze brake modulation for lap d within [lo,hi] meters
func brakeZone(d analysis.Lap, dt float64, lo, hi float64) brakeZoneStat {
	var s brakeZoneStat
	const onThresh = 0.05
	const noise = 0.02 // ignore brake wiggle smaller than this for reversal counting
	var sumB, sumRate2 float64
	nOn, nSat, nFine := 0, 0, 0
	prevDelta := 0.0
	haveDelta := false
	for i := 1; i < len(d.Dist); i++ {
		if d.Dist[i] < lo || d.Dist[i] > hi {
			continue
		}
		b := d.Brk[i]
		if b > onThresh {
			nOn++
			sumB += b
			if b > s.peakBrake {
				s.peakBrake = b
			}
			if b > 0.95 {
				nSat++
			}
			if b >= 0.15 && b <= 0.85 {
				nFine++
			}
			delta := d.Brk[i] - d.Brk[i-1]
			sumRate2 += delta * delta
			if math.Abs(delta) > noise {
				if haveDelta && signf(delta) != signf(prevDelta) {
					s.reversals++
				}
				prevDelta = delta
				haveDelta = true
			}
		}
	}
	s.secsOnBrake = float64(nOn) * dt
	if nOn > 0 {
		s.meanBrake = sumB / float64(nOn)
		s.rmsRate = math.Sqrt(sumRate2/float64(nOn)) / dt // brake units per second
		s.satFrac = float64(nSat) / float64(nOn)
		s.fineFrac = float64(nFine) / float64(nOn)
	}
	if s.secsOnBrake > 0 {
		s.reversalsPerSec = float64(s.reversals) / s.secsOnBrake
	}
	return s
}

func signf(x float64) int {
	if x < 0 {
		return -1
	}
	return 1
}

// sparkline of brake trace across [lo,hi]
func brakeSpark(d analysis.Lap, lo, hi float64, n int) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	out := make([]rune, n)
	for k := 0; k < n; k++ {
		x := lo + (hi-lo)*float64(k)/float64(n-1)
		v := analysis.Interp(d.Dist, d.Brk, x)
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		idx := int(v * float64(len(blocks)-1))
		out[k] = blocks[idx]
	}
	return string(out)
}

func runBrake(args []string) {
	if len(args) < 4 {
		fmt.Println("usage: ibt brake <you.ibt> <lap> <ref.ibt> <lap>")
		return
	}
	fY, lY := args[0], mustI(args[1])
	fR, lR := args[2], mustI(args[3])
	hY, err := ibt.Open(fY)
	if err != nil {
		panic(err)
	}
	hR, err := ibt.Open(fR)
	if err != nil {
		panic(err)
	}
	dtY := 1.0 / float64(hY.TickRate)
	dtR := 1.0 / float64(hR.TickRate)
	you := analysis.ExtractLap(hY, int32(lY))
	ref := analysis.ExtractLap(hR, int32(lR))
	if len(you.Dist) == 0 || len(ref.Dist) == 0 {
		fmt.Println("brake: no samples for the requested lap(s) — check the lap numbers")
		return
	}

	// corner set from the reference lap (auto-detected, named C1..Cn)
	trackLen := ref.Dist[len(ref.Dist)-1]
	rDist, rSpd := analysis.Resample(ref, 1000)
	corners := analysis.DetectCorners(rDist, rSpd, analysis.DefaultCornerOpts())
	for i := range corners {
		if corners[i].Name == "" {
			corners[i].Name = fmt.Sprintf("C%d", i+1)
		}
	}

	// zones = whole lap, then a generous braking window around each corner apex.
	// (satFrac/peak only count on-brake samples, so a wide window can't dilute
	// them — it only guarantees the whole braking event is captured.)
	type zone struct {
		name   string
		lo, hi float64
	}
	zones := []zone{{"WHOLE LAP (all braking)", 0, trackLen + 1}}
	for _, c := range corners {
		zones = append(zones, zone{
			name: fmt.Sprintf("%-9s @%.0fm", c.Name, c.ApexDist),
			lo:   c.ApexDist - 240,
			hi:   c.ApexDist + 60,
		})
	}

	// headline: saturation (mash-to-100%) vs fine mid-range modulation
	fmt.Printf("%-24s | %-26s | %-26s\n", "ZONE", "YOU  peak sat%% fine%% jerk", "REF  peak sat%% fine%% jerk")
	fmt.Println(strings.Repeat("-", 84))
	for _, z := range zones {
		y := brakeZone(you, dtY, z.lo, z.hi)
		r := brakeZone(ref, dtR, z.lo, z.hi)
		fmt.Printf("%-24s | %4.2f %5.0f %5.0f %5.1f | %4.2f %5.0f %5.0f %5.1f\n",
			z.name,
			y.peakBrake, y.satFrac*100, y.fineFrac*100, y.rmsRate,
			r.peakBrake, r.satFrac*100, r.fineFrac*100, r.rmsRate)
	}
	fmt.Println("\nsat%% = share of on-brake time pinned >95% (mashed). fine%% = share in 15-85% (modulating).")
	fmt.Println("jerk  = RMS brake-rate (higher = steppier application).")

	// brake-trace sparklines for the two slowest corners (heaviest braking)
	slow := make([]analysis.Corner, len(corners))
	copy(slow, corners)
	sort.Slice(slow, func(i, j int) bool { return slow[i].ApexSpeedKmh < slow[j].ApexSpeedKmh })
	for i := 0; i < 2 && i < len(slow); i++ {
		c := slow[i]
		lo, hi := c.ApexDist-240, c.ApexDist+60
		fmt.Printf("\nBRAKE TRACE — %s @%.0fm (%.0f-%.0fm), 60 chars:\n", c.Name, c.ApexDist, lo, hi)
		fmt.Printf("YOU: %s\n", brakeSpark(you, lo, hi, 60))
		fmt.Printf("REF: %s\n", brakeSpark(ref, lo, hi, 60))
	}
}
