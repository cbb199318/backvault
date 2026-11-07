#!/usr/bin/env python3
"""从 scripts/assets/logo-source.png 生成 fpk 需要的全部图标尺寸。

母版规格要求：1024x1024、RGBA、圆角方块外为透明。
改图标只需替换母版后重跑本脚本 —— 不要手工缩图，手工缩会在圆角处留白边、
在白/黄交界处留灰边。

用法：
    python3 scripts/gen_icons.py

产物：
    encbackup/ICON.PNG                      64x64   应用中心列表图标
    encbackup/ICON_256.PNG                  256x256 应用详情大图
    encbackup/app/ui/images/icon_64.png     64x64
    encbackup/app/ui/images/icon_256.png    256x256
"""
import os
import sys

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, 'scripts', 'assets', 'logo-source.png')

TARGETS = [
    ('encbackup/ICON.PNG', 64),
    ('encbackup/ICON_256.PNG', 256),
    ('encbackup/app/ui/images/icon_64.png', 64),
    ('encbackup/app/ui/images/icon_256.png', 256),
]


def main():
    if not os.path.exists(SRC):
        sys.exit('找不到母版：%s' % SRC)
    src = Image.open(SRC)
    if src.size != (1024, 1024):
        sys.exit('母版必须是 1024x1024，当前 %s' % (src.size,))
    src = src.convert('RGBA')
    lo, hi = src.getchannel('A').getextrema()
    if lo != 0 or hi != 255:
        sys.exit('母版必须带透明通道：四角透明、中心不透明')

    for rel, size in TARGETS:
        out = os.path.join(ROOT, rel)
        os.makedirs(os.path.dirname(out), exist_ok=True)
        src.resize((size, size), Image.LANCZOS).save(out, 'PNG', optimize=True)
        print('%-44s %dx%d  %d B' % (rel, size, size, os.path.getsize(out)))


if __name__ == '__main__':
    main()
