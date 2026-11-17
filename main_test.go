package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
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

const fakeMagick = "for output do :; done\nprintf converted > \"$output\"\n"

func TestRunConversions(t *testing.T) {
	tests := []struct {
		format string
		flags  []string
	}{
		{"webp", []string{"-quality", "100", "-define", "webp:lossless=true", "-define", "webp:exact=true", "-define", "webp:method=6"}},
		{"jxl", []string{"-quality", "100", "-define", "jxl:effort=9"}},
		{"avif", []string{}},
		{"jpeg", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			installFakeEncoder(t, "magick", "printf '%s\\n' \"$@\" >> \"$QIC_LOG\"\n"+fakeMagick)
			dir := t.TempDir()
			log := filepath.Join(dir, "args.log")
			t.Setenv("QIC_LOG", log)
			input := filepath.Join(dir, "photo.png")
			output := filepath.Join(dir, "photo."+tt.format)
			writeFile(t, input, "source")
			writeFile(t, output, "old")

			var stderr bytes.Buffer
			if err := run([]string{tt.format, input}, io.Discard, &stderr); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, output); got != "converted" {
				t.Fatalf("output = %q", got)
			}
			if got := readFile(t, input); got != "source" {
				t.Fatalf("input changed: %q", got)
			}
			args := strings.Split(strings.TrimSpace(readFile(t, log)), "\n")
			if len(args) != len(tt.flags)+2 || args[0] != input || !reflect.DeepEqual(args[1:len(args)-1], tt.flags) {
				t.Fatalf("unexpected encoder arguments: %q", args)
			}
		})
	}
}

func TestConversionReports(t *testing.T) {
	installFakeEncoder(t, "magick", `for output do :; done
case "$1" in
  *decrease.png) printf '%088d' 0 > "$output" ;;
  *increase.png) printf '%0105d' 0 > "$output" ;;
  *unchanged.png) printf '%0100d' 0 > "$output" ;;
  *small.png) printf '%0999d' 0 > "$output" ;;
  *empty.png) printf converted > "$output" ;;
esac
`)
	dir := t.TempDir()
	for _, file := range []struct {
		name string
		size int
	}{
		{"decrease.png", 100},
		{"empty.png", 0},
		{"increase.png", 100},
		{"small.png", 1000},
		{"unchanged.png", 100},
	} {
		writeFile(t, filepath.Join(dir, file.name), strings.Repeat("x", file.size))
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"jxl", filepath.Join(dir, "*.png")}, &stdout, &stderr); err != nil {
		t.Fatalf("%v: %s", err, &stderr)
	}
	want := "\x1b[32m-12%\x1b[0m " + filepath.Join(dir, "decrease.jxl") + "\n" +
		"n/a " + filepath.Join(dir, "empty.jxl") + "\n" +
		"\x1b[31m+5%\x1b[0m " + filepath.Join(dir, "increase.jxl") + "\n" +
		"\x1b[32m-0%\x1b[0m " + filepath.Join(dir, "small.jxl") + "\n" +
		"0% " + filepath.Join(dir, "unchanged.jxl") + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("report = %q, want %q", got, want)
	}
}

func TestReportWriteFailure(t *testing.T) {
	installFakeEncoder(t, "magick", fakeMagick)
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	writeFile(t, input, "source")
	reader, writer := io.Pipe()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	err := run([]string{"webp", input}, writer, &bytes.Buffer{})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error = %v, want closed pipe", err)
	}
	if got := readFile(t, filepath.Join(dir, "input.webp")); got != "converted" {
		t.Fatalf("output = %q", got)
	}
}

