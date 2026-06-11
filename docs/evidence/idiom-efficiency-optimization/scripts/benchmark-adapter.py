#!/usr/bin/env python3
"""A5.4 Benchmark Adapter — report cache and cost metrics from benchmark artifacts.

Usage:
  # Report a single summary.json from a benchmark run
  python3 benchmark-adapter.py .idiom/benchmark/20260611-142351/summary.json

  # Report with pricing for cost estimate
  python3 benchmark-adapter.py .idiom/benchmark/20260611-142351/summary.json \
    --pricing docs/evidence/idiom-efficiency-optimization/fixtures/deepseek-pricing.json

  # Report a session_summary JSONL line (legacy or new format)
  python3 benchmark-adapter.py --session-summary fixtures/legacy-session-summary.jsonl

  # Report a Reasonix metrics-*.json file (has cache_hit_tokens / cache_miss_tokens)
  python3 benchmark-adapter.py --reasonix-metrics .idiom/benchmark/20260611-122151/reasonix/metrics-1.json

  # Report multiple trial JSONL lines and aggregate
  python3 benchmark-adapter.py --session-summary-glob "fixtures/*-session-summary.jsonl" \
    --pricing fixtures/deepseek-pricing.json
"""

import argparse
import json
import os
import sys


def load_pricing(path):
    """Load a pricing config and return (provider, model, date, rates, currency)."""
    with open(path) as f:
        p = json.load(f)
    rates = p.get("rates")
    return (
        p.get("provider", "unknown"),
        p.get("model", "unknown"),
        p.get("pricing_snapshot_date", "unknown"),
        rates,
        p.get("currency", "USD"),
    )


def report_session_summary(summary, pricing=None):
    """Print a cache/cost report from a single session_summary dict.

    Accepts both legacy (no cache fields) and new (with cache fields) formats.
    Returns a dict of extracted fields for aggregation.
    """
    lines = []
    fields = {}

    prompt_tokens = summary.get("prompt_tokens", 0)
    completion_tokens = summary.get("completion_tokens", 0)
    total_tokens = summary.get("total_tokens", 0)

    cache_hit = summary.get("prompt_cache_hit_tokens")
    cache_miss = summary.get("prompt_cache_miss_tokens")
    accounting_known = summary.get("prompt_cache_accounting_known", False)
    reasoning_tokens = summary.get("reasoning_tokens")
    hit_rate = summary.get("prompt_cache_hit_rate")

    model_calls = summary.get("model_calls", 0)
    duration_ms = summary.get("duration_ms", 0)
    session_id = summary.get("session_id", "")

    lines.append(f"  Session:       {session_id}")
    lines.append(f"  Model calls:   {model_calls}")
    lines.append(f"  Duration:      {duration_ms} ms")
    lines.append(f"  Raw tokens:")
    lines.append(f"    prompt_tokens:          {prompt_tokens:>10}")
    lines.append(f"    completion_tokens:      {completion_tokens:>10}")
    lines.append(f"    total_tokens:           {total_tokens:>10}")

    fields["prompt_tokens"] = prompt_tokens
    fields["completion_tokens"] = completion_tokens
    fields["total_tokens"] = total_tokens
    fields["prompt_cache_accounting_known"] = accounting_known

    if cache_hit is not None or cache_miss is not None:
        cache_hit = cache_hit or 0
        cache_miss = cache_miss or 0
        fields["prompt_cache_hit_tokens"] = cache_hit
        fields["prompt_cache_miss_tokens"] = cache_miss

        lines.append(f"  Cache accounting known:  {'YES' if accounting_known else 'NO'}")
        lines.append(f"  Cache breakdown:")
        lines.append(f"    prompt_cache_hit_tokens:   {cache_hit:>10}")
        lines.append(f"    prompt_cache_miss_tokens:  {cache_miss:>10}")

        if accounting_known and (cache_hit + cache_miss) > 0:
            rate = hit_rate if hit_rate is not None else (cache_hit / (cache_hit + cache_miss))
            fields["prompt_cache_hit_rate"] = rate
            lines.append(f"    prompt_cache_hit_rate:     {rate:>10.2%}")
        elif accounting_known:
            lines.append(f"    prompt_cache_hit_rate:     {'N/A (zero tokens)'}")
        else:
            lines.append(f"    prompt_cache_hit_rate:     {'UNKNOWN (cache accounting not reported)'}")
    else:
        fields["prompt_cache_accounting_known"] = False
        lines.append(f"  Cache accounting:        NOT REPORTED (legacy format)")
        lines.append(f"  Cache breakdown:         unavailable")

    if reasoning_tokens is not None:
        fields["reasoning_tokens"] = reasoning_tokens
        lines.append(f"  Reasoning tokens:        {reasoning_tokens:>10}")

    # Cost estimate
    if pricing:
        provider, model, date, rates, currency = pricing
        if rates and accounting_known and cache_hit is not None and cache_miss is not None:
            input_miss_rate = rates.get("input_miss_rate", 0)
            input_hit_rate = rates.get("input_hit_rate", 0)
            output_rate = rates.get("output_rate", 0)

            input_cost = (cache_miss * input_miss_rate) + (cache_hit * input_hit_rate)
            output_cost = completion_tokens * output_rate
            total_cost = input_cost + output_cost

            lines.append(f"  Estimated cost ({currency}):")
            lines.append(f"    Pricing snapshot:         {provider} {model} ({date})")
            lines.append(f"    Input miss rate:          {input_miss_rate}")
            lines.append(f"    Input hit rate:           {input_hit_rate}")
            lines.append(f"    Output rate:              {output_rate}")
            lines.append(f"    Input cost (cached):      {currency} {cache_hit * input_hit_rate:.8f}")
            lines.append(f"    Input cost (miss):        {currency} {cache_miss * input_miss_rate:.8f}")
            lines.append(f"    Output cost:              {currency} {output_cost:.8f}")
            lines.append(f"    Estimated total cost:     {currency} {total_cost:.8f}")
        elif not accounting_known:
            lines.append(f"  Cost estimate:            SKIPPED (cache accounting unknown)")
        else:
            lines.append(f"  Cost estimate:            SKIPPED (incomplete cache fields or rates)")

    print("\n".join(lines))
    print()
    return fields


