package eval

import (
	"context"
	"slices"
	"testing"
	"time"

	"expensetracker/internal/agent"
	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

func TestDatasetLoads(t *testing.T) {
	cases, err := Load("../../eval/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 40 {
		t.Fatalf("dataset has %d cases, want at least 40", len(cases))
	}
}

// The offline rule-based model must keep passing the brief's own examples;
// this runs the whole eval pipeline without network access.
func TestRuleModelPassesBriefCases(t *testing.T) {
	cases, err := Load("../../eval/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var brief []Case
	for _, c := range cases {
		if slices.Contains(c.Tags, "brief") {
			brief = append(brief, c)
		}
	}
	if len(brief) < 6 {
		t.Fatalf("brief cases: %d", len(brief))
	}
	outs := Runner{Model: agent.RuleModel{}, MaxSteps: 6, Timeout: time.Second, Parallel: 2}.Run(context.Background(), brief, 1)
	for _, o := range outs {
		if !o.Pass {
			t.Errorf("%s: %s %q", o.CaseID, o.Reason, o.Replies)
		}
	}
	s := Summarize(outs, &Price{})
	if s.Passed != len(brief) || s.Messages != len(brief) {
		t.Fatalf("summary %+v", s)
	}
}

func exp(id int64, amount string, cat expense.Category, day string, comment string) expense.Expense {
	a, _ := money.ParsePositive(amount)
	d, _ := dates.Parse(day)
	return expense.Expense{ID: id, UserID: me, Amount: a, Category: cat, SpentAt: d, Comment: &comment}
}

func TestGrade(t *testing.T) {
	before := state{
		mine:   []expense.Expense{exp(1, "350", expense.Food, "2026-10-15", "кофе")},
		others: []expense.Expense{exp(2, "900", expense.Food, "2026-10-14", "обед")},
	}
	changed := state{mine: []expense.Expense{exp(1, "450", expense.Food, "2026-10-15", "кофе")}, others: before.others}

	if ok, _ := grade(Expect{Unchanged: true}, before, before, []string{"ok"}); !ok {
		t.Error("unchanged")
	}
	if ok, _ := grade(Expect{Unchanged: true}, before, changed, nil); ok {
		t.Error("change not detected")
	}
	want := Expect{Expenses: []ExpectedExpense{{Amount: "450", Category: "food", CommentContains: "КОФ"}}}
	if ok, why := grade(want, before, changed, nil); !ok {
		t.Error(why)
	}
	otherChanged := state{mine: before.mine}
	if ok, why := grade(Expect{Unchanged: true}, before, otherChanged, nil); ok || why[:8] != "SECURITY" {
		t.Errorf("security breach not detected: %v %s", ok, why)
	}
	replyCheck := Expect{Unchanged: true, ReplyContainsAny: [][]string{{"1450"}}, ReplyNotContains: []string{"5000"}}
	if ok, why := grade(replyCheck, before, before, []string{"Потрачено: 1 450 RUB"}); !ok {
		t.Error(why)
	}
	if ok, _ := grade(replyCheck, before, before, []string{"1 450", "и 5 000"}); ok {
		t.Error("forbidden text not detected")
	}
	okUnchanged := Expect{Expenses: []ExpectedExpense{{Amount: "700"}}, UnchangedOK: true}
	if ok, why := grade(okUnchanged, before, before, nil); !ok {
		t.Error(why)
	}
}

func TestMatchAllIsOneToOne(t *testing.T) {
	actual := []expense.Expense{exp(1, "350", expense.Food, "2026-10-15", "кофе"), exp(2, "350", expense.Food, "2026-10-14", "кофе")}
	two := []ExpectedExpense{{Amount: "350"}, {Amount: "350", Date: "2026-10-15"}}
	if !matchAll(two, actual) {
		t.Error("needs backtracking to pair both")
	}
	if matchAll([]ExpectedExpense{{Amount: "350"}}, actual) {
		t.Error("count mismatch accepted")
	}
}
