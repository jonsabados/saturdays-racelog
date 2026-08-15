package analysis

import (
	"math"
	"sort"
)

// Resample returns a uniform distance grid over the lap and the speed (km/h) at
// each grid point, interpolated from native samples.
func Resample(lap Lap, n int) (dist, speedKmh []float64) {
	dist = make([]float64, n)
	speedKmh = make([]float64, n)
	if len(lap.Dist) == 0 {
		return dist, speedKmh
	}
	trackLen := lap.Dist[len(lap.Dist)-1]
	for i := 0; i < n; i++ {
		x := float64(i) / float64(n-1) * trackLen
		dist[i] = x
		speedKmh[i] = Interp(lap.Dist, lap.Speed, x) * 3.6
	}
	return dist, speedKmh
}

// Corner is a detected corner: the apex distance from S/F, the minimum speed
// there, and an optional human name (populated from a per-track overlay).
type Corner struct {
	ApexDist     float64
	ApexSpeedKmh float64
	Name         string
}

// CornerOpts parameterizes speed-minima corner detection.
type CornerOpts struct {
	ProminenceKmh   float64 // required speed rise on both sides of a minimum (rejects wiggles)
	MinSpacingM     float64 // minima closer than this collapse to the slower one
	MaxApexSpeedKmh float64 // ignore minima faster than this (flat-out kinks)
	SmoothWin       int     // half-window for smoothing the speed trace
}

// DefaultCornerOpts is tuned for open-wheel/GT speed traces: a corner must shed
// at least 12 km/h relative to the straights bracketing it, corners within 150 m
// merge, and anything above 255 km/h is treated as a straight-line kink.
func DefaultCornerOpts() CornerOpts {
	return CornerOpts{ProminenceKmh: 12, MinSpacingM: 150, MaxApexSpeedKmh: 255, SmoothWin: 8}
}

// DetectCorners finds corner apexes as prominent local minima on a resampled,
// smoothed speed trace. dist and speedKmh must be the uniform grid from Resample
// (equal length). Prominence is the topographic key-col measure: from a minimum,
// walk outward until the trace drops below it (entering another basin); the
// highest point reached on each side, minus the minimum, bounds the prominence.
func DetectCorners(dist, speedKmh []float64, opt CornerOpts) []Corner {
	n := len(speedKmh)
	if n < 5 || len(dist) != n {
		return nil
	}
	sm := Smooth(speedKmh, opt.SmoothWin)

	const wp = 3 // local-minimum flatness window (grid points)
	type cand struct {
		i     int
		speed float64
	}
	var cands []cand
	for i := wp; i < n-wp; i++ {
		if sm[i] > opt.MaxApexSpeedKmh {
			continue
		}
		isMin := true
		for k := i - wp; k <= i+wp; k++ {
			if sm[k] < sm[i] {
				isMin = false
				break
			}
		}
		if !isMin {
			continue
		}
		leftMax := sm[i]
		for k := i - 1; k >= 0; k-- {
			if sm[k] < sm[i]-0.1 {
				break // dropped into a deeper basin: this is the col
			}
			if sm[k] > leftMax {
				leftMax = sm[k]
			}
		}
		rightMax := sm[i]
		for k := i + 1; k < n; k++ {
			if sm[k] < sm[i]-0.1 {
				break
			}
			if sm[k] > rightMax {
				rightMax = sm[k]
			}
		}
		prom := math.Min(leftMax-sm[i], rightMax-sm[i])
		if prom >= opt.ProminenceKmh {
			cands = append(cands, cand{i, sm[i]})
		}
	}

	// Collapse minima closer than MinSpacingM, keeping the slower apex.
	sort.Slice(cands, func(a, b int) bool { return cands[a].i < cands[b].i })
	var kept []cand
	for _, c := range cands {
		if len(kept) > 0 && dist[c.i]-dist[kept[len(kept)-1].i] < opt.MinSpacingM {
			if c.speed < kept[len(kept)-1].speed {
				kept[len(kept)-1] = c
			}
			continue
		}
		kept = append(kept, c)
	}

	corners := make([]Corner, len(kept))
	for i, c := range kept {
		corners[i] = Corner{ApexDist: dist[c.i], ApexSpeedKmh: c.speed}
	}
	return corners
}
