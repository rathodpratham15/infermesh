#!/usr/bin/env python3
"""Turns inferbench JSON reports into Markdown tables so numbers are never transcribed by hand.

  results_to_md.py table  report.json [...]        one table per report
  results_to_md.py repeat report1.json report2.json [...]   run-to-run variance of the same scenario
  results_to_md.py overhead direct.json gateway.json        gateway overhead (same load, with/without gateway)
  results_to_md.py pairs DIR                                 paired overhead runs (overhead-pairN-direct/gateway.json)
  results_to_md.py pooled DIR [DIR...]                       paired runs pooled across directories, with a rough 95% interval
  results_to_md.py runs SUITE_A SUITE_B                      the same experiments from two separate suite runs, side by side
  results_to_md.py steady-vs-wall SUITE_DIR                  how far the steady-state and wall-clock req/s differ
"""
import json
import statistics
import sys


def load(p):
    with open(p) as f:
        return json.load(f)


def f(v, d=0):
    return f"{v:.{d}f}"


def table(rep):
    stream = rep["config"]["stream"]
    head = ["conc", "reqs", "failed", "req/s", "tok/s", "p50 ms", "p95 ms", "p99 ms"]
    has_wall = all("wall_requests_per_sec" in l for l in rep["levels"])
    if has_wall:
        head.append("req/s (wall)")
    if stream:
        head += ["TTFT p50", "TTFT p95", "TTFT p99"]
    rows = []
    for l in rep["levels"]:
        r = [l["concurrency"], l["requests"], l["failed"], f(l["requests_per_sec"], 2), f(l["output_tokens_per_sec"], 1),
             f(l["latency"]["p50_ms"]), f(l["latency"]["p95_ms"]), f(l["latency"]["p99_ms"])]
        if has_wall:
            r.append(f(l["wall_requests_per_sec"], 2))
        if stream:
            t = l.get("ttft")
            r += [f(t["p50_ms"]), f(t["p95_ms"]), f(t["p99_ms"])] if t else ["-", "-", "-"]
        rows.append(r)
    return md(head, rows)


def md(head, rows):
    out = ["| " + " | ".join(head) + " |", "|" + "|".join("---:" for _ in head) + "|"]
    out += ["| " + " | ".join(str(c) for c in r) + " |" for r in rows]
    return "\n".join(out)


def repeat(paths):
    reps = [load(p) for p in paths]
    levels = range(len(reps[0]["levels"]))
    rows = []
    for i in levels:
        conc = reps[0]["levels"][i]["concurrency"]
        for key, label, d in [("requests_per_sec", "req/s", 2), ("output_tokens_per_sec", "tok/s", 1)]:
            vals = [r["levels"][i][key] for r in reps]
            rows.append([conc, label] + [f(v, d) for v in vals] + [f(statistics.mean(vals), d), f(max(vals) - min(vals), d)])
        p50 = [r["levels"][i]["latency"]["p50_ms"] for r in reps]
        rows.append([conc, "p50 ms"] + [f(v) for v in p50] + [f(statistics.mean(p50)), f(max(p50) - min(p50))])
    head = ["conc", "metric"] + [f"run {n+1}" for n in range(len(reps))] + ["mean", "max-min"]
    return md(head, rows)


def overhead(direct, gateway):
    rows = []
    for a, b in zip(direct["levels"], gateway["levels"]):
        for key, label in [("p50_ms", "p50 latency ms"), ("p95_ms", "p95 latency ms"), ("p99_ms", "p99 latency ms")]:
            x, y = a["latency"][key], b["latency"][key]
            rows.append([a["concurrency"], label, f(x), f(y), f(y - x), f"{(y / x - 1) * 100:+.1f}%" if x else "-"])
        x, y = a["requests_per_sec"], b["requests_per_sec"]
        rows.append([a["concurrency"], "req/s", f(x, 2), f(y, 2), f(y - x, 2), f"{(y / x - 1) * 100:+.1f}%" if x else "-"])
    return md(["conc", "metric", "direct to engine", "via gateway", "difference", "relative"], rows)


def pairs(directory):
    import glob
    import os
    idx = sorted({int(os.path.basename(p).split("pair")[1].split("-")[0]) for p in glob.glob(os.path.join(directory, "overhead-pair*-direct.json"))})
    per_conc = {}
    for i in idx:
        d = load(os.path.join(directory, f"overhead-pair{i}-direct.json"))
        g = load(os.path.join(directory, f"overhead-pair{i}-gateway.json"))
        for a, b in zip(d["levels"], g["levels"]):
            per_conc.setdefault(a["concurrency"], []).append((i, a, b))
    out = []
    for conc, rows in sorted(per_conc.items()):
        out.append(f"**concurrency {conc}**: per-pair results (direct to engine vs via gateway)\n")
        body, dp50, drps, dtok = [], [], [], []
        for i, a, b in rows:
            d50 = b["latency"]["p50_ms"] - a["latency"]["p50_ms"]
            drate = b["requests_per_sec"] / a["requests_per_sec"] - 1 if a["requests_per_sec"] else 0
            dp50.append(d50)
            drps.append(drate * 100)
            body.append([i, f(a["latency"]["p50_ms"]), f(b["latency"]["p50_ms"]), f"{d50:+.0f}",
                         f(a["requests_per_sec"], 2), f(b["requests_per_sec"], 2), f"{drate * 100:+.1f}%",
                         a["failed"] + b["failed"]])
        out.append(md(["pair", "direct p50 ms", "gateway p50 ms", "p50 diff ms", "direct req/s", "gateway req/s", "req/s diff", "failed"], body))
        out.append(f"\np50 difference (gateway - direct): median {statistics.median(dp50):+.0f} ms, min {min(dp50):+.0f}, max {max(dp50):+.0f} over {len(rows)} pairs; "
                   f"req/s difference: median {statistics.median(drps):+.1f}%, min {min(drps):+.1f}%, max {max(drps):+.1f}%\n")
    return "\n".join(out)


