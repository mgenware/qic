# qic

qic (Quick Image Conversion) converts files to lossless WebP or JPEG XL.
Both formats use the encoders' maximum standard compression effort, which can
make conversion slower. WebP uses `-q 100` and `-m 6`.

## Prerequisites

- `cwebp` in `PATH` for WebP
- `cjxl` in `PATH` for JPEG XL

## Installation

Install from the repository:

```sh
go install github.com/mgenware/qic@latest
```

Ensure Go's binary directory (`GOBIN`, or `GOPATH/bin` if `GOBIN` is unset)
is in your `PATH`. Alternatively, build from a local checkout with
`go build -o qic .`.

## Usage

```text
qic <format> <input> [outdir]
```

`format` is `webp` or `jxl`. `input` is a file or a glob pattern; quote
patterns to prevent the shell from expanding them. Without `outdir`, each
converted file is saved next to its input, with its extension replaced.
If provided, `outdir` must be an existing directory, for both single and
multiple inputs.

Existing output files are overwritten. An output cannot be an input file,
and multiple inputs cannot map to the same output. A failed conversion
leaves its existing output unchanged; when converting multiple files,
previously completed conversions are not rolled back.

```sh
qic webp photo.png
qic jxl 'photos/*.png' converted/
qic webp photo.png converted/
```
