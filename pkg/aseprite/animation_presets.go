package aseprite

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
)

// Animation preset names accepted by BuildAnimationPlan.
const (
	PresetFlicker = "flicker"
	PresetPulse   = "pulse"
	PresetBob     = "bob"
	PresetSway    = "sway"
	PresetCycle   = "cycle"
)

// DefaultFlickerOpacities is the default per-frame cel opacity list for the flicker preset.
var DefaultFlickerOpacities = []int{255, 255, 140, 255, 60, 255}

// AnimationPresetParams holds the user-facing parameters of an animation preset.
//
// Every field is optional; zero values select the preset defaults.
type AnimationPresetParams struct {
	// Frames is the number of frames to animate (0 = preset default).
	Frames int
	// Seed drives the random flicker mode (0 = 1).
	Seed uint64

	// Opacities is an explicit per-frame opacity list for flicker (repeated to fill Frames).
	Opacities []int
	// Random switches flicker to seeded random opacities in [MinOpacity, MaxOpacity].
	Random     bool
	MinOpacity int
	MaxOpacity int

	// Multipliers is an explicit per-frame brightness curve for pulse (repeated to fill Frames).
	Multipliers []float64
	// Peak is the brightness multiplier reached mid-cycle by the default pulse curve (0 = 1.25).
	Peak float64

	// Offsets is an explicit per-frame cel offset list for bob/sway (repeated to fill Frames).
	Offsets []Point
	// Amplitude is the maximum pixel offset for the default bob/sway curves (0 = 2 for bob, 1 for sway).
	Amplitude int

	// Colors is the ordered colour list rotated by the cycle preset (at least 2 entries).
	Colors []Color
	// Step is how many palette slots the cycle advances per frame (0 = 1).
	Step int
}

// AnimationPlan is the fully resolved per-frame animation applied to a single layer.
//
// Exactly one of Opacities, Multipliers, Offsets or Cycle is set. Every per-frame
// slice has length Frames; index 0 is frame 1 (the rest frame).
type AnimationPlan struct {
	Preset      string
	Frames      int
	Opacities   []int
	Multipliers []float64
	Offsets     []Point
	Cycle       []Color
	CycleShifts []int
}

