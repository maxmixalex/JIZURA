// Command jizura-render renders short JIZURA text effects headlessly.
// It embeds the upstream browser renderer; only Chromium and FFmpeg executables
// are needed (both are included in release packages). No Node/Python runtime.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed zh-hans/index.html tools/cli/render-bridge.js
var assets embed.FS

const timeout = 30 * time.Minute

type options struct {
	lyrics, lyricsFile, projectFile, output, format, style, title, aspect, lang string
	seed                                                                        int64
	fps, res, width, height                                                     int
	duration, start                                                             float64
	alpha, noHUD, overwrite, writeProject                                       bool
	browser, ffmpeg                                                             string
}

func parseFlags() (options, error) {
	o := options{}
	flag.StringVar(&o.lyrics, "text", "", "Text or lyrics to render. Use \\n for multiple lines.")
	flag.StringVar(&o.lyricsFile, "text-file", "", "UTF-8 text/LRC file (alternative to --text).")
	flag.StringVar(&o.projectFile, "project", "", "Existing .jizura.json project (preserves style/timing).")
	flag.StringVar(&o.output, "output", "", "Output movie path or PNG directory (required).")
	flag.StringVar(&o.format, "format", "auto", "auto | mp4 | qtrle | prores4444 | png")
	flag.StringVar(&o.style, "style", "", "Existing JIZURA style key, e.g. noir, crimson, paper.")
	flag.StringVar(&o.title, "title", "", "Optional title, overriding the project.")
	flag.StringVar(&o.aspect, "aspect", "", "16:9, 9:16, 1:1, 4:3, 3:4, 4:5, 21:9")
	flag.StringVar(&o.lang, "lang", "", "auto | ja | en | zh-Hans | zh-Hant | ko")
	flag.Int64Var(&o.seed, "seed", -1, "Fixed style random seed (-1 preserves project/default).")
	flag.IntVar(&o.fps, "fps", 0, "24, 30 or 60. Zero uses the project's FPS.")
	flag.IntVar(&o.res, "res", 0, "Output short-edge resolution (e.g. 720, 1080).")
	flag.IntVar(&o.width, "width", 0, "Override width in pixels; specify height too.")
	flag.IntVar(&o.height, "height", 0, "Override height in pixels; specify width too.")
	flag.Float64Var(&o.duration, "duration", 0, "Clip duration in seconds (0 = JIZURA plan length).")
	flag.Float64Var(&o.start, "start", 0, "Start time within the JIZURA plan, in seconds.")
	flag.BoolVar(&o.alpha, "alpha", false, "Preserve transparency (required for transparent PNG). MOV alpha formats imply this.")
	flag.BoolVar(&o.noHUD, "no-hud", false, "Disable JIZURA's decorative on-screen HUD.")
	flag.BoolVar(&o.overwrite, "overwrite", false, "Overwrite existing output file / PNG frame directory.")
	flag.BoolVar(&o.writeProject, "write-project", false, "Also save reproducible .jizura.json project beside the movie.")
	flag.StringVar(&o.browser, "chromium", "", "Custom Chromium or chrome-headless-shell executable path.")
	flag.StringVar(&o.ffmpeg, "ffmpeg", "", "Custom FFmpeg executable path.")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), `JIZURA native renderer — headless text-effect clips for video editors

Examples:
  jizura-render --text "一瞬だけ" --duration 2 --seed 42 --format qtrle --output title.mov
  jizura-render --text "KINETIC TYPE" --alpha --format png --output ./frames
  jizura-render --project scene.jizura.json --format prores4444 --output clip.mov

No browser UI is opened. Release packages include Chromium and FFmpeg.

Options:`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if o.lyrics != "" {
		o.lyrics = strings.ReplaceAll(o.lyrics, `\n`, "\n")
	}
	n := 0
	for _, v := range []string{o.lyrics, o.lyricsFile, o.projectFile} {
		if v != "" {
			n++
		}
	}
	if n != 1 {
		return o, errors.New("supply exactly one of --text, --text-file or --project")
	}
	if o.output == "" {
		return o, errors.New("--output is required")
	}
	if o.duration < 0 || o.start < 0 {
		return o, errors.New("--duration and --start must be nonnegative")
	}
	if o.fps != 0 && o.fps != 24 && o.fps != 30 && o.fps != 60 {
		return o, errors.New("--fps must be 24, 30 or 60")
	}
	if (o.width == 0) != (o.height == 0) {
		return o, errors.New("--width and --height must be specified together")
	}
	if o.width < 0 || o.height < 0 || o.res < 0 {
		return o, errors.New("resolution cannot be negative")
	}
	if o.width > 0 && (o.width%2 != 0 || o.height%2 != 0 || o.width < 16 || o.height < 16) {
		return o, errors.New("width/height must be even and >=16")
	}
	if o.res > 0 && (o.res < 16 || o.res > 8192) {
		return o, errors.New("--res must be 16..8192")
	}
	if o.aspect != "" && !map[string]bool{"16:9": true, "9:16": true, "4:3": true, "3:4": true, "1:1": true, "4:5": true, "21:9": true}[o.aspect] {
		return o, fmt.Errorf("invalid aspect: %s", o.aspect)
	}
	if o.lang != "" && !map[string]bool{"auto": true, "ja": true, "en": true, "ko": true, "zh-Hant": true, "zh-Hans": true}[o.lang] {
		return o, fmt.Errorf("invalid language: %s", o.lang)
	}
	switch strings.ToLower(o.format) {
	case "auto":
		switch strings.ToLower(filepath.Ext(o.output)) {
		case ".mp4":
			o.format = "mp4"
		case ".mov":
			o.format = "qtrle"
		default:
			o.format = "png"
		}
	case "mp4", "h264":
		o.format = "mp4"
	case "qtrle", "animation":
		o.format = "qtrle"
	case "prores", "prores4444":
		o.format = "prores4444"
	case "png", "png-alpha":
		if o.format == "png-alpha" {
			o.alpha = true
		}
		o.format = "png"
	default:
		return o, fmt.Errorf("unsupported --format %q", o.format)
	}
	if o.format == "mp4" && o.alpha {
		return o, errors.New("H.264 MP4 does not preserve alpha; use --format qtrle, prores4444 or png --alpha")
	}
	if o.format == "qtrle" || o.format == "prores4444" {
		o.alpha = true
	}
	if o.format != "png" && filepath.Ext(o.output) == "" {
		return o, errors.New("movie output needs a file extension")
	}
	if o.format == "png" && filepath.Ext(o.output) == ".png" {
		return o, errors.New("PNG format writes a sequence into a directory; --output must be a directory")
	}
	if o.format == "mp4" && strings.ToLower(filepath.Ext(o.output)) != ".mp4" {
		return o, errors.New("MP4 output must end in .mp4")
	}
	if (o.format == "qtrle" || o.format == "prores4444") && strings.ToLower(filepath.Ext(o.output)) != ".mov" {
		return o, errors.New("transparent movie output must end in .mov")
	}
	if o.lyricsFile != "" {
		p, err := os.ReadFile(o.lyricsFile)
		if err != nil {
			return o, err
		}
		o.lyrics = strings.TrimPrefix(string(p), "\ufeff")
	}
	return o, nil
}

