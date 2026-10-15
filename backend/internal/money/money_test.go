package money

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	ok := map[string]string{
		"350":          "350.00",
		"350.5":        "350.50",
		"350,50":       "350.50",
		"0.01":         "0.01",
		"1 200":        "1200.00",
		"1 200,5":      "1200.50",
		"  42 ":        "42.00",
		"1.500":        "1.50",
		".5":           "0.50",
		"999999999.99": "999999999.99",
	}
	for in, want := range ok {
		a, err := ParsePositive(in)
		if err != nil {
			t.Errorf("ParsePositive(%q) error: %v", in, err)
			continue
		}
		if a.String() != want {
			t.Errorf("ParsePositive(%q) = %s, want %s", in, a, want)
		}
	}

	bad := map[string]error{
		"":              ErrEmpty,
		"abc":           ErrSyntax,
		"1.2.3":         ErrSyntax,
		"12.":           ErrSyntax,
		"1e3":           ErrSyntax,
		"350.555":       ErrPrecision,
		"0":             ErrNotPositive,
		"0.00":          ErrNotPositive,
		"-5":            ErrNotPositive,
		"1000000000":    ErrTooLarge,
		"9999999999999": ErrTooLarge,
	}
	for in, want := range bad {
		if _, err := ParsePositive(in); !errors.Is(err, want) {
			t.Errorf("ParsePositive(%q) error = %v, want %v", in, err, want)
		}
	}
}

func TestJSONRoundTripIsExact(t *testing.T) {
	var a Amount
	if err := json.Unmarshal([]byte(`0.1`), &a); err != nil {
		t.Fatal(err)
	}
	var b Amount
	if err := json.Unmarshal([]byte(`"0.2"`), &b); err != nil {
		t.Fatal(err)
	}
	if (a + b).String() != "0.30" {
		t.Fatalf("0.1 + 0.2 = %s, want 0.30", a+b)
	}
	out, _ := json.Marshal(a + b)
	if string(out) != `"0.30"` {
		t.Fatalf("marshal = %s", out)
	}
	if err := json.Unmarshal([]byte(`1.005`), &a); !errors.Is(err, ErrPrecision) {
		t.Fatalf("expected precision error, got %v", err)
	}
}

func TestHuman(t *testing.T) {
	cases := map[Amount]string{
		35000:     "350",
		35050:     "350.50",
		120000000: "1 200 000",
	}
	for a, want := range cases {
		if got := a.Human(); got != want {
			t.Errorf("Human(%d) = %q, want %q", a, got, want)
		}
	}
}
