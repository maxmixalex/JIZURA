# Native JIZURA CLI — short text effects for Premiere

The JIZURA web editor remains unchanged. This additional **Go-native command-line frontend** embeds `zh-hans/index.html` and calls the existing `J.plan` and `J.Renderer.frame` functions through Chromium DevTools Protocol. It draws one Canvas frame at a time and sends PNG bytes directly to the FFmpeg stdin pipe (or to individual PNG files). No GUI, image ZIP intermediate, Python, Node, or network service is required when running the bundled distribution.

## Install

1. From **Actions → Native CLI (Windows / Linux)**, download the `jizura-render-windows-x64` or `jizura-render-linux-x64` artifact.
2. Extract the ZIP **completely**, retaining the `runtime/` directory next to `jizura-render.exe` / `jizura-render`.
3. Run in a terminal from that folder. System Chromium and FFmpeg are not needed when the `runtime/` binaries are present. On Linux, ordinary system libraries required by Chromium are still needed.

If building locally, run `go build -o jizura-render .` from the repository root (Go 1.22+). This produces an embedded renderer, but you must also supply Chromium + FFmpeg on the PATH, or pass `--chromium` / `--ffmpeg`. `go build` by itself does *not* download these separate executables.

### Examples

```powershell
# Single-file transparent video; PR 2015.4+ (10.4) can natively import Animation MOV
.\jizura-render.exe --text "取笑着黑" --duration 2 --style noir --seed 42 --fps 30 --res 1080 --format qtrle --output "take.mov"

# Lossless transparent PNG sequence: robust fallback in older Premiere builds
.\jizura-render.exe --text "让文字动起来" --duration 1.5 --fps 24 --format png --alpha --res 1080 --output "frames"

# ProRes 4444 MOV with an alpha channel (test decoder availability in your PR version)
.\jizura-render.exe --text "鬼" --duration 2 --format prores4444 --res 1080 --output "ghost.mov"

# Ordinary, opaque H.264 MP4
.\jizura-render.exe --text "The Present" --duration 2 --lang en --format mp4 --output "opaque.mp4"

# Reuse a project already saved in the JIZURA browser editor
.\jizura-render.exe --project "scene.jizura.json" --format qtrle --output "scene.mov" --write-project
```

`--text` supports literal `\\n` line breaks. `--text-file` accepts UTF-8 text or timestamped LRC. Use exactly one of `--text`, `--text-file`, or `--project`. Other useful flags: `--aspect 9:16`, `--style`, `--seed`, `--fps 24|30|60`, `--res`, `--width` + `--height`, `--start` (trim), `--duration`, `--lang ja|en|zh-Hans|zh-Hant|ko|auto`, `--no-hud`, `--overwrite`.

The CLI uses a stable seed when specified. Without `--duration`, it exports the planned duration. When exporting text (not a saved project) it removes the browser editor's default lead-in to suit short clips. Imported projects retain their saved timing. Audio is intentionally not exported; sync/mix with your soundtrack in the NLE. If a selected period extends beyond the planned animation, JIZURA naturally renders an empty tail.

## Formats and Alpha

| CLI format | File/container | Alpha | Typical use |
|---|---|---|---|
| `mp4` | H.264, YUV 4:2:0 | **No** | Review/full-frame clips |
| `qtrle` | QuickTime Animation, ARGB | **Yes**, lossless | Premiere 2015.4+ transparent overlay clips |
| `prores4444` | QuickTime ProRes 4444, YUVA | **Yes**, visually near-lossless | Intermediate / modern NLEs |
| `png --alpha` | RGBA PNG image sequence | **Yes**, lossless | Most compatible route with legacy Premiere |

`--format qtrle` and `--format prores4444` imply `--alpha`. `--format mp4 --alpha` produces an error rather than silently flattening transparent artwork. Output file suffixes must match their format.

Premiere usage: import the `.mov` clip and place it on a higher video track. Do not use green-screen keying. For the PNG sequence, select the first image (`jizura_00000.png`) with **Image Sequence** checked in Premiere's import dialog; set the assumed frame rate to the CLI export FPS.

**Version-specific Adobe detail:** Premiere Pro CC 2015.4 (10.4) added native QuickTime Animation and QuickTime PNG import without needing QuickTime 7 on Windows. Earlier Premiere Pro 2015 versions may require QuickTime components, and import behavior can vary. If in doubt use PNG sequence; test QuickTime Animation against your exact installed version.

## Implementation notes and limitations

- The native binary is not a reimplementation of the animation math. It controls the real, unmodified JIZURA HTML/Canvas engine via CDP, loaded entirely in memory, without navigating to localhost.
- FFmpeg receives **one PNG frame at a time**, avoiding the in-browser ZIP → download → unpack round trip. Frame encoding is done by FFmpeg, not the browser WebCodecs API.
- The browser runner is still a separate Chromium headless runtime, **bundled in the CI artifact**; this is a native/offline distributable, not a pure-Go replacement for Canvas.
- Actual fonts are subject to installation and availability. Offline environments use OS font fallbacks; supply appropriate CJK fonts to match a chosen look. Embedded audio and user-supplied browser font files are not yet implemented by the CLI.
- Render length is limited to 54,000 frames per call, although these exports are intended for short clips. The binary has a 30-minute safety timeout.
- FFmpeg bundled with release artifacts may be GPL-licensed depending on the build; consult `THIRD_PARTY_NOTICES.txt` before redistribution.
