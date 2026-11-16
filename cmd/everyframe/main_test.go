package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	stdgif "image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testGIF returns a three-frame, 8x6 animation that loops forever. The
// second frame is a 3x2 patch at (2,1).
func testGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White, color.RGBA{R: 0xFF, A: 0xFF}, color.RGBA{B: 0xFF, A: 0xFF}}
	g := &stdgif.GIF{}
	for i, r := range []image.Rectangle{image.Rect(0, 0, 8, 6), image.Rect(2, 1, 5, 3), image.Rect(0, 0, 8, 6)} {
		img := image.NewPaletted(r, pal)
		for j := range img.Pix {
			img.Pix[j] = uint8(i + 1)
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10*(i+1))
		g.Disposal = append(g.Disposal, stdgif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := stdgif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeFile stores data in a temporary directory and returns its path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCLI runs the command line and returns its exit code and output.
func runCLI(stdin []byte, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, bytes.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodes(t *testing.T) {
	good := writeFile(t, "good.gif", testGIF(t))
	notGIF := writeFile(t, "not.gif", []byte("this is not a GIF"))
	tests := []struct {
		name       string
		args       []string
		want       int
		wantStderr string // substring; empty means stderr must be empty
	}{
		{"no arguments", nil, 2, "usage: everyframe"},
		{"help", []string{"help"}, 0, ""},
		{"unknown command", []string{"explode"}, 2, `unknown command "explode"`},
		{"command help", []string{"inspect", "-h"}, 0, "usage: everyframe inspect"},
		{"unknown flag", []string{"inspect", "-bogus", good}, 2, "flag provided but not defined"},
		{"no file", []string{"inspect"}, 2, "expected one file, got 0"},
		{"two files", []string{"inspect", good, good}, 2, "expected one file, got 2"},
		{"missing file", []string{"inspect", filepath.Join(t.TempDir(), "nope.gif")}, 1, "no such file"},
		{"not a gif", []string{"inspect", notGIF}, 1, "not a GIF"},
		{"limit exceeded", []string{"inspect", "-max-frames", "2", good}, 1, "frames limit exceeded"},
		{"good file", []string{"inspect", good}, 0, ""},
		{"flag after the file", []string{"inspect", good, "-json"}, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCLI(nil, tt.args...)
			if code != tt.want {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.want, stderr)
			}
			if tt.wantStderr == "" && stderr != "" {
				t.Errorf("unexpected stderr: %s", stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
			// A mistake is explained once, not once by the flag
			// package and again by us.
			if n := strings.Count(stderr, "flag provided but not defined"); n > 1 {
				t.Errorf("flag error printed %d times", n)
			}
		})
	}
}

func TestInspectText(t *testing.T) {
	path := writeFile(t, "anim.gif", append(testGIF(t), "PK\x03\x04 appended"...))
	code, stdout, stderr := runCLI(nil, "inspect", path)
	if code != 0 || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"format          GIF89a",
		"canvas          8x6",
		"frames          3",
		"duration        600ms",
		"loop count      0 (forever)",
		`"NETSCAPE2.0"`,
		"trailer         present",
		`trailing data   13 bytes, starting "PK\x03\x04 appended"`,
		"1      ", "3x2", "2,1", "200ms",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestInspectJSON(t *testing.T) {
	code, stdout, stderr := runCLI(testGIF(t), "inspect", "-json", "-")
	if code != 0 || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	var rep report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if rep.File != "-" || rep.Error != "" || rep.Header == nil || rep.Header.Width != 8 || rep.Header.Height != 6 {
		t.Errorf("report = %+v, header = %+v", rep, rep.Header)
	}
	if len(rep.Frames) != 3 || rep.DurationMS != 600 {
		t.Fatalf("%d frames lasting %d ms, want 3 lasting 600", len(rep.Frames), rep.DurationMS)
	}
	if f := rep.Frames[1]; f.X != 2 || f.Y != 1 || f.Width != 3 || f.Height != 2 || f.DelayMS != 200 || f.Disposal != "keep" {
		t.Errorf("frame 1 = %+v", f)
	}
	if lc := rep.Metadata.LoopCount; lc == nil || *lc != 0 {
		t.Errorf("loop count = %v, want 0", lc)
	}
	// Empty lists are [] and absent values null, so consumers need no
	// special cases.
	for _, want := range []string{`"flags": []`, `"comments": []`, `"transparent_index": null`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("JSON lacks %s", want)
		}
	}
}

// A file that stops part-way is reported as far as it goes, with the error
// in the report and a failing exit code.
func TestInspectTruncated(t *testing.T) {
	data := testGIF(t)
	path := writeFile(t, "cut.gif", data[:len(data)-12])
	code, stdout, stderr := runCLI(nil, "inspect", "-json", path)
	if code != 1 || !strings.Contains(stderr, "truncated") {
		t.Errorf("exit code %d, stderr %q; want 1 and a truncation message", code, stderr)
	}
	var rep report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(rep.Error, "truncated") || len(rep.Frames) < 2 {
		t.Errorf("error %q with %d frames, want a truncation error after at least 2 frames", rep.Error, len(rep.Frames))
	}

	_, stdout, _ = runCLI(nil, "inspect", path)
	if !strings.Contains(stdout, "trailer         not reached") {
		t.Errorf("text report does not say the trailer was not reached:\n%s", stdout)
	}
}
