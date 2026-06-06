#!/usr/bin/env python3
"""
Charts for the Fenwick matchmaking article. Values are pinned from the
actual benchmark runs in cmd/scaleup, cmd/memcheck, and the testing.B
output captured in bench/. Re-run after a fresh benchmark sweep to refresh.
"""
import matplotlib.pyplot as plt
import matplotlib.ticker as mticker
import numpy as np
import os

OUT_DIR = os.path.dirname(os.path.abspath(__file__))

# Color palette tuned for printable contrast.
COLORS = {
    "fenwick":   "#1f77b4",
    "hybrid":    "#2ca02c",
    "sortedarr": "#7f7f7f",
    "skiplist":  "#d62728",
}

LABELS = {
    "fenwick":   "Fenwick",
    "hybrid":    "Hybrid",
    "sortedarr": "SortedArr",
    "skiplist":  "SkipList",
}

# ------------------------------------------------------------------
# Chart 1: Isolated operation latency at N=100K
# ------------------------------------------------------------------
# Values: averaged across 3 distributions, ns/op from testing.B runs.
ops = ["Rank", "Range count", "Update (R+A)"]
data = {
    "fenwick":   [17,  31,    225],
    "hybrid":    [17,  32,    357],
    "sortedarr": [181, 355,   269000],
    "skiplist":  [624, 1190,  2993],
}

order = ["fenwick", "hybrid", "sortedarr", "skiplist"]
fig, ax = plt.subplots(figsize=(9, 5.5))
x = np.arange(len(ops))
width = 0.2

for i, key in enumerate(order):
    offset = (i - (len(order) - 1) / 2) * width
    bars = ax.bar(x + offset, data[key], width, label=LABELS[key], color=COLORS[key])
    for bar, val in zip(bars, data[key]):
        # Label each bar above the top.
        if val >= 1000:
            txt = f"{val/1000:.1f}K" if val < 100000 else f"{val/1000:.0f}K"
        else:
            txt = str(val)
        ax.text(bar.get_x() + bar.get_width()/2, val * 1.15, txt,
                ha="center", va="bottom", fontsize=8)

ax.set_yscale("log")
ax.set_ylabel("nanoseconds per operation (log scale)")
ax.set_title("Isolated operation latency at N=100,000 players")
ax.set_xticks(x)
ax.set_xticklabels(ops)
ax.legend(loc="upper left", fontsize=9, framealpha=0.95)
ax.grid(True, which="both", axis="y", alpha=0.25)
ax.yaxis.set_major_formatter(mticker.FuncFormatter(lambda y, _: f"{int(y):,}"))
ax.set_ylim(8, 1_000_000)

plt.tight_layout()
plt.savefig(f"{OUT_DIR}/01_isolated_latency.png", dpi=140)
print(f"Wrote {OUT_DIR}/01_isolated_latency.png")
plt.close()

# ------------------------------------------------------------------
# Chart 2: Rank latency scaling 100K -> 1M
# ------------------------------------------------------------------
# Values: from cmd/scaleup, uniform distribution.
ns_values = [100_000, 250_000, 500_000, 1_000_000]
scaling = {
    "fenwick":  [16,  17,  16,   16],
    "hybrid":   [17,  17,  17,   17],
    "skiplist": [576, 821, 1029, 1274],
}

fig, ax = plt.subplots(figsize=(9, 5.5))
for key, vals in scaling.items():
    ax.plot(ns_values, vals, marker="o", linewidth=2.2, markersize=8,
            label=LABELS[key], color=COLORS[key])
    # Annotate first and last points.
    ax.annotate(f"{vals[0]} ns", xy=(ns_values[0], vals[0]),
                xytext=(-12, 8), textcoords="offset points",
                fontsize=8.5, color=COLORS[key])
    ax.annotate(f"{vals[-1]} ns", xy=(ns_values[-1], vals[-1]),
                xytext=(8, 0), textcoords="offset points",
                fontsize=8.5, color=COLORS[key])

ax.set_xscale("log")
ax.set_xlabel("steady-state players (N)")
ax.set_ylabel("Rank query latency (nanoseconds)")
ax.set_title("Rank query latency vs population size\nFenwick and Hybrid are flat. SkipList grows 2.2× per 10×N.")
ax.set_xticks(ns_values)
ax.xaxis.set_major_formatter(mticker.FuncFormatter(lambda x, _: f"{int(x/1000)}K" if x < 1_000_000 else "1M"))
ax.legend(loc="upper left", fontsize=10)
ax.grid(True, which="both", alpha=0.25)
ax.set_ylim(0, 1400)

plt.tight_layout()
plt.savefig(f"{OUT_DIR}/02_scaling.png", dpi=140)
print(f"Wrote {OUT_DIR}/02_scaling.png")
plt.close()

# ------------------------------------------------------------------
# Chart 3: Memory footprint per player
# ------------------------------------------------------------------
mem = {
    "fenwick":   28,
    "hybrid":    41,
    "sortedarr": 44,
    "skiplist":  97,
}

fig, ax = plt.subplots(figsize=(9, 5))
keys = ["fenwick", "hybrid", "sortedarr", "skiplist"]
labels_x = [LABELS[k] for k in keys]
vals = [mem[k] for k in keys]
colors = [COLORS[k] for k in keys]
bars = ax.bar(labels_x, vals, color=colors)

for bar, v in zip(bars, vals):
    ax.text(bar.get_x() + bar.get_width()/2, v + 2.5, f"{v} B",
            ha="center", va="bottom", fontsize=10, fontweight="bold")

# Add a secondary annotation: what this projects to at 20M players.
ax2 = ax.twinx()
ax2.set_ylim(0, max(vals) * 20_000_000 / 1e9 * 1.15)
ax2.set_ylabel("projected total at N=20M (GB)", color="#555")
ax.set_ylabel("bytes per player (heap delta at N=100K)")
ax.set_title("Per-player memory footprint at steady state")
ax.set_ylim(0, max(vals) * 1.15)
ax.grid(True, axis="y", alpha=0.25)
ax.tick_params(axis="x", labelsize=10)

# Project text inside chart.
for i, (k, v) in enumerate(zip(keys, vals)):
    projected_gb = v * 20_000_000 / 1e9
    ax.text(i, v / 2, f"~{projected_gb:.2f} GB\n@ N=20M",
            ha="center", va="center", fontsize=8.5, color="white",
            fontweight="bold")

plt.tight_layout()
plt.savefig(f"{OUT_DIR}/03_memory.png", dpi=140)
print(f"Wrote {OUT_DIR}/03_memory.png")
plt.close()

print("\nAll charts written to", OUT_DIR)
