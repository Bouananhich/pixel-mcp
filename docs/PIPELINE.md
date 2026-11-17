# Asset Pipeline: compose → animate → export

Fork additions for turning a still background plus separate prop images into
game-ready animated spritesheets. The game draws the still background once and plays
each prop's sheet on top at the offset given in its JSON.

```
manifest.json ──compose_from_manifest──▶ scene.aseprite (base + 1 layer per prop)
                                            │
                       animate_layer (one call per prop layer)
                                            │
                    export_layers (split + trim + include_json)
                                            ▼
            props_neon.png / props_neon.json, props_crowd.png / …
```

## 1. Compose

`compose_from_manifest` with `manifest_path` = absolute path of:

```json
{
  "base": "background.png",
  "out": "build/club.aseprite",
  "png": "build/club_preview.png",
  "layers": [
    { "name": "neon",    "file": "props/neon_sign.png", "x": 40,  "y": 30 },
    { "name": "bulbs",   "file": "props/bulbs.png",     "x": 100, "y": 28, "opacity": 230 },
    { "name": "cabinet", "file": "props/cabinet.png",   "x": 300, "y": 500, "w": 20, "h": 30 },
    { "name": "crowd",   "file": "props/crowd.png",     "x": 200, "y": 700, "flipX": true }
  ]
}
```

- Relative paths resolve against the manifest's folder; absolute input paths are allowed.
- The sprite is RGB with the size of `base`; `base` becomes the bottom layer named `base`,
  then one layer per entry in order (later entries are on top).
- `w`/`h` resize with nearest-neighbour; give only one to keep the aspect ratio.
- `flipX` mirrors horizontally; `opacity` (0-255) is the layer opacity.
- `png` (optional) writes a flattened preview.
- `out`/`png` must stay inside the manifest's folder (or an `allowed_dirs` entry).

## 2. Animate

`animate_layer` changes **only** the named layer. Its frame-1 cel is the rest pose.
If the sprite has fewer than `frames` frames, new frames are appended as copies of
frame 1 on every other layer (so the background and other props stay put). Linked cels
on the animated layer are unlinked; other layers' cels are not touched.

| preset | drives | default | params |
|--------|--------|---------|--------|
| `flicker` | cel opacity | 6 frames: 255,255,140,255,60,255 | `opacities` list, or `random` + `min_opacity`/`max_opacity` + `seed` |
| `pulse` | RGB brightness (alpha kept, clamped) | 6 frames: 1, 1.125, 1.2165, 1.25, 1.2165, 1.125 | `multipliers` list, or `peak` |
| `bob` | cel y offset | 4 frames: dy 0,-1,-2,-1 | `amplitude`, or `offsets` [{x,y}] |
| `sway` | cel x offset | 4 frames: dx 0,1,0,-1 | `amplitude`, or `offsets` [{x,y}] |
| `cycle` | colour rotation of pixels matching `colors` | `len(colors)` frames | `colors` (hex, ≥2), `step` |

Common: `frames`, `frame_duration_ms` (sets frames 1..N), `tag_name` (re-creates a
forward tag over 1..N). Lists shorter than `frames` repeat.

Everything is deterministic: curves are computed in Go and returned in the tool output,
and random flicker uses a seeded PCG generator.

Because each preset drives a different cel attribute, presets on the same layer stack
(e.g. `pulse` then `bob`). Running the same preset again replaces its previous effect (keep the first value of explicit `offsets`/`multipliers` at the identity so frame 1 stays the rest pose).
`pulse`/`cycle` require an RGB sprite.

Note: frame durations and tags are sprite-wide in Aseprite. Props that share one
sprite share frame timing; use one frame count / duration for the scene's ambient loop,
or keep props with different rhythms in separate sprites.

Example calls:

```json
{ "sprite_path": "/abs/build/club.aseprite", "layer_name": "neon", "preset": "flicker",
  "frame_duration_ms": 100, "tag_name": "ambient" }
{ "sprite_path": "/abs/build/club.aseprite", "layer_name": "bulbs", "preset": "pulse", "peak": 1.3 }
{ "sprite_path": "/abs/build/club.aseprite", "layer_name": "cabinet", "preset": "bob", "frames": 6,
  "offsets": [{"x":0,"y":0},{"x":0,"y":-1},{"x":0,"y":-2}] }
{ "sprite_path": "/abs/build/club.aseprite", "layer_name": "crowd", "preset": "sway", "frames": 6 }
```

Use `set_layer_properties` for visibility, opacity, renames and blend modes
(e.g. `"blend_mode": "addition"` for glows).

## 3. Export

`export_layers`:

```json
{ "sprite_path": "/abs/build/club.aseprite",
  "layers": ["neon", "bulbs", "cabinet", "crowd"],
  "output_path": "/abs/build/sheets/props.png",
  "split": true, "trim": true, "include_json": true }
```

- `split: true` writes `props_<layer>.png` (+ `.json`) per layer next to `output_path`;
  `split: false` writes one sheet at `output_path` containing only the listed layers.
- Only the listed layers are rendered (requested layers are shown even if hidden in the
  file; all others are hidden). The source `.aseprite` is opened read-only and never saved.
- `trim: true` crops to the union of the visible pixels over **all** frames, so every frame
  cell has the same size and one offset positions the sheet.
- JSON is Aseprite's json-hash: `frames` (named `<layer> <n>`, with `duration` in ms),
  `meta.frameTags`, `meta.layers` (incl. `blendMode`), plus two added keys:
  - `meta.offset` `{x, y, w, h}`: where to draw each frame in the original sprite
  - `meta.spriteSize` `{w, h}`: the original sprite size
- Layer opacity and cel opacity are baked into the PNG. Blend modes cannot be baked into a
  transparent sheet: apply `meta.layers[0].blendMode` (e.g. `addition`) in the game.

Game side (pseudo code):

```
bg = load("club_background.png")
for prop in ["neon", "bulbs", "cabinet", "crowd"]:
    sheet = load(f"props_{prop}.png"); data = load_json(f"props_{prop}.json")
    pos = (data.meta.offset.x, data.meta.offset.y)
    play frames in order with frames[i].duration, drawn at pos over bg
```

## Path safety

`export_layers` and `compose_from_manifest` only write to absolute paths that resolve
(symlinks included) inside the sprite's / manifest's folder, or inside an `allowed_dirs`
entry of `~/.config/pixel-mcp/config.json`:

```json
{ "aseprite_path": "...", "allowed_dirs": ["/Users/me/Projects/band-deck/assets"] }
```

## run_lua (escape hatch)

`run_lua` executes arbitrary Aseprite Lua (`script`, optional `sprite_path` opened first
as `app.activeSprite`) and returns stdout. It is **disabled unless `allowed_dirs` is set**,
and `sprite_path` must be inside an allowed dir. The script itself is not sandboxed: it
can read and write any file Aseprite can. Prefer the dedicated tools and use this only
for one-off operations they don't cover; save explicitly with `spr:saveAs(spr.filename)`.
