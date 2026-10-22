"""Puts back each screenshot whose pixels barely changed, so a refresh commits only real changes.

    python3 -I scripts/screenshots-changed.py src/assets/screenshots

A screenshot counts as changed when more than 0.2% of its pixels differ by more than 24 levels
from the committed one: a clock or a relative time doesn't, a changed layout does. New
screenshots stay. It needs Pillow and runs inside the git work tree.
"""

import io
import subprocess
import sys

from PIL import Image, ImageChops

THRESHOLD = 0.002  # the share of pixels that must differ
LEVEL = 24  # how much a pixel must differ, of 255


def git(*args: str) -> bytes:
    return subprocess.run(["git", *args], check=True, capture_output=True).stdout


def main(directory: str) -> None:
    modified = git("diff", "--name-only", "--relative", "--", directory).decode().split()
    for path in modified:
        if not path.endswith((".jpg", ".png")):
            continue
        before = Image.open(io.BytesIO(git("show", "HEAD:./" + path))).convert("RGB")
        after = Image.open(path).convert("RGB")
        if before.size != after.size:
            print(f"changed {path}: {before.size} → {after.size}")
            continue
        histogram = ImageChops.difference(before, after).convert("L").histogram()
        share = sum(histogram[LEVEL + 1 :]) / (after.width * after.height)
        if share > THRESHOLD:
            print(f"changed {path}: {share:.2%} of the pixels")
        else:
            git("checkout", "--", path)
            print(f"same    {path}: {share:.2%} of the pixels")


if __name__ == "__main__":
    main(sys.argv[1])
