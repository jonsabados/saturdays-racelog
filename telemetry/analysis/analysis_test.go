package analysis

import (
	"math"
	"testing"
)

func TestInterp(t *testing.T) {
	xs := []float64{0, 10, 20}
	ys := []float64{0, 100, 300}
	cases := []struct {
		x, want float64
	}{
		{-5, 0},   // clamp low
		{0, 0},    // exact start
		{5, 50},   // mid first segment
		{10, 100}, // knot
		{15, 200}, // mid second segment
		{20, 300}, // exact end
		{99, 300}, // clamp high
	}
	for _, c := range cases {
		got := Interp(xs, ys, c.x)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Interp(%.0f) = %.3f, want %.3f", c.x, got, c.want)
		}
	}
	if got := Interp(nil, nil, 5); got != 0 {
		t.Errorf("Interp(empty) = %.3f, want 0", got)
	}
}

func TestNearest(t *testing.T) {
	// discrete gear channel: must snap, never blend (no 1.3 between 1 and 2).
	xs := []float64{0, 10, 20}
	ys := []float64{1, 2, 3}
	cases := []struct {
		x, want float64
	}{
		{-5, 1}, // clamp low
		{2, 1},  // closer to xs[0]
		{4, 1},  // still closer to xs[0]
		{6, 2},  // closer to xs[1]
		{10, 2}, // exact
		{16, 3}, // closer to xs[2]
		{99, 3}, // clamp high
	}
	for _, c := range cases {
		if got := Nearest(xs, ys, c.x); got != c.want {
			t.Errorf("Nearest(%.0f) = %.1f, want %.1f (must not interpolate)", c.x, got, c.want)
		}
	}
}

func TestFilterFlying(t *testing.T) {
	// Mugello-like stint: one out-lap, several ~87s flyers, one messy lap, one pit lap.
	laps := []LapTime{
		{Num: 0, Dur: 109.8}, // out-lap: excluded (too slow)
		{Num: 1, Dur: 87.5},
		{Num: 2, Dur: 87.6},
		{Num: 3, Dur: 89.2}, // messy but within tolerance: kept
		{Num: 8, Dur: 86.7}, // best
		{Num: 20, Dur: 150}, // pit lap: excluded
	}
	out := filterFlying(laps, DefaultFlyingOpts())
	wantNums := map[int32]bool{1: true, 2: true, 3: true, 8: true}
	if len(out) != len(wantNums) {
		t.Fatalf("got %d flying laps, want %d (%v)", len(out), len(wantNums), out)
	}
	for _, l := range out {
		if !wantNums[l.Num] {
			t.Errorf("lap %d should not be flying (dur %.1f)", l.Num, l.Dur)
		}
	}
}

func TestFilterFlyingIgnoresPartialsWhenPickingBest(t *testing.T) {
	// A 5s partial must not be treated as the "best" lap and drag the window down.
	laps := []LapTime{{Num: 0, Dur: 5}, {Num: 1, Dur: 90}, {Num: 2, Dur: 91}}
	out := filterFlying(laps, DefaultFlyingOpts())
	if len(out) != 2 {
		t.Fatalf("got %d flying laps, want 2 (%v)", len(out), out)
	}
}

func TestFilterFlyingIgnoresLongPartialOutLap(t *testing.T) {
	// Regression: telemetry that starts mid-lap produces a partial far longer
	// than a fixed seconds floor. Mugello 2026-08-14 14-59-37 opened with a
	// 21.817s partial; it cleared the 20s floor, became the "best" lap, and the
	// +/-20% window then discarded every real ~87s lap in the session.
	laps := []LapTime{
		{Num: 0, Dur: 21.817}, // partial: telemetry started mid-lap
		{Num: 1, Dur: 92.950},
		{Num: 2, Dur: 88.400},
		{Num: 3, Dur: 87.317},
		{Num: 4, Dur: 86.900}, // best
		{Num: 5, Dur: 87.550},
		{Num: 6, Dur: 87.317},
		{Num: 7, Dur: 87.067},
	}
	out := filterFlying(laps, DefaultFlyingOpts())
	if len(out) != 7 {
		t.Fatalf("got %d flying laps, want 7 (%v)", len(out), out)
	}
	for _, l := range out {
		if l.Num == 0 {
			t.Errorf("partial out-lap (%.3fs) must not be a flying lap", l.Dur)
		}
	}
}

func TestFilterFlyingIgnoresVeryLongPartialOutLap(t *testing.T) {
	// Same defect, larger partial: Mugello 2026-08-14 14-11-26 opened with a
	// 34.217s partial ahead of a 19-lap time-trial stint.
	laps := []LapTime{{Num: 0, Dur: 34.217}}
	for i := 1; i <= 19; i++ {
		laps = append(laps, LapTime{Num: int32(i), Dur: 85.350 + float64(i%4)})
	}
	out := filterFlying(laps, DefaultFlyingOpts())
	if len(out) != 19 {
		t.Fatalf("got %d flying laps, want 19 (%v)", len(out), out)
	}
	for _, l := range out {
		if l.Num == 0 {
			t.Errorf("partial out-lap (%.3fs) must not be a flying lap", l.Dur)
		}
	}
}

