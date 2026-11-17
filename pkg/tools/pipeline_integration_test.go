//go:build integration
// +build integration

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willibrandon/mtlog"
	"github.com/willibrandon/mtlog/core"
	"github.com/willibrandon/pixel-mcp/internal/testutil"
	"github.com/willibrandon/pixel-mcp/pkg/aseprite"
)

// Integration tests for the pipeline tools with real Aseprite.
// Run with: go test -tags=integration -run Pipeline -v ./pkg/tools

type pipelineEnv struct {
	session *mcp.ClientSession
	client  *aseprite.Client
}

func newPipelineEnv(t *testing.T, allowedDirs []string) *pipelineEnv {
	t.Helper()
	cfg := testutil.LoadTestConfig(t)
	cfg.AllowedDirs = allowedDirs
	client := aseprite.NewClient(cfg.AsepritePath, cfg.TempDir, cfg.Timeout)
	gen := aseprite.NewLuaGenerator()
	logger := mtlog.New(mtlog.WithMinimumLevel(core.ErrorLevel))

	server := mcp.NewServer(&mcp.Implementation{Name: "pixel-mcp-test", Version: "1.0.0"}, nil)
	RegisterPipelineTools(server, client, gen, cfg, logger)

	st, ct := mcp.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return &pipelineEnv{session: session, client: client}
}

// call invokes a tool and returns (text, isError).
func (e *pipelineEnv) call(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// mustCall invokes a tool, fails on tool error and decodes the structured result into out.
func (e *pipelineEnv) mustCall(t *testing.T, name string, args map[string]any, out any) {
	t.Helper()
	text, isErr := e.call(t, name, args)
	require.False(t, isErr, "%s failed: %s", name, text)
	if out != nil {
		require.NoError(t, json.Unmarshal([]byte(text), out), text)
	}
}

func writeTestPNG(t *testing.T, path string, w, h int, px func(x, y int) color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, px(x, y))
		}
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, img))
}

func solid(c color.NRGBA) func(int, int) color.NRGBA {
	return func(int, int) color.NRGBA { return c }
}

func readTestPNG(t *testing.T, path string) *image.NRGBA {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	img, err := png.Decode(f)
	require.NoError(t, err)
	out := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

var (
	cBlue  = color.NRGBA{0, 0, 255, 255}
	cRed   = color.NRGBA{200, 100, 50, 255}
	cGrey  = color.NRGBA{100, 100, 100, 255}
	cGreen = color.NRGBA{0, 200, 0, 255}
	cDark  = color.NRGBA{50, 50, 50, 255}
)

// composeScene builds a 16x16 scene: base (blue) + neon/bulb/crate/tree 2x2 props + strip (3 colours).
func composeScene(t *testing.T, e *pipelineEnv, dir string) string {
	t.Helper()
	writeTestPNG(t, filepath.Join(dir, "src", "base.png"), 16, 16, solid(cBlue))
	writeTestPNG(t, filepath.Join(dir, "src", "neon.png"), 2, 2, solid(cRed))
	writeTestPNG(t, filepath.Join(dir, "src", "bulb.png"), 2, 2, solid(cGrey))
	writeTestPNG(t, filepath.Join(dir, "src", "crate.png"), 2, 2, solid(cGreen))
	writeTestPNG(t, filepath.Join(dir, "src", "tree.png"), 2, 2, solid(cDark))
	writeTestPNG(t, filepath.Join(dir, "src", "strip.png"), 3, 1, func(x, _ int) color.NRGBA {
		return []color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}[x]
	})
	manifest := map[string]any{
		"base": "src/base.png",
		"out":  "scene.aseprite",
		"png":  "scene.png",
		"layers": []map[string]any{
			{"name": "neon", "file": "src/neon.png", "x": 3, "y": 3},
			{"name": "bulb", "file": "src/bulb.png", "x": 8, "y": 3},
			{"name": "crate", "file": "src/crate.png", "x": 3, "y": 10},
			{"name": "tree", "file": "src/tree.png", "x": 10, "y": 10},
			{"name": "strip", "file": "src/strip.png", "x": 1, "y": 14},
		},
	}
	data, _ := json.Marshal(manifest)
	mPath := filepath.Join(dir, "scene.json")
	require.NoError(t, os.WriteFile(mPath, data, 0644))

	var out ComposeFromManifestOutput
	e.mustCall(t, "compose_from_manifest", map[string]any{"manifest_path": mPath}, &out)
	require.Equal(t, 6, out.Layers)
	return out.Out
}

