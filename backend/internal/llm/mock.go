package llm

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

// Mock is a deterministic, rule-based stand-in for a language model, for LOCAL
// DEVELOPMENT AND TESTS ONLY. It understands a small set of Russian and
// English phrasings (the examples from the brief) and produces the same JSON
// contract as a real model, so the full validation path is exercised. It is
// not a substitute for a real model in production.
type Mock struct{}

var (
	reNumber    = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)
	reUpdateRU  = regexp.MustCompile(`не\s+([-+]?\d+(?:[.,]\d+)?)\s*,?\s*а\s+([-+]?\d+(?:[.,]\d+)?)`)
	reUpdateEN  = regexp.MustCompile(`([-+]?\d+(?:[.,]\d+)?)\s*,?\s*not\s+([-+]?\d+(?:[.,]\d+)?)`)
	reWord      = regexp.MustCompile(`[\p{L}]+`)
	reUserInput = regexp.MustCompile(`User message \(JSON string\): (.*)$`)
)

var categoryStems = map[string][]string{
	"food":          {"кофе", "кафе", "еда", "еду", "продукт", "обед", "ужин", "завтрак", "ресторан", "пицц", "coffee", "food", "lunch", "dinner", "grocer", "restaurant"},
	"transport":     {"такси", "метро", "автобус", "бензин", "транспорт", "taxi", "metro", "bus", "fuel", "transport"},
	"housing":       {"аренд", "квартир", "жкх", "коммунал", "жиль", "rent", "housing", "utilities"},
	"entertainment": {"кино", "развлеч", "концерт", "cinema", "movie", "concert", "entertainment"},
	"health":        {"аптек", "врач", "лекарств", "здоров", "pharmacy", "doctor", "medicine", "health"},
	"shopping":      {"одежд", "покупк", "магазин", "clothes", "shopping"},
	"other":         {"другое", "прочее", "other"},
}

var fillerWords = map[string]bool{
	"удали": true, "удалить": true, "delete": true, "remove": true, "трату": true, "траты": true, "расход": true,
	"expense": true, "the": true, "за": true, "на": true, "в": true, "последнюю": true, "последнее": true,
	"последняя": true, "last": true, "one": true, "вчера": true, "сегодня": true, "позавчера": true,
	"yesterday": true, "today": true, "руб": true, "рублей": true, "р": true, "for": true, "on": true,
}

func categoryOf(word string) string {
	for cat, stems := range categoryStems {
		for _, s := range stems {
			if strings.HasPrefix(word, s) {
				return cat
			}
		}
	}
	return ""
}

func periodOf(t string) map[string]any {
	preset := ""
	switch {
	case strings.Contains(t, "сегодня") || strings.Contains(t, "today"):
		preset = "today"
	case strings.Contains(t, "вчера") || strings.Contains(t, "yesterday"):
		preset = "yesterday"
	case (strings.Contains(t, "прошл") && strings.Contains(t, "недел")) || strings.Contains(t, "last week"):
		preset = "last_week"
	case strings.Contains(t, "недел") || strings.Contains(t, "week"):
		preset = "this_week"
	case (strings.Contains(t, "прошл") && strings.Contains(t, "месяц")) || strings.Contains(t, "last month"):
		preset = "last_month"
	case strings.Contains(t, "месяц") || strings.Contains(t, "month"):
		preset = "this_month"
	case strings.Contains(t, "год") || strings.Contains(t, "year"):
		preset = "this_year"
	}
	if preset == "" {
		return nil
	}
	return map[string]any{"preset": preset, "from": nil, "to": nil}
}

func dateOf(t string) any {
	switch {
	case strings.Contains(t, "позавчера"):
		return "day_before_yesterday"
	case strings.Contains(t, "вчера") || strings.Contains(t, "yesterday"):
		return "yesterday"
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Complete implements Completer by applying rules to the quoted user message.
func (Mock) Complete(_ context.Context, _, user string) (string, error) {
	text := user
	if m := reUserInput.FindStringSubmatch(user); m != nil {
		_ = json.Unmarshal([]byte(m[1]), &text)
	}
	t := strings.ToLower(strings.TrimSpace(text))
	out := map[string]any{
		"action": "help", "amount": nil, "category": nil, "date": nil, "comment": nil,
		"period": nil, "target": nil, "changes": nil, "question": nil,
	}
	words := reWord.FindAllString(t, -1)
	isLast := strings.Contains(t, "последн") || strings.Contains(t, "last")

	switch {
	case strings.HasPrefix(t, "удали") || strings.HasPrefix(t, "delete") || strings.HasPrefix(t, "remove"):
		target := map[string]any{"last": isLast, "amount": nil, "category": nil, "text": nil, "date": dateOf(t)}
		if n := reNumber.FindString(t); n != "" {
			target["amount"] = n
		}
		for _, w := range words {
			if !fillerWords[w] {
				target["text"] = w
				break
			}
		}
		out["action"], out["target"] = "delete", target

	case reUpdateRU.MatchString(t) || reUpdateEN.MatchString(t):
		var oldV, newV string
		if m := reUpdateRU.FindStringSubmatch(t); m != nil {
			oldV, newV = m[1], m[2]
		} else {
			m := reUpdateEN.FindStringSubmatch(t)
			newV, oldV = m[1], m[2]
		}
		out["action"] = "update"
		out["target"] = map[string]any{"last": isLast, "amount": oldV, "category": nil, "text": nil, "date": nil}
		out["changes"] = map[string]any{"amount": newV, "category": nil, "date": nil, "comment": nil}

	case strings.Contains(t, "сколько") || strings.Contains(t, "how much"):
		cat := ""
		for _, w := range words {
			if c := categoryOf(w); c != "" {
				cat = c
			}
		}
		out["action"], out["category"], out["period"] = "summary", nullable(cat), periodOf(t)

	case strings.Contains(t, "покажи") || strings.Contains(t, "show") || strings.Contains(t, "список") || strings.HasPrefix(t, "траты"):
		cat := ""
		for _, w := range words {
			if c := categoryOf(w); c != "" {
				cat = c
			}
		}
		out["action"], out["category"], out["period"] = "list", nullable(cat), periodOf(t)

	case reNumber.MatchString(t):
		out["action"] = "create"
		out["amount"] = reNumber.FindString(t)
		out["date"] = dateOf(t)
		var comment []string
		cat := ""
		for _, w := range words {
			if fillerWords[w] {
				continue
			}
			if c := categoryOf(w); c != "" && cat == "" {
				cat = c
			}
			comment = append(comment, w)
		}
		out["category"] = nullable(cat)
		out["comment"] = nullable(strings.Join(comment, " "))
	}
	b, err := json.Marshal(out)
	return string(b), err
}
