#!/usr/bin/env python3
"""Draw the home-screen icons into web/public/.

The manifest used to declare icon-192.png and icon-512.png while web/public/
held nothing but icon.svg, so both entries 404'd and Android had no maskable
asset to build an adaptive icon from - which is what produced a letterboxed
icon with a browser badge instead of the app's own.

Generating them here rather than committing binaries keeps one source of truth
for the artwork and makes it impossible for the manifest to reference a file
the build does not produce.

Nothing outside the standard library is used on purpose. The build already
needs Go and npm; making a home-screen icon also require ImageMagick, Pillow
or librsvg would be a poor trade for four flat images. The shapes here are a
rounded rectangle and a two-segment stroke, which a distance field draws
directly and exactly.
"""

import math
import os
import struct
import sys
import zlib

GROUND = (0x1E, 0x1E, 0x2E)  # --base, matches manifest background_color
MARK = (0x89, 0xB4, 0xFA)  # --blue

# Geometry as a fraction of the canvas, so every size is the same drawing.
#
# The old artwork was a terminal frame plus a chevron plus a green underline at
# stroke-width 16 on a 512 grid. At 48dp that is about 1.5px a stroke: the
# frame vanishes and the rest turns to mush. One bold chevron is what survives,
# and it still reads as a terminal.
#
# FULL is for purpose "any", where the whole square is shown.
#
# SAFE is for purpose "maskable", where a launcher crops to a circle, squircle
# or rounded square of its choosing and only the middle 80% is guaranteed. The
# ground still bleeds to the edge - it is the mark that shrinks, so a crop
# never exposes a corner. Its furthest point sits at radius 0.335 of the
# canvas, inside the 0.4 the circular mask leaves.
FULL = dict(height=0.56, width=0.36, stroke=0.145)
SAFE = dict(height=0.46, width=0.30, stroke=0.120)


def chevron(size, shape):
    """The ❯ as two segments and a stroke radius, in pixels."""
    c = size / 2.0
    h, w = shape['height'] * size, shape['width'] * size
    a = (c - w / 2, c - h / 2)
    b = (c + w / 2, c)
    d = (c - w / 2, c + h / 2)
    return [(a, b), (b, d)], shape['stroke'] * size / 2.0


def dist_to_segment(px, py, seg):
    (x1, y1), (x2, y2) = seg
    dx, dy = x2 - x1, y2 - y1
    t = ((px - x1) * dx + (py - y1) * dy) / (dx * dx + dy * dy)
    t = 0.0 if t < 0.0 else 1.0 if t > 1.0 else t
    return math.hypot(px - (x1 + t * dx), py - (y1 + t * dy))


def render(size, shape):
    """One RGB row per line, the mark composited onto the ground."""
    segs, radius = chevron(size, shape)
    gr, gg, gb = GROUND
    mr, mg, mb = MARK
    ground_row = bytes(GROUND) * size

    rows = []
    for y in range(size):
        py = y + 0.5
        # Rows the mark cannot touch are the ground, untouched and unmeasured.
        if py < size * (0.5 - shape['height'] / 2) - radius - 1 or py > size * (
            0.5 + shape['height'] / 2
        ) + radius + 1:
            rows.append(ground_row)
            continue

        row = bytearray(ground_row)
        for x in range(size):
            d = min(dist_to_segment(x + 0.5, py, s) for s in segs)
            # A one-pixel ramp across the edge. Supersampling would cost far
            # more and show no difference on a shape with no thin features.
            cov = radius - d + 0.5
            if cov <= 0:
                continue
            if cov >= 1:
                row[x * 3 : x * 3 + 3] = bytes(MARK)
                continue
            row[x * 3 + 0] = int(gr + (mr - gr) * cov + 0.5)
            row[x * 3 + 1] = int(gg + (mg - gg) * cov + 0.5)
            row[x * 3 + 2] = int(gb + (mb - gb) * cov + 0.5)
        rows.append(bytes(row))
    return rows


def write_png(path, size, rows):
    """Truecolour 8-bit PNG. No alpha: the ground is opaque to the edge, which
    is what both a maskable icon and iOS want anyway."""

    def chunk(tag, data):
        return (
            struct.pack('>I', len(data))
            + tag
            + data
            + struct.pack('>I', zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    raw = b''.join(b'\x00' + r for r in rows)  # filter type 0 per scanline
    png = (
        b'\x89PNG\r\n\x1a\n'
        + chunk(b'IHDR', struct.pack('>IIBBBBB', size, size, 8, 2, 0, 0, 0))
        + chunk(b'IDAT', zlib.compress(raw, 9))
        + chunk(b'IEND', b'')
    )
    with open(path, 'wb') as f:
        f.write(png)
    return len(png)


def main():
    out = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'web', 'public')
    targets = [
        ('icon-192.png', 192, FULL),
        ('icon-512.png', 512, FULL),
        # iOS ignores the manifest and applies its own rounded-rect mask, so it
        # takes the full-bleed art rather than the shrunken maskable one.
        ('icon-180.png', 180, FULL),
        ('icon-maskable-512.png', 512, SAFE),
    ]
    for name, size, shape in targets:
        path = os.path.join(out, name)
        n = write_png(path, size, render(size, shape))
        print('    %-24s %4dx%-4d %5d bytes' % (name, size, size, n))
    return 0


if __name__ == '__main__':
    sys.exit(main())
