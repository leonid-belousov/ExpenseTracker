package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"expensetracker/internal/llm"
	"expensetracker/internal/money"
)

// RuleModel is a deterministic stand-in for a language model, for LOCAL
// DEVELOPMENT AND TESTS ONLY. It understands roughly the brief's Russian
// examples (plus a few English ones) and drives the same tools as a real
// model, through the same validation, so the whole bot pipeline runs offline.
// It ignores conversation history and replies in Russian.
type RuleModel struct{}

var (
	reNumber   = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)
	reUpdateRU = regexp.MustCompile(`не\s+([-+]?\d+(?:[.,]\d+)?)\s*,?\s*а\s+([-+]?\d+(?:[.,]\d+)?)`)
	reUpdateEN = regexp.MustCompile(`([-+]?\d+(?:[.,]\d+)?)\s*,?\s*not\s+([-+]?\d+(?:[.,]\d+)?)`)
	reWord     = regexp.MustCompile(`\p{L}+`)
	reSplit    = regexp.MustCompile(`\s*(?:,\s+|;\s*|\s+и\s+|\s+and\s+)`) // "99,90" stays one amount
)

var categoryStems = []struct {
	cat   string
	stems []string
}{
	{"food", []string{"кофе", "кафе", "еда", "еду", "продукт", "обед", "ужин", "завтрак", "ресторан", "пицц", "булк", "coffee", "food", "lunch", "dinner", "grocer", "restaurant"}},
	{"transport", []string{"такси", "метро", "автобус", "бензин", "транспорт", "taxi", "metro", "bus", "fuel", "transport"}},
	{"housing", []string{"аренд", "квартир", "жкх", "коммунал", "жиль", "rent", "housing", "utilities"}},
	{"entertainment", []string{"кино", "развлеч", "концерт", "cinema", "movie", "concert", "entertainment"}},
	{"health", []string{"аптек", "врач", "лекарств", "здоров", "pharmacy", "doctor", "medicine", "health"}},
	{"shopping", []string{"одежд", "покупк", "магазин", "clothes", "shopping"}},
	{"other", []string{"другое", "прочее", "other"}},
}

var fillerWords = map[string]bool{
	"удали": true, "удалить": true, "delete": true, "remove": true, "трату": true, "траты": true, "расход": true,
	"expense": true, "the": true, "за": true, "на": true, "в": true, "последнюю": true, "последнее": true,
	"последняя": true, "last": true, "one": true, "вчера": true, "сегодня": true, "позавчера": true,
	"yesterday": true, "today": true, "руб": true, "рублей": true, "р": true, "for": true, "on": true,
}

var categoryRU = map[string]string{
	"food": "еда", "transport": "транспорт", "housing": "жильё", "entertainment": "развлечения",
	"health": "здоровье", "shopping": "покупки", "other": "другое",
}

func categoryOf(word string) string {
	for _, c := range categoryStems {
		for _, s := range c.stems {
			if strings.HasPrefix(word, s) {
				return c.cat
			}
		}
	}
	return ""
}

