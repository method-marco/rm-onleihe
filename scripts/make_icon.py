"""Generate app/icon.png (a bold "O" around a small open book) with only the stdlib.

Black on white for e-ink, anti-aliased by 4x4 supersampling.
"""
import math, struct, sys, zlib

W = H = 128
SS = 4  # supersampling factor
CX = CY = W / 2

RING_OUTER = 58
RING_INNER = 40


def ink(x, y):
    """True if the point (in icon pixels) is black."""
    d = math.hypot(x - CX, y - CY)
    if RING_INNER <= d <= RING_OUTER:
        return True
    # small open book in the centre of the O
    dx, dy = x - CX, y - CY
    if abs(dx) <= 1.5 and -13 <= dy <= 13:  # spine
        return True
    for side in (-1, 1):
        px = dx * side  # mirror: 0 at spine, growing outward
        if 1.5 < px <= 22:
            top = -12 + px * 0.12   # pages curve down slightly towards the edge
            bottom = 12 + px * 0.12
            on_top = top <= dy <= top + 3
            on_bottom = bottom - 3 <= dy <= bottom
            on_edge = px >= 19 and top <= dy <= bottom
            if on_top or on_bottom or on_edge:
                return True
            # text lines
            if 6 <= px <= 16:
                for ly in (-5, 1):
                    if ly + px * 0.12 <= dy <= ly + 2 + px * 0.12:
                        return True
    return False


rows = []
for y in range(H):
    row = bytearray()
    for x in range(W):
        n = sum(
            ink(x + (i + 0.5) / SS, y + (j + 0.5) / SS)
            for i in range(SS)
            for j in range(SS)
        )
        row.append(round(255 * (1 - n / (SS * SS))))
    rows.append(bytes(row))

raw = b"".join(b"\x00" + r for r in rows)


def chunk(t, d):
    return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)


png = (
    b"\x89PNG\r\n\x1a\n"
    + chunk(b"IHDR", struct.pack(">IIBBBBB", W, H, 8, 0, 0, 0, 0))
    + chunk(b"IDAT", zlib.compress(raw, 9))
    + chunk(b"IEND", b"")
)
open(sys.argv[1] if len(sys.argv) > 1 else "app/icon.png", "wb").write(png)