type jobConfig struct {
	Project      json.RawMessage `json:"project"`
	Lyrics       *string         `json:"lyrics"`
	Style        *string         `json:"style"`
	Title        *string         `json:"title"`
	Aspect       *string         `json:"aspect"`
	Lang         *string         `json:"lang"`
	Seed         *int64          `json:"seed"`
	FPS          *int            `json:"fps"`
	Res          *int            `json:"res"`
	Width        int             `json:"width"`
	Height       int             `json:"height"`
	Start        float64         `json:"start"`
	Duration     float64         `json:"duration"`
	Alpha        bool            `json:"alpha"`
	NoHUD        bool            `json:"noHud"`
	StartAtZero  bool            `json:"startAtZero"`
	WriteProject bool            `json:"writeProject"`
	MaxFrames    int             `json:"maxFrames"`
}

func ptr[T any](v T) *T { return &v }
func configFor(o options) (jobConfig, error) {
	c := jobConfig{Start: o.start, Duration: o.duration, Width: o.width, Height: o.height, Alpha: o.alpha, NoHUD: o.noHUD,
		StartAtZero: o.projectFile == "", WriteProject: o.writeProject, MaxFrames: 60 * 60 * 15}
	if o.lyrics != "" {
		c.Lyrics = ptr(o.lyrics)
	}
	if o.style != "" {
		c.Style = ptr(o.style)
	}
	if o.title != "" {
		c.Title = ptr(o.title)
	}
	if o.lang != "" {
		c.Lang = ptr(o.lang)
	}
	if o.aspect != "" {
		c.Aspect = ptr(o.aspect)
	}
	if o.seed != -1 {
		c.Seed = ptr(o.seed)
	}
	if o.fps != 0 {
		c.FPS = ptr(o.fps)
	}
	if o.res != 0 {
		c.Res = ptr(o.res)
	}
	if o.projectFile != "" {
		b, err := os.ReadFile(o.projectFile)
		if err != nil {
			return c, err
		}
		if !json.Valid(b) {
			return c, errors.New("project is not valid JSON")
		}
		c.Project = b
	}
	return c, nil
}