type inspCel struct {
	X, Y, Opacity, W, H int
	Px                  []int `json:"px"`
}

type inspLayer struct {
	Name    string
	Visible bool
	Opacity int
	Blend   int
	Cels    map[string]inspCel // frame number -> cel
}

type inspSprite struct {
	Frames []int // durations in ms
	Tags   []struct {
		Name     string
		From, To int
	}
	Layers []inspLayer
}

// inspect reads layers/cels/frames/tags of a sprite through a Lua query script.
func (e *pipelineEnv) inspect(t *testing.T, spritePath string) inspSprite {
	t.Helper()
	script := `local spr = app.activeSprite
local pc = app.pixelColor
local res = { frames = {}, tags = {}, layers = {} }
for _, f in ipairs(spr.frames) do table.insert(res.frames, math.floor(f.duration * 1000 + 0.5)) end
for _, tg in ipairs(spr.tags) do table.insert(res.tags, { name = tg.name, from = tg.fromFrame.frameNumber, to = tg.toFrame.frameNumber }) end
for _, l in ipairs(spr.layers) do
	local cels = {}
	for _, f in ipairs(spr.frames) do
		local c = l:cel(f)
		if c then
			local v = c.image:getPixel(0, 0)
			cels[tostring(f.frameNumber)] = { x = c.position.x, y = c.position.y, opacity = c.opacity,
				w = c.image.width, h = c.image.height, px = { pc.rgbaR(v), pc.rgbaG(v), pc.rgbaB(v), pc.rgbaA(v) } }
		end
	end
	table.insert(res.layers, { name = l.name, visible = l.isVisible, opacity = l.opacity or 255, blend = l.blendMode, cels = cels })
end
print(json.encode(res))`
	out, err := e.client.ExecuteLua(context.Background(), script, spritePath)
	require.NoError(t, err)
	var s inspSprite
	require.NoError(t, json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &s), out)
	return s
}

