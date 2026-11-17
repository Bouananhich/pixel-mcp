package aseprite

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildAnimationPlan_Defaults(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		params AnimationPresetParams
		check  func(t *testing.T, p *AnimationPlan)
	}{
		{
			name:   "flicker default list",
			preset: "flicker",
			check: func(t *testing.T, p *AnimationPlan) {
				want := []int{255, 255, 140, 255, 60, 255}
				if p.Frames != 6 || !reflect.DeepEqual(p.Opacities, want) {
					t.Errorf("got frames=%d opacities=%v, want 6 %v", p.Frames, p.Opacities, want)
				}
			},
		},
		{
			name:   "flicker list repeated to frames",
			preset: "FLICKER",
			params: AnimationPresetParams{Frames: 5, Opacities: []int{255, 0}},
			check: func(t *testing.T, p *AnimationPlan) {
				want := []int{255, 0, 255, 0, 255}
				if !reflect.DeepEqual(p.Opacities, want) {
					t.Errorf("opacities = %v, want %v", p.Opacities, want)
				}
			},
		},
		{
			name:   "pulse default sine curve",
			preset: "pulse",
			check: func(t *testing.T, p *AnimationPlan) {
				want := []float64{1, 1.125, 1.2165, 1.25, 1.2165, 1.125}
				if !reflect.DeepEqual(p.Multipliers, want) {
					t.Errorf("multipliers = %v, want %v", p.Multipliers, want)
				}
			},
		},
		{
			name:   "bob default",
			preset: "bob",
			check: func(t *testing.T, p *AnimationPlan) {
				want := []Point{{0, 0}, {0, -1}, {0, -2}, {0, -1}}
				if !reflect.DeepEqual(p.Offsets, want) {
					t.Errorf("offsets = %v, want %v", p.Offsets, want)
				}
			},
		},
		{
			name:   "sway default",
			preset: "sway",
			check: func(t *testing.T, p *AnimationPlan) {
				want := []Point{{0, 0}, {1, 0}, {0, 0}, {-1, 0}}
				if !reflect.DeepEqual(p.Offsets, want) {
					t.Errorf("offsets = %v, want %v", p.Offsets, want)
				}
			},
		},
		{
			name:   "explicit offsets",
			preset: "bob",
			params: AnimationPresetParams{Offsets: []Point{{1, 2}, {3, 4}}, Frames: 3},
			check: func(t *testing.T, p *AnimationPlan) {
				want := []Point{{1, 2}, {3, 4}, {1, 2}}
				if !reflect.DeepEqual(p.Offsets, want) {
					t.Errorf("offsets = %v, want %v", p.Offsets, want)
				}
			},
		},
		{
			name:   "cycle shifts",
			preset: "cycle",
			params: AnimationPresetParams{Colors: []Color{{R: 1}, {G: 1}, {B: 1}}},
			check: func(t *testing.T, p *AnimationPlan) {
				if p.Frames != 3 || !reflect.DeepEqual(p.CycleShifts, []int{0, 1, 2}) {
					t.Errorf("frames=%d shifts=%v", p.Frames, p.CycleShifts)
				}
			},
		},
		{
			name:   "cycle step 2 over 4 frames",
			preset: "cycle",
			params: AnimationPresetParams{Colors: []Color{{R: 1}, {G: 1}, {B: 1}}, Step: 2, Frames: 4},
			check: func(t *testing.T, p *AnimationPlan) {
				if !reflect.DeepEqual(p.CycleShifts, []int{0, 2, 1, 0}) {
					t.Errorf("shifts=%v", p.CycleShifts)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := BuildAnimationPlan(tt.preset, tt.params)
			if err != nil {
				t.Fatalf("BuildAnimationPlan error = %v", err)
			}
			tt.check(t, p)
		})
	}
}

func TestBuildAnimationPlan_RandomFlickerIsSeeded(t *testing.T) {
	params := AnimationPresetParams{Random: true, Seed: 42, MinOpacity: 50, MaxOpacity: 200, Frames: 8}
	a, err := BuildAnimationPlan("flicker", params)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := BuildAnimationPlan("flicker", params)
	if !reflect.DeepEqual(a.Opacities, b.Opacities) {
		t.Errorf("same seed gave different results: %v vs %v", a.Opacities, b.Opacities)
	}
	for _, o := range a.Opacities {
		if o < 50 || o > 200 {
			t.Errorf("opacity %d out of [50,200]", o)
		}
	}
	params.Seed = 43
	c, _ := BuildAnimationPlan("flicker", params)
	if reflect.DeepEqual(a.Opacities, c.Opacities) {
		t.Errorf("different seeds gave identical results: %v", a.Opacities)
	}
}

func TestBuildAnimationPlan_Errors(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		params AnimationPresetParams
		errMsg string
	}{
		{"unknown preset", "wobble", AnimationPresetParams{}, "unknown preset"},
		{"negative frames", "bob", AnimationPresetParams{Frames: -1}, "frames must be >= 0"},
		{"too many frames", "bob", AnimationPresetParams{Frames: 999}, "frames must be <= 256"},
		{"bad opacity", "flicker", AnimationPresetParams{Opacities: []int{300}}, "opacities must be between"},
		{"bad random range", "flicker", AnimationPresetParams{Random: true, MinOpacity: 200, MaxOpacity: 100}, "min_opacity/max_opacity"},
		{"bad multiplier", "pulse", AnimationPresetParams{Multipliers: []float64{-1}}, "multipliers must be between"},
		{"cycle needs colors", "cycle", AnimationPresetParams{Colors: []Color{{R: 1}}}, "at least 2 colors"},
		{"bad amplitude", "sway", AnimationPresetParams{Amplitude: 100}, "amplitude must be between"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildAnimationPlan(tt.preset, tt.params)
			if err == nil || !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error = %v, want containing %q", err, tt.errMsg)
			}
		})
	}
}
