package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFFmpegArgumentsAlpha(t *testing.T) {
	cases := []struct{ format, want string }{
		{"mp4", "yuv420p"},
		{"qtrle", "argb"},
		{"prores4444", "yuva444p10le"},
	}
	for _, tt := range cases {
		a := strings.Join(ffmpegArgs(options{format: tt.format, output: "test.mov"}, info{FPS: 24}), " ")
		if !strings.Contains(a, "-pix_fmt "+tt.want) {
			t.Fatalf("%s: %s", tt.format, a)
		}
		if strings.Contains(a, " -i pipe:0 ") == false {
			t.Fatalf("missing frame pipe in %s", a)
		}
	}
}
func TestEmbeddedEngineIsPresent(t *testing.T) {
	h, err := assets.ReadFile("zh-hans/index.html")
	if err != nil || len(h) < 100_000 {
		t.Fatalf("missing embedded JIZURA: size=%d err=%v", len(h), err)
	}
	js, err := assets.ReadFile("tools/cli/render-bridge.js")
	if err != nil || !strings.Contains(string(js), "J.Renderer") {
		t.Fatalf("missing render bridge: %v", err)
	}
}

func TestTechniqueArgumentsArePreserved(t *testing.T) {
	o := options{lyrics: "取笑着黑", tech: repeatable{"layout=huge", "enter=type", "treat=outline", "fx=flash"}}
	c, err := configFor(o)
	if err != nil {
		t.Fatal(err)
	}
	if c.Techniques["layout"] != "huge" || c.Techniques["enter"] != "type" || c.Techniques["fx"] != "flash" {
		t.Fatalf("lost technique choice: %#v", c.Techniques)
	}
	if validTechniqueGroup("nonsense", true) {
		t.Fatal("unexpected group accepted")
	}
}
func TestFractionalFramerate(t *testing.T) {
	for _, tc := range []struct {
		input    float64
		expected string
	}{
		{23.976, "24000/1001"}, {29.97, "30000/1001"},
		{59.94, "60000/1001"}, {25, "25"}, {50, "50"}, {120, "120"},
	} {
		value := fpsString(normalizedFPS(tc.input))
		if value != tc.expected {
			t.Errorf("fps %g: expected %s got %s", tc.input, tc.expected, value)
		}
	}
	if !math.IsNaN(math.NaN()) {
		t.Fatal("Go math unavailable")
	}
}
func TestFontFileLoadingAndMissingFile(t *testing.T) {
	if _, err := resolveFontChoice("absent-file.ttf"); err == nil {
		t.Fatal("missing file should be rejected")
	}
	if spec, err := resolveFontChoice("Arial"); err != nil || spec.Name != "Arial" {
		t.Fatalf("named font failed: %#v %v", spec, err)
	}
	path := filepath.Join(t.TempDir(), "test.otf")
	if err := os.WriteFile(path, []byte("font-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := resolveFontChoice(path)
	if err != nil || spec.Filename != "test.otf" || spec.Base64 == "" {
		t.Fatalf("font file failed: %#v %v", spec, err)
	}
	c, err := configFor(options{lyrics: "HELLO", font: path})
	if err != nil || len(c.Fonts) != 3 {
		t.Fatalf("font roles failed: %+v %v", c.Fonts, err)
	}
}