// Runtime executables are next to the CLI in packaged distributions; PATH
// fallback is intentional for development and installed Chromium/FFmpeg.
func resolveBinary(explicit, name string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err = os.Stat(abs); err != nil {
			return "", err
		}
		return abs, nil
	}
	exe, _ := os.Executable()
	base := filepath.Dir(exe)
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	candidates := []string{}
	if name == "chromium" {
		for _, n := range []string{"chrome-headless-shell", "chrome", "chromium"} {
			candidates = append(candidates, filepath.Join(base, "runtime", "chrome", n+ext), filepath.Join(base, n+ext))
		}
	} else {
		candidates = append(candidates, filepath.Join(base, "runtime", "ffmpeg"+ext), filepath.Join(base, "ffmpeg"+ext))
	}
	for _, p := range candidates {
		if f, e := os.Stat(p); e == nil && !f.IsDir() {
			return p, nil
		}
	}
	names := []string{name}
	if name == "chromium" {
		names = []string{"chromium", "chromium-browser", "google-chrome", "chrome-headless-shell"}
	}
	for _, n := range names {
		if p, e := exec.LookPath(n); e == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("cannot find %s; use --%s or place it under runtime/", name, map[bool]string{true: "chromium", false: "ffmpeg"}[name == "chromium"])
}

type info struct {
	Frames       int             `json:"frames"`
	Width        int             `json:"width"`
	Height       int             `json:"height"`
	FPS          int             `json:"fps"`
	Duration     float64         `json:"duration"`
	PlanDuration float64         `json:"planDuration"`
	Transparent  bool            `json:"transparent"`
	Project      json.RawMessage `json:"project,omitempty"`
}
type outputSink struct {
	opts         options
	index, count int
	stdin        io.WriteCloser
	encoder      *exec.Cmd
	encoderErr   bytes.Buffer
	project      json.RawMessage
}

