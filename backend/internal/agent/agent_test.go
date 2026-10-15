package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/expense/memstore"
	"expensetracker/internal/llm"
	"expensetracker/internal/money"
)

// checkStrict walks a schema and fails if an object does not list every
// property as required or allows additional properties.
func checkStrict(t *testing.T, path string, s any) {
	t.Helper()
	m, ok := s.(map[string]any)
	if !ok {
		return
	}
	if m["type"] == "object" {
		props, _ := m["properties"].(map[string]any)
		req, _ := m["required"].([]string)
		if len(req) != len(props) {
			t.Errorf("%s: required %v does not list all properties", path, req)
		}
		if m["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties must be false", path)
		}
		for k, v := range props {
			checkStrict(t, path+"."+k, v)
		}
	}
	if items, ok := m["items"]; ok {
		checkStrict(t, path+"[]", items)
	}
	if any, ok := m["anyOf"].([]any); ok {
		for _, v := range any {
			checkStrict(t, path, v)
		}
	}
}

func TestToolSchemasAreStrictAndOwnerFree(t *testing.T) {
	for _, tool := range Tools() {
		checkStrict(t, tool.Name, tool.Schema)
		b, _ := json.Marshal(tool.Schema)
		if strings.Contains(string(b), "user_id") || strings.Contains(string(b), "owner") {
			t.Errorf("%s: tools must not take an owner", tool.Name)
		}
	}
	a, _ := json.Marshal(Tools())
	b, _ := json.Marshal(Tools())
	if string(a) != string(b) {
		t.Error("tool definitions must be deterministic (prompt cache)")
	}
}

func TestHistoryWindow(t *testing.T) {
	msgs := []expense.ChatMessage{
		{Role: expense.ChatAssistant, Content: "orphan reply"},
		{Role: expense.ChatUser, Content: "удали такси"},
		{Role: expense.ChatAssistant, Content: "Какую?\n[ids: 12, 15]"},
		{Role: expense.ChatUser, Content: "первую"},
		{Role: expense.ChatUser, Content: "нет, вторую"},
		{Role: expense.ChatAssistant, Content: "Готово"},
	}
	h, ids := History(msgs, 10_000)
	if len(h) != 4 || h[0].Role != llm.RoleUser || h[0].Text != "удали такси" || h[2].Text != "первую\nнет, вторую" || h[3].Role != llm.RoleAssistant {
		t.Fatalf("history %+v", h)
	}
	if len(ids) != 2 || ids[0] != 12 || ids[1] != 15 {
		t.Fatalf("ids %v", ids)
	}
	// A small budget keeps only the newest messages that fit, still starting
	// with a user message and ending with an assistant one.
	h, _ = History(msgs, len("нет, вторую")+len("Готово"))
	if len(h) != 2 || h[0].Text != "нет, вторую" {
		t.Fatalf("budget window %+v", h)
	}
	if h, _ := History(msgs[:2], 10_000); len(h) != 0 {
		t.Fatalf("a window ending with a user message must be dropped: %+v", h)
	}
}

type oneShot struct{ turns []llm.Turn }

func (o *oneShot) Start(llm.Request) llm.Session { return &oneShotSession{o: o} }

type oneShotSession struct {
	o    *oneShot
	i    int
	seen [][]llm.ToolResult
}

func (s *oneShotSession) Next(_ context.Context, r []llm.ToolResult) (llm.Turn, error) {
	s.seen = append(s.seen, r)
	if s.i >= len(s.o.turns) {
		return llm.Turn{Text: "done"}, nil
	}
	s.i++
	return s.o.turns[s.i-1], nil
}