// BuildAnimationPlan resolves a preset and its parameters into a deterministic per-frame plan.
//
// The plan is computed in Go (not Lua) so it is reproducible and unit-testable:
//   - flicker: opacity list (default DefaultFlickerOpacities) or seeded random values in [min,max]
//   - pulse:   brightness multipliers, default 1 + (peak-1)*sin(pi*i/N) → 1.0 … peak … back towards 1.0
//   - bob:     dy = -round(A*sin(pi*i/N)), default A=2, N=4 → 0,-1,-2,-1
//   - sway:    dx = round(A*sin(2*pi*i/N)), default A=1, N=4 → 0,1,0,-1
//   - cycle:   shift = i*step mod len(colors), default N = len(colors)
//
// Frame 1 always gets the identity value in the default curves so it stays the rest pose.
func BuildAnimationPlan(preset string, p AnimationPresetParams) (*AnimationPlan, error) {
	if p.Frames < 0 {
		return nil, fmt.Errorf("frames must be >= 0, got %d", p.Frames)
	}
	if p.Frames > 256 {
		return nil, fmt.Errorf("frames must be <= 256, got %d", p.Frames)
	}

	plan := &AnimationPlan{Preset: strings.ToLower(preset)}

	switch plan.Preset {
	case PresetFlicker:
		n := p.Frames
		if p.Random {
			if n == 0 {
				n = 6
			}
			lo, hi := p.MinOpacity, p.MaxOpacity
			if hi == 0 {
				hi = 255
			}
			if lo < 0 || hi > 255 || lo > hi {
				return nil, fmt.Errorf("min_opacity/max_opacity must satisfy 0 <= min <= max <= 255, got %d/%d", lo, hi)
			}
			seed := p.Seed
			if seed == 0 {
				seed = 1
			}
			rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			plan.Opacities = make([]int, n)
			for i := range plan.Opacities {
				plan.Opacities[i] = lo + rng.IntN(hi-lo+1)
			}
		} else {
			src := p.Opacities
			if len(src) == 0 {
				src = DefaultFlickerOpacities
			}
			for _, o := range src {
				if o < 0 || o > 255 {
					return nil, fmt.Errorf("opacities must be between 0 and 255, got %d", o)
				}
			}
			if n == 0 {
				n = len(src)
			}
			plan.Opacities = repeatInts(src, n)
		}
		plan.Frames = n

	case PresetPulse:
		n := p.Frames
		if len(p.Multipliers) > 0 {
			for _, m := range p.Multipliers {
				if m < 0 || m > 16 {
					return nil, fmt.Errorf("multipliers must be between 0 and 16, got %g", m)
				}
			}
			if n == 0 {
				n = len(p.Multipliers)
			}
			plan.Multipliers = repeatFloats(p.Multipliers, n)
		} else {
			if n == 0 {
				n = 6
			}
			peak := p.Peak
			if peak == 0 {
				peak = 1.25
			}
			if peak < 0 || peak > 16 {
				return nil, fmt.Errorf("peak must be between 0 and 16, got %g", peak)
			}
			plan.Multipliers = make([]float64, n)
			for i := range plan.Multipliers {
				m := 1 + (peak-1)*math.Sin(math.Pi*float64(i)/float64(n))
				plan.Multipliers[i] = math.Round(m*10000) / 10000
			}
		}
		plan.Frames = n

	case PresetBob, PresetSway:
		n := p.Frames
		if len(p.Offsets) > 0 {
			if n == 0 {
				n = len(p.Offsets)
			}
			plan.Offsets = repeatPoints(p.Offsets, n)
		} else {
			if n == 0 {
				n = 4
			}
			amp := p.Amplitude
			if amp == 0 {
				if plan.Preset == PresetBob {
					amp = 2
				} else {
					amp = 1
				}
			}
			if amp < 0 || amp > 64 {
				return nil, fmt.Errorf("amplitude must be between 0 and 64, got %d", amp)
			}
			plan.Offsets = make([]Point, n)
			for i := range plan.Offsets {
				if plan.Preset == PresetBob {
					plan.Offsets[i] = Point{X: 0, Y: -roundInt(float64(amp) * math.Sin(math.Pi*float64(i)/float64(n)))}
				} else {
					plan.Offsets[i] = Point{X: roundInt(float64(amp) * math.Sin(2*math.Pi*float64(i)/float64(n))), Y: 0}
				}
			}
		}
		plan.Frames = n

	case PresetCycle:
		if len(p.Colors) < 2 {
			return nil, fmt.Errorf("cycle preset requires at least 2 colors, got %d", len(p.Colors))
		}
		step := p.Step
		if step == 0 {
			step = 1
		}
		n := p.Frames
		if n == 0 {
			n = len(p.Colors)
		}
		plan.Cycle = append([]Color(nil), p.Colors...)
		plan.CycleShifts = make([]int, n)
		k := len(p.Colors)
		for i := range plan.CycleShifts {
			plan.CycleShifts[i] = ((i*step)%k + k) % k
		}
		plan.Frames = n

	default:
		return nil, fmt.Errorf("unknown preset: %q (valid: flicker, pulse, bob, sway, cycle)", preset)
	}

	if plan.Frames < 1 {
		return nil, fmt.Errorf("animation must have at least 1 frame")
	}
	return plan, nil
}

func roundInt(f float64) int {
	return int(math.Round(f))
}

func repeatInts(src []int, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = src[i%len(src)]
	}
	return out
}

func repeatFloats(src []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = src[i%len(src)]
	}
	return out
}

func repeatPoints(src []Point, n int) []Point {
	out := make([]Point, n)
	for i := range out {
		out[i] = src[i%len(src)]
	}
	return out
}
