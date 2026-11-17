package aseprite

import (
	"context"
	"strings"
	"testing"

	"github.com/willibrandon/pixel-mcp/internal/testutil"
)

const injection = `x") os.execute("touch /tmp/pwned") --`

func assertContainsAll(t *testing.T, script string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(script, p) {
			t.Errorf("script missing %q", p)
		}
	}
}

func TestLuaGenerator_SetLayerProperties(t *testing.T) {
	gen := NewLuaGenerator()
	visible := false
	opacity := 128
	newName := "glow"
	script := gen.SetLayerProperties("neon", LayerProperties{Visible: &visible, Opacity: &opacity, NewName: &newName, BlendMode: "addition"})
	assertContainsAll(t, script,
		`findLayer(spr.layers, "neon")`,
		"layer.isVisible = false",
		"layer.opacity = 128",
		"layer.blendMode = BlendMode.ADDITION",
		`layer.name = "glow"`,
		"app.transaction(",
		"spr:saveAs(spr.filename)",
		"json.encode(",
	)

	// Only requested properties are emitted
	script = gen.SetLayerProperties("neon", LayerProperties{BlendMode: "screen"})
	if strings.Contains(script, "layer.opacity =") || strings.Contains(script, "layer.isVisible =") || strings.Contains(script, "layer.name =") {
		t.Error("script changes properties that were not requested")
	}
	assertContainsAll(t, script, "BlendMode.SCREEN")
}

func TestLuaGenerator_SetLayerProperties_Escaping(t *testing.T) {
	gen := NewLuaGenerator()
	name := injection
	script := gen.SetLayerProperties(name, LayerProperties{NewName: &name})
	if strings.Contains(script, `"x") os.execute(`) {
		t.Error("layer name not escaped")
	}
	assertContainsAll(t, script, `x\") os.execute(\"touch /tmp/pwned\") --`)
}

func TestLuaGenerator_AnimateLayer(t *testing.T) {
	gen := NewLuaGenerator()

	flicker, _ := BuildAnimationPlan(PresetFlicker, AnimationPresetParams{})
	script := gen.AnimateLayer("neon", flicker, 80, "flick")
	assertContainsAll(t, script,
		`findLayer(spr.layers, "neon")`,
		"local N = 6",
		"opacities = {255, 255, 140, 255, 60, 255}",
		"local durationSec = 0.080",
		`local tagName = "flick"`,
		"spr:newEmptyFrame(",
		"spr:newCel(layer, i, img, pos)",
		"app.transaction(",
		"spr:saveAs(spr.filename)",
	)

	bob, _ := BuildAnimationPlan(PresetBob, AnimationPresetParams{})
	script = gen.AnimateLayer("prop", bob, 0, "")
	assertContainsAll(t, script, "offsets = {{0, 0}, {0, -1}, {0, -2}, {0, -1}}", "local durationSec = nil", "local tagName = nil")
	if strings.Contains(script, "opacities = {") {
		t.Error("bob plan should not set opacities")
	}

	pulse, _ := BuildAnimationPlan(PresetPulse, AnimationPresetParams{Multipliers: []float64{1, 1.5}})
	assertContainsAll(t, gen.AnimateLayer("p", pulse, 0, ""), "multipliers = {1.0000, 1.5000}")

	cycle, _ := BuildAnimationPlan(PresetCycle, AnimationPresetParams{Colors: []Color{{R: 255}, {G: 255}}})
	assertContainsAll(t, gen.AnimateLayer("c", cycle, 0, ""), "cycleColors = {{255, 0, 0}, {0, 255, 0}}", "cycleShifts = {0, 1}")

	// Escaping of layer and tag names
	script = gen.AnimateLayer(injection, bob, 0, injection)
	if strings.Contains(script, `"x") os.execute(`) {
		t.Error("names not escaped")
	}
}

