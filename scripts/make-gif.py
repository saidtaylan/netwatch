#!/usr/bin/env python3
"""Assemble screenshots/frames/*.png into screenshots/demo.gif.

Frames are captured by frontend/tests/e2e/live/zz-tour.spec.ts (run under
playwright.live.config.ts against a live cluster). Requires Pillow.

Usage:
    python3 scripts/make-gif.py [--width 1000] [--ms 1800]
"""
import argparse
import glob
import os
import sys

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FRAMES_DIR = os.path.join(ROOT, "screenshots", "frames")
OUT = os.path.join(ROOT, "screenshots", "demo.gif")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--width", type=int, default=1000, help="output width (px)")
    ap.add_argument("--ms", type=int, default=1800, help="ms per frame")
    args = ap.parse_args()

    paths = sorted(glob.glob(os.path.join(FRAMES_DIR, "*.png")))
    if not paths:
        print(f"No frames in {FRAMES_DIR}", file=sys.stderr)
        return 1

    frames = []
    for p in paths:
        im = Image.open(p).convert("RGB")
        if im.width != args.width:
            h = round(im.height * args.width / im.width)
            im = im.resize((args.width, h), Image.LANCZOS)
        # Adaptive palette keeps the UI colors crisp.
        frames.append(im.quantize(colors=256, method=Image.MEDIANCUT))

    frames[0].save(
        OUT,
        save_all=True,
        append_images=frames[1:],
        duration=args.ms,
        loop=0,
        optimize=True,
        disposal=2,
    )
    size_kb = os.path.getsize(OUT) / 1024
    print(f"Wrote {OUT}  ({len(frames)} frames, {args.width}px, {size_kb:.0f} KB)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