func (s *outputSink) start(meta info) error {
	if meta.Frames <= 0 || meta.Frames > 60*60*15 || meta.Width < 16 || meta.Height < 16 || meta.Width > 8192 || meta.Height > 8192 || meta.FPS <= 0 {
		return errors.New("invalid render dimensions/fps")
	}
	s.count = meta.Frames
	s.project = meta.Project
	if s.opts.format == "png" {
		if err := os.MkdirAll(s.opts.output, 0755); err != nil {
			return err
		}
	} else {
		ff, err := resolveBinary(s.opts.ffmpeg, "ffmpeg")
		if err != nil {
			return err
		}
		cmd := exec.Command(ff, ffmpegArgs(s.opts, meta)...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		cmd.Stdout = io.Discard
		cmd.Stderr = &s.encoderErr
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			return fmt.Errorf("start FFmpeg: %w", err)
		}
		s.encoder = cmd
		s.stdin = stdin
	}
	log.Printf("Rendering %d frames at %dx%d, %d fps (%.2f s; plan %.2f s), format=%s alpha=%v", meta.Frames, meta.Width, meta.Height, meta.FPS, meta.Duration, meta.PlanDuration, s.opts.format, meta.Transparent)
	return nil
}
func (s *outputSink) writeFrame(i int, b []byte) error {
	if i != s.index || s.index >= s.count {
		return fmt.Errorf("unexpected frame %d, wanted %d", i, s.index)
	}
	if len(b) < 8 || !bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return errors.New("invalid PNG signature")
	}
	if s.opts.format == "png" {
		path := filepath.Join(s.opts.output, fmt.Sprintf("jizura_%05d.png", i))
		if e := os.WriteFile(path, b, 0644); e != nil {
			return e
		}
	} else {
		if _, e := s.stdin.Write(b); e != nil {
			return fmt.Errorf("FFmpeg pipe: %s; %s", e, s.encoderErr.String())
		}
	}
	s.index++
	if s.index == 1 || s.index%15 == 0 || s.index == s.count {
		log.Printf("frames: %d/%d", s.index, s.count)
	}
	return nil
}
func (s *outputSink) finish() error {
	if s.index != s.count {
		return fmt.Errorf("incomplete render: %d/%d frames", s.index, s.count)
	}
	if s.encoder != nil {
		if err := s.stdin.Close(); err != nil {
			return err
		}
		s.stdin = nil
		if err := s.encoder.Wait(); err != nil {
			return fmt.Errorf("FFmpeg: %w\n%s", err, s.encoderErr.String())
		}
		s.encoder = nil
	}
	if s.opts.writeProject && len(s.project) > 0 {
		name := s.opts.output + ".jizura.json"
		if s.opts.format == "png" {
			name = filepath.Join(s.opts.output, "project.jizura.json")
		}
		pretty := new(bytes.Buffer)
		if e := json.Indent(pretty, s.project, "", "  "); e != nil {
			return e
		}
		if err := os.WriteFile(name, pretty.Bytes(), 0644); err != nil {
			return fmt.Errorf("save project: %w", err)
		}
	}
	return nil
}
func (s *outputSink) abort() {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.encoder != nil {
		_ = s.encoder.Process.Kill()
		_ = s.encoder.Wait()
	}
}
func ffmpegArgs(o options, m info) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-f", "image2pipe", "-framerate", strconv.Itoa(m.FPS), "-c:v", "png", "-i", "pipe:0", "-an"}
	switch o.format {
	case "mp4":
		args = append(args, "-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p", "-movflags", "+faststart")
	case "qtrle":
		args = append(args, "-c:v", "qtrle", "-pix_fmt", "argb", "-g", "1", "-f", "mov")
	case "prores4444":
		args = append(args, "-c:v", "prores_ks", "-profile:v", "4", "-pix_fmt", "yuva444p10le", "-alpha_bits", "16", "-qscale:v", "8", "-f", "mov")
	}
	return append(args, o.output)
}