func TestPartialFloorScalesWithLapLength(t *testing.T) {
	// The floor must track the car/track, not a constant: a 21.8s partial is
	// bogus at Mugello (~87s laps) but a plausible lap somewhere much shorter.
	mugello := []LapTime{{Dur: 21.817}, {Dur: 87.3}, {Dur: 86.9}, {Dur: 87.5}}
	if got := partialFloor(mugello, DefaultFlyingOpts()); got <= 21.817 {
		t.Errorf("Mugello floor = %.3f, want > 21.817 so the partial is rejected", got)
	}
	kart := []LapTime{{Dur: 21.817}, {Dur: 22.4}, {Dur: 22.1}, {Dur: 21.9}}
	if got := partialFloor(kart, DefaultFlyingOpts()); got > 21.817 {
		t.Errorf("short-track floor = %.3f, want <= 21.817 so real laps survive", got)
	}
}

type dip struct{ center, depth, sigma float64 }

// makeDips builds a uniform-grid speed trace (km/h) with Gaussian speed dips,
// standing in for corners on an otherwise flat-out straight.
func makeDips(n int, trackLen, base float64, dips []dip) (dist, spd []float64) {
	dist = make([]float64, n)
	spd = make([]float64, n)
	for i := 0; i < n; i++ {
		x := float64(i) / float64(n-1) * trackLen
		v := base
		for _, d := range dips {
			v -= d.depth * math.Exp(-((x-d.center)*(x-d.center))/(2*d.sigma*d.sigma))
		}
		dist[i] = x
		spd[i] = v
	}
	return dist, spd
}

func nearestCorner(cs []Corner, at float64) (Corner, float64) {
	best := Corner{}
	bestD := math.Inf(1)
	for _, c := range cs {
		if d := math.Abs(c.ApexDist - at); d < bestD {
			bestD = d
			best = c
		}
	}
	return best, bestD
}

func TestDetectCorners(t *testing.T) {
	// three real corners plus a shallow straight-line kink that must be ignored.
	dist, spd := makeDips(600, 6000, 285, []dip{
		{center: 1000, depth: 185, sigma: 60}, // ~100 km/h
		{center: 3000, depth: 125, sigma: 60}, // ~160 km/h
		{center: 4500, depth: 6, sigma: 50},   // shallow: reject
		{center: 5200, depth: 205, sigma: 55}, // ~80 km/h
	})
	cs := DetectCorners(dist, spd, DefaultCornerOpts())
	if len(cs) != 3 {
		t.Fatalf("detected %d corners, want 3: %+v", len(cs), cs)
	}
	for _, at := range []float64{1000, 3000, 5200} {
		if _, d := nearestCorner(cs, at); d > 60 {
			t.Errorf("no corner within 60m of %.0fm (nearest %.0fm off)", at, d)
		}
	}
	if _, d := nearestCorner(cs, 4500); d < 200 {
		t.Errorf("shallow kink at 4500m was detected as a corner (%.0fm away)", d)
	}
}

func TestDetectCornersProminenceThreshold(t *testing.T) {
	opt := CornerOpts{ProminenceKmh: 12, MinSpacingM: 150, MaxApexSpeedKmh: 300, SmoothWin: 4}

	_, shallow := makeDips(400, 4000, 240, []dip{{center: 2000, depth: 6, sigma: 60}})
	distS, _ := makeDips(400, 4000, 240, nil)
	if cs := DetectCorners(distS, shallow, opt); len(cs) != 0 {
		t.Errorf("6 km/h dip (below prominence) detected as %d corners: %+v", len(cs), cs)
	}

	dist, deep := makeDips(400, 4000, 240, []dip{{center: 2000, depth: 20, sigma: 60}})
	if cs := DetectCorners(dist, deep, opt); len(cs) != 1 {
		t.Errorf("20 km/h dip (above prominence) detected as %d corners, want 1", len(cs))
	}
}

func TestDetectCornersMergesBySpacing(t *testing.T) {
	// two minima 110m apart (< MinSpacing) must collapse to the slower one.
	opt := DefaultCornerOpts()
	opt.SmoothWin = 1 // keep the two sharp minima distinct
	dist, spd := makeDips(600, 6000, 285, []dip{
		{center: 2000, depth: 110, sigma: 25}, // ~175 km/h
		{center: 2110, depth: 150, sigma: 25}, // ~135 km/h (slower)
	})
	cs := DetectCorners(dist, spd, opt)
	if len(cs) != 1 {
		t.Fatalf("detected %d corners, want 1 (merged): %+v", len(cs), cs)
	}
	if cs[0].ApexDist < 2050 || cs[0].ApexDist > 2170 {
		t.Errorf("merged corner apex %.0fm, want the slower one near 2110m", cs[0].ApexDist)
	}
	if cs[0].ApexSpeedKmh > 160 {
		t.Errorf("merged corner kept the faster apex (%.1f km/h); want the slower ~135", cs[0].ApexSpeedKmh)
	}
}
