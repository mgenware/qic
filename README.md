# qic

qic (Quick Image Conversion) converts files to common image formats using
opinionated settings, powered by ImageMagick.

## Supported formats

| Format | Settings                                      |
| ------ | --------------------------------------------- |
| `webp` | Lossless, maximum standard compression effort |
| `jxl`  | Lossless, maximum standard compression effort |
| `avif` | Use `-q` to set quality                       |
| `jpeg` | Use `-q` to set quality                       |

WebP and JPEG XL use `-quality 100` for lossless encoding, with
`webp:method=6` for WebP and `jxl:effort=9` for JPEG XL. Maximum effort
can make conversion slower.

## Prerequisites

- ImageMagick 7 executable `magick` must be in `PATH`.

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
Usage: qic [-size geometry] [-q quality] <format> <input> [outdir]

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
```

## Examples

```sh
# Convert a PNG to WebP alongside the original.
qic webp photo.png

# Convert PNGs in photos/ to JPEG XL in converted/.
qic jxl 'photos/*.png' converted/

# Convert WebP files recursively to JPEG XL alongside their originals.
qic jxl 'photos/**/*.webp'

# Save a converted file in an existing output directory.
qic webp photo.png converted/

# Limit the output width to 100 pixels.
qic -size 100x webp photo.png

# Limit the output height to 100 pixels.
qic -size x100 jxl 'photos/*.png' converted/

# Fit the output within 100 by 100 pixels.
qic -size 100x100 webp photo.png

# Convert to AVIF with quality 80.
qic -q 80 avif photo.png

# Resize and convert to JPEG with quality 85.
qic -size 100x100 -q 85 jpeg photo.png converted/
```
