#!/usr/bin/env python3
"""Turns inferbench JSON reports into Markdown tables so numbers are never transcribed by hand.

  results_to_md.py table  report.json [...]        one table per report
  results_to_md.py repeat report1.json report2.json [...]   run-to-run variance of the same scenario
  results_to_md.py overhead direct.json gateway.json        gateway overhead (same load, with/without gateway)
  results_to_md.py pairs DIR                                 paired overhead runs (overhead-pairN-direct/gateway.json)
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
    if stream:
        head += ["TTFT p50", "TTFT p95", "TTFT p99"]
    rows = []
    for l in rep["levels"]:
        r = [l["concurrency"], l["requests"], l["failed"], f(l["requests_per_sec"], 2), f(l["output_tokens_per_sec"], 1),
             f(l["latency"]["p50_ms"]), f(l["latency"]["p95_ms"]), f(l["latency"]["p99_ms"])]
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


if __name__ == "__main__":
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    mode, args = sys.argv[1], sys.argv[2:]
    if mode == "table":
        for p in args:
            print(f"**{p.split('/')[-1]}**\n\n{table(load(p))}\n")
    elif mode == "repeat":
        print(repeat(args))
    elif mode == "pairs":
        print(pairs(args[0]))
    elif mode == "overhead":
        print(overhead(load(args[0]), load(args[1])))
    else:
        sys.exit(__doc__)
