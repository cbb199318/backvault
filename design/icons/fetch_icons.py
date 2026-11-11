#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从 Iconify 拉取候选图标，生成选型预览页。"""
import os, json, urllib.request, urllib.error
from concurrent.futures import ThreadPoolExecutor

BASE = "https://api.iconify.design"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "svg")
os.makedirs(OUT, exist_ok=True)

# ---------------- 整套风格对比（备份 / 快照 / 历史 / 新增） ----------------
FAMILIES = [
    ("ph",           "Phosphor 线性",        ["folder-simple", "stack", "clock-counter-clockwise", "plus"]),
    ("ph-bold",      "Phosphor 粗线性",      ["folder-simple-bold", "stack-bold", "clock-counter-clockwise-bold", "plus-bold"]),
    ("ph-duotone",   "Phosphor 双色",        ["folder-simple-duotone", "stack-duotone", "clock-counter-clockwise-duotone", "plus-duotone"]),
    ("solar",        "Solar 双色（Bold Duotone）", ["folder-bold-duotone", "layers-bold-duotone", "history-bold-duotone", "add-circle-bold-duotone"]),
    ("tabler",       "Tabler 2px",           ["folder", "stack-2", "history", "plus"]),
    ("lucide",       "Lucide 1.5px",         ["folder", "layers", "rotate-ccw-clock", "plus"]),
    ("iconoir",      "Iconoir 几何",         ["folder", "multiple-pages", "clock", "plus"]),
    ("mingcute",     "MingCute 圆润",        ["folder-line", "layers-line", "history-line", "add-line"]),
    ("material-symbols", "Material Rounded", ["folder-outline", "layers-outline", "history", "add"]),
    ("fluent",       "Fluent 微软风",        ["folder-24-regular", "layer-24-regular", "history-24-regular", "add-24-regular"]),
    ("ri",           "Remix 均衡",           ["folder-3-line", "stack-line", "history-line", "add-line"]),
]

