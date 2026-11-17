package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/willibrandon/mtlog/core"
	"github.com/willibrandon/pixel-mcp/pkg/aseprite"
	"github.com/willibrandon/pixel-mcp/pkg/config"
)

// ---------------------------------------------------------------------------
// set_layer_properties
// ---------------------------------------------------------------------------

// SetLayerPropertiesInput defines the input parameters for the set_layer_properties tool.
type SetLayerPropertiesInput struct {
	SpritePath string  `json:"sprite_path" jsonschema:"Path to the Aseprite sprite file"`
	LayerName  string  `json:"layer_name" jsonschema:"Name of the layer to change (searched recursively inside groups, first match)"`
	Visible    *bool   `json:"visible,omitempty" jsonschema:"Show (true) or hide (false) the layer"`
	Opacity    *int    `json:"opacity,omitempty" jsonschema:"Layer opacity 0-255"`
	NewName    *string `json:"new_name,omitempty" jsonschema:"Rename the layer (must not clash with another layer)"`
	BlendMode  string  `json:"blend_mode,omitempty" jsonschema:"Blend mode: normal, multiply, screen, overlay, darken, lighten, color_dodge, color_burn, hard_light, soft_light, difference, exclusion, hue, saturation, color, luminosity, addition, subtract, divide"`
}

// SetLayerPropertiesOutput defines the output for the set_layer_properties tool.
type SetLayerPropertiesOutput struct {
	Name      string `json:"name" jsonschema:"Layer name after the change"`
	Visible   bool   `json:"visible" jsonschema:"Layer visibility after the change"`
	Opacity   int    `json:"opacity" jsonschema:"Layer opacity after the change"`
	BlendMode string `json:"blend_mode" jsonschema:"Layer blend mode after the change"`
}

