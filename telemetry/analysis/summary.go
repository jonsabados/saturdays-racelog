package analysis

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/jonsabados/saturdaysspinout/telemetry/ibt"
)

// CornerConsistency captures how repeatable a corner is across a session's
// flying laps: apex minimum-speed spread and braking-point spread.
type CornerConsistency struct {
	Name         string  `json:"name"`
	ApexDist     float64 `json:"apex_m"`
	MinSpeedMean float64 `json:"min_kmh_mean"`
	MinSpeedStd  float64 `json:"min_kmh_std"`
	MinSpeedLo   float64 `json:"min_kmh_lo"`
	MinSpeedHi   float64 `json:"min_kmh_hi"`
	// nil when there's no braking here or the onset fell outside the search
	// window — distinct from a genuine, repeatable 0.0m spread.
	BrakePointStd *float64 `json:"brake_pt_std_m,omitempty"`
}

// SessionSummary is the trendable digest of one telemetry session: identity,
// lap-time distribution over flying laps, and per-corner consistency. It is the
// unit the journal appends and the future ingestion feature would produce.
type SessionSummary struct {
	Track  string `json:"track"`
	Car    string `json:"car"`
	Driver string `json:"driver"`
	// Conditions — needed so trends don't line up non-comparable laps (an 8L
	// quali lap next to a 55L race lap). Kept as raw strings incl. units.
	SessionType string              `json:"session_type,omitempty"`
	Fuel        string              `json:"fuel,omitempty"`
	BrakeBias   string              `json:"brake_bias,omitempty"`
	TrackTempC  string              `json:"track_temp_c,omitempty"`
	AirTempC    string              `json:"air_temp_c,omitempty"`
	FlyingLaps  int                 `json:"flying_laps"`
	BestLapSec  float64             `json:"best_lap_s"`
	MeanLapSec  float64             `json:"mean_lap_s"`
	StdLapSec   float64             `json:"std_lap_s"`
	TrackLen    float64             `json:"track_len_m"`
	Corners     []CornerConsistency `json:"corners"`
}

// SummarizeSession computes a SessionSummary: flying laps, lap-time stats, and
// per-corner min-speed / braking-point consistency using auto-detected corners
// from the fastest flying lap.
func SummarizeSession(h *ibt.IBT, opt CornerOpts) SessionSummary {
	var s SessionSummary
	yaml := h.SessionYAML()
	s.Track = sessionField(yaml, "TrackName")
	s.Car = sessionField(yaml, "CarScreenName")
	s.Driver = sessionField(yaml, "UserName")
	s.SessionType = sessionField(yaml, "SessionType")
	s.Fuel = sessionField(yaml, "FuelLevel")
	s.BrakeBias = sessionField(yaml, "BrakePressureBias")
	s.TrackTempC = sessionField(yaml, "TrackSurfaceTemp")
	s.AirTempC = sessionField(yaml, "TrackAirTemp")

	laps := FlyingLaps(h)
	s.FlyingLaps = len(laps)
	if len(laps) == 0 {
		return s
	}
	best := laps[0]
	var durs []float64
	for _, l := range laps {
		durs = append(durs, l.Dur)
		if l.Dur < best.Dur {
			best = l
		}
	}
	s.BestLapSec = best.Dur
	s.MeanLapSec = mean(durs)
	s.StdLapSec = std(durs)

	refLap := ExtractLap(h, best.Num)
	s.TrackLen = refLap.Dist[len(refLap.Dist)-1]
	const N = 800
	grid := make([]float64, N)
	for i := range grid {
		grid[i] = float64(i) / float64(N-1) * s.TrackLen
	}
	rDist, rSpd := Resample(refLap, N)
	corners := DetectCorners(rDist, rSpd, opt)
	for i := range corners {
		if corners[i].Name == "" {
			corners[i].Name = fmt.Sprintf("C%d", i+1)
		}
	}

	// resample every flying lap onto the grid once
	type lapGrid struct{ spd, brk []float64 }
	var grids []lapGrid
	for _, li := range laps {
		d := ExtractLap(h, li.Num)
		if len(d.Dist) < 10 {
			continue
		}
		sp := make([]float64, N)
		br := make([]float64, N)
		for i, x := range grid {
			sp[i] = Interp(d.Dist, d.Speed, x) * 3.6
			br[i] = Interp(d.Dist, d.Brk, x)
		}
		grids = append(grids, lapGrid{sp, br})
	}

	win := int(60.0 / s.TrackLen * float64(N))
	brakeWin := int(160.0 / s.TrackLen * float64(N))
	for _, c := range corners {
		gi := int(c.ApexDist / s.TrackLen * float64(N-1))
		var mins, bpts []float64
		for _, g := range grids {
			mn := math.Inf(1)
			for k := gi - win; k <= gi+win; k++ {
				if k >= 0 && k < N && g.spd[k] < mn {
					mn = g.spd[k]
				}
			}
			mins = append(mins, mn)
			// only a valid braking point if braking happened and the onset was
			// found inside the window (didn't clamp at the edge) — else a false 0.0
			kk := gi
			for kk > gi-brakeWin && kk > 0 && g.brk[kk] < 0.08 {
				kk--
			}
			braked := kk > gi-brakeWin && kk > 0 && g.brk[kk] >= 0.08
			for kk > gi-brakeWin && kk > 0 && g.brk[kk] >= 0.08 {
				kk--
			}
			if braked && kk > gi-brakeWin && kk > 0 {
				bpts = append(bpts, grid[kk+1])
			}
		}
		lo, hi := minmax(mins)
		cc := CornerConsistency{
			Name:         c.Name,
			ApexDist:     c.ApexDist,
			MinSpeedMean: mean(mins),
			MinSpeedStd:  std(mins),
			MinSpeedLo:   lo,
			MinSpeedHi:   hi,
		}
		if len(bpts) >= 2 {
			v := std(bpts)
			cc.BrakePointStd = &v
		}
		s.Corners = append(s.Corners, cc)
	}
	return s
}

// TrackName returns the session's TrackName field from the embedded YAML.
func TrackName(h *ibt.IBT) string {
	return sessionField(h.SessionYAML(), "TrackName")
}

func sessionField(yaml, key string) string {
	if m := regexp.MustCompile(key + `: ?(.*)`).FindStringSubmatch(yaml); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
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
	if len(v) == 0 {
		return 0, 0
	}
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