func TestLuaGenerator_ExportLayers(t *testing.T) {
	gen := NewLuaGenerator()
	sheets := []LayerSheet{
		{Name: "neon", Layers: []string{"neon"}, PNGPath: "/out/props_neon.png", JSONPath: "/out/props_neon.json"},
		{Name: "a{b}", Layers: []string{"a{b}"}, PNGPath: "/out/props_a_b.png"},
	}
	script := gen.ExportLayers("/art/scene.aseprite", sheets, "rows", 2, true)
	assertContainsAll(t, script,
		`local srcPath = "/art/scene.aseprite"`,
		"local trim = true",
		`png = "/out/props_neon.png", json = "/out/props_neon.json", layers = {"neon"}`,
		`name = "a(b)"`, // braces are not passed to filenameFormat
		`json = nil, layers = {"a{b}"}`,
		"SpriteSheetType.ROWS",
		"SpriteSheetDataFormat.JSON_HASH",
		"listTags = true",
		"borderPadding = 2",
		"spr:crop(bounds)",
		"spr:close()",
	)
	if strings.Contains(script, "saveAs") || strings.Contains(script, ":save") {
		t.Error("export_layers must never save the source sprite")
	}

	script = gen.ExportLayers(injection, []LayerSheet{{Name: injection, Layers: []string{injection}, PNGPath: injection}}, "bogus", 0, false)
	if strings.Contains(script, `"x") os.execute(`) {
		t.Error("strings not escaped")
	}
	assertContainsAll(t, script, "SpriteSheetType.HORIZONTAL", "local trim = false")
}

func TestLuaGenerator_ComposeFromManifest(t *testing.T) {
	gen := NewLuaGenerator()
	script := gen.ComposeFromManifest("/m/base.png", []ManifestLayer{
		{Name: "neon", File: "/m/neon.png", X: 3, Y: 4, W: 10, FlipX: true, Opacity: 200},
	}, "/m/out.aseprite", "/m/out.png")
	assertContainsAll(t, script,
		`local basePath = "/m/base.png"`,
		`local outPath = "/m/out.aseprite"`,
		`local pngPath = "/m/out.png"`,
		`{ name = "neon", file = "/m/neon.png", x = 3, y = 4, w = 10, h = 0, flipX = true, opacity = 200 }`,
		`baseLayer.name = "base"`,
		"spr:saveCopyAs(pngPath)",
	)
	script = gen.ComposeFromManifest("/m/base.png", nil, "/m/out.aseprite", "")
	assertContainsAll(t, script, "local pngPath = nil")

	script = gen.ComposeFromManifest(injection, []ManifestLayer{{Name: injection, File: injection}}, injection, injection)
	if strings.Contains(script, `"x") os.execute(`) {
		t.Error("strings not escaped")
	}
}

// TestLuaGenerator_DrawWithDither_UnknownPatternEscaped is a regression test: the unknown
// pattern used to be interpolated raw into a Lua string, allowing script injection.
func TestLuaGenerator_DrawWithDither_UnknownPatternEscaped(t *testing.T) {
	gen := NewLuaGenerator()
	pattern := `nope") print("INJECTED") error("`
	script := gen.DrawWithDither("Layer 1", 1, 0, 0, 4, 4, "#FF0000", "#00FF00", pattern, 0.5)
	if strings.Contains(script, `nope") print(`) {
		t.Fatalf("pattern was not escaped: %s", script)
	}

	cfg := testutil.LoadTestConfig(t)
	client := NewClient(cfg.AsepritePath, cfg.TempDir, cfg.Timeout)
	out, err := client.ExecuteLua(context.Background(), script, "")
	if err == nil {
		t.Fatal("expected the script to raise an error")
	}
	if strings.Contains(out, "INJECTED") || strings.Contains(err.Error(), "\nINJECTED") {
		t.Errorf("injected code ran: out=%q err=%v", out, err)
	}
	if !strings.Contains(err.Error(), "Unknown dithering pattern") {
		t.Errorf("expected 'Unknown dithering pattern' error, got %v", err)
	}
}
