package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const usage = `Usage: qic [-size geometry] [-q quality] <format> <input> [outdir]

Formats:
  webp  lossless WebP, maximum standard compression effort
  jxl   lossless JPEG XL, maximum standard compression effort
  avif  AVIF, use -q to set quality
  jpeg  JPEG, use -q to set quality

Requires ImageMagick 7's magick in PATH with support for the selected format.
Optional -size geometry (e.g. 100x, x100, 100x100) is passed to magick -resize
with > appended to fit the specified dimensions without ever upscaling.
Optional -q quality (0-100) applies only to AVIF and JPEG; if omitted,
ImageMagick's default quality is used.

Input is a file or a glob pattern. Quote glob patterns to prevent shell expansion.
Use **/ to match zero or more directory levels (without following symlinked directories).
By default, output files are saved alongside their inputs. If specified,
outdir must be an existing directory.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "qic:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("qic", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, usage) }
	size := flags.String("size", "", "maximum dimensions, resized without upscaling")
	quality := ""
	flags.Func("q", "AVIF/JPEG quality (0-100; default: ImageMagick's default)", func(value string) error {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 100 {
			return errors.New("quality must be an integer between 0 and 100")
		}
		quality = strconv.Itoa(n)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = flags.Args()
	if len(args) < 2 || len(args) > 3 {
		return errors.New(usage)
	}

	format := args[0]
	switch format {
	case "webp", "jxl":
		if quality != "" {
			return errors.New("-q is only supported for avif and jpeg; webp and jxl are always lossless")
		}
		quality = "100"
	case "avif", "jpeg":
	default:
		return fmt.Errorf("unsupported format %q (expected webp, jxl, avif, or jpeg)", format)
	}

	inputs, err := findInputs(args[1])
	if err != nil {
		return err
	}
	outdir := ""
	if len(args) == 3 {
		if args[2] == "" {
			return errors.New("outdir must not be empty")
		}
		outdir = args[2]
	}
	outputs, err := planOutputs(inputs, format, outdir)
	if err != nil {
		return err
	}
	encoder, err := exec.LookPath("magick")
	if err != nil {
		return fmt.Errorf("magick is required for %s conversion: %w", format, err)
	}

	for i, input := range inputs {
		inputInfo, err := os.Stat(input)
		if err != nil {
			return fmt.Errorf("inspect input %q: %w", input, err)
		}
		if err := convert(encoder, format, input, outputs[i], *size, quality, stderr); err != nil {
			return err
		}
		outputInfo, err := os.Stat(outputs[i])
		if err != nil {
			return fmt.Errorf("inspect output %q: %w", outputs[i], err)
		}
		change := "n/a"
		if inputInfo.Size() > 0 {
			difference := outputInfo.Size() - inputInfo.Size()
			switch {
			case difference < 0:
				change = fmt.Sprintf("\x1b[32m%+.0f%%\x1b[0m", float64(difference)*100/float64(inputInfo.Size()))
			case difference > 0:
				change = fmt.Sprintf("\x1b[31m%+.0f%%\x1b[0m", float64(difference)*100/float64(inputInfo.Size()))
			default:
				change = "0%"
			}
		}
		if _, err := fmt.Fprintf(stdout, "%s %s\n", change, outputs[i]); err != nil {
			return fmt.Errorf("report output %q: %w", outputs[i], err)
		}
	}
	return nil
}

func findInputs(pattern string) ([]string, error) {
	if pattern == "" {
		return nil, errors.New("input must not be empty")
	}

	var matches []string
	if _, err := os.Stat(pattern); err == nil {
		matches = []string{pattern}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect input %q: %w", pattern, err)
		}
		var globErr error
		matches, globErr = globInputs(pattern)
		if globErr != nil {
			if errors.Is(globErr, filepath.ErrBadPattern) {
				return nil, fmt.Errorf("invalid input glob %q: %w", pattern, globErr)
			}
			return nil, fmt.Errorf("match input glob %q: %w", pattern, globErr)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no files match input %q", pattern)
		}
	}

	for i, match := range matches {
		absolute, err := filepath.Abs(match)
		if err != nil {
			return nil, fmt.Errorf("resolve input %q: %w", match, err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, fmt.Errorf("inspect input %q: %w", match, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("input %q is not a regular file", match)
		}
		matches[i] = absolute
	}
	return matches, nil
}

func globInputs(pattern string) ([]string, error) {
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, err
	}

	separator := string(filepath.Separator)
	parts := strings.Split(pattern, separator)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] != "**" {
			continue
		}

		rootPattern := "."
		if i > 0 {
			rootPattern = strings.Join(parts[:i], separator)
			if filepath.IsAbs(pattern) && rootPattern == filepath.VolumeName(pattern) {
				rootPattern += separator
			}
		}
		roots, err := filepath.Glob(rootPattern)
		if err != nil {
			return nil, err
		}
		suffix := strings.Join(parts[i+1:], separator)
		seen := make(map[string]bool)
		var matches []string
		var visit func(string) error
		visit = func(dir string) error {
			found, err := globInputs(filepath.Join(escapeGlobPath(dir), suffix))
			if err != nil {
				return err
			}
			for _, match := range found {
				if !seen[match] {
					seen[match] = true
					matches = append(matches, match)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Errorf("read directory %q: %w", dir, err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					if err := visit(filepath.Join(dir, entry.Name())); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for _, root := range roots {
			info, err := os.Stat(root)
			if err != nil {
				return nil, fmt.Errorf("inspect directory %q: %w", root, err)
			}
			if info.IsDir() {
				if err := visit(root); err != nil {
					return nil, err
				}
			}
		}
		sort.Strings(matches)
		return matches, nil
	}
	return filepath.Glob(pattern)
}

func escapeGlobPath(path string) string {
	var escaped strings.Builder
	for _, char := range path {
		switch char {
		case '*', '?', '[':
			escaped.WriteByte('[')
			escaped.WriteRune(char)
			escaped.WriteByte(']')
		case '\\':
			if filepath.Separator != '\\' {
				escaped.WriteByte('\\')
			}
			escaped.WriteRune(char)
		default:
			escaped.WriteRune(char)
		}
	}
	return escaped.String()
}

func planOutputs(inputs []string, format, outdir string) ([]string, error) {
	if outdir != "" {
		info, err := os.Stat(outdir)
		if err != nil {
			return nil, fmt.Errorf("inspect output directory %q: %w", outdir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("outdir %q must be an existing directory", outdir)
		}
	}

	inputInfos := make([]os.FileInfo, len(inputs))
	for i, input := range inputs {
		info, err := os.Stat(input)
		if err != nil {
			return nil, fmt.Errorf("inspect input %q: %w", input, err)
		}
		inputInfos[i] = info
	}

	outputs := make([]string, len(inputs))
	seen := make(map[string]bool)
	for i, input := range inputs {
		name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)) + "." + format
		path := filepath.Join(filepath.Dir(input), name)
		if outdir != "" {
			path = filepath.Join(outdir, name)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve output %q: %w", path, err)
		}
		if seen[absolute] {
			return nil, fmt.Errorf("multiple inputs map to output %q", absolute)
		}
		seen[absolute] = true
		info, err := os.Stat(absolute)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect output %q: %w", absolute, err)
		}
		if err == nil {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("output %q is not a regular file", absolute)
			}
			for j, inputInfo := range inputInfos {
				if os.SameFile(info, inputInfo) {
					return nil, fmt.Errorf("output %q would overwrite input %q", absolute, inputs[j])
				}
			}
		}
		outputs[i] = absolute
	}
	return outputs, nil
}

func convert(encoder, format, input, output, size, quality string, stderr io.Writer) error {
	temp, err := os.CreateTemp(filepath.Dir(output), ".qic-*."+format)
	if err != nil {
		return fmt.Errorf("prepare output %q: %w", output, err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Close(); err != nil {
		return fmt.Errorf("prepare output %q: %w", output, err)
	}

	args := []string{input}
	if size != "" {
		args = append(args, "-resize", size+">")
	}
	if quality != "" {
		args = append(args, "-quality", quality)
	}
	switch format {
	case "webp":
		args = append(args, "-define", "webp:lossless=true", "-define", "webp:exact=true", "-define", "webp:method=6")
	case "jxl":
		args = append(args, "-define", "jxl:effort=9")
	}
	args = append(args, tempPath)
	cmd := exec.Command(encoder, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("convert %q to %q: %w", input, output, err)
	}
	if err := os.Rename(tempPath, output); err != nil {
		return fmt.Errorf("save output %q: %w", output, err)
	}
	return nil
}
