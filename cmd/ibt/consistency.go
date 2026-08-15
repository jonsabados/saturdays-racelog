package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/jonsabados/saturdaysspinout/telemetry/analysis"
	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}
func std(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := mean(v)
	s := 0.0
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}
func minmax(v []float64) (float64, float64) {
	lo, hi := v[0], v[0]
	for _, x := range v {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return lo, hi
}

// resample a lap onto a fixed grid; return speed(km/h), brake, elapsed-time arrays
func gridLap(h *ibt.IBT, lap int32, grid []float64) (spd, brk, tel []float64) {
	d := analysis.ExtractLap(h, lap)
	if len(d.Dist) < 10 {
		return nil, nil, nil
	}
	for _, x := range grid {
		spd = append(spd, analysis.Interp(d.Dist, d.Speed, x)*3.6)
		brk = append(brk, analysis.Interp(d.Dist, d.Brk, x))
		tel = append(tel, analysis.Interp(d.Dist, d.TElap, x))
	}
	return
}

func runConsistency(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: ibt consistency <file.ibt> [prominenceKmh] [spacingM] [maxApexKmh] [smoothWin] [gridN]")
		return
	}
	h, err := ibt.Open(args[0])
	if err != nil {
		panic(err)
	}
	dt := 1.0 / float64(h.TickRate)
	laps := analysis.FlyingLaps(h)
	if len(laps) == 0 {
		fmt.Println("no flying laps found")
		return
	}

	// reference lap for corner detection + track length = fastest flyer
	best := laps[0]
	for _, l := range laps {
		if l.Dur < best.Dur {
			best = l
		}
	}
	refLap := analysis.ExtractLap(h, best.Num)
	trackLen := refLap.Dist[len(refLap.Dist)-1]
	N := 800
	if len(args) >= 6 {
		if v, e := strconv.Atoi(args[5]); e == nil && v >= 100 {
			N = v
		}
	}
	grid := make([]float64, N)
	for i := range grid {
		grid[i] = float64(i) / float64(N-1) * trackLen
	}
	rDist, rSpd := analysis.Resample(refLap, N)
	// optional detection tuning: consistency <file> [prominenceKmh] [spacingM] [maxApexKmh]
	opt := analysis.DefaultCornerOpts()
	if len(args) >= 2 {
		if v, e := strconv.ParseFloat(args[1], 64); e == nil {
			opt.ProminenceKmh = v
		}
	}
	if len(args) >= 3 {
		if v, e := strconv.ParseFloat(args[2], 64); e == nil {
			opt.MinSpacingM = v
		}
	}
	if len(args) >= 4 {
		if v, e := strconv.ParseFloat(args[3], 64); e == nil {
			opt.MaxApexSpeedKmh = v
		}
	}
	if len(args) >= 5 {
		if v, e := strconv.Atoi(args[4]); e == nil {
			opt.SmoothWin = v
		}
	}
	corners := analysis.DetectCorners(rDist, rSpd, opt)
	for i := range corners {
		if corners[i].Name == "" {
			corners[i].Name = fmt.Sprintf("C%d", i+1)
		}
	}

	// collect per-lap series
	type lapGrid struct {
		num      int32
		dur      float64
		spd, brk []float64
		tel      []float64
	}
	var lgs []lapGrid
	var durs []float64
	for _, li := range laps {
		s, b, t := gridLap(h, li.Num, grid)
		if s == nil {
			continue
		}
		lgs = append(lgs, lapGrid{li.Num, li.Dur, s, b, t})
		durs = append(durs, li.Dur)
	}

	fmt.Printf("Flying laps analyzed: %d  (laptime %s ± %.3fs, range %s..%s)\n",
		len(lgs), fmtTime(mean(durs)), std(durs), fmtSecShort(minSlice(durs)), fmtSecShort(maxSlice(durs)))
	fmt.Printf("Detected %d corners on lap %d (%s), track length %.0fm\n",
		len(corners), best.Num, fmtSecShort(best.Dur), trackLen)

	// per-corner min speed & brake onset across laps
	fmt.Printf("\n=== PER-CORNER CONSISTENCY (min speed, km/h) ===\n")
	fmt.Printf("%-12s %7s %8s %7s %14s %10s\n", "corner", "apex(m)", "mean", "STDDEV", "range", "brakeSTD(m)")
	type row struct {
		name   string
		spdStd float64
		line   string
	}
	brakeWin := int(160.0 / trackLen * float64(N))
	win := int(60.0 / trackLen * float64(N)) // ±60m window
	var rows []row
	for _, c := range corners {
		gi := int(c.ApexDist / trackLen * float64(N-1))
		var mins, brakePts []float64
		for _, lg := range lgs {
			// min speed near apex
			mn := 1e9
			for k := gi - win; k <= gi+win; k++ {
				if k >= 0 && k < N && lg.spd[k] < mn {
					mn = lg.spd[k]
				}
			}
			mins = append(mins, mn)
			// brake onset: back from apex through off-brake, then through braking
			// zone. Only a valid point if braking actually happened AND the walk
			// found the off-brake edge inside the window (didn't clamp at the
			// boundary) — otherwise brakeSTD would read a false 0.0.
			kk := gi
			for kk > gi-brakeWin && kk > 0 && lg.brk[kk] < 0.08 {
				kk--
			}
			braked := kk > gi-brakeWin && kk > 0 && lg.brk[kk] >= 0.08
			for kk > gi-brakeWin && kk > 0 && lg.brk[kk] >= 0.08 {
				kk--
			}
			if braked && kk > gi-brakeWin && kk > 0 {
				brakePts = append(brakePts, grid[kk+1])
			}
		}
		lo, hi := minmax(mins)
		brakeStd := "   n/a" // no braking here, or onset outside the window
		if len(brakePts) >= 2 {
			brakeStd = fmt.Sprintf("%6.1f", std(brakePts))
		}
		rows = append(rows, row{c.Name, std(mins),
			fmt.Sprintf("%-12s %7.0f %8.1f %7.2f %6.1f-%-6.1f %10s",
				c.Name, c.ApexDist, mean(mins), std(mins), lo, hi, brakeStd)})
	}
	for _, r := range rows {
		fmt.Println(r.line)
	}
	// rank worst
	sort.Slice(rows, func(i, j int) bool { return rows[i].spdStd > rows[j].spdStd })
	fmt.Printf("\nLeast consistent corners (min-speed stddev): ")
	for i := 0; i < 3 && i < len(rows); i++ {
		fmt.Printf("%s(±%.1f) ", rows[i].name, rows[i].spdStd)
	}
	fmt.Println()

	// per-sector TIME variance (12 sectors) -> where lap-to-lap time swings most
	fmt.Printf("\n=== SECTOR TIME CONSISTENCY (12 sectors) ===\n")
	fmt.Printf("%-6s %-13s %-8s %-8s %s\n", "sector", "dist", "meanT", "STDDEV", "")
	segN := 12
	type seg struct {
		i         int
		lo, hi    float64
		std, mean float64
	}
	var segs []seg
	for s := 0; s < segN; s++ {
		i0 := s * N / segN
		i1 := (s+1)*N/segN - 1
		var times []float64
		for _, lg := range lgs {
			times = append(times, lg.tel[i1]-lg.tel[i0])
		}
		segs = append(segs, seg{s, grid[i0], grid[i1], std(times), mean(times)})
	}
	// find max std for bar scaling
	maxStd := 0.0
	for _, sg := range segs {
		if sg.std > maxStd {
			maxStd = sg.std
		}
	}
	for _, sg := range segs {
		bar := ""
		n := int(sg.std / maxStd * 30)
		for b := 0; b < n; b++ {
			bar += "#"
		}
		note := cornerIn(sg.lo, sg.hi, corners)
		fmt.Printf("%-6d %5.0f-%-5.0f %7.2fs %7.3fs %s %s\n", sg.i+1, sg.lo, sg.hi, sg.mean, sg.std, bar, note)
	}

	// brake saturation across laps at the slowest corner (heaviest braking =
	// where the mash-to-100% hardware tendency shows up)
	if len(corners) == 0 {
		fmt.Printf("\nno corners detected on the reference lap - skipping brake saturation\n")
		return
	}
	slow := corners[0]
	for _, c := range corners {
		if c.ApexSpeedKmh < slow.ApexSpeedKmh {
			slow = c
		}
	}
	zoneLo, zoneHi := slow.ApexDist-230, slow.ApexDist+120
	fmt.Printf("\n=== %s BRAKE SATURATION across every flying lap (slowest corner @ %.0fm) ===\n", slow.Name, slow.ApexDist)
	var sats, peaks []float64
	for _, li := range laps {
		d := analysis.ExtractLap(h, li.Num)
		bz := brakeZone(d, dt, zoneLo, zoneHi)
		sats = append(sats, bz.satFrac*100)
		peaks = append(peaks, bz.peakBrake)
		fmt.Printf("  lap %2d: peak %.2f  sat %4.0f%%  (%s)\n", li.Num, bz.peakBrake, bz.satFrac*100, fmtSecShort(li.Dur))
	}
	fmt.Printf("  --> peak %.2f±%.2f, saturation %.0f%%±%.0f%% of the braking zone\n",
		mean(peaks), std(peaks), mean(sats), std(sats))
	nMash := 0
	for _, p := range peaks {
		if p > 0.98 {
			nMash++
		}
	}
	fmt.Printf("  --> hit >=98%% brake in %d of %d laps\n", nMash, len(peaks))
}

func cornerIn(lo, hi float64, corners []analysis.Corner) string {
	for _, c := range corners {
		if c.ApexDist >= lo && c.ApexDist <= hi {
			return "<- " + c.Name
		}
	}
	return ""
}

func fmtSecShort(s float64) string {
	m := int(s) / 60
	return fmt.Sprintf("%d:%05.2f", m, s-float64(m*60))
}
func minSlice(v []float64) float64 { lo, _ := minmax(v); return lo }
func maxSlice(v []float64) float64 { _, hi := minmax(v); return hi }
