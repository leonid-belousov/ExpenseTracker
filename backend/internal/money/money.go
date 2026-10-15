// Package money implements exact fixed-point amounts with two fractional
// digits. Values are stored as int64 minor units (e.g. kopecks, cents) so no
// floating-point arithmetic is ever involved.
package money

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Scale is the number of fractional digits supported by the deployment.
const Scale = 2

const minorPerMajor = 100

// MaxAmount is the largest single amount accepted (999 999 999.99). It keeps
// sums of many expenses comfortably inside both int64 and NUMERIC(14,2).
const MaxAmount Amount = 99_999_999_999

// Amount is an exact amount in minor units.
type Amount int64

var (
	ErrEmpty       = errors.New("amount is required")
	ErrSyntax      = errors.New("amount must be a decimal number like 350 or 350.50")
	ErrPrecision   = fmt.Errorf("amount supports at most %d decimal places", Scale)
	ErrNotPositive = errors.New("amount must be greater than zero")
	ErrTooLarge    = errors.New("amount is too large")
)

// Parse parses a decimal string. It accepts an optional leading sign, a dot or
// a comma as the decimal separator, and ignores spaces used as thousands
// separators ("1 200,50"). It never rounds: more than Scale fractional digits
// is an error. The result may be zero or negative; use ParsePositive for
// user-supplied expense amounts.
func Parse(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer(" ", "", " ", "", " ", "", "_", "").Replace(s)
	if s == "" {
		return 0, ErrEmpty
	}
	neg := false
	switch s[0] {
	case '-':
		neg = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	s = strings.Replace(s, ",", ".", 1)
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if intPart == "" && (!hasDot || fracPart == "") {
		return 0, ErrSyntax
	}
	if hasDot && fracPart == "" {
		return 0, ErrSyntax
	}
	if !digitsOnly(intPart) || !digitsOnly(fracPart) {
		return 0, ErrSyntax
	}
	if len(fracPart) > Scale {
		// Trailing zeros beyond the scale are still exact ("1.500").
		if strings.TrimRight(fracPart[Scale:], "0") != "" {
			return 0, ErrPrecision
		}
		fracPart = fracPart[:Scale]
	}
	for len(fracPart) < Scale {
		fracPart += "0"
	}
	intPart = strings.TrimLeft(intPart, "0")
	if len(intPart) > 12 {
		return 0, ErrTooLarge
	}
	var major int64
	if intPart != "" {
		v, err := strconv.ParseInt(intPart, 10, 64)
		if err != nil {
			return 0, ErrSyntax
		}
		major = v
	}
	minor, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, ErrSyntax
	}
	a := Amount(major*minorPerMajor + minor)
	if a > MaxAmount {
		return 0, ErrTooLarge
	}
	if neg {
		a = -a
	}
	return a, nil
}

// ParsePositive parses an amount and requires 0 < amount <= MaxAmount.
func ParsePositive(s string) (Amount, error) {
	a, err := Parse(s)
	if err != nil {
		return 0, err
	}
	if err := a.ValidatePositive(); err != nil {
		return 0, err
	}
	return a, nil
}

// ValidatePositive checks the bounds for a single expense amount.
func (a Amount) ValidatePositive() error {
	if a <= 0 {
		return ErrNotPositive
	}
	if a > MaxAmount {
		return ErrTooLarge
	}
	return nil
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// String renders the canonical form with exactly Scale fractional digits,
// e.g. "350.00", "-0.50".
func (a Amount) String() string {
	v := int64(a)
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/minorPerMajor, v%minorPerMajor)
}

// Human renders an amount for chat replies: thousands separated by a thin
// space and fractional digits omitted when zero ("1 200", "350.50").
func (a Amount) Human() string {
	v := int64(a)
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	major := strconv.FormatInt(v/minorPerMajor, 10)
	var b strings.Builder
	for i, r := range major {
		if i > 0 && (len(major)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	if frac := v % minorPerMajor; frac != 0 {
		fmt.Fprintf(&b, ".%02d", frac)
	}
	return sign + b.String()
}

// MarshalJSON encodes the amount as a decimal string to avoid float rounding
// in JSON consumers.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// UnmarshalJSON accepts either a JSON string ("350.50") or a JSON number
// literal (350.5). Number literals are parsed from their exact text, never via
// float64.
func (a *Amount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return ErrEmpty
	}
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return ErrSyntax
		}
	} else {
		if bytes.ContainsAny(b, "eE") {
			return ErrSyntax
		}
		s = string(b)
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*a = v
	return nil
}
