package aseprite

import (
	"fmt"
	"sort"
	"strings"
)

// BlendModes maps the user-facing blend mode names accepted by SetLayerProperties
// to Aseprite's Lua BlendMode enum members.
var BlendModes = map[string]string{
	"normal":      "NORMAL",
	"multiply":    "MULTIPLY",
	"screen":      "SCREEN",
	"overlay":     "OVERLAY",
	"darken":      "DARKEN",
	"lighten":     "LIGHTEN",
	"color_dodge": "COLOR_DODGE",
	"color_burn":  "COLOR_BURN",
	"hard_light":  "HARD_LIGHT",
	"soft_light":  "SOFT_LIGHT",
	"difference":  "DIFFERENCE",
	"exclusion":   "EXCLUSION",
	"hue":         "HSL_HUE",
	"saturation":  "HSL_SATURATION",
	"color":       "HSL_COLOR",
	"luminosity":  "HSL_LUMINOSITY",
	"addition":    "ADDITION",
	"subtract":    "SUBTRACT",
	"divide":      "DIVIDE",
}

// BlendModeNames returns the sorted list of accepted blend mode names.
func BlendModeNames() []string {
	names := make([]string, 0, len(BlendModes))
	for k := range BlendModes {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// luaFindLayerHelper defines findLayer(layers, name) (recursive, descends into groups)
// and allLayers(layers, out) (flattened list of every layer, groups included).
const luaFindLayerHelper = `
local function findLayer(layers, name)
	for _, l in ipairs(layers) do
		if l.name == name then return l end
		if l.isGroup then
			local found = findLayer(l.layers, name)
			if found then return found end
		end
	end
	return nil
end

-- Returns every layer (groups included) in depth-first order, plus parentIdx[i] =
-- index of the enclosing group in that list (nil for top-level layers).
-- Indices are used because Aseprite returns a fresh userdata on every access,
-- so layer objects cannot be used as table keys.
local function allLayers(layers)
	local out, parentIdx = {}, {}
	local function walk(ls, parent)
		for _, l in ipairs(ls) do
			table.insert(out, l)
			local i = #out
			parentIdx[i] = parent
			if l.isGroup then walk(l.layers, i) end
		end
	end
	walk(layers, nil)
	return out, parentIdx
end
`

// luaBlendNameHelper builds a reverse lookup table from BlendMode enum values to names.
func luaBlendNameHelper() string {
	var b strings.Builder
	b.WriteString("local blendNames = {}\n")
	for _, name := range BlendModeNames() {
		fmt.Fprintf(&b, "blendNames[BlendMode.%s] = \"%s\"\n", BlendModes[name], name)
	}
	return b.String()
}

// LayerProperties holds the optional properties applied by SetLayerProperties.
// Nil pointers / empty strings leave the property unchanged.
type LayerProperties struct {
	Visible   *bool
	Opacity   *int
	NewName   *string
	BlendMode string
}

// SetLayerProperties generates a Lua script that changes visibility, opacity, name
// and/or blend mode of a layer (searched recursively, first match wins) and saves the sprite.
//
// BlendMode must be a key of BlendModes (validated by the caller); unknown values are ignored.
// Prints a JSON object {"name","visible","opacity","blend_mode"} describing the final state.
func (g *LuaGenerator) SetLayerProperties(layerName string, p LayerProperties) string {
	name := EscapeString(layerName)

	var muts strings.Builder
	if p.Visible != nil {
		fmt.Fprintf(&muts, "\tlayer.isVisible = %t\n", *p.Visible)
	}
	if p.Opacity != nil {
		fmt.Fprintf(&muts, "\tlayer.opacity = %d\n", *p.Opacity)
	}
	if enum, ok := BlendModes[p.BlendMode]; ok {
		fmt.Fprintf(&muts, "\tlayer.blendMode = BlendMode.%s\n", enum)
	}
	collision := ""
	if p.NewName != nil {
		newName := EscapeString(*p.NewName)
		fmt.Fprintf(&muts, "\tlayer.name = \"%s\"\n", newName)
		collision = fmt.Sprintf(`
local existing = findLayer(spr.layers, "%s")
if existing and existing ~= layer then
	error("A layer named \"%s\" already exists")
end
`, newName, newName)
	}

	return fmt.Sprintf(`local spr = app.activeSprite
if not spr then
	error("No active sprite")
end
%s
%s
local layer = findLayer(spr.layers, "%s")
if not layer then
	error("Layer not found: %s")
end
%s
app.transaction(function()
%send)

spr:saveAs(spr.filename)
print(json.encode({
	name = layer.name,
	visible = layer.isVisible,
	opacity = layer.opacity or 255,
	blend_mode = blendNames[layer.blendMode] or tostring(layer.blendMode)
}))`, luaFindLayerHelper, luaBlendNameHelper(), name, EscapeString(name), collision, muts.String())
}

// AnimateLayer generates a Lua script applying an AnimationPlan to one layer.
//
// Behaviour:
//   - The sprite is extended to plan.Frames frames if needed. New frames copy frame 1's
//     cels (image, position, opacity) on every other image layer and frame 1's duration.
//   - Only the named layer is modified on frames 1..plan.Frames. Its frame-1 cel is the
//     rest state. Each frame's cel is re-created (so linked cels are unlinked for this layer
//     only) and exactly one attribute is driven by the preset:
//     flicker → cel opacity, bob/sway → cel position (rest + offset),
//     pulse → RGB brightness of the rest image, cycle → colour rotation of the rest image.
//     The other attributes keep each frame's current value, so presets touching different
//     attributes (e.g. pulse then bob) stack.
//   - durationMs > 0 sets the duration of frames 1..plan.Frames.
//   - tagName != "" (re)creates a forward tag spanning frames 1..plan.Frames.
//
// Prints JSON {"layer","frames_animated","total_frames","tag"}.
func (g *LuaGenerator) AnimateLayer(layerName string, plan *AnimationPlan, durationMs int, tagName string) string {
	name := EscapeString(layerName)

	var data strings.Builder
	data.WriteString("local opacities = nil\nlocal offsets = nil\nlocal multipliers = nil\nlocal cycleColors = nil\nlocal cycleShifts = nil\n")
	if plan.Opacities != nil {
		data.WriteString("opacities = {")
		for i, o := range plan.Opacities {
			if i > 0 {
				data.WriteString(", ")
			}
			fmt.Fprintf(&data, "%d", o)
		}
		data.WriteString("}\n")
	}
	if plan.Offsets != nil {
		data.WriteString("offsets = {")
		for i, o := range plan.Offsets {
			if i > 0 {
				data.WriteString(", ")
			}
			fmt.Fprintf(&data, "{%d, %d}", o.X, o.Y)
		}
		data.WriteString("}\n")
	}
	if plan.Multipliers != nil {
		data.WriteString("multipliers = {")
		for i, m := range plan.Multipliers {
			if i > 0 {
				data.WriteString(", ")
			}
			fmt.Fprintf(&data, "%.4f", m)
		}
		data.WriteString("}\n")
	}
	if plan.Cycle != nil {
		data.WriteString("cycleColors = {")
		for i, c := range plan.Cycle {
			if i > 0 {
				data.WriteString(", ")
			}
			fmt.Fprintf(&data, "{%d, %d, %d}", c.R, c.G, c.B)
		}
		data.WriteString("}\ncycleShifts = {")
		for i, s := range plan.CycleShifts {
			if i > 0 {
				data.WriteString(", ")
			}
			fmt.Fprintf(&data, "%d", s)
		}
		data.WriteString("}\n")
	}

	durationLua := "nil"
	if durationMs > 0 {
		durationLua = fmt.Sprintf("%.3f", float64(durationMs)/1000.0)
	}
	tagLua := "nil"
	if tagName != "" {
		tagLua = fmt.Sprintf("\"%s\"", EscapeString(tagName))
	}

	return fmt.Sprintf(`local spr = app.activeSprite
if not spr then
	error("No active sprite")
end
%s
local layer = findLayer(spr.layers, "%s")
if not layer then
	error("Layer not found: %s")
end
if layer.isGroup or layer.isTilemap then
	error("Layer must be a plain image layer: %s")
end

local N = %d
local durationSec = %s
local tagName = %s
%s
if (multipliers or cycleColors) and spr.colorMode ~= ColorMode.RGB then
	error("pulse and cycle presets require an RGB sprite")
end

local restCel = layer:cel(1)
if not restCel then
	error("Layer has no cel in frame 1: %s")
end
local restImage = restCel.image:clone()
local restPos = Point(restCel.position.x, restCel.position.y)
local restOpacity = restCel.opacity

local pc = app.pixelColor
local function clamp(v)
	if v < 0 then return 0 end
	if v > 255 then return 255 end
	return v
end

local function pulseImage(src, m)
	local img = src:clone()
	for it in img:pixels() do
		local v = it()
		local a = pc.rgbaA(v)
		if a > 0 then
			it(pc.rgba(
				clamp(math.floor(pc.rgbaR(v) * m + 0.5)),
				clamp(math.floor(pc.rgbaG(v) * m + 0.5)),
				clamp(math.floor(pc.rgbaB(v) * m + 0.5)),
				a))
		end
	end
	return img
end

local cycleIndex = {}
if cycleColors then
	for k, c in ipairs(cycleColors) do
		cycleIndex[c[1] * 65536 + c[2] * 256 + c[3]] = k
	end
end

local function cycleImage(src, shift)
	local img = src:clone()
	if shift == 0 then return img end
	local n = #cycleColors
	for it in img:pixels() do
		local v = it()
		local a = pc.rgbaA(v)
		if a > 0 then
			local k = cycleIndex[pc.rgbaR(v) * 65536 + pc.rgbaG(v) * 256 + pc.rgbaB(v)]
			if k then
				local c = cycleColors[((k - 1 + shift) %% n) + 1]
				it(pc.rgba(c[1], c[2], c[3], a))
			end
		end
	end
	return img
end

app.transaction(function()
	local origCount = #spr.frames
	local firstDuration = spr.frames[1].duration

	-- Ensure the sprite has at least N frames (new frames duplicate frame 1)
	while #spr.frames < N do
		local fr = spr:newEmptyFrame(#spr.frames + 1)
		fr.duration = firstDuration
	end
	if #spr.frames > origCount then
		for _, l in ipairs(allLayers(spr.layers)) do
			if l ~= layer and not l.isGroup and not l.isTilemap then
				local c1 = l:cel(1)
				if c1 then
					for f = origCount + 1, #spr.frames do
						local c = spr:newCel(l, f, c1.image, c1.position)
						c.opacity = c1.opacity
					end
				end
			end
		end
	end

	-- Re-create the target layer's cels on frames 1..N
	for i = 1, N do
		local cur = layer:cel(i)
		local img, pos, op
		if i == 1 then
			img, pos, op = restImage:clone(), restPos, restOpacity
		elseif cur and i <= origCount then
			img, pos, op = cur.image:clone(), Point(cur.position.x, cur.position.y), cur.opacity
		else
			img, pos, op = restImage:clone(), restPos, restOpacity
		end

		if multipliers then img = pulseImage(restImage, multipliers[i]) end
		if cycleColors then img = cycleImage(restImage, cycleShifts[i]) end
		if offsets then pos = Point(restPos.x + offsets[i][1], restPos.y + offsets[i][2]) end
		if opacities then op = opacities[i] end

		local c = spr:newCel(layer, i, img, pos)
		c.opacity = op
	end

	if durationSec then
		for i = 1, N do
			spr.frames[i].duration = durationSec
		end
	end

	if tagName then
		for _, t in ipairs(spr.tags) do
			if t.name == tagName then
				spr:deleteTag(t)
				break
			end
		end
		local t = spr:newTag(1, N)
		t.name = tagName
	end
end)

spr:saveAs(spr.filename)
print(json.encode({
	layer = layer.name,
	frames_animated = N,
	total_frames = #spr.frames,
	tag = tagName or ""
}))`, luaFindLayerHelper, name, EscapeString(name), EscapeString(name),
		plan.Frames, durationLua, tagLua, data.String(), EscapeString(name))
}

// LayerSheet describes one spritesheet produced by ExportLayers.
type LayerSheet struct {
	// Name is used as the frame-name prefix in the JSON ("<name> <frame>").
	Name string
	// Layers are the layer names shown on this sheet (all others are hidden).
	Layers []string
	// PNGPath is the absolute output image path.
	PNGPath string
	// JSONPath is the absolute JSON data path ("" = no JSON).
	JSONPath string
}

// sheetTypes maps layout names to Aseprite SpriteSheetType members.
var sheetTypes = map[string]string{
	"horizontal": "HORIZONTAL",
	"vertical":   "VERTICAL",
	"rows":       "ROWS",
	"columns":    "COLUMNS",
	"packed":     "PACKED",
}

// ExportLayersResultMarker prefixes the result line printed by the ExportLayers script.
// Aseprite's ExportSpriteSheet command can write its own JSON to stdout, so callers
// should parse only the text following the last occurrence of this marker.
const ExportLayersResultMarker = "PIXEL_MCP_RESULT:"

// ExportLayers generates a Lua script exporting one spritesheet per LayerSheet,
// showing only that sheet's layers.
//
// The source sprite is opened with app.open for each sheet and closed without saving,
// so the file on disk is never modified. Requested layers are forced visible (with their
// parent groups); every other layer is hidden. When trim is true the sprite is cropped to
// the union of the visible pixels over all frames, so every frame cell has the same size
// and a single (x, y) offset places the sheet in the original sprite.
//
// JSON is written in Aseprite's json-hash format with frameTags and layers listed.
// Prints ExportLayersResultMarker followed by {"sheets":[{"name","png","json","x","y","w","h","frames"}], "sprite_width", "sprite_height"}.
func (g *LuaGenerator) ExportLayers(spritePath string, sheets []LayerSheet, layout string, padding int, trim bool) string {
	sheetType, ok := sheetTypes[layout]
	if !ok {
		sheetType = "HORIZONTAL"
	}

	var defs strings.Builder
	defs.WriteString("local sheets = {\n")
	for _, s := range sheets {
		defs.WriteString("\t{ name = \"")
		// Braces would be interpreted by Aseprite's filename format.
		defs.WriteString(EscapeString(strings.NewReplacer("{", "(", "}", ")").Replace(s.Name)))
		defs.WriteString("\", png = \"")
		defs.WriteString(EscapeString(s.PNGPath))
		defs.WriteString("\", json = ")
		if s.JSONPath != "" {
			defs.WriteString("\"" + EscapeString(s.JSONPath) + "\"")
		} else {
			defs.WriteString("nil")
		}
		defs.WriteString(", layers = {")
		for i, l := range s.Layers {
			if i > 0 {
				defs.WriteString(", ")
			}
			defs.WriteString("\"" + EscapeString(l) + "\"")
		}
		defs.WriteString("} },\n")
	}
	defs.WriteString("}\n")

	return fmt.Sprintf(`local ExportLayersResultMarker = "%s"
local srcPath = "%s"
local trim = %t
%s
%s
local results = {}
local spriteW, spriteH = 0, 0

local function effectivelyVisible(everything, parentIdx, i)
	while i do
		if not everything[i].isVisible then return false end
		i = parentIdx[i]
	end
	return true
end

for _, sheet in ipairs(sheets) do
	local spr = app.open(srcPath)
	if not spr then
		error("Failed to open sprite: " .. srcPath)
	end
	spriteW, spriteH = spr.width, spr.height

	local everything, parentIdx = allLayers(spr.layers)
	local original = {}
	for i, l in ipairs(everything) do
		original[i] = l.isVisible
		l.isVisible = false
	end

	for _, name in ipairs(sheet.layers) do
		-- first depth-first match, same order as findLayer
		local idx = nil
		for i, l in ipairs(everything) do
			if l.name == name then idx = i break end
		end
		if not idx then
			spr:close()
			error("Layer not found: " .. name)
		end
		everything[idx].isVisible = true
		local p = parentIdx[idx]
		while p do
			everything[p].isVisible = true
			p = parentIdx[p]
		end
		if everything[idx].isGroup then
			-- restore the original visibility of the group's descendants
			for i = idx + 1, #everything do
				local q = parentIdx[i]
				local inside = false
				while q do
					if q == idx then inside = true break end
					q = parentIdx[q]
				end
				if not inside then break end
				everything[i].isVisible = original[i]
			end
		end
	end

	local bounds = Rectangle(0, 0, spr.width, spr.height)
	if trim then
		local u = nil
		for i, l in ipairs(everything) do
			if not l.isGroup and effectivelyVisible(everything, parentIdx, i) then
				for _, fr in ipairs(spr.frames) do
					local c = l:cel(fr)
					if c then
						local sb = c.image:shrinkBounds()
						if not sb.isEmpty then
							local r = Rectangle(c.position.x + sb.x, c.position.y + sb.y, sb.width, sb.height)
							if u then u = u:union(r) else u = r end
						end
					end
				end
			end
		end
		if u then u = u:intersect(Rectangle(0, 0, spr.width, spr.height)) end
		if not u or u.isEmpty then
			spr:close()
			error("No visible pixels to export for sheet: " .. sheet.name)
		end
		bounds = u
		spr:crop(bounds)
	end

	local params = {
		ui = false,
		askOverwrite = false,
		type = SpriteSheetType.%s,
		textureFilename = sheet.png,
		filenameFormat = sheet.name .. " {frame}",
		borderPadding = %d,
		shapePadding = %d,
		innerPadding = 0,
		trim = false,
		ignoreEmpty = false,
		mergeDuplicates = false,
		openGenerated = false,
	}
	if sheet.json then
		params.dataFilename = sheet.json
		params.dataFormat = SpriteSheetDataFormat.JSON_HASH
		params.listTags = true
		params.listLayers = true
		params.listSlices = false
	end
	app.command.ExportSpriteSheet(params)

	table.insert(results, {
		name = sheet.name,
		png = sheet.png,
		json = sheet.json or "",
		x = bounds.x, y = bounds.y, w = bounds.width, h = bounds.height,
		frames = #spr.frames
	})
	spr:close()
end

-- ExportSpriteSheet may print its own data to stdout, so mark our result line
print(ExportLayersResultMarker .. json.encode({ sheets = results, sprite_width = spriteW, sprite_height = spriteH }))`,
		ExportLayersResultMarker, EscapeString(spritePath), trim, luaFindLayerHelper, defs.String(), sheetType, padding, padding)
}

// ManifestLayer is a resolved compose_from_manifest layer entry (absolute file path).
type ManifestLayer struct {
	Name    string
	File    string
	X       int
	Y       int
	W       int
	H       int
	FlipX   bool
	Opacity int
}

// ComposeFromManifest generates a Lua script that builds a new RGB sprite from a base
// image plus one layer per entry (in order, bottom to top), then saves it to outPath and,
// if pngPath is non-empty, a flattened PNG copy.
//
// Entries with W and/or H > 0 are resized with nearest-neighbour sampling (a missing
// dimension keeps the aspect ratio). FlipX mirrors the image horizontally. Opacity is the
// layer opacity (0-255). The base layer is named "base".
//
// Prints JSON {"out","png","width","height","layers"}.
func (g *LuaGenerator) ComposeFromManifest(basePath string, layers []ManifestLayer, outPath, pngPath string) string {
	var defs strings.Builder
	defs.WriteString("local entries = {\n")
	for _, l := range layers {
		fmt.Fprintf(&defs, "\t{ name = \"%s\", file = \"%s\", x = %d, y = %d, w = %d, h = %d, flipX = %t, opacity = %d },\n",
			EscapeString(l.Name), EscapeString(l.File), l.X, l.Y, l.W, l.H, l.FlipX, l.Opacity)
	}
	defs.WriteString("}\n")

	pngLua := "nil"
	if pngPath != "" {
		pngLua = "\"" + EscapeString(pngPath) + "\""
	}

	return fmt.Sprintf(`local basePath = "%s"
local outPath = "%s"
local pngPath = %s
%s
local function loadRGB(path)
	local ok, img = pcall(function() return Image{ fromFile = path } end)
	if not ok or not img then
		error("Failed to load image: " .. path)
	end
	if img.colorMode ~= ColorMode.RGB then
		local conv = Image(img.width, img.height, ColorMode.RGB)
		conv:drawImage(img, Point(0, 0), 255, BlendMode.SRC)
		img = conv
	end
	return img
end

local function transform(src, w, h, flipX)
	local sw, sh = src.width, src.height
	if w <= 0 and h <= 0 then
		w, h = sw, sh
	elseif w <= 0 then
		w = math.max(1, math.floor(sw * h / sh + 0.5))
	elseif h <= 0 then
		h = math.max(1, math.floor(sh * w / sw + 0.5))
	end
	if w == sw and h == sh and not flipX then
		return src
	end
	local dst = Image(w, h, ColorMode.RGB)
	for y = 0, h - 1 do
		local sy = math.floor(y * sh / h)
		for x = 0, w - 1 do
			local sx = math.floor(x * sw / w)
			if flipX then sx = sw - 1 - sx end
			dst:drawPixel(x, y, src:getPixel(sx, sy))
		end
	end
	return dst
end

local base = loadRGB(basePath)
local spr = Sprite(base.width, base.height, ColorMode.RGB)
app.transaction(function()
	local baseLayer = spr.layers[1]
	baseLayer.name = "base"
	spr:newCel(baseLayer, 1, base, Point(0, 0))

	for _, e in ipairs(entries) do
		local img = transform(loadRGB(e.file), e.w, e.h, e.flipX)
		local layer = spr:newLayer()
		layer.name = e.name
		layer.opacity = e.opacity
		spr:newCel(layer, 1, img, Point(e.x, e.y))
	end
end)

spr:saveAs(outPath)
if pngPath then
	spr:saveCopyAs(pngPath)
end
local result = {
	out = outPath,
	png = pngPath or "",
	width = spr.width,
	height = spr.height,
	layers = #spr.layers
}
spr:close()
print(json.encode(result))`, EscapeString(basePath), EscapeString(outPath), pngLua, defs.String())
}