func validateSetLayerProperties(in SetLayerPropertiesInput) error {
	if in.SpritePath == "" {
		return fmt.Errorf("sprite_path cannot be empty")
	}
	if in.LayerName == "" {
		return fmt.Errorf("layer_name cannot be empty")
	}
	if in.Visible == nil && in.Opacity == nil && in.NewName == nil && in.BlendMode == "" {
		return fmt.Errorf("nothing to change: set at least one of visible, opacity, new_name, blend_mode")
	}
	if in.Opacity != nil && (*in.Opacity < 0 || *in.Opacity > 255) {
		return fmt.Errorf("opacity must be between 0 and 255, got %d", *in.Opacity)
	}
	if in.NewName != nil && strings.TrimSpace(*in.NewName) == "" {
		return fmt.Errorf("new_name cannot be empty")
	}
	if in.BlendMode != "" {
		if _, ok := aseprite.BlendModes[strings.ToLower(in.BlendMode)]; !ok {
			return fmt.Errorf("invalid blend_mode: %s (valid: %s)", in.BlendMode, strings.Join(aseprite.BlendModeNames(), ", "))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// animate_layer
// ---------------------------------------------------------------------------

// AnimateLayerInput defines the input parameters for the animate_layer tool.
type AnimateLayerInput struct {
	SpritePath      string           `json:"sprite_path" jsonschema:"Path to the Aseprite sprite file"`
	LayerName       string           `json:"layer_name" jsonschema:"Layer to animate; its frame-1 cel is the rest pose. Only this layer is changed"`
	Preset          string           `json:"preset" jsonschema:"Animation preset: flicker (cel opacity), pulse (RGB brightness), bob (vertical offset), sway (horizontal offset), cycle (colour cycling)"`
	Frames          int              `json:"frames,omitempty" jsonschema:"Number of frames to animate starting at frame 1 (default: flicker 6, pulse 6, bob 4, sway 4, cycle = number of colors). Missing frames are added as copies of frame 1"`
	FrameDurationMs int              `json:"frame_duration_ms,omitempty" jsonschema:"If > 0, set the duration of the animated frames (1-65535 ms)"`
	TagName         string           `json:"tag_name,omitempty" jsonschema:"If set, (re)create a forward tag with this name over the animated frames"`
	Seed            int64            `json:"seed,omitempty" jsonschema:"Seed for flicker random mode (default 1). Same seed = same output"`
	Opacities       []int            `json:"opacities,omitempty" jsonschema:"flicker: per-frame cel opacity list 0-255 (default [255,255,140,255,60,255]); repeated to fill frames"`
	Random          bool             `json:"random,omitempty" jsonschema:"flicker: use seeded random opacities between min_opacity and max_opacity instead of a list"`
	MinOpacity      *int             `json:"min_opacity,omitempty" jsonschema:"flicker random: minimum opacity (default 0)"`
	MaxOpacity      *int             `json:"max_opacity,omitempty" jsonschema:"flicker random: maximum opacity (default 255)"`
	Multipliers     []float64        `json:"multipliers,omitempty" jsonschema:"pulse: per-frame brightness multipliers (e.g. [1,1.1,1.25,1.1]); default is a sine curve 1.0 to peak"`
	Peak            float64          `json:"peak,omitempty" jsonschema:"pulse: peak multiplier of the default curve (default 1.25)"`
	Offsets         []aseprite.Point `json:"offsets,omitempty" jsonschema:"bob/sway: explicit per-frame cel offsets [{x,y}] relative to the rest position"`
	Amplitude       int              `json:"amplitude,omitempty" jsonschema:"bob/sway: max offset in pixels for the default curve (bob 2 gives dy 0,-1,-2,-1; sway 1 gives dx 0,1,0,-1)"`
	Colors          []string         `json:"colors,omitempty" jsonschema:"cycle: ordered hex colours (#RRGGBB); pixels matching one are rotated through the list each frame"`
	Step            int              `json:"step,omitempty" jsonschema:"cycle: slots to advance per frame (default 1)"`
}

// AnimateLayerOutput defines the output for the animate_layer tool.
type AnimateLayerOutput struct {
	Layer          string           `json:"layer" jsonschema:"Animated layer"`
	FramesAnimated int              `json:"frames_animated" jsonschema:"Number of frames animated (1..N)"`
	TotalFrames    int              `json:"total_frames" jsonschema:"Frame count of the sprite after the operation"`
	Tag            string           `json:"tag,omitempty" jsonschema:"Tag created over the animated frames"`
	Opacities      []int            `json:"opacities,omitempty" jsonschema:"Per-frame opacities applied (flicker)"`
	Multipliers    []float64        `json:"multipliers,omitempty" jsonschema:"Per-frame brightness multipliers applied (pulse)"`
	Offsets        []aseprite.Point `json:"offsets,omitempty" jsonschema:"Per-frame offsets applied (bob/sway)"`
	CycleShifts    []int            `json:"cycle_shifts,omitempty" jsonschema:"Per-frame colour shifts applied (cycle)"`
}

func buildPlanFromInput(in AnimateLayerInput) (*aseprite.AnimationPlan, error) {
	if in.SpritePath == "" {
		return nil, fmt.Errorf("sprite_path cannot be empty")
	}
	if in.LayerName == "" {
		return nil, fmt.Errorf("layer_name cannot be empty")
	}
	if in.FrameDurationMs < 0 || in.FrameDurationMs > 65535 {
		return nil, fmt.Errorf("frame_duration_ms must be between 1 and 65535 (or 0 to keep durations), got %d", in.FrameDurationMs)
	}
	if in.Seed < 0 {
		return nil, fmt.Errorf("seed must be >= 0, got %d", in.Seed)
	}
	if in.Step < 0 {
		return nil, fmt.Errorf("step must be >= 0, got %d", in.Step)
	}
	params := aseprite.AnimationPresetParams{
		Frames:      in.Frames,
		Seed:        uint64(in.Seed),
		Opacities:   in.Opacities,
		Random:      in.Random,
		Multipliers: in.Multipliers,
		Peak:        in.Peak,
		Offsets:     in.Offsets,
		Amplitude:   in.Amplitude,
		Step:        in.Step,
	}
	if in.MinOpacity != nil {
		params.MinOpacity = *in.MinOpacity
	}
	params.MaxOpacity = 255
	if in.MaxOpacity != nil {
		params.MaxOpacity = *in.MaxOpacity
	}
	for _, hex := range in.Colors {
		var c aseprite.Color
		if err := c.FromHex(hex); err != nil {
			return nil, fmt.Errorf("invalid color in colors: %w", err)
		}
		params.Colors = append(params.Colors, c)
	}
	return aseprite.BuildAnimationPlan(in.Preset, params)
}

// ---------------------------------------------------------------------------
// export_layers
// ---------------------------------------------------------------------------

// ExportLayersInput defines the input parameters for the export_layers tool.
type ExportLayersInput struct {
	SpritePath  string   `json:"sprite_path" jsonschema:"Path to the Aseprite sprite file (never modified)"`
	Layers      []string `json:"layers" jsonschema:"Layer names to export; every other layer is hidden in the output"`
	OutputPath  string   `json:"output_path" jsonschema:"Absolute .png path. Combined: the sheet itself. split=true: sheets are written next to it as <stem>_<layer>.png. Must be inside the sprite's folder or an allowed_dirs entry"`
	Layout      string   `json:"layout,omitempty" jsonschema:"Sheet layout: horizontal (default), vertical, rows, columns, packed"`
	Padding     int      `json:"padding,omitempty" jsonschema:"Padding between frames in pixels (0-100)"`
	IncludeJSON bool     `json:"include_json,omitempty" jsonschema:"Write Aseprite json-hash data next to each sheet (frames with durations, frameTags, layers, plus meta.offset)"`
	Trim        bool     `json:"trim,omitempty" jsonschema:"Crop to the union of visible pixels over all frames; meta.offset gives the crop position in the sprite"`
	Split       bool     `json:"split,omitempty" jsonschema:"true = one sheet per layer, false = one combined sheet of the listed layers"`
}

// ExportedSheet describes one sheet written by export_layers.
type ExportedSheet struct {
	Name   string   `json:"name" jsonschema:"Sheet name (layer name or output stem)"`
	Layers []string `json:"layers" jsonschema:"Layers included in this sheet"`
	PNG    string   `json:"png" jsonschema:"Spritesheet image path"`
	JSON   string   `json:"json,omitempty" jsonschema:"JSON data path"`
	X      int      `json:"x" jsonschema:"X offset of the sheet frames in the source sprite"`
	Y      int      `json:"y" jsonschema:"Y offset of the sheet frames in the source sprite"`
	W      int      `json:"w" jsonschema:"Frame width"`
	H      int      `json:"h" jsonschema:"Frame height"`
	Frames int      `json:"frames" jsonschema:"Number of frames"`
}

// ExportLayersOutput defines the output for the export_layers tool.
type ExportLayersOutput struct {
	Sheets []ExportedSheet `json:"sheets" jsonschema:"Sheets written"`
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeFileName turns a layer name into a safe file name component.
func sanitizeFileName(name string) string {
	s := strings.Trim(unsafeFileChars.ReplaceAllString(name, "_"), "._")
	if s == "" {
		s = "layer"
	}
	return s
}

// planLayerSheets computes the sheets (and their output paths) for export_layers.
func planLayerSheets(layers []string, outputPath string, split, includeJSON bool) ([]aseprite.LayerSheet, error) {
	if len(layers) == 0 {
		return nil, fmt.Errorf("layers cannot be empty")
	}
	seen := map[string]bool{}
	for _, l := range layers {
		if l == "" {
			return nil, fmt.Errorf("layer names cannot be empty")
		}
		if seen[l] {
			return nil, fmt.Errorf("duplicate layer name in layers: %s", l)
		}
		seen[l] = true
	}
	if !strings.EqualFold(filepath.Ext(outputPath), ".png") {
		return nil, fmt.Errorf("output_path must end with .png, got %s", outputPath)
	}
	dir := filepath.Dir(outputPath)
	stem := strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))
	jsonFor := func(png string) string {
		if !includeJSON {
			return ""
		}
		return strings.TrimSuffix(png, filepath.Ext(png)) + ".json"
	}

	if !split {
		return []aseprite.LayerSheet{{Name: stem, Layers: layers, PNGPath: outputPath, JSONPath: jsonFor(outputPath)}}, nil
	}

	var sheets []aseprite.LayerSheet
	files := map[string]string{}
	for _, l := range layers {
		file := stem + "_" + sanitizeFileName(l) + ".png"
		if other, ok := files[strings.ToLower(file)]; ok {
			return nil, fmt.Errorf("layers %q and %q map to the same file name %s", other, l, file)
		}
		files[strings.ToLower(file)] = l
		png := filepath.Join(dir, file)
		sheets = append(sheets, aseprite.LayerSheet{Name: l, Layers: []string{l}, PNGPath: png, JSONPath: jsonFor(png)})
	}
	return sheets, nil
}

// addOffsetToSheetJSON adds meta.offset {x,y,w,h} and meta.spriteSize {w,h} to an
// Aseprite json-hash file, preserving the order of the frames object.
func addOffsetToSheetJSON(path string, s ExportedSheet, spriteW, spriteH int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc struct {
		Frames json.RawMessage            `json:"frames"`
		Meta   map[string]json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Meta == nil {
		doc.Meta = map[string]json.RawMessage{}
	}
	offset, _ := json.Marshal(map[string]int{"x": s.X, "y": s.Y, "w": s.W, "h": s.H})
	size, _ := json.Marshal(map[string]int{"w": spriteW, "h": spriteH})
	doc.Meta["offset"] = offset
	doc.Meta["spriteSize"] = size
	out, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
}

// ---------------------------------------------------------------------------
// compose_from_manifest
// ---------------------------------------------------------------------------

// ComposeFromManifestInput defines the input parameters for the compose_from_manifest tool.
type ComposeFromManifestInput struct {
	ManifestPath string `json:"manifest_path" jsonschema:"Absolute path to a JSON manifest {base, out, png?, layers:[{name, file, x, y, w?, h?, flipX?, opacity?}]}. Relative paths inside resolve against the manifest's folder"`
}

// ComposeFromManifestOutput defines the output for the compose_from_manifest tool.
type ComposeFromManifestOutput struct {
	Out    string `json:"out" jsonschema:"Path of the created .aseprite file"`
	PNG    string `json:"png,omitempty" jsonschema:"Path of the flattened PNG if requested"`
	Width  int    `json:"width" jsonschema:"Sprite width (base image width)"`
	Height int    `json:"height" jsonschema:"Sprite height (base image height)"`
	Layers int    `json:"layers" jsonschema:"Number of layers including base"`
}

// Manifest is the JSON document read by compose_from_manifest.
type Manifest struct {
	Base   string          `json:"base"`
	Out    string          `json:"out"`
	PNG    string          `json:"png,omitempty"`
	Layers []ManifestEntry `json:"layers"`
}

// ManifestEntry is one prop layer in a Manifest.
type ManifestEntry struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w,omitempty"`
	H       int    `json:"h,omitempty"`
	FlipX   bool   `json:"flipX,omitempty"`
	Opacity *int   `json:"opacity,omitempty"`
}

// resolvedManifest is a Manifest with absolute, validated paths.
type resolvedManifest struct {
	Base   string
	Out    string
	PNG    string
	Layers []aseprite.ManifestLayer
}

// loadManifest reads, resolves and validates a manifest file.
func loadManifest(manifestPath string, allowed []string) (*resolvedManifest, error) {
	if manifestPath == "" {
		return nil, fmt.Errorf("manifest_path cannot be empty")
	}
	if !filepath.IsAbs(manifestPath) {
		return nil, fmt.Errorf("manifest_path must be absolute: %s", manifestPath)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}

	dir := filepath.Dir(manifestPath)
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(dir, p)
	}
	mustExist := func(field, p string) error {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s not found: %s", field, p)
		}
		return nil
	}

	r := &resolvedManifest{}
	if m.Base == "" {
		return nil, fmt.Errorf("manifest: base is required")
	}
	r.Base = abs(m.Base)
	if err := mustExist("base", r.Base); err != nil {
		return nil, err
	}

	if m.Out == "" {
		return nil, fmt.Errorf("manifest: out is required")
	}
	r.Out = abs(m.Out)
	if ext := strings.ToLower(filepath.Ext(r.Out)); ext != ".aseprite" && ext != ".ase" {
		return nil, fmt.Errorf("manifest: out must end with .aseprite or .ase, got %s", m.Out)
	}
	if err := checkOutputPath(r.Out, dir, allowed); err != nil {
		return nil, fmt.Errorf("manifest out: %w", err)
	}
	if m.PNG != "" {
		r.PNG = abs(m.PNG)
		if !strings.EqualFold(filepath.Ext(r.PNG), ".png") {
			return nil, fmt.Errorf("manifest: png must end with .png, got %s", m.PNG)
		}
		if err := checkOutputPath(r.PNG, dir, allowed); err != nil {
			return nil, fmt.Errorf("manifest png: %w", err)
		}
	}

	names := map[string]bool{"base": true}
	for i, e := range m.Layers {
		if strings.TrimSpace(e.Name) == "" {
			return nil, fmt.Errorf("manifest: layers[%d].name is required", i)
		}
		if names[e.Name] {
			return nil, fmt.Errorf("manifest: duplicate layer name %q (\"base\" is reserved)", e.Name)
		}
		names[e.Name] = true
		if e.File == "" {
			return nil, fmt.Errorf("manifest: layers[%d].file is required", i)
		}
		file := abs(e.File)
		if err := mustExist(fmt.Sprintf("layers[%d].file", i), file); err != nil {
			return nil, err
		}
		if e.W < 0 || e.H < 0 || e.W > 8192 || e.H > 8192 {
			return nil, fmt.Errorf("manifest: layers[%d] w/h must be between 0 and 8192", i)
		}
		opacity := 255
		if e.Opacity != nil {
			opacity = *e.Opacity
		}
		if opacity < 0 || opacity > 255 {
			return nil, fmt.Errorf("manifest: layers[%d].opacity must be between 0 and 255, got %d", i, opacity)
		}
		r.Layers = append(r.Layers, aseprite.ManifestLayer{
			Name: e.Name, File: file, X: e.X, Y: e.Y, W: e.W, H: e.H, FlipX: e.FlipX, Opacity: opacity,
		})
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// run_lua
// ---------------------------------------------------------------------------

// RunLuaInput defines the input parameters for the run_lua tool.
type RunLuaInput struct {
	Script     string `json:"script" jsonschema:"Lua source executed by Aseprite in --batch mode (app.activeSprite is sprite_path when given). The script must save the sprite itself"`
	SpritePath string `json:"sprite_path,omitempty" jsonschema:"Optional sprite to open first; must be inside an allowed_dirs entry"`
}

// RunLuaOutput defines the output for the run_lua tool.
type RunLuaOutput struct {
	Output string `json:"output" jsonschema:"Everything the script printed to stdout"`
}

func validateRunLua(in RunLuaInput, allowed []string) error {
	if len(allowed) == 0 {
		return fmt.Errorf("run_lua is disabled: set \"allowed_dirs\" in ~/.config/pixel-mcp/config.json to enable it")
	}
	if strings.TrimSpace(in.Script) == "" {
		return fmt.Errorf("script cannot be empty")
	}
	if in.SpritePath != "" {
		if !filepath.IsAbs(in.SpritePath) {
			return fmt.Errorf("sprite_path must be absolute: %s", in.SpritePath)
		}
		if !isUnderAllowedDir(in.SpritePath, allowed) {
			return fmt.Errorf("sprite_path %s is not inside any allowed_dirs entry", in.SpritePath)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPipelineTools registers the asset-pipeline tools with the MCP server.
//
// Registers:
//   - set_layer_properties: visibility, opacity, name, blend mode of a layer
//   - animate_layer: preset ambient animations (flicker, pulse, bob, sway, cycle) on one layer
//   - export_layers: per-layer or combined spritesheets of selected layers (+ json-hash, trim offsets)
//   - compose_from_manifest: build a layered .aseprite from a JSON manifest of images
//   - run_lua: raw Lua escape hatch, disabled unless allowed_dirs is configured
func RegisterPipelineTools(server *mcp.Server, client *aseprite.Client, gen *aseprite.LuaGenerator, cfg *config.Config, logger core.Logger) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "set_layer_properties",
			Description: "Change a layer's visibility, opacity (0-255), name and/or blend mode (normal, addition, screen, multiply, ...). Saves the sprite.",
		},
		maybeWrapWithTiming("set_layer_properties", logger, cfg.EnableTiming, func(ctx context.Context, req *mcp.CallToolRequest, input SetLayerPropertiesInput) (*mcp.CallToolResult, *SetLayerPropertiesOutput, error) {
			opLogger := logger.WithContext(ctx)
			if err := validateSetLayerProperties(input); err != nil {
				return nil, nil, err
			}
			script := gen.SetLayerProperties(input.LayerName, aseprite.LayerProperties{
				Visible:   input.Visible,
				Opacity:   input.Opacity,
				NewName:   input.NewName,
				BlendMode: strings.ToLower(input.BlendMode),
			})
			output, err := client.ExecuteLua(ctx, script, input.SpritePath)
			if err != nil {
				opLogger.Error("Failed to set layer properties", "error", err)
				return nil, nil, fmt.Errorf("failed to set layer properties: %w", err)
			}
			var result SetLayerPropertiesOutput
			if err := parseJSON(output, &result); err != nil {
				return nil, nil, fmt.Errorf("failed to parse output: %w", err)
			}
			opLogger.Information("Layer properties set", "sprite", input.SpritePath, "layer", result.Name)
			return nil, &result, nil
		}),
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name: "animate_layer",
			Description: "Animate ONE layer with a deterministic preset (flicker = cel opacity, pulse = brightness, bob/sway = cel offset, cycle = colour cycling). " +
				"Extends the sprite to `frames` frames (new frames copy frame 1 on all layers), regenerates the layer's cels on frames 1..N from its frame-1 rest pose, " +
				"optionally sets frame durations and creates a tag. Presets drive different cel attributes, so e.g. pulse then bob stack. Saves the sprite.",
		},
		maybeWrapWithTiming("animate_layer", logger, cfg.EnableTiming, func(ctx context.Context, req *mcp.CallToolRequest, input AnimateLayerInput) (*mcp.CallToolResult, *AnimateLayerOutput, error) {
			opLogger := logger.WithContext(ctx)
			plan, err := buildPlanFromInput(input)
			if err != nil {
				return nil, nil, err
			}
			script := gen.AnimateLayer(input.LayerName, plan, input.FrameDurationMs, input.TagName)
			output, err := client.ExecuteLua(ctx, script, input.SpritePath)
			if err != nil {
				opLogger.Error("Failed to animate layer", "error", err)
				return nil, nil, fmt.Errorf("failed to animate layer: %w", err)
			}
			var result AnimateLayerOutput
			if err := parseJSON(output, &result); err != nil {
				return nil, nil, fmt.Errorf("failed to parse output: %w", err)
			}
			result.Opacities = plan.Opacities
			result.Multipliers = plan.Multipliers
			result.Offsets = plan.Offsets
			result.CycleShifts = plan.CycleShifts
			opLogger.Information("Layer animated", "sprite", input.SpritePath, "layer", input.LayerName, "preset", plan.Preset, "frames", plan.Frames)
			return nil, &result, nil
		}),
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name: "export_layers",
			Description: "Export spritesheets containing ONLY the listed layers (others hidden), without modifying the source file. " +
				"split=true writes one sheet per layer (<stem>_<layer>.png), otherwise one combined sheet. " +
				"include_json writes Aseprite json-hash (frame durations, frameTags) plus meta.offset; trim crops to the visible pixels and meta.offset tells where to place the sheet in the sprite.",
		},
		maybeWrapWithTiming("export_layers", logger, cfg.EnableTiming, func(ctx context.Context, req *mcp.CallToolRequest, input ExportLayersInput) (*mcp.CallToolResult, *ExportLayersOutput, error) {
			opLogger := logger.WithContext(ctx)
			if input.SpritePath == "" {
				return nil, nil, fmt.Errorf("sprite_path cannot be empty")
			}
			if _, err := os.Stat(input.SpritePath); err != nil {
				return nil, nil, fmt.Errorf("sprite file not found: %s", input.SpritePath)
			}
			spriteAbs, err := filepath.Abs(input.SpritePath)
			if err != nil {
				return nil, nil, err
			}
			layout := input.Layout
			if layout == "" {
				layout = "horizontal"
			}
			valid := map[string]bool{"horizontal": true, "vertical": true, "rows": true, "columns": true, "packed": true}
			if !valid[layout] {
				return nil, nil, fmt.Errorf("invalid layout: %s (valid: horizontal, vertical, rows, columns, packed)", input.Layout)
			}
			if input.Padding < 0 || input.Padding > 100 {
				return nil, nil, fmt.Errorf("padding must be between 0 and 100, got %d", input.Padding)
			}
			if err := checkOutputPath(input.OutputPath, filepath.Dir(spriteAbs), cfg.AllowedDirs); err != nil {
				return nil, nil, err
			}
			sheets, err := planLayerSheets(input.Layers, input.OutputPath, input.Split, input.IncludeJSON)
			if err != nil {
				return nil, nil, err
			}
			if err := os.MkdirAll(filepath.Dir(input.OutputPath), 0755); err != nil {
				return nil, nil, fmt.Errorf("failed to create output directory: %w", err)
			}

			script := gen.ExportLayers(spriteAbs, sheets, layout, input.Padding, input.Trim)
			output, err := client.ExecuteLua(ctx, script, "")
			if err != nil {
				opLogger.Error("Failed to export layers", "error", err)
				return nil, nil, fmt.Errorf("failed to export layers: %w", err)
			}
			var raw struct {
				Sheets       []ExportedSheet `json:"sheets"`
				SpriteWidth  int             `json:"sprite_width"`
				SpriteHeight int             `json:"sprite_height"`
			}
			idx := strings.LastIndex(output, aseprite.ExportLayersResultMarker)
			if idx < 0 {
				return nil, nil, fmt.Errorf("unexpected export_layers output: %s", output)
			}
			if err := parseJSON(output[idx+len(aseprite.ExportLayersResultMarker):], &raw); err != nil {
				return nil, nil, fmt.Errorf("failed to parse output: %w", err)
			}
			for i := range raw.Sheets {
				raw.Sheets[i].Layers = sheets[i].Layers
				if raw.Sheets[i].JSON != "" {
					if err := addOffsetToSheetJSON(raw.Sheets[i].JSON, raw.Sheets[i], raw.SpriteWidth, raw.SpriteHeight); err != nil {
						return nil, nil, fmt.Errorf("failed to add offset to sheet JSON: %w", err)
					}
				}
			}
			opLogger.Information("Layers exported", "sprite", input.SpritePath, "sheets", len(raw.Sheets))
			return nil, &ExportLayersOutput{Sheets: raw.Sheets}, nil
		}),
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name: "compose_from_manifest",
			Description: "Build a layered RGB .aseprite from a JSON manifest: {base, out, png?, layers:[{name, file, x, y, w?, h?, flipX?, opacity?}]}. " +
				"The base image becomes layer \"base\"; each entry becomes a layer (bottom to top) placed at x,y, resized nearest-neighbour when w/h are given. " +
				"Relative paths resolve against the manifest folder; out/png must stay inside it (or allowed_dirs). Optionally writes a flattened PNG.",
		},
		maybeWrapWithTiming("compose_from_manifest", logger, cfg.EnableTiming, func(ctx context.Context, req *mcp.CallToolRequest, input ComposeFromManifestInput) (*mcp.CallToolResult, *ComposeFromManifestOutput, error) {
			opLogger := logger.WithContext(ctx)
			m, err := loadManifest(input.ManifestPath, cfg.AllowedDirs)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range []string{m.Out, m.PNG} {
				if p != "" {
					if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
						return nil, nil, fmt.Errorf("failed to create output directory: %w", err)
					}
				}
			}
			script := gen.ComposeFromManifest(m.Base, m.Layers, m.Out, m.PNG)
			output, err := client.ExecuteLua(ctx, script, "")
			if err != nil {
				opLogger.Error("Failed to compose from manifest", "error", err)
				return nil, nil, fmt.Errorf("failed to compose from manifest: %w", err)
			}
			var result ComposeFromManifestOutput
			if err := parseJSON(output, &result); err != nil {
				return nil, nil, fmt.Errorf("failed to parse output: %w", err)
			}
			opLogger.Information("Composed sprite from manifest", "manifest", input.ManifestPath, "out", result.Out, "layers", result.Layers)
			return nil, &result, nil
		}),
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name: "run_lua",
			Description: "ESCAPE HATCH: run an arbitrary Aseprite Lua script in batch mode and return its stdout. " +
				"Disabled unless allowed_dirs is set in the server config; sprite_path (optional) must be inside an allowed dir. " +
				"The script is NOT sandboxed (it can read/write any file Aseprite can) - prefer the dedicated tools.",
		},
		maybeWrapWithTiming("run_lua", logger, cfg.EnableTiming, func(ctx context.Context, req *mcp.CallToolRequest, input RunLuaInput) (*mcp.CallToolResult, *RunLuaOutput, error) {
			opLogger := logger.WithContext(ctx)
			if err := validateRunLua(input, cfg.AllowedDirs); err != nil {
				return nil, nil, err
			}
			output, err := client.ExecuteLua(ctx, input.Script, input.SpritePath)
			if err != nil {
				opLogger.Error("run_lua failed", "error", err)
				return nil, nil, fmt.Errorf("lua script failed: %w", err)
			}
			opLogger.Information("run_lua executed", "sprite", input.SpritePath)
			return nil, &RunLuaOutput{Output: output}, nil
		}),
	)
}
