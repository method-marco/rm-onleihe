"""Generate app/icon.png (a simple open-book glyph) with only the stdlib."""
import struct, sys, zlib

W = H = 128
px = [[255] * W for _ in range(H)]

def rect(x0, y0, x1, y1, v=0):
    for y in range(y0, y1):
        for x in range(x0, x1):
            px[y][x] = v

# two pages
for (x0, x1) in ((14, 62), (66, 114)):
    rect(x0, 24, x1, 30); rect(x0, 98, x1, 104)
    rect(x0, 24, x0 + 6, 104); rect(x1 - 6, 24, x1, 104)
    for y in (44, 58, 72, 86):
        rect(x0 + 12, y, x1 - 12, y + 4)
rect(62, 30, 66, 108)  # spine

raw = b"".join(b"\x00" + bytes(row) for row in px)
def chunk(t, d):
    return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", W, H, 8, 0, 0, 0, 0)) \
    + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")
open(sys.argv[1] if len(sys.argv) > 1 else "app/icon.png", "wb").write(png)
