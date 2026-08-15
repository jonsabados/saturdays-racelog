// Package analysis turns decoded .ibt telemetry (telemetry/ibt) into
// track-agnostic driving metrics: per-lap channel extraction, resampling onto a
// distance grid, a data-driven flying-lap filter, and speed-minima corner
// detection.
//
// Everything here is pure (data in, data out, no printing) so it can back both
// the local coaching CLI and any future server-side analysis.
package analysis

import (
	"math"
	"sort"

	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

// Lap holds one lap's channels at native sample rate, with distance monotonic
// from the start/finish line.
type Lap struct {
	Dist  []float64 // meters from S/F (monotonic)
	TElap []float64 // elapsed seconds from lap start
	Speed []float64 // m/s
	Thr   []float64
	Brk   []float64
	Steer []float64 // SteeringWheelAngle, radians (×180/π for degrees)
	Gear  []float64 // discrete — sample with Nearest, never Interp (interpolation yields 1.3 mid-shift)
	RPM   []float64
	LatA  []float64
	Lat   []float64
	Lon   []float64
}

// ExtractLap pulls a single lap's channels from a decoded file, keeping distance
// monotonic (dropping rare backward GPS jitter).
func ExtractLap(h *ibt.IBT, lap int32) Lap {
	L := h.Vars["Lap"]
	st := h.Vars["SessionTime"]
	ld := h.Vars["LapDist"]
	sp := h.Vars["Speed"]
	th := h.Vars["Throttle"]
	br := h.Vars["Brake"]
	sw := h.Vars["SteeringWheelAngle"]
	gr := h.Vars["Gear"]
	rp := h.Vars["RPM"]
	la := h.Vars["LatAccel"]
	latV := h.Vars["Lat"]
	lonV := h.Vars["Lon"]
	var d Lap
	t0 := 0.0
	started := false
	prevDist := -1.0
	for i := 0; i < h.NumSamples; i++ {
		if h.Int(L, i) != lap {
			continue
		}
		if !started {
			t0 = h.Float(st, i)
			started = true
		}
		dist := h.Float(ld, i)
		if dist < prevDist-1 {
			continue
		}
		prevDist = dist
		d.Dist = append(d.Dist, dist)
		d.TElap = append(d.TElap, h.Float(st, i)-t0)
		d.Speed = append(d.Speed, h.Float(sp, i))
		d.Thr = append(d.Thr, h.Float(th, i))
		d.Brk = append(d.Brk, h.Float(br, i))
		d.Steer = append(d.Steer, h.Float(sw, i))
		d.Gear = append(d.Gear, h.Float(gr, i))
		d.RPM = append(d.RPM, h.Float(rp, i))
		d.LatA = append(d.LatA, h.Float(la, i))
		d.Lat = append(d.Lat, h.Float(latV, i))
		d.Lon = append(d.Lon, h.Float(lonV, i))
	}
	return d
}

// Interp linearly interpolates y at x given monotonic-increasing xs.
func Interp(xs, ys []float64, x float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	if x <= xs[0] {
		return ys[0]
	}
	if x >= xs[len(xs)-1] {
		return ys[len(ys)-1]
	}
	lo, hi := 0, len(xs)-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if xs[mid] <= x {
			lo = mid
		} else {
			hi = mid
		}
	}
	f := (x - xs[lo]) / (xs[hi] - xs[lo])
	return ys[lo] + f*(ys[hi]-ys[lo])
}

// Nearest samples ys at the xs closest to x — for discrete channels (gear)
// that must not be linearly interpolated. xs must be monotonic-increasing.
func Nearest(xs, ys []float64, x float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	if x <= xs[0] {
		return ys[0]
	}
	if x >= xs[len(xs)-1] {
		return ys[len(ys)-1]
	}
	lo, hi := 0, len(xs)-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if xs[mid] <= x {
			lo = mid
		} else {
			hi = mid
		}
	}
	if x-xs[lo] <= xs[hi]-x {
		return ys[lo]
	}
	return ys[hi]
}