func TestQualityArguments(t *testing.T) {
	for _, format := range []string{"avif", "jpeg"} {
		for _, quality := range []string{"0", "80", "100"} {
			t.Run(format+"/"+quality, func(t *testing.T) {
				installFakeEncoder(t, "magick", "printf '%s\\n' \"$@\" > \"$QIC_LOG\"\n"+fakeMagick)
				dir := t.TempDir()
				log := filepath.Join(dir, "args.log")
				t.Setenv("QIC_LOG", log)
				input := filepath.Join(dir, "input.png")
				writeFile(t, input, "source")
				if err := run([]string{"-q", quality, "-size", "100x", format, input}, io.Discard, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				args := strings.Split(strings.TrimSpace(readFile(t, log)), "\n")
				want := []string{input, "-resize", "100x>", "-quality", quality}
				if len(args) != len(want)+1 || !reflect.DeepEqual(args[:len(want)], want) {
					t.Fatalf("unexpected quality arguments: %q", args)
				}
			})
		}
	}
}

func TestSizeArguments(t *testing.T) {
	for _, format := range []string{"webp", "jxl"} {
		for _, size := range []string{"100x", "x100", "100x100"} {
			t.Run(format+"/"+size, func(t *testing.T) {
				installFakeEncoder(t, "magick", "printf '%s\\n' \"$@\" > \"$QIC_LOG\"\n"+fakeMagick)
				dir := t.TempDir()
				log := filepath.Join(dir, "args.log")
				t.Setenv("QIC_LOG", log)
				input := filepath.Join(dir, "input.png")
				writeFile(t, input, "source")
				if err := run([]string{"-size", size, format, input}, io.Discard, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				args := strings.Split(strings.TrimSpace(readFile(t, log)), "\n")
				if len(args) < 3 || !reflect.DeepEqual(args[:3], []string{input, "-resize", size + ">"}) {
					t.Fatalf("unexpected resize arguments: %q", args)
				}
			})
		}
	}
}

func TestGlobAndOutputDirectory(t *testing.T) {
	installFakeEncoder(t, "magick", fakeMagick)
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.png", "b.png"} {
		writeFile(t, filepath.Join(dir, name), "source")
	}
	if err := run([]string{"webp", filepath.Join(dir, "*.png"), out}, io.Discard, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.webp", "b.webp"} {
		if got := readFile(t, filepath.Join(out, name)); got != "converted" {
			t.Fatalf("%s = %q", name, got)
		}
	}
}

func TestRecursiveGlob(t *testing.T) {
	installFakeEncoder(t, "magick", fakeMagick)
	dir := t.TempDir()
	for _, name := range []string{"root.webp", "child/one.webp", "child/deep/two.webp", "special[1]/three.webp", "special[1]/star*/four.webp", "child/ignore.png"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, "source")
	}
	if err := os.Symlink(filepath.Join(dir, "child"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}

	for _, pattern := range []string{
		filepath.Join(dir, "**", "*.webp"),
		filepath.Join(dir, "**", "**", "*.webp"),
	} {
		inputs, err := findInputs(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(inputs) != 5 {
			t.Fatalf("pattern %q matched %v, want 5 files", pattern, inputs)
		}
		if err := run([]string{"jxl", pattern}, io.Discard, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"root.jxl", "child/one.jxl", "child/deep/two.jxl", "special[1]/three.jxl", "special[1]/star*/four.jxl"} {
		if got := readFile(t, filepath.Join(dir, name)); got != "converted" {
			t.Fatalf("%s = %q", name, got)
		}
	}

	inputs, err := findInputs(filepath.Join(dir, "special*", "**", "*.webp"))
	if err != nil || len(inputs) != 2 {
		t.Fatalf("wildcard prefix: inputs = %q, error = %v", inputs, err)
	}
	for _, pattern := range []string{filepath.Join(dir, "**", "*.missing"), filepath.Join(dir, "**", "[")} {
		if _, err := findInputs(pattern); err == nil {
			t.Fatalf("expected error for %q", pattern)
		}
	}
}

func TestSingleInputOutputDirectory(t *testing.T) {
	installFakeEncoder(t, "magick", fakeMagick)
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "photo.png")
	writeFile(t, input, "source")
	for _, selection := range []string{input, filepath.Join(dir, "*.png")} {
		if err := run([]string{"webp", selection, out}, io.Discard, &bytes.Buffer{}); err != nil {
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
		{"missing size", []string{"-size"}, "flag needs an argument"},
		{"unknown flag", []string{"-unknown", "webp", input}, "flag provided but not defined"},
		{"missing quality", []string{"-q"}, "flag needs an argument"},
		{"empty quality", []string{"-q", "", "jpeg", input}, "quality must be an integer between 0 and 100"},
		{"negative quality", []string{"-q", "-1", "jpeg", input}, "quality must be an integer between 0 and 100"},
		{"quality too high", []string{"-q", "101", "avif", input}, "quality must be an integer between 0 and 100"},
		{"noninteger quality", []string{"-q", "80.5", "avif", input}, "quality must be an integer between 0 and 100"},
		{"nonnumeric quality", []string{"-q", "high", "jpeg", input}, "quality must be an integer between 0 and 100"},
		{"webp quality", []string{"-q", "80", "webp", input}, "-q is only supported for avif and jpeg"},
		{"jxl quality", []string{"-q", "100", "jxl", input}, "-q is only supported for avif and jpeg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, io.Discard, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConversionFailurePreservesOutput(t *testing.T) {
	installFakeEncoder(t, "magick", "for output do :; done\nprintf incomplete > \"$output\"\nprintf 'encoder failed\\n' >&2\nexit 7\n")
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	output := filepath.Join(dir, "input.webp")
	writeFile(t, input, "source")
	writeFile(t, output, "old")
	var stdout, stderr bytes.Buffer
	err := run([]string{"webp", input, dir}, &stdout, &stderr)
	if err == nil || !strings.Contains(stderr.String(), "encoder failed") {
		t.Fatalf("error = %v, stderr = %q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("reported failed conversion: %q", stdout.String())
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
	err := run([]string{"jxl", input}, io.Discard, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "magick is required") || !errors.Is(err, exec.ErrNotFound) {
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

func TestRealLossless(t *testing.T) {
	for _, format := range []string{"webp", "jxl"} {
		t.Run(format, func(t *testing.T) {
			testRealLossless(t, format)
		})
	}
}

func testRealLossless(t *testing.T, format string) {
	t.Helper()
	magick, err := exec.LookPath("magick")
	if err != nil {
		t.Skip("ImageMagick 7 not installed")
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
	if err := run([]string{format, input}, io.Discard, &stderr); err != nil {
		t.Fatalf("%v: %s", err, &stderr)
	}
	decodedPath := filepath.Join(dir, "decoded.png")
	cmd := exec.Command(magick, filepath.Join(dir, "input."+format), decodedPath)
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

func TestRealResize(t *testing.T) {
	magick, err := exec.LookPath("magick")
	if err != nil {
		t.Skip("ImageMagick 7 not installed")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	if output, err := exec.Command(magick, "-size", "200x100", "xc:red", input).CombinedOutput(); err != nil {
		t.Fatalf("create input: %v: %s", err, output)
	}
	tests := []struct {
		size string
		want string
	}{
		{"100x", "100x50"},
		{"x25", "50x25"},
		{"100x100", "100x50"},
		{"400x", "200x100"},
		{"x400", "200x100"},
		{"400x400", "200x100"},
		{"400x50", "100x50"},
	}
	for _, format := range []string{"webp", "jxl", "avif", "jpeg"} {
		for _, tt := range tests {
			t.Run(format+"/"+tt.size, func(t *testing.T) {
				var stderr bytes.Buffer
				args := []string{"-size", tt.size}
				if format == "avif" || format == "jpeg" {
					args = append(args, "-q", "80")
				}
				args = append(args, format, input)
				if err := run(args, io.Discard, &stderr); err != nil {
					t.Fatalf("%v: %s", err, &stderr)
				}
				output, err := exec.Command(magick, "identify", "-format", "%wx%h", filepath.Join(dir, "input."+format)).CombinedOutput()
				if err != nil {
					t.Fatalf("identify output: %v: %s", err, output)
				}
				if string(output) != tt.want {
					t.Fatalf("dimensions = %q, want %q", output, tt.want)
				}
			})
		}
	}
}
