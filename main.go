package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const usage = `Usage: qic <format> <input> [outdir]

Formats:
  webp  lossless WebP (requires cwebp in PATH)
  jxl   lossless JPEG XL (requires cjxl in PATH)

Input is a file or a glob pattern. Quote glob patterns to prevent shell expansion.
By default, output files are saved alongside their inputs. If specified,
outdir must be an existing directory.
`

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "qic:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, err := io.WriteString(stderr, usage)
		return err
	}
	if len(args) < 2 || len(args) > 3 {
		return errors.New(usage)
	}

	format := args[0]
	var executable string
	switch format {
	case "webp":
		executable = "cwebp"
	case "jxl":
		executable = "cjxl"
	default:
		return fmt.Errorf("unsupported format %q (expected webp or jxl)", format)
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
	encoder, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("%s is required for %s conversion: %w", executable, format, err)
	}

	for i, input := range inputs {
		if err := convert(encoder, format, input, outputs[i], stderr); err != nil {
			return err
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
		matches, globErr = filepath.Glob(pattern)
		if globErr != nil {
			return nil, fmt.Errorf("invalid input glob %q: %w", pattern, globErr)
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

func convert(encoder, format, input, output string, stderr io.Writer) error {
	temp, err := os.CreateTemp(filepath.Dir(output), ".qic-*."+format)
	if err != nil {
		return fmt.Errorf("prepare output %q: %w", output, err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Close(); err != nil {
		return fmt.Errorf("prepare output %q: %w", output, err)
	}

	var args []string
	switch format {
	case "webp":
		args = []string{"-lossless", "-exact", "-m", "6", "-q", "100", input, "-o", tempPath}
	case "jxl":
		args = []string{input, tempPath, "-d", "0", "-e", "9"}
	}
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
