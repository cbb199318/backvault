#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""按图标集批量拉取（Iconify JSON API），失败重试，结果缓存到 svg/。"""
import os, json, time, urllib.request, urllib.error, urllib.parse

BASE = "https://api.iconify.design"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "svg")
CACHE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "cache.json")
os.makedirs(OUT, exist_ok=True)


def load_cache():
    if os.path.exists(CACHE):
        return json.load(open(CACHE, encoding="utf-8"))
    return {}


def save_cache(c):
    json.dump(c, open(CACHE, "w", encoding="utf-8"), ensure_ascii=False)


def get_json(url, tries=4):
    for i in range(tries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "icon-picker/1.0"})
            with urllib.request.urlopen(req, timeout=25) as r:
                return json.loads(r.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            if e.code == 429:
                time.sleep(3 + i * 4)
                continue
            if e.code == 404:
                return None
            time.sleep(2 + i * 3)
        except Exception:
            time.sleep(2 + i * 3)
    return None


def fetch_set(prefix, names, cache):
    """返回 {name: (body, width, height)}"""
    todo = [n for n in names if f"{prefix}:{n}" not in cache]
    if todo:
        # 分批，每批 40 个
        for i in range(0, len(todo), 40):
            chunk = todo[i:i + 40]
            q = urllib.parse.urlencode({"icons": ",".join(chunk)})
            data = get_json(f"{BASE}/{prefix}.json?{q}")
            if not data:
                print(f"  ! {prefix} 批次失败: {chunk[:3]}...")
                continue
            if not isinstance(data, dict) or "icons" not in data:
                print(f"  ! {prefix} 返回异常")
                continue
            w = data.get("width", 24)
            h = data.get("height", 24)
            for n, ic in (data.get("icons") or {}).items():
                cache[f"{prefix}:{n}"] = {"body": ic.get("body", ""), "w": w, "h": h}
            miss = data.get("not_found") or []
            if miss:
                print(f"  ! {prefix} not_found: {miss}")
            time.sleep(1.2)
    return {n: cache.get(f"{prefix}:{n}") for n in names}


NORM = {"ph-bold": "ph", "ph-duotone": "ph", "ph-fill": "ph"}


def main():
    from fetch_icons import FAMILIES, SLOTS

    need = {}
    for prefix, _l, names in FAMILIES:
        for n in names:
            need.setdefault(NORM.get(prefix, prefix), set()).add(n)
    for _t, _d, cands in SLOTS:
        for p, n in cands:
            need.setdefault(NORM.get(p, p), set()).add(n)

    cache = load_cache()
    for prefix, names in need.items():
        print("fetch", prefix, len(names))
        fetch_set(prefix, sorted(names), cache)
        save_cache(cache)

    allnames = [f"{p}:{n}" for p, ns in need.items() for n in ns]
    missing = [k for k in allnames if not cache.get(k)]
    print("TOTAL", len(allnames), "MISSING", len(missing))
    for m in missing:
        print("  -", m)


if __name__ == "__main__":
    main()