def report_reasonix_metrics(metrics, pricing=None):
    """Print a cache/cost report from a Reasonix metrics JSON file.

    Reasonix metrics-*.json files have cache_hit_tokens, cache_miss_tokens,
    cost, and currency fields directly.
    """
    lines = []
    fields = {}

    prompt_tokens = metrics.get("prompt_tokens", 0)
    completion_tokens = metrics.get("completion_tokens", 0)
    cache_hit = metrics.get("cache_hit_tokens")
    cache_miss = metrics.get("cache_miss_tokens")
    steps = metrics.get("steps", 0)
    cost = metrics.get("cost")
    currency = metrics.get("currency", "¥")
    compactions = metrics.get("compactions", 0)

    lines.append(f"  Steps:               {steps}")
    lines.append(f"  Compactions:         {compactions}")
    lines.append(f"  Raw tokens:")
    lines.append(f"    prompt_tokens:          {prompt_tokens:>10}")
    lines.append(f"    completion_tokens:      {completion_tokens:>10}")

    fields["prompt_tokens"] = prompt_tokens
    fields["completion_tokens"] = completion_tokens

    if cache_hit is not None and cache_miss is not None:
        hit = cache_hit
        miss = cache_miss
        total = hit + miss
        fields["prompt_cache_hit_tokens"] = hit
        fields["prompt_cache_miss_tokens"] = miss
        fields["prompt_cache_accounting_known"] = True

        lines.append(f"  Cache accounting known:  YES")
        lines.append(f"  Cache breakdown:")
        lines.append(f"    cache_hit_tokens:          {hit:>10}")
        lines.append(f"    cache_miss_tokens:         {miss:>10}")

        if total > 0:
            rate = hit / total
            fields["prompt_cache_hit_rate"] = rate
            lines.append(f"    cache_hit_rate:            {rate:>10.2%}")
        else:
            lines.append(f"    cache_hit_rate:            {'N/A (zero tokens)'}")

        # Reasonix already includes cost, but we can also compute it from pricing rates
        if cost is not None:
            lines.append(f"  Cost ({currency}):              {currency} {cost:.8f}")

        if pricing:
            provider, model, date, rates, pricing_currency = pricing
            input_miss_rate = rates.get("input_miss_rate", 0)
            input_hit_rate = rates.get("input_hit_rate", 0)
            output_rate = rates.get("output_rate", 0)

            computed_input_cost = (miss * input_miss_rate) + (hit * input_hit_rate)
            computed_output_cost = completion_tokens * output_rate
            computed_total = computed_input_cost + computed_output_cost

            lines.append(f"  Estimated cost ({pricing_currency}) from pricing snapshot:")
            lines.append(f"    Pricing snapshot:         {provider} {model} ({date})")
            lines.append(f"    Input miss rate:          {input_miss_rate}")
            lines.append(f"    Input hit rate:           {input_hit_rate}")
            lines.append(f"    Output rate:              {output_rate}")
            lines.append(f"    Computed input cost:      {pricing_currency} {computed_input_cost:.8f}")
            lines.append(f"    Computed output cost:     {pricing_currency} {computed_output_cost:.8f}")
            lines.append(f"    Computed total cost:      {pricing_currency} {computed_total:.8f}")
            if cost is not None:
                lines.append(f"    (Reasonix reported cost: {currency} {cost:.8f})")
    else:
        fields["prompt_cache_accounting_known"] = False
        lines.append(f"  Cache accounting:        NOT REPORTED")

    print("\n".join(lines))
    print()
    return fields