func (s inspSprite) layer(t *testing.T, name string) inspLayer {
	t.Helper()
	for _, l := range s.Layers {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("layer %s not found", name)
	return inspLayer{}
}

func (l inspLayer) cel(t *testing.T, frame int) inspCel {
	t.Helper()
	c, ok := l.Cels[fmt.Sprint(frame)]
	if !ok {
		t.Fatalf("layer %s has no cel in frame %d", l.Name, frame)
	}
	return c
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return sha256.Sum256(data)
}

// ---------------------------------------------------------------------------

func TestIntegration_Pipeline_ComposeFromManifest(t *testing.T) {
	e := newPipelineEnv(t, nil)
	dir := t.TempDir()
	sprite := composeScene(t, e, dir)

	s := e.inspect(t, sprite)
	names := []string{}
	for _, l := range s.Layers {
		names = append(names, l.Name)
	}
	assert.Equal(t, []string{"base", "neon", "bulb", "crate", "tree", "strip"}, names)
	assert.Equal(t, 3, s.layer(t, "neon").cel(t, 1).X)
	assert.Equal(t, 3, s.layer(t, "neon").cel(t, 1).Y)

	flat := readTestPNG(t, filepath.Join(dir, "scene.png"))
	assert.Equal(t, cBlue, flat.NRGBAAt(0, 0))
	assert.Equal(t, cRed, flat.NRGBAAt(4, 4))
	assert.Equal(t, cGreen, flat.NRGBAAt(3, 10))

	// Resize (nearest, aspect kept), flipX and opacity
	writeTestPNG(t, filepath.Join(dir, "rg.png"), 2, 1, func(x, _ int) color.NRGBA {
		return []color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}}[x]
	})
	m2 := filepath.Join(dir, "resize.json")
	require.NoError(t, os.WriteFile(m2, []byte(`{"base":"src/base.png","out":"resize.aseprite","png":"resize.png",
		"layers":[{"name":"rg","file":"rg.png","x":0,"y":0,"w":4,"flipX":true},
		          {"name":"ghost","file":"rg.png","x":0,"y":8,"h":2,"opacity":0}]}`), 0644))
	var out ComposeFromManifestOutput
	e.mustCall(t, "compose_from_manifest", map[string]any{"manifest_path": m2}, &out)
	img := readTestPNG(t, out.PNG)
	green, red := color.NRGBA{0, 255, 0, 255}, color.NRGBA{255, 0, 0, 255}
	for y := 0; y < 2; y++ {
		assert.Equal(t, []color.NRGBA{green, green, red, red},
			[]color.NRGBA{img.NRGBAAt(0, y), img.NRGBAAt(1, y), img.NRGBAAt(2, y), img.NRGBAAt(3, y)}, "row %d", y)
	}
	assert.Equal(t, cBlue, img.NRGBAAt(0, 2), "resized layer must be 4x2")
	assert.Equal(t, cBlue, img.NRGBAAt(0, 8), "opacity 0 layer must be invisible")
	ghost := e.inspect(t, out.Out).layer(t, "ghost")
	assert.Equal(t, 0, ghost.Opacity)
	assert.Equal(t, 4, ghost.cel(t, 1).W, "h=2 on a 2x1 image keeps aspect -> 4x2")

	// Path safety: out outside the manifest folder
	m3 := filepath.Join(dir, "evil.json")
	require.NoError(t, os.WriteFile(m3, []byte(`{"base":"src/base.png","out":"../evil.aseprite"}`), 0644))
	text, isErr := e.call(t, "compose_from_manifest", map[string]any{"manifest_path": m3})
	assert.True(t, isErr)
	assert.Contains(t, text, "outside")
}

func TestIntegration_Pipeline_SetLayerProperties(t *testing.T) {
	e := newPipelineEnv(t, nil)
	sprite := composeScene(t, e, t.TempDir())

	var out SetLayerPropertiesOutput
	e.mustCall(t, "set_layer_properties", map[string]any{
		"sprite_path": sprite, "layer_name": "neon",
		"visible": false, "opacity": 128, "blend_mode": "addition", "new_name": "neon glow",
	}, &out)
	assert.Equal(t, SetLayerPropertiesOutput{Name: "neon glow", Visible: false, Opacity: 128, BlendMode: "addition"}, out)

	l := e.inspect(t, sprite).layer(t, "neon glow")
	assert.False(t, l.Visible)
	assert.Equal(t, 128, l.Opacity)

	for _, mode := range []string{"screen", "multiply", "normal"} {
		e.mustCall(t, "set_layer_properties", map[string]any{"sprite_path": sprite, "layer_name": "neon glow", "blend_mode": mode}, &out)
		assert.Equal(t, mode, out.BlendMode)
	}
	e.mustCall(t, "set_layer_properties", map[string]any{"sprite_path": sprite, "layer_name": "neon glow", "visible": true}, &out)
	assert.True(t, out.Visible)

	text, isErr := e.call(t, "set_layer_properties", map[string]any{"sprite_path": sprite, "layer_name": "bulb", "new_name": "crate"})
	assert.True(t, isErr)
	assert.Contains(t, text, "already exists")

	text, isErr = e.call(t, "set_layer_properties", map[string]any{"sprite_path": sprite, "layer_name": "missing", "opacity": 1})
	assert.True(t, isErr)
	assert.Contains(t, text, "Layer not found")

	// Names with quotes/backslashes survive escaping
	weird := `say "hi" \ there`
	e.mustCall(t, "set_layer_properties", map[string]any{"sprite_path": sprite, "layer_name": "tree", "new_name": weird}, &out)
	assert.Equal(t, weird, out.Name)
}