def pooled(dirs):
    import glob
    import math
    import os
    per_conc = {}
    for d in dirs:
        for dp in sorted(glob.glob(os.path.join(d, "overhead-pair*-direct.json"))):
            gp = dp.replace("-direct.json", "-gateway.json")
            a, g = load(dp), load(gp)
            for la, lb in zip(a["levels"], g["levels"]):
                per_conc.setdefault(la["concurrency"], []).append(lb["latency"]["p50_ms"] - la["latency"]["p50_ms"])
    t95 = {2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571, 6: 2.447, 7: 2.365, 8: 2.306, 9: 2.262, 10: 2.228,
           11: 2.201, 12: 2.179, 13: 2.160, 14: 2.145, 15: 2.131}
    rows = []
    for conc, diffs in sorted(per_conc.items()):
        n = len(diffs)
        mean = statistics.mean(diffs)
        sd = statistics.stdev(diffs) if n > 1 else float("nan")
        half = t95.get(n, 1.96) * sd / math.sqrt(n) if n > 1 else float("nan")
        rows.append([conc, n, f"{mean:+.0f}", f"{statistics.median(diffs):+.0f}", f(sd), f"{mean - half:+.0f} to {mean + half:+.0f}",
                     f"{min(diffs):+.0f} / {max(diffs):+.0f}"])
    return md(["conc", "pairs", "mean diff ms", "median diff ms", "stdev ms", "rough 95% interval (mean) ms", "min / max ms"], rows)


def runs(dir_a, dir_b):
    import os
    names = ["b-increasing-concurrency", "c-long-prompts", "d-short-prompts", "e-streaming"]
    rows = []
    for n in names:
        pa, pb = os.path.join(dir_a, n + ".json"), os.path.join(dir_b, n + ".json")
        if not (os.path.exists(pa) and os.path.exists(pb)):
            continue
        a, b = load(pa), load(pb)
        for la, lb in zip(a["levels"], b["levels"]):
            # run 1 predates the steady-state definition, so compare on the wall-clock figure both have
            wa = la.get("wall_requests_per_sec", la["requests_per_sec"])
            wb = lb.get("wall_requests_per_sec", lb["requests_per_sec"])
            ta, tb = la.get("ttft"), lb.get("ttft")
            rows.append([n.split("-")[0].upper(), la["concurrency"], f(wa, 2), f(wb, 2), f"{(wb / wa - 1) * 100:+.0f}%",
                         f(la["latency"]["p50_ms"]), f(lb["latency"]["p50_ms"]),
                         f(ta["p50_ms"]) if ta else "-", f(tb["p50_ms"]) if tb else "-"])
    return md(["exp", "conc", "run 1 req/s (wall)", "run 2 req/s (wall)", "change", "run 1 p50 ms", "run 2 p50 ms", "run 1 TTFT p50", "run 2 TTFT p50"], rows)


def steady_vs_wall(d):
    import glob
    import os
    rows, diffs = [], []
    for p in sorted(glob.glob(os.path.join(d, "*.json"))):
        rep = load(p)
        for l in rep["levels"]:
            if "wall_requests_per_sec" not in l or not l["wall_requests_per_sec"]:
                continue
            pct = (l["requests_per_sec"] / l["wall_requests_per_sec"] - 1) * 100
            diffs.append(pct)
            rows.append([os.path.basename(p)[:-5], l["concurrency"], f(l["requests_per_sec"], 2), f(l["wall_requests_per_sec"], 2),
                         f(l["drain_seconds"], 1), f"{pct:+.1f}%"])
    out = md(["experiment", "conc", "steady req/s", "wall req/s", "drain s", "steady vs wall"], rows)
    return out + f"\n\nrange of the difference: {min(diffs):+.1f}% to {max(diffs):+.1f}%, mean {statistics.mean(diffs):+.1f}%"


if __name__ == "__main__":
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    mode, args = sys.argv[1], sys.argv[2:]
    if mode == "table":
        for p in args:
            print(f"**{p.split('/')[-1]}**\n\n{table(load(p))}\n")
    elif mode == "repeat":
        print(repeat(args))
    elif mode == "pooled":
        print(pooled(args))
    elif mode == "runs":
        print(runs(args[0], args[1]))
    elif mode == "steady-vs-wall":
        print(steady_vs_wall(args[0]))
    elif mode == "pairs":
        print(pairs(args[0]))
    elif mode == "overhead":
        print(overhead(load(args[0]), load(args[1])))
    else:
        sys.exit(__doc__)
