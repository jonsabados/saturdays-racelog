package main

// The "section" command: a deep-dive over an arbitrary distance window
// [loM,hiM] of the lap — speed/throttle modulation, steering lock, gear, GPS
// line deviation vs the reference, and exit commitment.

import (
	"fmt"
	"math"
	"strconv"

	"github.com/jonsabados/saturdaysspinout/telemetry/analysis"
	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

// haversine distance in meters between two lat/lon (decimal degrees)
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

// throttle modulation stats within [lo,hi]
type thrStat struct {
	fullFrac, fineFrac, liftFrac float64
	lifts                        int
	jerk                         float64
}

func throttleZone(d analysis.Lap, dt, lo, hi float64) thrStat {
	var s thrStat
	n, nFull, nFine, nLift := 0, 0, 0, 0
	var sumRate2 float64
	wasUp := false
	for i := 1; i < len(d.Dist); i++ {
		if d.Dist[i] < lo || d.Dist[i] > hi {
			continue
		}
		t := d.Thr[i]
		n++
		if t > 0.98 {
			nFull++
		}
		if t >= 0.15 && t <= 0.85 {
			nFine++
		}
		if t < 0.05 {
			nLift++
			if wasUp {
				s.lifts++
			}
			wasUp = false
		} else if t > 0.30 {
			wasUp = true
		}
		delta := d.Thr[i] - d.Thr[i-1]
		sumRate2 += delta * delta
	}
	if n > 0 {
		s.fullFrac = float64(nFull) / float64(n)
		s.fineFrac = float64(nFine) / float64(n)
		s.liftFrac = float64(nLift) / float64(n)
		s.jerk = math.Sqrt(sumRate2/float64(n)) / dt
	}
	return s
}

func spark(vals []float64, vmin, vmax float64) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	out := make([]rune, len(vals))
	for i, v := range vals {
		f := (v - vmin) / (vmax - vmin)
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		out[i] = blocks[int(f*float64(len(blocks)-1))]
	}
	return string(out)
}

func mustF(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return v
}