func TestIntegration_Pipeline_AnimateLayer_Flicker(t *testing.T) {
	e := newPipelineEnv(t, nil)
	sprite := composeScene(t, e, t.TempDir())

	var out AnimateLayerOutput
	e.mustCall(t, "animate_layer", map[string]any{
		"sprite_path": sprite, "layer_name": "neon", "preset": "flicker",
		"frame_duration_ms": 80, "tag_name": "neon_flicker",
	}, &out)
	assert.Equal(t, 6, out.FramesAnimated)
	assert.Equal(t, 6, out.TotalFrames)
	assert.Equal(t, []int{255, 255, 140, 255, 60, 255}, out.Opacities)

	s := e.inspect(t, sprite)
	assert.Equal(t, []int{80, 80, 80, 80, 80, 80}, s.Frames)
	require.Len(t, s.Tags, 1)
	assert.Equal(t, "neon_flicker", s.Tags[0].Name)
	assert.Equal(t, 1, s.Tags[0].From)
	assert.Equal(t, 6, s.Tags[0].To)

	neon := s.layer(t, "neon")
	for i, want := range []int{255, 255, 140, 255, 60, 255} {
		c := neon.cel(t, i+1)
		assert.Equal(t, want, c.Opacity, "frame %d opacity", i+1)
		assert.Equal(t, []int{200, 100, 50, 255}, c.Px, "frame %d pixel", i+1)
		assert.Equal(t, 3, c.X)
	}
	// Other layers keep their content on the new frames, untouched
	for _, name := range []string{"base", "bulb", "crate"} {
		l := s.layer(t, name)
		first := l.cel(t, 1)
		for f := 2; f <= 6; f++ {
			c := l.cel(t, f)
			assert.Equal(t, first, c, "%s frame %d", name, f)
		}
	}

	// Seeded random mode is deterministic; re-running replaces the tag (no duplicates)
	var r1, r2 AnimateLayerOutput
	args := map[string]any{"sprite_path": sprite, "layer_name": "neon", "preset": "flicker", "random": true,
		"min_opacity": 40, "max_opacity": 220, "seed": 99, "tag_name": "neon_flicker"}
	e.mustCall(t, "animate_layer", args, &r1)
	e.mustCall(t, "animate_layer", args, &r2)
	assert.Equal(t, r1.Opacities, r2.Opacities)
	s = e.inspect(t, sprite)
	assert.Len(t, s.Tags, 1)
	for i, want := range r1.Opacities {
		assert.Equal(t, want, s.layer(t, "neon").cel(t, i+1).Opacity)
	}
}

