package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
)

var today = dates.New(2026, time.October, 15)

func TestDecodeValidIntent(t *testing.T) {
	in, err := Decode("```json\n{\"action\":\"create\",\"amount\":1200,\"category\":\"transport\",\"date\":\"yesterday\",\"comment\":\"такси\",\"user_id\":999}\n```", today)
	if err != nil {
		t.Fatal(err)
	}
	if in.Action != ActionCreate || in.Amount.String() != "1200.00" || *in.Category != expense.Transport ||
		in.Date.String() != "2026-10-14" || *in.Comment != "такси" {
		t.Fatalf("unexpected %+v", in)
	}
}

func TestDecodePeriods(t *testing.T) {
	in, err := Decode(`{"action":"summary","period":{"preset":"this_week"}}`, today)
	if err != nil || in.Period.From.String() != "2026-10-12" || in.Period.To.String() != "2026-10-19" {
		t.Fatalf("preset: %+v %v", in.Period, err)
	}
	in, err = Decode(`{"action":"list","period":{"from":"2026-10-01","to":"2026-10-10"}}`, today)
	if err != nil || in.Period.To.String() != "2026-10-11" {
		t.Fatalf("inclusive end must become exclusive: %+v %v", in.Period, err)
	}
	if _, err := Decode(`{"action":"list","period":{"from":"2026-10-10","to":"2026-10-01"}}`, today); err == nil {
		t.Fatal("reversed period must fail")
	}
	if _, err := Decode(`{"action":"list","period":{"preset":"forever"}}`, today); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unknown preset: %v", err)
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "I think you spent 350", "{not json}", `{"action":"drop_table"}`, `{"action":"create","amount":{"x":1}}`} {
		_, err := Decode(s, today)
		var fe *FieldError
		if !errors.Is(err, ErrMalformed) && !errors.As(err, &fe) {
			t.Errorf("Decode(%q) = %v, want malformed/field error", s, err)
		}
	}
}

func TestDecodeFieldErrors(t *testing.T) {
	for _, s := range []string{
		`{"action":"create","amount":"-5","category":"food"}`,
		`{"action":"create","amount":"0","category":"food"}`,
		`{"action":"create","amount":"3.333","category":"food"}`,
		`{"action":"create","amount":"5","date":"15/10/2026"}`,
	} {
		var fe *FieldError
		if _, err := Decode(s, today); !errors.As(err, &fe) {
			t.Errorf("Decode(%s) = %v, want FieldError", s, err)
		}
	}
}

func TestDecodeUnknownCategoryBecomesUnclear(t *testing.T) {
	in, err := Decode(`{"action":"create","amount":"5","category":"groceries"}`, today)
	if err != nil || in.Category != nil {
		t.Fatalf("got %+v %v", in.Category, err)
	}
}

func TestMockUnderstandsBriefExamples(t *testing.T) {
	p := ModelParser{Backend: Mock{}}
	parse := func(text string) Intent {
		t.Helper()
		in, err := p.Parse(context.Background(), Request{Text: text, Today: today, TimeZone: "UTC"})
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		return in
	}
	if in := parse("кофе 350"); in.Action != ActionCreate || in.Amount.String() != "350.00" || *in.Category != expense.Food || in.Date != nil {
		t.Errorf("кофе 350: %+v", in)
	}
	if in := parse("такси 1200 вчера"); in.Action != ActionCreate || *in.Category != expense.Transport || in.Date.String() != "2026-10-14" || *in.Comment != "такси" {
		t.Errorf("такси: %+v", in)
	}
	if in := parse("сколько потратил на еду в этом месяце"); in.Action != ActionSummary || *in.Category != expense.Food || in.Period.From.String() != "2026-10-01" {
		t.Errorf("summary: %+v", in)
	}
	if in := parse("покажи траты за неделю"); in.Action != ActionList || in.Period.From.String() != "2026-10-12" {
		t.Errorf("list: %+v", in)
	}
	if in := parse("последнее было не 350, а 450"); in.Action != ActionUpdate || !in.Target.Last || in.Target.Amount.String() != "350.00" || in.Changes.Amount.String() != "450.00" {
		t.Errorf("update: %+v", in)
	}
	if in := parse("The last one was 450, not 350"); in.Action != ActionUpdate || in.Target.Amount.String() != "350.00" || in.Changes.Amount.String() != "450.00" {
		t.Errorf("update en: %+v", in)
	}
	if in := parse("удали такси"); in.Action != ActionDelete || in.Target.Text != "такси" || in.Target.Last {
		t.Errorf("delete: %+v", in)
	}
	if in := parse("подарок 500"); in.Action != ActionCreate || in.Category != nil {
		t.Errorf("unclear category must stay nil: %+v", in)
	}
	if in := parse("привет"); in.Action != ActionHelp {
		t.Errorf("help: %+v", in)
	}
}

type fakeCompleter struct {
	answer string
	err    error
	delay  time.Duration
}

func (f fakeCompleter) Complete(ctx context.Context, _, _ string) (string, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return f.answer, f.err
}

func TestModelParserErrorClassification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := ModelParser{Backend: fakeCompleter{delay: time.Second}}.Parse(ctx, Request{Text: "кофе 350", Today: today})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("timeout should be ErrUnavailable, got %v", err)
	}
	_, err = ModelParser{Backend: fakeCompleter{answer: "sorry, I can't"}}.Parse(context.Background(), Request{Text: "x", Today: today})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("prose should be ErrMalformed, got %v", err)
	}
}

func TestOpenAICompatibleClient(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.Contains(r.URL.Path, "slow") {
			time.Sleep(200 * time.Millisecond)
		}
		if strings.Contains(r.URL.Path, "fail") {
			w.WriteHeader(500)
			return
		}
		if strings.Contains(r.URL.Path, "garbage") {
			_, _ = w.Write([]byte(`<html>`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"action\":\"help\"}"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	ok := NewOpenAICompatible(srv.URL, "test-key", "m", time.Second)
	out, err := ok.Complete(context.Background(), "s", "u")
	if err != nil || out != `{"action":"help"}` || gotAuth != "Bearer test-key" {
		t.Fatalf("ok: %q %v %q", out, err, gotAuth)
	}
	if _, err := NewOpenAICompatible(srv.URL+"/fail", "k", "m", time.Second).Complete(context.Background(), "s", "u"); err == nil || strings.Contains(err.Error(), "test-key") {
		t.Fatalf("500 should fail without leaking key: %v", err)
	}
	if _, err := NewOpenAICompatible(srv.URL+"/garbage", "k", "m", time.Second).Complete(context.Background(), "s", "u"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("garbage: %v", err)
	}
	_, err = NewOpenAICompatible(srv.URL+"/slow", "k", "m", 50*time.Millisecond).Complete(context.Background(), "s", "u")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow: %v", err)
	}
}

func TestSchemaIsDeterministic(t *testing.T) {
	a, b := IntentSchema(), IntentSchema()
	if strings.Join(a["required"].([]string), ",") != strings.Join(b["required"].([]string), ",") {
		t.Fatal("schema must be byte-stable")
	}
}
