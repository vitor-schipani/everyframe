package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vitor-schipani/everyframe/internal/corpus"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		want       int
		wantStdout string
		wantStderr string
	}{
		{"no command", nil, 2, "", "usage:"},
		{"help", []string{"help"}, 0, "usage:", ""},
		{"unknown command", []string{"bogus"}, 2, "", `unknown command "bogus"`},
		{"command help", []string{"fetch", "-h"}, 0, "", "-limit"},
		{"bad flag", []string{"verify", "-nope"}, 1, "", "-nope"},
		{"missing manifest", []string{"verify", "-manifest", "does-not-exist.json"}, 1, "", "does-not-exist.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(context.Background(), tc.args, &stdout, &stderr); got != tc.want {
				t.Errorf("exit code = %d, want %d (stderr: %s)", got, tc.want, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tc.wantStdout)
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

func TestRunFailsWhenOutputCannotBeWritten(t *testing.T) {
	var stderr bytes.Buffer
	if got := run(context.Background(), []string{"help"}, failingWriter{}, &stderr); got != 1 {
		t.Errorf("exit code = %d, want 1 when stdout is broken", got)
	}
}

// writeCorpus serves two small files and writes a manifest describing them.
func writeCorpus(t *testing.T) (manifest, dir string) {
	t.Helper()
	bodies := map[string][]byte{"/a.gif": []byte("first file"), "/b.gif": []byte("second file!")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)

	m := &corpus.Manifest{}
	for _, name := range []string{"a", "b"} {
		body := bodies["/"+name+".gif"]
		sum := sha1.Sum(body)
		m.Entries = append(m.Entries, corpus.Entry{
			Title: "File:" + name + ".gif",
			URL:   srv.URL + "/" + name + ".gif",
			SHA1:  hex.EncodeToString(sum[:]),
			Size:  int64(len(body)),
		})
	}
	root := t.TempDir()
	manifest = filepath.Join(root, "manifest.json")
	if err := m.Save(manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, filepath.Join(root, "files")
}

// runOK runs the tool and returns its stdout, failing on a non-zero exit.
func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("%v: exit code %d\nstdout: %s\nstderr: %s", args, code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestFetchAndVerifyOutput(t *testing.T) {
	manifest, dir := writeCorpus(t)

	// One download at a time keeps the progress lines in manifest order.
	got := runOK(t, "fetch", "-manifest", manifest, "-dir", dir, "-jobs", "1")
	want := "==> Fetching 2 GIFs (22 B) into " + dir + "\n" +
		"  [1/2]      10 B  a.gif\n" +
		"  [2/2]      12 B  b.gif\n" +
		"  ✓ 2 downloaded (22 B), 0 already present\n"
	if got != want {
		t.Errorf("first fetch output:\n%s\nwant:\n%s", got, want)
	}

	got = runOK(t, "fetch", "-manifest", manifest, "-dir", dir)
	want = "==> Fetching 2 GIFs (22 B) into " + dir + "\n" +
		"  ✓ 0 downloaded (0 B), 2 already present\n"
	if got != want {
		t.Errorf("second fetch output:\n%s\nwant:\n%s", got, want)
	}

	got = runOK(t, "verify", "-manifest", manifest, "-dir", dir)
	want = "==> Verifying " + dir + " against " + manifest + "\n" +
		"  ✓ 2 of 2 files ok\n"
	if got != want {
		t.Errorf("verify output:\n%s\nwant:\n%s", got, want)
	}
}

func TestVerifyReportsProblems(t *testing.T) {
	manifest, dir := writeCorpus(t)
	runOK(t, "fetch", "-manifest", manifest, "-dir", dir, "-limit", "1")
	if err := os.WriteFile(filepath.Join(dir, "stray.gif"), []byte("?"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"verify", "-manifest", manifest, "-dir", dir}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	for _, want := range []string{"✗ missing  ", "  b.gif\n", "! not in manifest: stray.gif\n", "✗ 1 of 2 files ok, 1 missing, 0 corrupt\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("verify output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestOutputStyles(t *testing.T) {
	var plain, colored bytes.Buffer
	(&output{w: &plain}).header("Fetching %d", 3)
	(&output{w: &colored, color: true}).header("Fetching %d", 3)
	if got := plain.String(); got != "==> Fetching 3\n" {
		t.Errorf("plain header = %q", got)
	}
	if got := colored.String(); got != "\x1b[1;36m==>\x1b[0m \x1b[1mFetching 3\x1b[0m\n" {
		t.Errorf("colored header = %q", got)
	}
	// A buffer is not a terminal, so it must never get escape codes.
	if newOutput(&plain).color {
		t.Error("colour enabled for a non-terminal writer")
	}
}