def report_summary_json(path, runner_key, pricing=None):
    """Report from the summary.json aggregates for a given runner key.

    The summary.json includes per-trial data under runner_key (e.g. "idiom")
    and aggregated stats under ["aggregates"][runner_key].
    """
    with open(path) as f:
        data = json.load(f)

    trials = data.get(runner_key, [])
    agg = data.get("aggregates", {}).get(runner_key, {})

    if not trials:
        print(f"  No trials found for runner '{runner_key}'")
        print()
        return

    print(f"  Runner:            {runner_key}")
    print(f"  Run directory:     {data.get('run_dir', path)}")
    print(f"  Requested model:   {data.get('requested_model', 'unknown')}")
    print(f"  Trials:            {len(trials)}")
    print()

    # Aggregate-level raw tokens
    prompt_max = agg.get("prompt_tokens", {}).get("max", 0)
    prompt_med = agg.get("prompt_tokens", {}).get("median", 0)
    prompt_min = agg.get("prompt_tokens", {}).get("min", 0)
    comp_max = agg.get("completion_tokens", {}).get("max", 0)
    comp_med = agg.get("completion_tokens", {}).get("median", 0)
    comp_min = agg.get("completion_tokens", {}).get("min", 0)
    total_max = agg.get("total_tokens", {}).get("max", 0)
    total_med = agg.get("total_tokens", {}).get("median", 0)
    total_min = agg.get("total_tokens", {}).get("min", 0)

    print(f"  Raw tokens (aggregate across {len(trials)} trials):")
    print(f"    prompt_tokens       min={prompt_min:<8} median={prompt_med:<8} max={prompt_max}")
    print(f"    completion_tokens   min={comp_min:<8} median={comp_med:<8} max={comp_max}")
    print(f"    total_tokens        min={total_min:<8} median={total_med:<8} max={total_max}")

    # Cache fields from summary.json — support the final Idiom JSONL contract
    # and the earlier Reasonix parser names used by existing benchmark artifacts.
    cache_hit_key = "prompt_cache_hit_tokens"
    cache_miss_key = "prompt_cache_miss_tokens"
    if cache_hit_key not in agg and "cached_prompt_tokens" in agg:
        cache_hit_key = "cached_prompt_tokens"
        cache_miss_key = "new_prompt_tokens"
    cached_max = agg.get(cache_hit_key, {}).get("max")
    cached_med = agg.get(cache_hit_key, {}).get("median")
    new_max = agg.get(cache_miss_key, {}).get("max")
    new_med = agg.get(cache_miss_key, {}).get("median")
    cost_yuan_med = agg.get("cost_yuan", {}).get("median")
    reasoning_med = agg.get("reasoning_tokens", {}).get("median")

    if cached_max is not None:
        print(f"  Cache accounting known:  YES")
        print(f"  Cache breakdown (aggregate):")
        print(f"    {cache_hit_key:<24} min={agg[cache_hit_key]['min']:<8} median={cached_med:<8} max={cached_max}")
        print(f"    {cache_miss_key:<24} min={agg[cache_miss_key]['min']:<8} median={new_med:<8} max={new_max}")

        # Compute hit rate from medians
        total_prompt_med = prompt_med
        if total_prompt_med > 0:
            hit_rate_med = cached_med / total_prompt_med
            print(f"    prompt_cache_hit_rate   median={hit_rate_med:.2%}")
        else:
            print(f"    prompt_cache_hit_rate   N/A")

        if reasoning_med is not None:
            print(f"    reasoning_tokens        median={reasoning_med}")

        if cost_yuan_med is not None:
            print(f"  Cost (¥, from Reasonix):  median={cost_yuan_med}")

        # Pricing-based estimate
        if pricing:
            provider, model, date, rates, currency = pricing
            input_miss_rate = rates.get("input_miss_rate", 0)
            input_hit_rate = rates.get("input_hit_rate", 0)
            output_rate = rates.get("output_rate", 0)

            # Use medians for estimate
            input_cost = (new_med * input_miss_rate) + (cached_med * input_hit_rate)
            output_cost = comp_med * output_rate
            total_cost = input_cost + output_cost

            print(f"  Estimated cost ({currency}) from pricing snapshot:")
            print(f"    Pricing snapshot:         {provider} {model} ({date})")
            print(f"    Input miss rate:          {input_miss_rate}")
            print(f"    Input hit rate:           {input_hit_rate}")
            print(f"    Output rate:              {output_rate}")
            print(f"    Estimated input cost:     {currency} {input_cost:.8f}")
            print(f"    Estimated output cost:    {currency} {output_cost:.8f}")
            print(f"    Estimated total cost:     {currency} {total_cost:.8f}")
    else:
        print(f"  Cache accounting known:  NO (legacy format or provider omitted cache fields)")
        print(f"  Cache breakdown:         unavailable in current idiom telemetry")

        if pricing:
            print(f"  Cost estimate:           SKIPPED (cache accounting unknown)")

    print()


