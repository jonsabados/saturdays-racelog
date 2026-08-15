package main

import (
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/jonsabados/saturdaysspinout/telemetry/analysis"
	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

func runCompare(args []string) {
	// args: fileYou lapYou fileRef lapRef
	if len(args) < 4 {
		fmt.Println("usage: ibt compare <you.ibt> <lap> <ref.ibt> <lap> [csv]")
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
	you := analysis.ExtractLap(hY, int32(lY))
	ref := analysis.ExtractLap(hR, int32(lR))
	if len(you.Dist) == 0 || len(ref.Dist) == 0 {
		fmt.Println("compare: no samples for the requested lap(s) — check the lap numbers")
		return
	}

	trackLen := math.Min(you.Dist[len(you.Dist)-1], ref.Dist[len(ref.Dist)-1])
	N := 1000
	grid := make([]float64, N)
	for i := range grid {
		grid[i] = float64(i) / float64(N-1) * trackLen
	}

	// interpolate onto grid
	type G struct{ t, sp, th, br, st []float64 }
	mk := func(d analysis.Lap) G {
		g := G{}
		for _, x := range grid {
			g.t = append(g.t, analysis.Interp(d.Dist, d.TElap, x))
			g.sp = append(g.sp, analysis.Interp(d.Dist, d.Speed, x))
			g.th = append(g.th, analysis.Interp(d.Dist, d.Thr, x))
			g.br = append(g.br, analysis.Interp(d.Dist, d.Brk, x))
			g.st = append(g.st, analysis.Interp(d.Dist, d.Steer, x))
		}
		return g
	}
	gY, gR := mk(you), mk(ref)

	tYtot := you.TElap[len(you.TElap)-1]
	tRtot := ref.TElap[len(ref.TElap)-1]
	fmt.Printf("YOU  lap %d: %s over %.0fm\n", lY, fmtTime(tYtot), you.Dist[len(you.Dist)-1])
	fmt.Printf("REF  lap %d: %s over %.0fm\n", lR, fmtTime(tRtot), ref.Dist[len(ref.Dist)-1])
	fmt.Printf("RAW GAP: %+.3fs\n\n", tYtot-tRtot)

	// delta-t trace (you - ref), both anchored at 0
	delta := make([]float64, N)
	for i := 0; i < N; i++ {
		delta[i] = gY.t[i] - gR.t[i]
	}

	// corner detection on smoothed ref speed: local minima
	sm := analysis.Smooth(gR.sp, 8)
	type corner struct{ idx int }
	var corners []corner
	win := 25
	for i := win; i < N-win; i++ {
		isMin := true
		for k := i - win; k <= i+win; k++ {
			if sm[k] < sm[i] {
				isMin = false
				break
			}
		}
		// require it be an actual slow point (< 250 km/h) and not adjacent dup
		if isMin && sm[i]*3.6 < 255 {
			if len(corners) == 0 || i-corners[len(corners)-1].idx > win {
				corners = append(corners, corner{i})
			}
		}
	}

	fmt.Printf("=== CORNER-BY-CORNER (min speed & braking) ===\n")
	fmt.Printf("%-12s %-9s %-9s %-8s  %-10s %-10s\n", "corner", "youKmh", "refKmh", "dSpd", "youBrake@", "refBrake@")
	for ci, c := range corners {
		i := c.idx
		// find your local min speed near this corner (+-40 idx)
		yMin, yIdx := 1e9, i
		for k := i - 30; k <= i+30; k++ {
			if k >= 0 && k < N && gY.sp[k] < yMin {
				yMin = gY.sp[k]
				yIdx = k
			}
		}
		_ = yIdx
		refKmh := gR.sp[i] * 3.6
		youKmh := yMin * 3.6
		// braking point: find onset of the braking zone preceding the apex.
		// walk back from apex through any off-brake coast at the apex, into the
		// braking zone, then back to where the brakes first came on.
		bp := func(g G, apex int) float64 {
			k := apex
			for k > apex-160 && k > 0 && g.br[k] < 0.08 { // skip off-brake at apex
				k--
			}
			for k > apex-160 && k > 0 && g.br[k] >= 0.08 { // back through braking zone
				k--
			}
			return grid[k+1]
		}
		yBrk := bp(gY, i)
		rBrk := bp(gR, i)
		fmt.Printf("%-12s %-9.1f %-9.1f %+-8.1f %-10.0f %-10.0f\n",
			fmt.Sprintf("C%d@%.0f", ci+1, grid[i]), youKmh, refKmh, youKmh-refKmh, yBrk, rBrk)
	}

	// cumulative delta by segment (10 equal-distance segments)
	fmt.Printf("\n=== TIME GAIN/LOSS BY SEGMENT (lap split into 10) ===\n")
	fmt.Printf("%-8s %-14s %-12s\n", "seg", "dist range", "d(this seg)")
	segN := 10
	for s := 0; s < segN; s++ {
		i0 := s * N / segN
		i1 := (s+1)*N/segN - 1
		seg := delta[i1] - delta[i0]
		bar := ""
		n := int(math.Abs(seg) * 20)
		for b := 0; b < n && b < 40; b++ {
			bar += "#"
		}
		sign := "LOSS"
		if seg < 0 {
			sign = "gain"
		}
		fmt.Printf("%-8d %5.0f-%5.0f  %+7.3f %-5s %s\n", s+1, grid[i0], grid[i1], seg, sign, bar)
	}
	fmt.Printf("\nFINAL DELTA: %+.3fs\n", delta[N-1])

	// driving-style aggregates
	pct := func(g G, f func(i int) bool) float64 {
		c := 0
		for i := 0; i < N; i++ {
			if f(i) {
				c++
			}
		}
		return 100 * float64(c) / float64(N)
	}
	fmt.Printf("\n=== DRIVING STYLE (%% of lap by distance) ===\n")
	fmt.Printf("%-18s %-8s %-8s\n", "", "YOU", "REF")
	fmt.Printf("%-18s %6.1f%%  %6.1f%%\n", "Full throttle", pct(gY, func(i int) bool { return gY.th[i] > 0.98 }), pct(gR, func(i int) bool { return gR.th[i] > 0.98 }))
	fmt.Printf("%-18s %6.1f%%  %6.1f%%\n", "On brakes", pct(gY, func(i int) bool { return gY.br[i] > 0.08 }), pct(gR, func(i int) bool { return gR.br[i] > 0.08 }))
	fmt.Printf("%-18s %6.1f%%  %6.1f%%\n", "Coasting", pct(gY, func(i int) bool { return gY.th[i] < 0.05 && gY.br[i] < 0.08 }), pct(gR, func(i int) bool { return gR.th[i] < 0.05 && gR.br[i] < 0.08 }))
	fmt.Printf("%-18s %6.1f%%  %6.1f%%\n", "Trail (brk+steer)", pct(gY, func(i int) bool { return gY.br[i] > 0.08 && math.Abs(gY.st[i]) > 0.15 }), pct(gR, func(i int) bool { return gR.br[i] > 0.08 && math.Abs(gR.st[i]) > 0.15 }))

	// throttle limited by rotation? where the ref is flat and you aren't, are you
	// carrying more steering lock? if so the car isn't rotated and the throttle
	// gap is a symptom — unwind the lock sooner, don't just "be flatter" (which
	// over the kerb with the rear light is how you crash).
	const rad2deg = 180.0 / math.Pi
	var nLim int
	var yLock, rLock float64
	for i := 0; i < N; i++ {
		if gR.th[i] > 0.98 && gY.th[i] < 0.90 {
			nLim++
			yLock += math.Abs(gY.st[i]) * rad2deg
			rLock += math.Abs(gR.st[i]) * rad2deg
		}
	}
	if nLim > 0 {
		fmt.Printf("\n=== THROTTLE LIMITED BY ROTATION? ===\n")
		fmt.Printf("At %d pts (%.0f%% of lap) ref is flat (>98%%) and you aren't (<90%%):\n",
			nLim, 100*float64(nLim)/float64(N))
		fmt.Printf("  your mean steering lock %.1f°  vs ref %.1f°  (%+.1f°)\n",
			yLock/float64(nLim), rLock/float64(nLim), (yLock-rLock)/float64(nLim))
		fmt.Println("  (more lock than ref => car not rotated; unwind sooner, don't just 'be flat')")
	}

	// dump aligned CSV for optional later plotting
	if len(args) > 4 && args[4] == "csv" {
		if err := os.MkdirAll("/tmp/ibt", 0o755); err != nil {
			fmt.Println("compare: could not create csv dir:", err)
			return
		}
		w, err := os.Create("/tmp/ibt/aligned.csv")
		if err != nil {
			fmt.Println("compare: could not write csv:", err)
			return
		}
		fmt.Fprintln(w, "dist,delta,you_spd,ref_spd,you_thr,ref_thr,you_brk,ref_brk")
		for i := 0; i < N; i++ {
			fmt.Fprintf(w, "%.1f,%.4f,%.2f,%.2f,%.3f,%.3f,%.3f,%.3f\n",
				grid[i], delta[i], gY.sp[i]*3.6, gR.sp[i]*3.6, gY.th[i], gR.th[i], gY.br[i], gR.br[i])
		}
		w.Close()
		fmt.Println("\nwrote /tmp/ibt/aligned.csv")
	}
}

func mustI(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return v
}
