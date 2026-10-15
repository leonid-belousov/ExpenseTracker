package eval

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Price is a model's price in USD per million tokens.
type Price struct {
	Input, Output, CacheRead float64
}

// Prices of current Claude models (USD per million tokens).
var Prices = map[string]Price{
	"claude-fable-5-1":  {10, 50, 1},
	"claude-opus-5-5":   {4, 20, 0.20},
	"claude-opus-5":     {5, 25, 0.50},
	"claude-sonnet-5-5": {2, 10, 0.20},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01},
}

// TagStat aggregates outcomes with one tag.
type TagStat struct {
	Tag    string `json:"tag"`
	Passed int    `json:"passed"`
	Total  int    `json:"total"`
}

// Summary aggregates a run.
type Summary struct {
	Passed      int           `json:"passed"`
	Total       int           `json:"total"`
	Tags        []TagStat     `json:"tags"`
	Messages    int           `json:"messages"`
	LatencyP50  time.Duration `json:"latency_p50_ns"`
	LatencyP95  time.Duration `json:"latency_p95_ns"`
	AvgSteps    float64       `json:"avg_model_calls_per_message"`
	AvgInput    float64       `json:"avg_input_tokens_per_message"`
	AvgOutput   float64       `json:"avg_output_tokens_per_message"`
	CacheShare  float64       `json:"cache_read_share"`
	CostUSD     float64       `json:"cost_usd"`
	CostKnown   bool          `json:"cost_known"`
	CostPer1000 float64       `json:"cost_per_1000_messages_usd"`
}

// Summarize aggregates outcomes; price may be nil when unknown.
func Summarize(outs []Outcome, price *Price) Summary {
	var s Summary
	tags := map[string]*TagStat{}
	var lat []time.Duration
	var in, outTok, cache, steps int64
	for _, o := range outs {
		s.Total++
		if o.Pass {
			s.Passed++
		}
		for _, t := range append([]string{"all"}, o.Tags...) {
			ts := tags[t]
			if ts == nil {
				ts = &TagStat{Tag: t}
				tags[t] = ts
			}
			ts.Total++
			if o.Pass {
				ts.Passed++
			}
		}
		s.Messages += o.Turns
		lat = append(lat, o.TurnDurations...)
		in += o.Usage.InputTokens
		outTok += o.Usage.OutputTokens
		cache += o.Usage.CacheReadTokens
		steps += int64(o.Steps)
	}
	for _, ts := range tags {
		s.Tags = append(s.Tags, *ts)
	}
	sort.Slice(s.Tags, func(i, j int) bool {
		if (s.Tags[i].Tag == "all") != (s.Tags[j].Tag == "all") {
			return s.Tags[i].Tag == "all"
		}
		return s.Tags[i].Tag < s.Tags[j].Tag
	})
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		s.LatencyP50 = lat[len(lat)/2]
		s.LatencyP95 = lat[min(len(lat)-1, len(lat)*95/100)]
	}
	if s.Messages > 0 {
		n := float64(s.Messages)
		s.AvgSteps, s.AvgInput, s.AvgOutput = float64(steps)/n, float64(in)/n, float64(outTok)/n
	}
	if in > 0 {
		s.CacheShare = float64(cache) / float64(in)
	}
	if price != nil {
		s.CostKnown = true
		s.CostUSD = (float64(in-cache)*price.Input + float64(cache)*price.CacheRead + float64(outTok)*price.Output) / 1e6
		if s.Messages > 0 {
			s.CostPer1000 = s.CostUSD / float64(s.Messages) * 1000
		}
	}
	return s
}

func pct(p, t int) string {
	if t == 0 {
		return "-"
	}
	return fmt.Sprintf("%5.1f%%", 100*float64(p)/float64(t))
}

// Print writes a human-readable report.
func Print(w io.Writer, outs []Outcome, s Summary, verbose bool) {
	for _, o := range outs {
		if o.Pass && !verbose {
			continue
		}
		mark := "PASS"
		if !o.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "%s %-34s run %d  %s\n", mark, o.CaseID, o.Run+1, o.Reason)
		if !o.Pass || verbose {
			for _, r := range o.Replies {
				fmt.Fprintf(w, "     reply: %s\n", strings.ReplaceAll(r, "\n", " / "))
			}
		}
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "tag\tpassed\ttotal\trate")
	for _, t := range s.Tags {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", t.Tag, t.Passed, t.Total, pct(t.Passed, t.Total))
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintf(w, "messages: %d   latency p50 %s, p95 %s   model calls/message %.2f\n",
		s.Messages, s.LatencyP50.Round(time.Millisecond), s.LatencyP95.Round(time.Millisecond), s.AvgSteps)
	fmt.Fprintf(w, "tokens/message: input %.0f (cache reads %.0f%%), output %.0f\n", s.AvgInput, 100*s.CacheShare, s.AvgOutput)
	if s.CostKnown {
		fmt.Fprintf(w, "cost: $%.4f total, $%.2f per 1000 messages\n", s.CostUSD, s.CostPer1000)
	} else {
		fmt.Fprintln(w, "cost: unknown for this model (pass -price-in/-price-out)")
	}
}