func TestIntegration_Pipeline_AnimateLayer_PulseBobSwayCycle(t *testing.T) {
	e := newPipelineEnv(t, nil)
	sprite := composeScene(t, e, t.TempDir())

	// pulse: grey 100 * [1, 1.125, 1.2165, 1.25, 1.2165, 1.125]
	var out AnimateLayerOutput
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "bulb", "preset": "pulse"}, &out)
	assert.Equal(t, []float64{1, 1.125, 1.2165, 1.25, 1.2165, 1.125}, out.Multipliers)
	s := e.inspect(t, sprite)
	for i, want := range []int{100, 113, 122, 125, 122, 113} {
		assert.Equal(t, []int{want, want, want, 255}, s.layer(t, "bulb").cel(t, i+1).Px, "pulse frame %d", i+1)
	}

	// bob on 4 frames (sprite already has 6): dy 0,-1,-2,-1 from rest (3,10); frames 5-6 untouched
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "crate", "preset": "bob", "tag_name": "bob"}, &out)
	assert.Equal(t, 6, out.TotalFrames)
	s = e.inspect(t, sprite)
	for i, dy := range []int{0, -1, -2, -1} {
		c := s.layer(t, "crate").cel(t, i+1)
		assert.Equal(t, [2]int{3, 10 + dy}, [2]int{c.X, c.Y}, "bob frame %d", i+1)
	}
	assert.Equal(t, 10, s.layer(t, "crate").cel(t, 5).Y)

	// sway: dx 0,1,0,-1 from rest (10,10)
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "tree", "preset": "sway"}, &out)
	s = e.inspect(t, sprite)
	for i, dx := range []int{0, 1, 0, -1} {
		assert.Equal(t, 10+dx, s.layer(t, "tree").cel(t, i+1).X, "sway frame %d", i+1)
	}

	// pulse + bob stack on the same layer (image vs position)
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "bulb", "preset": "bob", "amplitude": 2}, &out)
	s = e.inspect(t, sprite)
	c := s.layer(t, "bulb").cel(t, 3)
	assert.Equal(t, []int{122, 122, 122, 255}, c.Px, "pulse kept after bob")
	assert.Equal(t, 1, c.Y, "bob applied after pulse")

	// cycle: strip pixel 0 is red; with colors [red, green, blue] it becomes green then blue
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "strip", "preset": "cycle",
		"colors": []string{"#FF0000", "#00FF00", "#0000FF"}}, &out)
	s = e.inspect(t, sprite)
	assert.Equal(t, []int{255, 0, 0, 255}, s.layer(t, "strip").cel(t, 1).Px)
	assert.Equal(t, []int{0, 255, 0, 255}, s.layer(t, "strip").cel(t, 2).Px)
	assert.Equal(t, []int{0, 0, 255, 255}, s.layer(t, "strip").cel(t, 3).Px)

	text, isErr := e.call(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "nope", "preset": "bob"})
	assert.True(t, isErr)
	assert.Contains(t, text, "Layer not found")
}

func TestIntegration_Pipeline_AnimateLayer_LinkedCels(t *testing.T) {
	e := newPipelineEnv(t, nil)
	dir := t.TempDir()
	sprite := filepath.Join(dir, "linked.aseprite")
	// 3 frames, both layers continuous (linked cels across frames)
	script := fmt.Sprintf(`local spr = Sprite(8, 8, ColorMode.RGB)
local a = spr.layers[1]; a.name = "a"; a.isContinuous = true
local b = spr:newLayer(); b.name = "b"; b.isContinuous = true
local img = Image(2, 2, ColorMode.RGB); img:clear(app.pixelColor.rgba(10, 20, 30, 255))
spr:newCel(a, 1, img, Point(1, 1))
spr:newCel(b, 1, img, Point(4, 4))
spr:newFrame(1); spr:newFrame(1)
spr:saveAs("%s")`, aseprite.EscapeString(sprite))
	_, err := e.client.ExecuteLua(context.Background(), script, "")
	require.NoError(t, err)

	var out AnimateLayerOutput
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "a", "preset": "flicker",
		"opacities": []int{255, 100, 50, 0}}, &out)
	assert.Equal(t, 4, out.TotalFrames)

	s := e.inspect(t, sprite)
	for i, want := range []int{255, 100, 50, 0} {
		assert.Equal(t, want, s.layer(t, "a").cel(t, i+1).Opacity, "a frame %d", i+1)
	}
	for f := 1; f <= 4; f++ {
		c := s.layer(t, "b").cel(t, f)
		assert.Equal(t, 255, c.Opacity, "b frame %d must be untouched", f)
		assert.Equal(t, [2]int{4, 4}, [2]int{c.X, c.Y})
		assert.Equal(t, []int{10, 20, 30, 255}, c.Px)
	}
}