func presetOf(t string) any {
	switch {
	case strings.Contains(t, "сегодня") || strings.Contains(t, "today"):
		return "today"
	case strings.Contains(t, "вчера") || strings.Contains(t, "yesterday"):
		return "yesterday"
	case (strings.Contains(t, "прошл") && strings.Contains(t, "недел")) || strings.Contains(t, "last week"):
		return "last_week"
	case strings.Contains(t, "недел") || strings.Contains(t, "week"):
		return "this_week"
	case (strings.Contains(t, "прошл") && strings.Contains(t, "месяц")) || strings.Contains(t, "last month"):
		return "last_month"
	case strings.Contains(t, "месяц") || strings.Contains(t, "month"):
		return "this_month"
	case strings.Contains(t, "год") || strings.Contains(t, "year"):
		return "this_year"
	}
	return nil
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

func categoryIn(words []string) any {
	for _, w := range words {
		if c := categoryOf(w); c != "" {
			return c
		}
	}
	return nil
}

// Start implements llm.Model.
func (RuleModel) Start(req llm.Request) llm.Session {
	return &ruleSession{text: strings.ToLower(strings.TrimSpace(req.Text))}
}

type ruleSession struct {
	text string
	step int
	plan string // what the session is doing
	// lastOnly: the request is about the most recently added expense.
	lastOnly bool
	calls    int
	last     []byte // previous tool result
	isErr    bool
	old      string // update: the stated old amount
	new      string // update: the new amount
}

func (s *ruleSession) call(name string, input map[string]any) llm.Turn {
	s.calls++
	b, _ := json.Marshal(input)
	return llm.Turn{Calls: []llm.ToolCall{{ID: fmt.Sprintf("call_%d", s.calls), Name: name, Input: b}}}
}

func say(text string) llm.Turn { return llm.Turn{Text: text} }

type ruleFound struct {
	TotalCount int    `json:"total_count"`
	TotalSum   string `json:"total_sum"`
	Currency   string `json:"currency"`
	Period     struct {
		From string `json:"from"`
		To   string `json:"to_inclusive"`
	} `json:"period"`
	Items []expenseView `json:"items"`
}

func ruDate(iso string) string {
	if len(iso) != 10 {
		return iso
	}
	return iso[8:10] + "." + iso[5:7] + "." + iso[0:4]
}

func ruAmount(s string) string {
	if a, err := money.Parse(s); err == nil {
		return a.Human()
	}
	return s
}

func ruLine(v expenseView, currency string) string {
	line := fmt.Sprintf("%s · %s %s · %s", ruDate(v.Date), ruAmount(v.Amount), currency, categoryRU[v.Category])
	if v.Comment != "" {
		line += " · " + v.Comment
	}
	return line
}

func ruPeriod(f ruleFound) string {
	if f.Period.From == f.Period.To {
		return "за " + ruDate(f.Period.From)
	}
	return "за " + ruDate(f.Period.From) + " – " + ruDate(f.Period.To)
}

func errorText(raw []byte) string {
	var te toolError
	_ = json.Unmarshal(raw, &te)
	switch {
	case strings.Contains(te.Field, "amount"):
		return "Сумма должна быть больше нуля и иметь не больше двух знаков после запятой. Ничего не изменено."
	case strings.Contains(te.Field, "spent_at") || strings.Contains(te.Field, "date"):
		return "Дата не может быть в будущем или раньше 2000 года. Ничего не изменено."
	case strings.Contains(te.Field, "comment"):
		return "Комментарий слишком длинный. Ничего не изменено."
	}
	return "Не получилось: " + te.Message + ". Ничего не изменено."
}

// Next implements llm.Session.
func (s *ruleSession) Next(_ context.Context, results []llm.ToolResult) (llm.Turn, error) {
	s.step++
	if len(results) > 0 {
		s.last, s.isErr = []byte(results[0].Content), results[0].IsError
	}
	if s.step == 1 {
		return s.first(), nil
	}
	if s.isErr {
		return say(errorText(s.last)), nil
	}
	var f ruleFound
	_ = json.Unmarshal(s.last, &f)
	switch s.plan {
	case "summary":
		desc := ruPeriod(f)
		var sum struct {
			Category string `json:"category"`
		}
		_ = json.Unmarshal(s.last, &sum)
		if sum.Category != "" {
			desc = "в категории «" + categoryRU[sum.Category] + "» " + desc
		}
		return say(fmt.Sprintf("Потрачено %s: %s %s (%d шт.)", desc, ruAmount(f.TotalSum), f.Currency, f.TotalCount)), nil
	case "list":
		if f.TotalCount == 0 {
			return say("Трат " + ruPeriod(f) + " нет."), nil
		}
		lines := []string{"Траты " + ruPeriod(f) + ":"}
		for _, v := range f.Items {
			lines = append(lines, "• "+ruLine(v, f.Currency))
		}
		lines = append(lines, fmt.Sprintf("Итого: %d шт. на %s %s", f.TotalCount, ruAmount(f.TotalSum), f.Currency))
		return say(strings.Join(lines, "\n")), nil
	case "delete", "update":
		if s.step > 2 { // the mutation ran
			return say(""), nil
		}
		if s.lastOnly && len(f.Items) > 0 {
			f.TotalCount, f.Items = 1, f.Items[:1] // "the last one" is the newest match
		}
		switch {
		case f.TotalCount == 0:
			return say("Не нашёл такой траты среди ваших. Ничего не изменено."), nil
		case f.TotalCount > 1:
			lines := []string{fmt.Sprintf("Нашёл несколько подходящих трат (%d). Ничего не изменено — уточните сумму, дату или описание:", f.TotalCount)}
			for _, v := range f.Items {
				lines = append(lines, "• "+ruLine(v, f.Currency))
			}
			return say(strings.Join(lines, "\n")), nil
		}
		target := f.Items[0]
		if s.plan == "delete" {
			return s.call(ToolDeleteExpense, map[string]any{"id": target.ID}), nil
		}
		if old, err := money.Parse(s.old); err != nil || old.String() != target.Amount {
			return say("Последняя ваша трата — " + ruLine(target, f.Currency) + ". Она не совпадает с запросом, поэтому ничего не изменено. Уточните, какую трату имеете в виду."), nil
		}
		return s.call(ToolUpdateExpense, map[string]any{"id": target.ID, "amount": s.new, "category": nil, "date": nil, "comment": nil}), nil
	case "create":
		return say(""), nil
	case "create_other":
		return say("Категорию не узнал — записал как «другое». Исправить можно так: «последнее — покупки»."), nil
	}
	return say(""), nil
}

func (s *ruleSession) first() llm.Turn {
	t := s.text
	words := reWord.FindAllString(t, -1)
	isLast := strings.Contains(t, "последн") || strings.Contains(t, "last")
	s.lastOnly = isLast
	switch {
	case strings.HasPrefix(t, "удали") || strings.HasPrefix(t, "delete") || strings.HasPrefix(t, "remove"):
		s.plan = "delete"
		in := map[string]any{"preset": nil, "from": nil, "to": nil, "category": nil, "amount": nil, "text": nil, "sort": "recently_added", "limit": 5}
		if d := dateOf(t); d != nil {
			in["from"], in["to"] = d, d
		}
		if isLast {
			in["limit"] = 1
		}
		if n := reNumber.FindString(t); n != "" {
			in["amount"] = n
		}
		for _, w := range words {
			if !fillerWords[w] {
				in["text"] = w
				break
			}
		}
		return s.call(ToolFindExpenses, in)

	case reUpdateRU.MatchString(t) || reUpdateEN.MatchString(t):
		s.plan = "update"
		if m := reUpdateRU.FindStringSubmatch(t); m != nil {
			s.old, s.new = m[1], m[2]
		} else {
			m := reUpdateEN.FindStringSubmatch(t)
			s.new, s.old = m[1], m[2]
		}
		in := map[string]any{"preset": nil, "from": nil, "to": nil, "category": nil, "amount": nil, "text": nil, "sort": "recently_added", "limit": 1}
		if !isLast {
			in["amount"], in["limit"] = s.old, 5
		}
		return s.call(ToolFindExpenses, in)

	case strings.Contains(t, "сколько") || strings.Contains(t, "how much"):
		s.plan = "summary"
		return s.call(ToolSummarizeExpenses, map[string]any{"preset": presetOf(t), "from": nil, "to": nil, "category": categoryIn(words)})

	case strings.Contains(t, "покажи") || strings.Contains(t, "show") || strings.Contains(t, "список") || strings.HasPrefix(t, "траты"):
		s.plan = "list"
		preset := presetOf(t)
		if preset == nil {
			preset = "this_month"
		}
		return s.call(ToolFindExpenses, map[string]any{"preset": preset, "from": nil, "to": nil, "category": categoryIn(words),
			"amount": nil, "text": nil, "sort": "by_date", "limit": 20})

	case reNumber.MatchString(t):
		s.plan = "create"
		var items []map[string]any
		for _, part := range reSplit.Split(t, -1) {
			if !reNumber.MatchString(part) {
				continue
			}
			pw := reWord.FindAllString(part, -1)
			var comment []string
			for _, w := range pw {
				if !fillerWords[w] {
					comment = append(comment, w)
				}
			}
			cat := categoryIn(pw)
			if cat == nil {
				cat, s.plan = "other", "create_other"
			}
			var c any
			if len(comment) > 0 {
				c = strings.Join(comment, " ")
			}
			items = append(items, map[string]any{"amount": reNumber.FindString(part), "category": cat, "date": dateOf(t), "comment": c})
		}
		return s.call(ToolAddExpenses, map[string]any{"items": items})
	}
	return say("")
}