// Smooth returns a centered moving average with half-window w.
func Smooth(y []float64, w int) []float64 {
	out := make([]float64, len(y))
	for i := range y {
		s, n := 0.0, 0
		for k := -w; k <= w; k++ {
			j := i + k
			if j >= 0 && j < len(y) {
				s += y[j]
				n++
			}
		}
		out[i] = s / float64(n)
	}
	return out
}

// LapTime is one completed lap and its duration in seconds.
type LapTime struct {
	Num int32
	Dur float64
}

// LapTimes returns every completed lap (start/finish to start/finish) with its
// duration, derived from Lap-channel transitions. The final incomplete lap has
// no closing transition and is not included.
func LapTimes(h *ibt.IBT) []LapTime {
	lapV := h.Vars["Lap"]
	stV := h.Vars["SessionTime"]
	var out []LapTime
	cur := h.Int(lapV, 0)
	start := h.Float(stV, 0)
	for i := 1; i < h.NumSamples; i++ {
		l := h.Int(lapV, i)
		if l != cur {
			t := h.Float(stV, i)
			out = append(out, LapTime{cur, t - start})
			cur = l
			start = t
		}
	}
	return out
}

// FlyingOpts parameterizes the data-driven flying-lap filter.
type FlyingOpts struct {
	MinFrac    float64 // lower bound as a fraction of the best clean lap
	MaxFrac    float64 // upper bound as a fraction of the best clean lap
	FloorSec   float64 // absolute minimum plausible lap time (excludes partials/pit)
	MedianFrac float64 // minimum plausible lap time as a fraction of the median lap
}

// DefaultFlyingOpts keeps laps within [0.95x, 1.20x] of the best clean lap,
// which adapts to any car/track (Suzuka ~1:37 and Mugello ~1:27 alike) while
// excluding out/in and obviously broken laps.
func DefaultFlyingOpts() FlyingOpts {
	return FlyingOpts{MinFrac: 0.95, MaxFrac: 1.20, FloorSec: 20, MedianFrac: 0.5}
}

// FlyingLaps returns representative racing laps using DefaultFlyingOpts.
func FlyingLaps(h *ibt.IBT) []LapTime {
	return filterFlying(LapTimes(h), DefaultFlyingOpts())
}

// partialFloor is the shortest duration filterFlying will accept as a complete
// lap. A fixed floor cannot do this alone: telemetry that starts mid-lap yields
// a partial "lap" of arbitrary length, and one longer than the constant floor
// would otherwise become the best lap and collapse the window onto itself. The
// median lap is a robust anchor because real laps cluster tightly while
// partials and pit laps sit far out on either side. Degenerate stints (mostly
// partials, only a lap or two complete) can still pull the median down; those
// surface as an empty or tiny result rather than a plausible-looking wrong one.
func partialFloor(laps []LapTime, opt FlyingOpts) float64 {
	floor := opt.FloorSec
	durs := make([]float64, len(laps))
	for i, l := range laps {
		durs[i] = l.Dur
	}
	sort.Float64s(durs)
	if n := len(durs); n > 0 {
		med := durs[n/2]
		if n%2 == 0 {
			med = (durs[n/2-1] + durs[n/2]) / 2
		}
		if byMedian := med * opt.MedianFrac; byMedian > floor {
			floor = byMedian
		}
	}
	return floor
}

// filterFlying selects flying laps relative to the best lap above the floor.
func filterFlying(laps []LapTime, opt FlyingOpts) []LapTime {
	floor := partialFloor(laps, opt)
	best := math.Inf(1)
	for _, l := range laps {
		if l.Dur >= floor && l.Dur < best {
			best = l.Dur
		}
	}
	if math.IsInf(best, 1) {
		return nil
	}
	var out []LapTime
	for _, l := range laps {
		if l.Dur >= best*opt.MinFrac && l.Dur <= best*opt.MaxFrac {
			out = append(out, l)
		}
	}
	return out
}