func runSection(args []string) {
	// args: fileYou lapYou fileRef lapRef loM hiM
	if len(args) < 6 {
		fmt.Println("usage: ibt section <you.ibt> <lap> <ref.ibt> <lap> <loM> <hiM>")
		return
	}
	fY, lY := args[0], mustI(args[1])
	fR, lR := args[2], mustI(args[3])
	lo, hi := mustF(args[4]), mustF(args[5])
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
		fmt.Println("section: no samples for the requested lap(s) — check the lap numbers")
		return
	}

	N := int((hi - lo) / 14)
	if N < 20 {
		N = 20
	}
	grid := make([]float64, N)
	for i := range grid {
		grid[i] = lo + (hi-lo)*float64(i)/float64(N-1)
	}
	iy := func(d analysis.Lap, ch []float64) []float64 {
		out := make([]float64, N)
		for i, x := range grid {
			out[i] = analysis.Interp(d.Dist, ch, x)
		}
		return out
	}
	ySpd := iy(you, you.Speed)
	rSpd := iy(ref, ref.Speed)
	yThr := iy(you, you.Thr)
	rThr := iy(ref, ref.Thr)

	// auto-scale the speed sparkline to the data in the window
	smin, smax := math.Inf(1), math.Inf(-1)
	for i := 0; i < N; i++ {
		for _, v := range []float64{ySpd[i] * 3.6, rSpd[i] * 3.6} {
			if v < smin {
				smin = v
			}
			if v > smax {
				smax = v
			}
		}
	}

	fmt.Printf("=== SECTION %.0f-%.0fm  (each char ~%.0fm) ===\n\n", lo, hi, (hi-lo)/float64(N))
	fmt.Printf("SPEED (%.0f-%.0f km/h):\n", smin, smax)
	fmt.Printf("YOU %s\n", spark(scale(ySpd, 3.6), smin, smax))
	fmt.Printf("REF %s\n", spark(scale(rSpd, 3.6), smin, smax))
	fmt.Printf("THROTTLE (0-100%%):\n")
	fmt.Printf("YOU %s\n", spark(yThr, 0, 1))
	fmt.Printf("REF %s\n\n", spark(rThr, 0, 1))

	// sub-apex speeds at ref speed minima within the window
	fmt.Printf("=== SUB-APEX SPEED (you vs ref) ===\n")
	subApex := detectMinima(scale(rSpd, 3.6), grid, 4)
	for _, ai := range subApex {
		yg := analysis.Nearest(you.Dist, you.Gear, grid[ai])
		rg := analysis.Nearest(ref.Dist, ref.Gear, grid[ai])
		fmt.Printf("  %.0fm: you %.1f  ref %.1f  (%+.1f km/h)  gear you %.0f ref %.0f\n",
			grid[ai], ySpd[ai]*3.6, rSpd[ai]*3.6, (ySpd[ai]-rSpd[ai])*3.6, yg, rg)
	}

	// throttle modulation stats in the window
	ty := throttleZone(you, dtY, lo, hi)
	tr := throttleZone(ref, dtR, lo, hi)
	fmt.Printf("\n=== THROTTLE MODULATION ===\n")
	fmt.Printf("%-18s %-8s %-8s\n", "", "YOU", "REF")
	fmt.Printf("%-18s %6.0f%%  %6.0f%%   (pinned wide open)\n", "Full throttle", ty.fullFrac*100, tr.fullFrac*100)
	fmt.Printf("%-18s %6.0f%%  %6.0f%%   (mid-range balance)\n", "Fine (15-85%)", ty.fineFrac*100, tr.fineFrac*100)
	fmt.Printf("%-18s %6.0f%%  %6.0f%%   (fully off gas)\n", "Off throttle", ty.liftFrac*100, tr.liftFrac*100)
	fmt.Printf("%-18s %6d   %6d     (full lift-offs)\n", "Lift events", ty.lifts, tr.lifts)
	fmt.Printf("%-18s %6.1f   %6.1f     (higher=steppier)\n", "Throttle jerk", ty.jerk, tr.jerk)

	// steering lock (deg) over the window. Carrying more lock than the reference
	// where it's on the throttle means the car isn't rotated — a throttle gap is
	// then a symptom (unwind the lock sooner), not "just be flatter".
	const rad2deg = 180.0 / math.Pi
	yMeanLock, yPeakLock := absStats(iy(you, you.Steer), rad2deg)
	rMeanLock, rPeakLock := absStats(iy(ref, ref.Steer), rad2deg)
	fmt.Printf("\n=== STEERING LOCK (deg) ===\n")
	fmt.Printf("%-14s %6s %6s\n", "", "YOU", "REF")
	fmt.Printf("%-14s %6.1f %6.1f\n", "mean |lock|", yMeanLock, rMeanLock)
	fmt.Printf("%-14s %6.1f %6.1f\n", "peak |lock|", yPeakLock, rPeakLock)

	// GPS line deviation vs ref
	fmt.Printf("\n=== LINE DEVIATION vs reference (GPS, meters apart at same track pos) ===\n")
	var devs []float64
	maxDev, maxAt := 0.0, 0.0
	for _, x := range grid {
		yl := analysis.Interp(you.Dist, you.Lat, x)
		yo := analysis.Interp(you.Dist, you.Lon, x)
		rl := analysis.Interp(ref.Dist, ref.Lat, x)
		ro := analysis.Interp(ref.Dist, ref.Lon, x)
		dv := haversine(yl, yo, rl, ro)
		devs = append(devs, dv)
		if dv > maxDev {
			maxDev = dv
			maxAt = x
		}
	}
	fmt.Printf("  mean %.1fm off ref line, max %.1fm at %.0fm\n", mean(devs), maxDev, maxAt)
	fmt.Printf("  deviation trace: %s\n", spark(devs, 0, maxDev))

	// exit commitment: full-throttle resume after the last sub-apex in the window
	exitFrom := lo
	if len(subApex) > 0 {
		exitFrom = grid[subApex[len(subApex)-1]]
	}
	exitTo := hi + 400
	fmt.Printf("\n=== EXIT — full-throttle resume after %.0fm ===\n", exitFrom)
	fmt.Printf("  you: %.0fm   ref: %.0fm   (earlier=better)\n",
		fullThrottleResume(you, exitFrom, exitTo), fullThrottleResume(ref, exitFrom, exitTo))

	// consistency across your flying laps within the window
	fmt.Printf("\n=== YOUR CONSISTENCY through the section (all flying laps) ===\n")
	laps := analysis.FlyingLaps(hY)
	subDist := make([]float64, len(subApex))
	for i, ai := range subApex {
		subDist[i] = grid[ai]
	}
	perApex := make([][]float64, len(subDist))
	var resumes []float64
	latByPt := make([][]float64, N)
	lonByPt := make([][]float64, N)
	for _, li := range laps {
		d := analysis.ExtractLap(hY, li.Num)
		for i, sd := range subDist {
			mn := 1e9
			for _, x := range gridAround(sd, 40, 15) {
				v := analysis.Interp(d.Dist, d.Speed, x) * 3.6
				if v < mn {
					mn = v
				}
			}
			perApex[i] = append(perApex[i], mn)
		}
		resumes = append(resumes, fullThrottleResume(d, exitFrom, exitTo))
		for i, x := range grid {
			latByPt[i] = append(latByPt[i], analysis.Interp(d.Dist, d.Lat, x))
			lonByPt[i] = append(lonByPt[i], analysis.Interp(d.Dist, d.Lon, x))
		}
	}
	for i, sd := range subDist {
		lo2, hi2 := minmax(perApex[i])
		fmt.Printf("  apex %.0fm: %.1f ± %.1f km/h  (range %.1f-%.1f)\n", sd, mean(perApex[i]), std(perApex[i]), lo2, hi2)
	}
	fmt.Printf("  throttle-resume: %.0f ± %.0fm across laps\n", mean(resumes), std(resumes))
	var wander []float64
	maxW, maxWat := 0.0, 0.0
	for i := 0; i < N; i++ {
		mlat := mean(latByPt[i])
		mlon := mean(lonByPt[i])
		var d2 []float64
		for k := range latByPt[i] {
			d2 = append(d2, haversine(latByPt[i][k], lonByPt[i][k], mlat, mlon))
		}
		w := mean(d2)
		wander = append(wander, w)
		if w > maxW {
			maxW = w
			maxWat = grid[i]
		}
	}
	fmt.Printf("  line wander: mean %.1fm, worst %.1fm at %.0fm\n", mean(wander), maxW, maxWat)
	fmt.Printf("  wander trace: %s\n", spark(wander, 0, maxW))
}