func TestIntegration_Pipeline_ExportLayers(t *testing.T) {
	e := newPipelineEnv(t, nil)
	dir := t.TempDir()
	sprite := composeScene(t, e, dir)
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "neon", "preset": "flicker",
		"frame_duration_ms": 90, "tag_name": "flicker"}, nil)
	e.mustCall(t, "animate_layer", map[string]any{"sprite_path": sprite, "layer_name": "crate", "preset": "bob"}, nil)
	// hide a layer in the source to check it's still exportable when requested and hidden otherwise
	e.mustCall(t, "set_layer_properties", map[string]any{"sprite_path": sprite, "layer_name": "tree", "visible": false}, nil)
	before := fileHash(t, sprite)

	outPNG := filepath.Join(dir, "sheets", "props.png")
	var out ExportLayersOutput
	e.mustCall(t, "export_layers", map[string]any{"sprite_path": sprite, "layers": []string{"neon", "crate", "tree"},
		"output_path": outPNG, "split": true, "trim": true, "include_json": true}, &out)
	require.Len(t, out.Sheets, 3)
	assert.Equal(t, before, fileHash(t, sprite), "source sprite must not be modified")

	byName := map[string]ExportedSheet{}
	for _, s := range out.Sheets {
		byName[s.Name] = s
	}
	neon := byName["neon"]
	assert.Equal(t, filepath.Join(dir, "sheets", "props_neon.png"), neon.PNG)
	assert.Equal(t, [4]int{3, 3, 2, 2}, [4]int{neon.X, neon.Y, neon.W, neon.H})
	assert.Equal(t, 6, neon.Frames)
	crate := byName["crate"]
	assert.Equal(t, [4]int{3, 8, 2, 4}, [4]int{crate.X, crate.Y, crate.W, crate.H}, "bob union bounds dy 0..-2")
	tree := byName["tree"]
	assert.Equal(t, [4]int{10, 10, 2, 2}, [4]int{tree.X, tree.Y, tree.W, tree.H}, "hidden but requested layer is exported")

	// Neon sheet: 6 frames of 2x2 horizontally, only neon pixels (no blue base)
	img := readTestPNG(t, neon.PNG)
	assert.Equal(t, image.Rect(0, 0, 12, 2), img.Bounds())
	for f := 0; f < 6; f++ {
		px := img.NRGBAAt(f*2, 0)
		assert.Equal(t, [3]uint8{200, 100, 50}, [3]uint8{px.R, px.G, px.B}, "frame %d", f)
	}
	assert.Equal(t, uint8(140), img.NRGBAAt(4, 0).A, "flicker frame 3 opacity baked into sheet")

	// Crate sheet frame 3 (dy=-2): crate at top rows, bottom rows empty
	cimg := readTestPNG(t, crate.PNG)
	assert.Equal(t, image.Rect(0, 0, 12, 4), cimg.Bounds())
	assert.Equal(t, uint8(0), cimg.NRGBAAt(0, 0).A, "frame 1 (dy 0): top rows empty")
	assert.Equal(t, cGreen, cimg.NRGBAAt(0, 2), "frame 1 (dy 0): crate at rows 2-3")
	assert.Equal(t, cGreen, cimg.NRGBAAt(4, 0), "frame 3 (dy -2): crate at rows 0-1")
	assert.Equal(t, uint8(0), cimg.NRGBAAt(4, 2).A, "frame 3 (dy -2): bottom rows empty")

	// JSON: json-hash with durations, tags, offset
	var doc struct {
		Frames map[string]struct {
			Frame    map[string]int `json:"frame"`
			Duration int            `json:"duration"`
		} `json:"frames"`
		Meta struct {
			FrameTags []struct {
				Name     string `json:"name"`
				From, To int
			} `json:"frameTags"`
			Layers []struct {
				Name string `json:"name"`
			} `json:"layers"`
			Offset     map[string]int `json:"offset"`
			SpriteSize map[string]int `json:"spriteSize"`
		} `json:"meta"`
	}
	data, err := os.ReadFile(neon.JSON)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &doc), string(data))
	assert.Len(t, doc.Frames, 6)
	assert.Equal(t, 90, doc.Frames["neon 0"].Duration)
	require.Len(t, doc.Meta.FrameTags, 1)
	assert.Equal(t, "flicker", doc.Meta.FrameTags[0].Name)
	assert.Equal(t, 5, doc.Meta.FrameTags[0].To)
	require.Len(t, doc.Meta.Layers, 1)
	assert.Equal(t, "neon", doc.Meta.Layers[0].Name)
	assert.Equal(t, map[string]int{"x": 3, "y": 3, "w": 2, "h": 2}, doc.Meta.Offset)
	assert.Equal(t, map[string]int{"w": 16, "h": 16}, doc.Meta.SpriteSize)

	// Combined, untrimmed sheet of two layers
	combined := filepath.Join(dir, "combined.png")
	e.mustCall(t, "export_layers", map[string]any{"sprite_path": sprite, "layers": []string{"neon", "bulb"},
		"output_path": combined, "layout": "vertical", "padding": 1}, &out)
	require.Len(t, out.Sheets, 1)
	cimg = readTestPNG(t, combined)
	assert.Equal(t, 18, cimg.Bounds().Dx(), "16 + 2*1 border padding")
	assert.Equal(t, uint8(0), cimg.NRGBAAt(1, 1).A, "base layer hidden")
	assert.Equal(t, cRed, cimg.NRGBAAt(1+3, 1+3))
	assert.Equal(t, cGrey, cimg.NRGBAAt(1+8, 1+3))
	assert.Equal(t, uint8(0), cimg.NRGBAAt(1+3, 1+10).A, "crate not requested")
	_, err = os.Stat(filepath.Join(dir, "combined.json"))
	assert.True(t, os.IsNotExist(err), "no JSON without include_json")
	assert.Equal(t, before, fileHash(t, sprite))

	// Path safety
	text, isErr := e.call(t, "export_layers", map[string]any{"sprite_path": sprite, "layers": []string{"neon"},
		"output_path": filepath.Join(t.TempDir(), "x.png")})
	assert.True(t, isErr)
	assert.Contains(t, text, "allowed_dirs")
	text, isErr = e.call(t, "export_layers", map[string]any{"sprite_path": sprite, "layers": []string{"ghost"},
		"output_path": filepath.Join(dir, "g.png")})
	assert.True(t, isErr)
	assert.Contains(t, text, "Layer not found")
}