# ---------------- 按功能槽位挑候选 ----------------
SLOTS = [
    ("备份任务", "侧栏 / 底部导航 / 任务卡", [
        ("ph", "folder-simple"), ("ph-duotone", "folder-simple-duotone"),
        ("solar", "folder-bold-duotone"), ("tabler", "archive"),
        ("lucide", "archive"), ("fluent", "folder-24-regular"),
    ]),
    ("新增备份", "悬浮 + / 创建任务按钮", [
        ("ph", "plus-circle"), ("ph-duotone", "plus-circle-duotone"),
        ("solar", "add-circle-bold-duotone"), ("tabler", "folder-plus"),
        ("lucide", "folder-plus"), ("fluent", "folder-add-24-regular"),
    ]),
    ("新增云盘", "「新增云端」入口", [
        ("ph", "cloud-arrow-up"), ("ph-duotone", "cloud-arrow-up-duotone"),
        ("solar", "cloud-plus-bold-duotone"), ("tabler", "cloud-plus"),
        ("lucide", "cloud-upload"), ("fluent", "cloud-add-24-regular"),
    ]),
    ("快照", "侧栏 / 快照页", [
        ("ph", "stack"), ("ph-duotone", "stack-duotone"),
        ("solar", "layers-bold-duotone"), ("tabler", "stack-2"),
        ("lucide", "layers"), ("fluent", "layer-24-regular"),
    ]),
    ("浏览 / 目录", "文件浏览、目录选择", [
        ("ph", "folder-open"), ("ph-duotone", "folder-open-duotone"),
        ("solar", "folder-open-bold-duotone"), ("tabler", "folder-open"),
        ("lucide", "folder-open"), ("fluent", "folder-open-24-regular"),
    ]),
    ("历史", "侧栏 / 历史页", [
        ("ph", "clock-counter-clockwise"), ("ph-duotone", "clock-counter-clockwise-duotone"),
        ("solar", "history-bold-duotone"), ("tabler", "history"),
        ("lucide", "rotate-ccw-clock"), ("fluent", "history-24-regular"),
    ]),
    ("云端仓库", "快照页 KPI", [
        ("ph", "database"), ("ph-duotone", "database-duotone"),
        ("solar", "database-bold-duotone"), ("tabler", "database"),
        ("lucide", "database"), ("fluent", "database-24-regular"),
    ]),
    ("关联任务", "快照页 KPI", [
        ("ph", "list-checks"), ("ph-duotone", "list-checks-duotone"),
        ("solar", "checklist-bold-duotone"), ("tabler", "list-check"),
        ("lucide", "list-checks"), ("fluent", "task-list-24-regular"),
    ]),
    ("成功率", "历史页 KPI", [
        ("ph", "shield-check"), ("ph-duotone", "shield-check-duotone"),
        ("solar", "shield-check-bold-duotone"), ("tabler", "shield-check"),
        ("lucide", "shield-check"), ("fluent", "shield-checkmark-24-regular"),
    ]),
    ("失败", "历史页 KPI", [
        ("ph", "warning-circle"), ("ph-duotone", "warning-circle-duotone"),
        ("solar", "danger-circle-bold-duotone"), ("tabler", "alert-circle"),
        ("lucide", "circle-exclamation-point"), ("fluent", "warning-24-regular"),
    ]),
    ("还原", "历史页 KPI / 还原入口", [
        ("ph", "arrow-counter-clockwise"), ("ph-duotone", "arrow-counter-clockwise-duotone"),
        ("solar", "restart-bold-duotone"), ("tabler", "arrow-back-up"),
        ("lucide", "rotate-ccw"), ("fluent", "arrow-counterclockwise-24-regular"),
    ]),
    ("搜索", "任务列表搜索框", [
        ("ph", "magnifying-glass"), ("ph-duotone", "magnifying-glass-duotone"),
        ("solar", "magnifer-bold-duotone"), ("tabler", "search"),
        ("lucide", "search"), ("fluent", "search-24-regular"),
    ]),
    ("刷新", "快照刷新 / 下拉刷新", [
        ("ph", "arrows-clockwise"), ("ph-duotone", "arrows-clockwise-duotone"),
        ("solar", "refresh-bold-duotone"), ("tabler", "refresh"),
        ("lucide", "refresh-ccw"), ("fluent", "arrow-clockwise-24-regular"),
    ]),
    ("更多", "行内 ⋯ 菜单", [
        ("ph", "dots-three"), ("ph-duotone", "dots-three-duotone"),
        ("solar", "menu-dots-bold-duotone"), ("tabler", "dots"),
        ("lucide", "ellipsis"), ("fluent", "more-horizontal-24-regular"),
    ]),
    ("显示 / 隐藏密码", "密码输入框", [
        ("ph", "eye"), ("ph-duotone", "eye-duotone"),
        ("solar", "eye-bold-duotone"), ("tabler", "eye"),
        ("lucide", "eye"), ("fluent", "eye-24-regular"),
    ]),
    ("复制", "复制密码", [
        ("ph", "copy"), ("ph-duotone", "copy-duotone"),
        ("solar", "copy-bold-duotone"), ("tabler", "copy"),
        ("lucide", "copy"), ("fluent", "copy-24-regular"),
    ]),
    ("删除", "删除任务 / 移除仓库", [
        ("ph", "trash"), ("ph-duotone", "trash-duotone"),
        ("solar", "trash-bin-trash-bold-duotone"), ("tabler", "trash"),
        ("lucide", "trash"), ("fluent", "delete-24-regular"),
    ]),
    ("文件 / 文件夹", "文件浏览列表", [
        ("ph", "file"), ("ph-duotone", "file-duotone"),
        ("solar", "file-bold-duotone"), ("tabler", "file"),
        ("lucide", "file"), ("fluent", "document-24-regular"),
    ]),
    ("本机设备", "备份来源地", [
        ("ph", "desktop"), ("ph-duotone", "desktop-duotone"),
        ("solar", "monitor-bold-duotone"), ("tabler", "device-desktop"),
        ("lucide", "monitor"), ("fluent", "desktop-24-regular"),
    ]),
    ("目标位置", "还原目标", [
        ("ph", "map-pin"), ("ph-duotone", "map-pin-duotone"),
        ("solar", "map-point-bold-duotone"), ("tabler", "map-pin"),
        ("lucide", "map-pin"), ("fluent", "location-24-regular"),
    ]),
]


def key(prefix, name):
    return f"{prefix}:{name}"


def fetch(prefix, name):
    fn = os.path.join(OUT, f"{prefix}__{name}.svg")
    if os.path.exists(fn) and os.path.getsize(fn) > 40:
        return prefix, name, open(fn, encoding="utf-8").read(), True
    url = f"{BASE}/{prefix}/{name}.svg?height=24"
    try:
        with urllib.request.urlopen(url, timeout=15) as r:
            txt = r.read().decode("utf-8")
        open(fn, "w", encoding="utf-8").write(txt)
        return prefix, name, txt, True
    except urllib.error.HTTPError as e:
        return prefix, name, "", False
    except Exception as e:
        return prefix, name, "", False


def inner(svg):
    i = svg.find(">")
    j = svg.rfind("</svg>")
    body = svg[i + 1:j]
    return body.replace("currentColor", "currentColor")


def main():
    jobs = set()
    for prefix, _label, names in FAMILIES:
        for n in names:
            jobs.add((prefix, n))
    for _t, _d, cands in SLOTS:
        for p, n in cands:
            jobs.add((p, n))
    jobs = sorted(jobs)
    print("total icons:", len(jobs))
    res = {}
    with ThreadPoolExecutor(max_workers=12) as ex:
        for p, n, txt, ok in ex.map(lambda a: fetch(*a), jobs):
            res[(p, n)] = (txt, ok)
    missing = [f"{p}:{n}" for (p, n), (t, ok) in res.items() if not ok]
    if missing:
        print("MISSING:", missing)
    json.dump({f"{p}:{n}": (t if ok else "") for (p, n), (t, ok) in res.items()},
              open(os.path.join(OUT, "..", "icons.json"), "w"), ensure_ascii=False)
    return res, missing


if __name__ == "__main__":
    main()
