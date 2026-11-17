package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willibrandon/mtlog"
	"github.com/willibrandon/mtlog/core"
	"github.com/willibrandon/pixel-mcp/internal/testutil"
	"github.com/willibrandon/pixel-mcp/pkg/aseprite"
)

func TestRegisterPipelineTools(t *testing.T) {
	server, client, gen := createTestServer(t)
	cfg := testutil.LoadTestConfig(t)
	logger := mtlog.New(mtlog.WithMinimumLevel(core.ErrorLevel))

	RegisterPipelineTools(server, client, gen, cfg, logger)

	assert.NotNil(t, server)
}

func TestCheckOutputPath(t *testing.T) {
	base := t.TempDir()
	allowed := t.TempDir()
	other := t.TempDir()

	tests := []struct {
		name    string
		path    string
		allowed []string
		wantErr string
	}{
		{"inside base", filepath.Join(base, "out.png"), nil, ""},
		{"nested inside base", filepath.Join(base, "sheets", "a", "out.png"), nil, ""},
		{"relative rejected", "out.png", nil, "must be absolute"},
		{"empty rejected", "", nil, "cannot be empty"},
		{"dot-dot escape rejected", filepath.Join(base, "..", filepath.Base(other), "out.png"), nil, "outside"},
		{"other dir rejected", filepath.Join(other, "out.png"), nil, "allowed_dirs"},
		{"other dir allowed", filepath.Join(allowed, "x", "out.png"), []string{allowed}, ""},
		{"sibling with common prefix rejected", base + "-evil/out.png", nil, "outside"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkOutputPath(tt.path, base, tt.allowed)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheckOutputPath_Symlink(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(base, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := checkOutputPath(filepath.Join(link, "out.png"), base, nil)
	assert.Error(t, err, "a symlink pointing outside the base dir must be rejected")
}

func TestValidateSetLayerProperties(t *testing.T) {
	op := func(v int) *int { return &v }
	str := func(s string) *string { return &s }
	tests := []struct {
		name    string
		in      SetLayerPropertiesInput
		wantErr string
	}{
		{"valid blend", SetLayerPropertiesInput{SpritePath: "/a.aseprite", LayerName: "l", BlendMode: "Addition"}, ""},
		{"valid opacity", SetLayerPropertiesInput{SpritePath: "/a.aseprite", LayerName: "l", Opacity: op(0)}, ""},
		{"missing sprite", SetLayerPropertiesInput{LayerName: "l", Opacity: op(1)}, "sprite_path"},
		{"missing layer", SetLayerPropertiesInput{SpritePath: "/a", Opacity: op(1)}, "layer_name"},
		{"nothing to change", SetLayerPropertiesInput{SpritePath: "/a", LayerName: "l"}, "nothing to change"},
		{"opacity range", SetLayerPropertiesInput{SpritePath: "/a", LayerName: "l", Opacity: op(256)}, "opacity must be"},
		{"empty new name", SetLayerPropertiesInput{SpritePath: "/a", LayerName: "l", NewName: str(" ")}, "new_name"},
		{"bad blend", SetLayerPropertiesInput{SpritePath: "/a", LayerName: "l", BlendMode: "glow"}, "invalid blend_mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSetLayerProperties(tt.in)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildPlanFromInput(t *testing.T) {
	plan, err := buildPlanFromInput(AnimateLayerInput{SpritePath: "/a", LayerName: "l", Preset: "cycle", Colors: []string{"#FF0000", "#00FF00", "0000FF"}})
	require.NoError(t, err)
	assert.Equal(t, 3, plan.Frames)
	assert.Equal(t, []int{0, 1, 2}, plan.CycleShifts)

	lo, hi := 10, 20
	plan, err = buildPlanFromInput(AnimateLayerInput{SpritePath: "/a", LayerName: "l", Preset: "flicker", Random: true, MinOpacity: &lo, MaxOpacity: &hi, Seed: 7, Frames: 4})
	require.NoError(t, err)
	for _, o := range plan.Opacities {
		assert.True(t, o >= 10 && o <= 20, "opacity %d out of range", o)
	}

	_, err = buildPlanFromInput(AnimateLayerInput{SpritePath: "/a", LayerName: "l", Preset: "cycle", Colors: []string{"red", "#00FF00"}})
	assert.ErrorContains(t, err, "invalid color")
	_, err = buildPlanFromInput(AnimateLayerInput{SpritePath: "/a", LayerName: "l", Preset: "bob", FrameDurationMs: 70000})
	assert.ErrorContains(t, err, "frame_duration_ms")
	_, err = buildPlanFromInput(AnimateLayerInput{LayerName: "l", Preset: "bob"})
	assert.ErrorContains(t, err, "sprite_path")
	_, err = buildPlanFromInput(AnimateLayerInput{SpritePath: "/a", Preset: "bob"})
	assert.ErrorContains(t, err, "layer_name")
	_, err = buildPlanFromInput(AnimateLayerInput{SpritePath: "/a", LayerName: "l", Preset: "spin"})
	assert.ErrorContains(t, err, "unknown preset")
}

func TestPlanLayerSheets(t *testing.T) {
	sheets, err := planLayerSheets([]string{"Neon Sign", "crowd/2"}, "/out/props.png", true, true)
	require.NoError(t, err)
	require.Len(t, sheets, 2)
	assert.Equal(t, "/out/props_Neon_Sign.png", sheets[0].PNGPath)
	assert.Equal(t, "/out/props_Neon_Sign.json", sheets[0].JSONPath)
	assert.Equal(t, []string{"Neon Sign"}, sheets[0].Layers)
	assert.Equal(t, "Neon Sign", sheets[0].Name)
	assert.Equal(t, "/out/props_crowd_2.png", sheets[1].PNGPath)

	sheets, err = planLayerSheets([]string{"a", "b"}, "/out/props.png", false, false)
	require.NoError(t, err)
	require.Len(t, sheets, 1)
	assert.Equal(t, "/out/props.png", sheets[0].PNGPath)
	assert.Equal(t, "", sheets[0].JSONPath)
	assert.Equal(t, "props", sheets[0].Name)
	assert.Equal(t, []string{"a", "b"}, sheets[0].Layers)

	_, err = planLayerSheets(nil, "/out/p.png", true, false)
	assert.ErrorContains(t, err, "layers cannot be empty")
	_, err = planLayerSheets([]string{"a", "a"}, "/out/p.png", true, false)
	assert.ErrorContains(t, err, "duplicate")
	_, err = planLayerSheets([]string{"a b", "a_b"}, "/out/p.png", true, false)
	assert.ErrorContains(t, err, "same file name")
	_, err = planLayerSheets([]string{"a"}, "/out/p.gif", false, false)
	assert.ErrorContains(t, err, ".png")
}

func TestAddOffsetToSheetJSON_PreservesFrameOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	src := `{ "frames": { "n 10": {"duration": 1}, "n 2": {"duration": 2}, "n 1": {"duration": 3} }, "meta": { "frameTags": [] } }`
	require.NoError(t, os.WriteFile(path, []byte(src), 0644))

	require.NoError(t, addOffsetToSheetJSON(path, ExportedSheet{X: 5, Y: 6, W: 7, H: 8}, 100, 200))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(data)
	i10, i2, i1 := strings.Index(s, `"n 10"`), strings.Index(s, `"n 2"`), strings.Index(s, `"n 1"`)
	assert.True(t, i10 < i2 && i2 < i1, "frame order changed: %s", s)

	var doc struct {
		Meta struct {
			Offset     map[string]int  `json:"offset"`
			SpriteSize map[string]int  `json:"spriteSize"`
			FrameTags  json.RawMessage `json:"frameTags"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	assert.Equal(t, map[string]int{"x": 5, "y": 6, "w": 7, "h": 8}, doc.Meta.Offset)
	assert.Equal(t, map[string]int{"w": 100, "h": 200}, doc.Meta.SpriteSize)
	assert.NotNil(t, doc.Meta.FrameTags)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base.png"), "x")
	writeFile(t, filepath.Join(dir, "props", "neon.png"), "x")
	abs := filepath.Join(t.TempDir(), "abs.png")
	writeFile(t, abs, "x")

	manifest := filepath.Join(dir, "scene.json")
	writeFile(t, manifest, `{"base":"base.png","out":"out/scene.aseprite","png":"out/scene.png",
		"layers":[{"name":"neon","file":"props/neon.png","x":3,"y":4,"w":8,"flipX":true,"opacity":200},
		          {"name":"abs","file":"`+abs+`","x":0,"y":0}]}`)

	m, err := loadManifest(manifest, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "base.png"), m.Base)
	assert.Equal(t, filepath.Join(dir, "out", "scene.aseprite"), m.Out)
	assert.Equal(t, filepath.Join(dir, "out", "scene.png"), m.PNG)
	want := []aseprite.ManifestLayer{
		{Name: "neon", File: filepath.Join(dir, "props", "neon.png"), X: 3, Y: 4, W: 8, FlipX: true, Opacity: 200},
		{Name: "abs", File: abs, Opacity: 255},
	}
	if !reflect.DeepEqual(m.Layers, want) {
		t.Errorf("layers = %+v, want %+v", m.Layers, want)
	}

	bad := []struct {
		name, json, errMsg string
	}{
		{"missing base", `{"out":"o.aseprite","layers":[]}`, "base is required"},
		{"base not found", `{"base":"nope.png","out":"o.aseprite"}`, "base not found"},
		{"bad out ext", `{"base":"base.png","out":"o.png"}`, ".aseprite"},
		{"out escapes", `{"base":"base.png","out":"../o.aseprite"}`, "outside"},
		{"png escapes", `{"base":"base.png","out":"o.aseprite","png":"/etc/o.png"}`, "outside"},
		{"dup name", `{"base":"base.png","out":"o.aseprite","layers":[{"name":"a","file":"base.png"},{"name":"a","file":"base.png"}]}`, "duplicate"},
		{"reserved base", `{"base":"base.png","out":"o.aseprite","layers":[{"name":"base","file":"base.png"}]}`, "duplicate"},
		{"missing file", `{"base":"base.png","out":"o.aseprite","layers":[{"name":"a","file":"x.png"}]}`, "not found"},
		{"bad opacity", `{"base":"base.png","out":"o.aseprite","layers":[{"name":"a","file":"base.png","opacity":300}]}`, "opacity"},
		{"unknown field", `{"base":"base.png","out":"o.aseprite","layer":[]}`, "invalid manifest"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, "bad.json")
			writeFile(t, p, tt.json)
			_, err := loadManifest(p, nil)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tt.errMsg)
			}
		})
	}

	_, err = loadManifest("scene.json", nil)
	assert.ErrorContains(t, err, "absolute")
}

func TestValidateRunLua(t *testing.T) {
	allowed := t.TempDir()
	assert.ErrorContains(t, validateRunLua(RunLuaInput{Script: "print(1)"}, nil), "disabled")
	assert.NoError(t, validateRunLua(RunLuaInput{Script: "print(1)"}, []string{allowed}))
	assert.NoError(t, validateRunLua(RunLuaInput{Script: "print(1)", SpritePath: filepath.Join(allowed, "a.aseprite")}, []string{allowed}))
	assert.ErrorContains(t, validateRunLua(RunLuaInput{Script: "print(1)", SpritePath: "/elsewhere/a.aseprite"}, []string{allowed}), "not inside")
	assert.ErrorContains(t, validateRunLua(RunLuaInput{Script: "print(1)", SpritePath: "a.aseprite"}, []string{allowed}), "absolute")
	assert.ErrorContains(t, validateRunLua(RunLuaInput{Script: "  "}, []string{allowed}), "script cannot be empty")
}
