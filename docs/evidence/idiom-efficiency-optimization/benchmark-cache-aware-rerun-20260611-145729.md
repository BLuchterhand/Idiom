# Cache-Aware DeepSeek Flash Benchmark Rerun

Run directory: `.idiom/benchmark/20260611-145729`

Task: `Read README.md and answer in exactly one concise sentence under 35 words: what is Idiom? End with Source: README.md.`

Model contract:

- Reasonix command: `reasonix run --model deepseek-flash --max-steps 8 <task>`
- Idiom command: `.idiom/benchmark/20260611-145729/idiom-current -config .idiom/benchmark/deepseek-flash-config.json -prompt <task> -json`
- Requested hosted model: `deepseek-v4-flash`
- Idiom effective-model check: `deepseek-v4-flash` in all trials

| Runner | Median wall time | Median total tokens | Median prompt tokens | Median cache hit | Median cache miss | Median hit rate | Median output tokens | Est. median cost* |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Reasonix | 4.01s | 18,072 | 17,969 | 17,152 | 817 | 95.45% | 103 | $0.000191 |
| Idiom | 3.67s | 35,064 | 34,981 | 34,816 | 165 | 99.53% | 83 | $0.000144 |

Result:

- Reasonix used 48.5% fewer median total tokens.
- Idiom was 8.5% faster by median wall time in this run.
- Idiom now exposes provider cache accounting: median hit `34,816`, median miss `165`, median hit rate `99.53%`.
- Estimated median cost is now comparable because both runners expose cache hit/miss fields.

*Estimated cost uses `docs/evidence/idiom-efficiency-optimization/fixtures/deepseek-pricing.json`, an illustrative pricing snapshot. Verify current official DeepSeek rates before billing decisions.
