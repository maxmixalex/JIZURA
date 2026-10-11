package main

import (
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