// absStats returns the mean and peak of |v|*scale.
func absStats(v []float64, scaleF float64) (mean, peak float64) {
	if len(v) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, x := range v {
		a := math.Abs(x) * scaleF
		sum += a
		if a > peak {
			peak = a
		}
	}
	return sum / float64(len(v)), peak
}

func scale(v []float64, f float64) []float64 {
	out := make([]float64, len(v))
	for i := range v {
		out[i] = v[i] * f
	}
	return out
}

func detectMinima(v, grid []float64, win int) []int {
	var out []int
	for i := win; i < len(v)-win; i++ {
		isMin := true
		for k := i - win; k <= i+win; k++ {
			if v[k] < v[i] {
				isMin = false
				break
			}
		}
		if isMin {
			if len(out) == 0 || i-out[len(out)-1] > win {
				out = append(out, i)
			}
		}
	}
	return out
}

func fullThrottleResume(d analysis.Lap, from, to float64) float64 {
	// first distance where throttle exceeds 0.98 and holds >0.9 for the next ~40m
	for i := 0; i < len(d.Dist); i++ {
		if d.Dist[i] < from || d.Dist[i] > to {
			continue
		}
		if d.Thr[i] > 0.98 {
			ok := true
			for j := i; j < len(d.Dist) && d.Dist[j] < d.Dist[i]+40; j++ {
				if d.Thr[j] < 0.9 {
					ok = false
					break
				}
			}
			if ok {
				return d.Dist[i]
			}
		}
	}
	return to
}

func gridAround(center, span float64, n int) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = center - span + 2*span*float64(i)/float64(n-1)
	}
	return out
}