func newSvc(t *testing.T) (*expense.Service, *memstore.Store) {
	t.Helper()
	st := memstore.New()
	clock := dates.FixedClock{T: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	return expense.NewService(st, []expense.Member{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}, time.UTC, clock), st
}

func turn(name, input string) llm.Turn {
	return llm.Turn{Calls: []llm.ToolCall{{ID: name, Name: name, Input: json.RawMessage(input)}}}
}

func TestToolsWorkInsideOwnerScope(t *testing.T) {
	svc, st := newSvc(t)
	amt, _ := money.ParsePositive("700")
	other, _ := st.Create(context.Background(), expense.Fields{UserID: 2, Amount: amt, Category: expense.Food, SpentAt: dates.New(2026, 10, 15)})

	m := &oneShot{turns: []llm.Turn{
		turn(ToolAddExpenses, `{"items":[{"amount":"350","category":"food","date":"yesterday","comment":"кофе"},{"amount":"120","category":"food","date":null,"comment":null}]}`),
		turn(ToolSummarizeExpenses, `{"preset":null,"from":null,"to":null,"category":null}`),
		// A known id of another member is still out of scope.
		turn(ToolDeleteExpense, `{"id":`+itoa(other.ID)+`}`),
	}}
	res, err := New(m, Config{Currency: "RUB"}).Run(context.Background(), svc, 1, nil, []int64{other.ID}, "кофе 350 вчера и булка 120")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 || res.Changes[0].After.UserID != 1 || res.Changes[0].After.SpentAt.String() != "2026-10-14" {
		t.Fatalf("changes %+v", res.Changes)
	}
	if res.Stats.Steps != 4 || res.Stats.ToolErrors != 1 || strings.Join(res.Stats.ToolCalls, ",") != "add_expenses,summarize_expenses,delete_expense" {
		t.Fatalf("stats %+v", res.Stats)
	}
	if _, err := st.Get(context.Background(), expense.OwnerScope(2), other.ID); err != nil {
		t.Fatal("other member's expense deleted")
	}
	if len(res.Refs) != 2 {
		t.Fatalf("refs %v", res.Refs)
	}
}

func TestSummarizeAndFindResults(t *testing.T) {
	svc, _ := newSvc(t)
	tb := &toolbox{svc: svc, actor: 1, currency: "RUB", today: dates.New(2026, 10, 15), seen: map[int64]bool{}}
	ctx := context.Background()
	if _, err := tb.add(ctx, json.RawMessage(`{"items":[{"amount":"100.10","category":"food","date":"2026-10-01","comment":"a"},{"amount":"50","category":"transport","date":"2026-10-02","comment":"такси"},{"amount":"999","category":"food","date":"2026-09-30","comment":"prev"}]}`)); err != nil {
		t.Fatal(err)
	}
	out, err := tb.summarize(ctx, json.RawMessage(`{"preset":null,"from":null,"to":null,"category":null}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	for _, want := range []string{`"total_sum":"150.10"`, `"total_count":2`, `"from":"2026-10-01"`, `"to_inclusive":"2026-10-31"`, `{"category":"transport","count":1,"sum":"50.00"}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("summary %s lacks %s", b, want)
		}
	}
	out, err = tb.find(ctx, json.RawMessage(`{"preset":"all_time","from":null,"to":null,"category":null,"amount":null,"text":"ТАКС","sort":"by_date","limit":null}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(out)
	if !strings.Contains(string(b), `"total_count":1`) || !strings.Contains(string(b), `"comment":"такси"`) {
		t.Errorf("find %s", b)
	}
	// Invalid input is a tool error, not an abort.
	for _, in := range []string{
		`{"items":[{"amount":"-1","category":"food","date":null,"comment":null}]}`,
		`{"items":[{"amount":"1","category":"gifts","date":null,"comment":null}]}`,
		`{"items":[{"amount":"1","category":"food","date":"2026-10-20","comment":null}]}`,
		`{"items":[]}`,
		`not json`,
	} {
		_, err := tb.add(ctx, json.RawMessage(in))
		if _, ok := err.(*toolError); !ok {
			t.Errorf("%s: err %v", in, err)
		}
	}
	page, _ := svc.List(ctx, expense.OwnerScope(1), expense.Filter{Limit: 50})
	if page.Total != 3 {
		t.Fatalf("invalid items were saved: %d rows", page.Total)
	}
}

func TestStepLimit(t *testing.T) {
	svc, _ := newSvc(t)
	loop := turn(ToolFindExpenses, `{"preset":null,"from":null,"to":null,"category":null,"amount":null,"text":null,"sort":"by_date","limit":1}`)
	m := &oneShot{turns: []llm.Turn{loop, loop, loop, loop}}
	if _, err := New(m, Config{MaxSteps: 3}).Run(context.Background(), svc, 1, nil, nil, "x"); err != ErrStepLimit {
		t.Fatalf("err %v", err)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