func TestIntegration_Pipeline_ExportLayers_AllowedDir(t *testing.T) {
	allowed := t.TempDir()
	e := newPipelineEnv(t, []string{allowed})
	sprite := composeScene(t, e, t.TempDir())
	var out ExportLayersOutput
	e.mustCall(t, "export_layers", map[string]any{"sprite_path": sprite, "layers": []string{"neon"},
		"output_path": filepath.Join(allowed, "game", "neon.png"), "trim": true}, &out)
	require.Len(t, out.Sheets, 1)
	_, err := os.Stat(filepath.Join(allowed, "game", "neon.png"))
	assert.NoError(t, err)
}

func TestIntegration_Pipeline_RunLua(t *testing.T) {
	// Disabled without allowed_dirs
	e := newPipelineEnv(t, nil)
	text, isErr := e.call(t, "run_lua", map[string]any{"script": `print("hi")`})
	assert.True(t, isErr)
	assert.Contains(t, text, "disabled")

	allowed := t.TempDir()
	e = newPipelineEnv(t, []string{allowed})
	sprite := composeScene(t, e, allowed)

	var out RunLuaOutput
	e.mustCall(t, "run_lua", map[string]any{"script": `print("layers=" .. #app.activeSprite.layers)`, "sprite_path": sprite}, &out)
	assert.Contains(t, out.Output, "layers=6")

	outside := composeScene(t, e, t.TempDir())
	text, isErr = e.call(t, "run_lua", map[string]any{"script": `print(1)`, "sprite_path": outside})
	assert.True(t, isErr)
	assert.Contains(t, text, "not inside")

	text, isErr = e.call(t, "run_lua", map[string]any{"script": `error("boom")`})
	assert.True(t, isErr)
	assert.Contains(t, text, "boom")
}
