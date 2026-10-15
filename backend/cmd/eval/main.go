// Command eval measures the bot agent on the eval dataset.
//
// The model is configured with the same environment variables as the bot
// (LLM_PROVIDER, LLM_API_KEY, LLM_MODEL, LLM_BASE_URL, LLM_TIMEOUT). With
// LLM_PROVIDER=mock it runs offline against the rule-based model, which is a
// baseline, not a target. Running against a real provider costs money.
//
//	go run ./cmd/eval                       # all cases, 1 run each
//	go run ./cmd/eval -tags delete,memory   # only cases with these tags
//	go run ./cmd/eval -runs 3 -out report.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"expensetracker/internal/agent"
	"expensetracker/internal/config"
	"expensetracker/internal/eval"
)

func main() {
	var (
		casesPath = flag.String("cases", "eval/cases.json", "dataset file")
		tags      = flag.String("tags", "", "comma-separated tags; run only cases having any of them")
		ids       = flag.String("ids", "", "comma-separated case ids to run")
		runs      = flag.Int("runs", 1, "runs per case (models are not deterministic)")
		parallel  = flag.Int("parallel", 4, "cases run concurrently")
		maxSteps  = flag.Int("max-steps", 6, "model calls per message")
		out       = flag.String("out", "", "write a JSON report to this file")
		verbose   = flag.Bool("v", false, "print passing cases and replies too")
		priceIn   = flag.Float64("price-in", 0, "input price, USD per million tokens (overrides the built-in table)")
		priceOut  = flag.Float64("price-out", 0, "output price, USD per million tokens")
	)
	flag.Parse()

	llmCfg, err := config.LoadLLM()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	cases, err := eval.Load(*casesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	cases = filter(cases, *tags, *ids)
	if len(cases) == 0 {
		fmt.Fprintln(os.Stderr, "error: no cases selected")
		os.Exit(2)
	}

	model := llmCfg.Model
	if llmCfg.Provider == "mock" {
		model = "rule-based mock"
	}
	fmt.Printf("eval: %d cases x %d runs, provider %s, model %s\n\n", len(cases), *runs, llmCfg.Provider, model)

	r := eval.Runner{Model: agent.NewModel(llmCfg), MaxSteps: *maxSteps, Timeout: llmCfg.Timeout, Parallel: *parallel}
	outs := r.Run(context.Background(), cases, *runs)

	var price *eval.Price
	if p, ok := eval.Prices[llmCfg.Model]; ok && llmCfg.Provider == "anthropic" {
		price = &p
	}
	if *priceIn > 0 || *priceOut > 0 {
		price = &eval.Price{Input: *priceIn, Output: *priceOut, CacheRead: *priceIn}
	}
	if llmCfg.Provider == "mock" {
		price = &eval.Price{}
	}
	sum := eval.Summarize(outs, price)
	eval.Print(os.Stdout, outs, sum, *verbose)

	if *out != "" {
		b, _ := json.MarshalIndent(map[string]any{
			"provider": llmCfg.Provider, "model": model, "runs": *runs, "summary": sum, "outcomes": outs,
		}, "", "  ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	if sum.Passed != sum.Total {
		os.Exit(1)
	}
}

func filter(cases []eval.Case, tags, ids string) []eval.Case {
	split := func(s string) []string {
		var out []string
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	wantTags, wantIDs := split(tags), split(ids)
	var out []eval.Case
	for _, c := range cases {
		if len(wantIDs) > 0 && !slices.Contains(wantIDs, c.ID) {
			continue
		}
		if len(wantTags) > 0 && !slices.ContainsFunc(c.Tags, func(t string) bool { return slices.Contains(wantTags, t) }) {
			continue
		}
		out = append(out, c)
	}
	return out
}
