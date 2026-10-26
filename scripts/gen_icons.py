#!/usr/bin/env python3
"""生成应用图标：蓝色圆角方块 + 白色挂锁。纯 Python 写 PNG，无第三方依赖。"""
import struct
import zlib
import os

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")

def make_png(size, path):
    s = float(size)
    bg = (47, 111, 237)     # #2f6fed
    white = (255, 255, 255)

    def lock_pixel(x, y):
        fx, fy = x / s, y / s
        # 圆角背景
        r = 0.18
        cx = min(max(fx, r), 1 - r)
        cy = min(max(fy, r), 1 - r)
        if (fx - cx) ** 2 + (fy - cy) ** 2 > r * r:
            return None
        # 锁体
        if 0.28 <= fx <= 0.72 and 0.48 <= fy <= 0.78:
            return white
        # 锁梁（上半圆环）
        dx, dy = fx - 0.5, fy - 0.40
        d = (dx * dx + dy * dy) ** 0.5
        if 0.13 <= d <= 0.19 and fy <= 0.48:
            return white
        return bg

    rows = b""
    for y in range(size):
        rows += b"\x00"
        for x in range(size):
            p = lock_pixel(x, y)
            if p is None:
                rows += b"\x00\x00\x00\x00"
            else:
                rows += bytes(p) + b"\xff"

    def chunk(tag, data):
        c = tag + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)

    sig = b"\x89PNG\r\n\x1a\n"
    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)  # RGBA
    png = sig + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")
    with open(path, "wb") as f:
        f.write(png)
    print("wrote", path, len(png), "bytes")


ui_images = os.path.join(ROOT, "encbackup", "app", "ui", "images")
os.makedirs(ui_images, exist_ok=True)
make_png(256, os.path.join(ROOT, "encbackup", "ICON_256.PNG"))
make_png(64, os.path.join(ROOT, "encbackup", "ICON.PNG"))
make_png(256, os.path.join(ui_images, "icon_256.png"))
make_png(64, os.path.join(ui_images, "icon_64.png"))