func run(o options) error {
	if !o.overwrite {
		if _, e := os.Stat(o.output); e == nil {
			return fmt.Errorf("output already exists: %s (use --overwrite)", o.output)
		}
	}
	abs, e := filepath.Abs(o.output)
	if e != nil {
		return e
	}
	o.output = abs
	if o.format != "png" {
		if e := os.MkdirAll(filepath.Dir(o.output), 0755); e != nil {
			return e
		}
	}
	cfg, e := configFor(o)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(cfg)
	if e != nil {
		return e
	}
	html, e := assets.ReadFile("zh-hans/index.html")
	if e != nil {
		return e
	}
	bridge, e := assets.ReadFile("tools/cli/render-bridge.js")
	if e != nil {
		return e
	}
	sink := &outputSink{opts: o}
	defer sink.abort()
	browser, e := resolveBinary(o.browser, "chromium")
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	profile, e := os.MkdirTemp("", "jizura-cli-chrome-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(profile)
	flags := []string{"--headless", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-extensions", "--remote-allow-origins=*", "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"}
	cmd := exec.CommandContext(ctx, browser, flags...)
	var chromeLog bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &chromeLog
	if e := cmd.Start(); e != nil {
		return fmt.Errorf("start Chromium: %w", e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var port int
	for i := 0; i < 120; i++ {
		data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			line := strings.SplitN(string(data), "\n", 2)[0]
			port, _ = strconv.Atoi(line)
			if port > 0 {
				break
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(100 * time.Millisecond)
	}
	if port == 0 {
		return fmt.Errorf("Chromium debugging port was not created; %s", chromeLog.String())
	}
	endpoint, e := waitCDP(port)
	if e != nil {
		return e
	}
	c, e := connectCDP(endpoint)
	if e != nil {
		return e
	}
	defer c.close()
	if _, e = c.call("Page.enable", map[string]any{}); e != nil {
		return e
	}
	if _, e = c.call("Runtime.enable", map[string]any{}); e != nil {
		return e
	}
	frameTreeRaw, err := c.call("Page.getFrameTree", map[string]any{})
	if err != nil {
		return err
	}
	var ft struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := json.Unmarshal(frameTreeRaw, &ft); err != nil {
		return err
	}
	if ft.FrameTree.Frame.ID == "" {
		return errors.New("CDP missing page frame ID")
	}
	if _, err = c.call("Page.setDocumentContent", map[string]any{"frameId": ft.FrameTree.Frame.ID, "html": string(html)}); err != nil {
		return fmt.Errorf("embed JIZURA HTML: %w", err)
	}
	loaded := false
	var lastEval string
	for i := 0; i < 120; i++ {
		v, err := c.evaluate(`typeof J !== "undefined" && !!(J.ui && J.ui.project && J.plan && J.Renderer && J.defaultProject)`, false)
		if err != nil {
			lastEval = err.Error()
		} else {
			lastEval = string(v)
		}
		if lastEval == "true" {
			loaded = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !loaded {
		state, _ := c.evaluate(`JSON.stringify({ready:document.readyState,title:document.title,url:location.href,j:typeof J})`, false)
		return fmt.Errorf("JIZURA frontend failed to initialize: last=%s state=%s Chromium: %.400s", lastEval, state, chromeLog.String())
	}
	expr := string(bridge) + "\n__jizuraInit(" + string(raw) + ")"
	v, err := c.evaluate(expr, true)
	if err != nil {
		return fmt.Errorf("JIZURA init: %w", err)
	}
	var meta info
	if err := json.Unmarshal(v, &meta); err != nil {
		return err
	}
	if err := sink.start(meta); err != nil {
		return err
	}
	for i := 0; i < meta.Frames; i++ {
		rawFrame, err := c.evaluate(fmt.Sprintf("__jizuraFrame(%d)", i), false)
		if err != nil {
			return fmt.Errorf("render frame %d: %w", i, err)
		}
		var dataURL string
		if err := json.Unmarshal(rawFrame, &dataURL); err != nil {
			return fmt.Errorf("frame %d decode JSON: %w", i, err)
		}
		constPrefix := "data:image/png;base64,"
		if !strings.HasPrefix(dataURL, constPrefix) {
			return fmt.Errorf("frame %d was not a PNG data URL", i)
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, constPrefix))
		if err != nil {
			return fmt.Errorf("frame %d base64: %w", i, err)
		}
		if err := sink.writeFrame(i, data); err != nil {
			return err
		}
	}
	if err := sink.finish(); err != nil {
		return err
	}
	log.Printf("Saved %s", o.output)
	return nil
}
func main() {
	log.SetFlags(0)
	o, err := parseFlags()
	if err == nil {
		err = run(o)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "jizura-render:", err)
		os.Exit(1)
	}
}
