package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func installFakeEncoder(t *testing.T, name, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake encoders use shell scripts")
	}
	bin := t.TempDir()
	path := filepath.Join(bin, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestRunConversions(t *testing.T) {
	tests := []struct {
		format string
		binary string
		script string
		flags  []string
	}{
		{"webp", "cwebp", "printf '%s\\n' \"$@\" >> \"$QIC_LOG\"\nprintf converted > \"$9\"\n", []string{"-lossless", "-exact", "-m", "6", "-q", "100"}},
		{"jxl", "cjxl", "printf '%s\\n' \"$@\" >> \"$QIC_LOG\"\nprintf converted > \"$2\"\n", []string{"-d", "0", "-e", "9"}},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			installFakeEncoder(t, tt.binary, tt.script)
			dir := t.TempDir()
			log := filepath.Join(dir, "args.log")
			t.Setenv("QIC_LOG", log)
			input := filepath.Join(dir, "photo.png")
			output := filepath.Join(dir, "photo."+tt.format)
			writeFile(t, input, "source")
			writeFile(t, output, "old")

			var stderr bytes.Buffer
			if err := run([]string{tt.format, input}, &stderr); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, output); got != "converted" {
				t.Fatalf("output = %q", got)
			}
			if got := readFile(t, input); got != "source" {
				t.Fatalf("input changed: %q", got)
			}
			args := strings.Split(strings.TrimSpace(readFile(t, log)), "\n")
			if tt.format == "webp" {
				if len(args) != 9 || !reflect.DeepEqual(args[:6], tt.flags) || args[6] != input || args[7] != "-o" {
					t.Fatalf("unexpected encoder arguments: %q", args)
				}
			} else if len(args) != 6 || args[0] != input || !reflect.DeepEqual(args[2:], tt.flags) {
				t.Fatalf("unexpected encoder arguments: %q", args)
			}
		})
	}
}

func TestGlobAndOutputDirectory(t *testing.T) {
	installFakeEncoder(t, "cwebp", "printf converted > \"$9\"\n")
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.png", "b.png"} {
		writeFile(t, filepath.Join(dir, name), "source")
	}
	if err := run([]string{"webp", filepath.Join(dir, "*.png"), out}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.webp", "b.webp"} {
		if got := readFile(t, filepath.Join(out, name)); got != "converted" {
			t.Fatalf("%s = %q", name, got)
		}
	}
}

func TestSingleInputOutputDirectory(t *testing.T) {
	installFakeEncoder(t, "cwebp", "printf converted > \"$9\"\n")
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "photo.png")
	writeFile(t, input, "source")
	for _, selection := range []string{input, filepath.Join(dir, "*.png")} {
		if err := run([]string{"webp", selection, out}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(out, "photo.webp")); got != "converted" {
			t.Fatalf("output = %q", got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "photo.webp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected output alongside input: %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "photo.png")
	writeFile(t, input, "source")
	second := filepath.Join(dir, "photo.jpg")
	writeFile(t, second, "second")
	sameFormat := filepath.Join(dir, "already.webp")
	writeFile(t, sameFormat, "source")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"usage", nil, "Usage:"},
		{"format", []string{"png", input}, "unsupported format"},
		{"missing input", []string{"webp", filepath.Join(dir, "*.missing")}, "no files match"},
		{"invalid glob", []string{"webp", filepath.Join(dir, "[")}, "invalid input glob"},
		{"directory input", []string{"webp", dir}, "not a regular file"},
		{"multiple output not directory", []string{"webp", filepath.Join(dir, "photo.*"), input}, "existing directory"},
		{"single output not directory", []string{"webp", input, second}, "existing directory"},
		{"missing output directory", []string{"webp", input, filepath.Join(dir, "missing")}, "inspect output directory"},
		{"colliding outputs", []string{"webp", filepath.Join(dir, "photo.*")}, "multiple inputs map"},
		{"overwrite input", []string{"webp", sameFormat, dir}, "would overwrite input"},
		{"empty output", []string{"webp", input, ""}, "outdir must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConversionFailurePreservesOutput(t *testing.T) {
	installFakeEncoder(t, "cwebp", "printf incomplete > \"$9\"\nprintf 'encoder failed\\n' >&2\nexit 7\n")
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	output := filepath.Join(dir, "input.webp")
	writeFile(t, input, "source")
	writeFile(t, output, "old")
	var stderr bytes.Buffer
	err := run([]string{"webp", input, dir}, &stderr)
	if err == nil || !strings.Contains(stderr.String(), "encoder failed") {
		t.Fatalf("error = %v, stderr = %q", err, stderr.String())
	}
	if got := readFile(t, output); got != "old" {
		t.Fatalf("output changed: %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".qic-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %q, error = %v", matches, err)
	}
}

func TestMissingEncoder(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	input := filepath.Join(t.TempDir(), "input.png")
	writeFile(t, input, "source")
	err := run([]string{"jxl", input}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cjxl is required") || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLiteralGlobCharacters(t *testing.T) {
	input := filepath.Join(t.TempDir(), "image[1].png")
	writeFile(t, input, "source")
	inputs, err := findInputs(input)
	if err != nil || len(inputs) != 1 || inputs[0] != input {
		t.Fatalf("inputs = %q, error = %v", inputs, err)
	}
}

func TestRealWebPLossless(t *testing.T) {
	if _, err := exec.LookPath("cwebp"); err != nil {
		t.Skip("cwebp not installed")
	}
	dwebp, err := exec.LookPath("dwebp")
	if err != nil {
		t.Skip("dwebp not installed")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 12, G: 34, B: 56, A: 0})
	img.SetNRGBA(1, 0, color.NRGBA{R: 78, G: 90, B: 123, A: 200})
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if err := run([]string{"webp", input}, &stderr); err != nil {
		t.Fatalf("%v: %s", err, &stderr)
	}
	decodedPath := filepath.Join(dir, "decoded.png")
	cmd := exec.Command(dwebp, filepath.Join(dir, "input.webp"), "-o", decodedPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decode: %v: %s", err, output)
	}
	f, err = os.Open(decodedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 2; x++ {
		got := color.NRGBAModel.Convert(decoded.At(x, 0)).(color.NRGBA)
		if want := img.NRGBAAt(x, 0); got != want {
			t.Errorf("pixel %d = %v, want %v", x, got, want)
		}
	}
}