def main():
    parser = argparse.ArgumentParser(
        description="A5.4 Benchmark Adapter — report cache and cost metrics from benchmark artifacts"
    )
    parser.add_argument(
        "path", nargs="?",
        help="Path to a summary.json file from a benchmark run"
    )
    parser.add_argument(
        "--session-summary",
        help="Path to a single session_summary JSONL line (legacy or new format)"
    )
    parser.add_argument(
        "--session-summary-glob",
        help="Glob pattern for multiple session_summary JSONL files to aggregate"
    )
    parser.add_argument(
        "--reasonix-metrics",
        help="Path to a Reasonix metrics-*.json file"
    )
    parser.add_argument(
        "--runner",
        default="idiom",
        help="Runner key in summary.json (default: idiom)"
    )
    parser.add_argument(
        "--pricing",
        help="Path to a pricing JSON file for cost estimates"
    )
    args = parser.parse_args()

    pricing = None
    if args.pricing:
        pricing = load_pricing(args.pricing)
        print(f"Pricing snapshot loaded: {pricing[0]} {pricing[1]} ({pricing[2]})")
        print()

    if args.session_summary:
        # Single session_summary JSONL line
        with open(args.session_summary) as f:
            line = f.readline().strip()
            summary = json.loads(line)
        print(f"=== Session Summary Report ===")
        print(f"Source: {args.session_summary}")
        report_session_summary(summary, pricing)

    elif args.session_summary_glob:
        # Multiple JSONL files, aggregate
        import glob
        files = sorted(glob.glob(args.session_summary_glob))
        if not files:
            print(f"No files matched: {args.session_summary_glob}")
            sys.exit(1)

        all_fields = []
        for fpath in files:
            with open(fpath) as f:
                line = f.readline().strip()
                summary = json.loads(line)
            print(f"=== Session Summary: {os.path.basename(fpath)} ===")
            fields = report_session_summary(summary, pricing)
            all_fields.append(fields)

        # Aggregate across files
        if len(all_fields) > 1:
            print(f"=== Cross-Trial Aggregation ({len(all_fields)} trials) ===")
            keys = ["prompt_tokens", "completion_tokens", "total_tokens",
                    "prompt_cache_hit_tokens", "prompt_cache_miss_tokens",
                    "reasoning_tokens"]
            for key in keys:
                vals = [f[key] for f in all_fields if key in f]
                if vals and any(v is not None for v in vals):
                    print(f"  {key:<30} min={min(vals):<10} median={sorted(vals)[len(vals)//2]:<10} max={max(vals)}")

            # Aggregate hit rate
            hit_rates = [f["prompt_cache_hit_rate"] for f in all_fields if "prompt_cache_hit_rate" in f]
            if hit_rates:
                print(f"  prompt_cache_hit_rate     min={min(hit_rates):.2%}  median={sorted(hit_rates)[len(hit_rates)//2]:.2%}  max={max(hit_rates):.2%}")
            print()

    elif args.reasonix_metrics:
        with open(args.reasonix_metrics) as f:
            metrics = json.load(f)
        print(f"=== Reasonix Metrics Report ===")
        print(f"Source: {args.reasonix_metrics}")
        report_reasonix_metrics(metrics, pricing)

    elif args.path:
        # summary.json — report for both runners
        print(f"=== Benchmark Cache/Cost Report ===")
        print(f"Source: {args.path}")
        print()
        report_summary_json(args.path, "idiom", pricing)
        report_summary_json(args.path, "reasonix", pricing)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
