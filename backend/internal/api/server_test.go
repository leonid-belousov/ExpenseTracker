package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/expense/memstore"
)

const (
	mom int64 = 111
	dad int64 = 222
)

var origin = "http://127.0.0.1:8080"

func newTestServer(t *testing.T) (*httptest.Server, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	clock := dates.FixedClock{T: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	svc := expense.NewService(store, []expense.Member{{ID: mom, Name: "Мама"}, {ID: dad, Name: "Папа"}}, time.UTC, clock)
	srv := httptest.NewServer(New(svc, "RUB", []string{origin}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return srv, store
}

type resp struct {
	status int
	body   map[string]any
	header http.Header
}

func do(t *testing.T, method, url, body string, hdr map[string]string) resp {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out := resp{status: res.StatusCode, header: res.Header}
	data, _ := io.ReadAll(res.Body)
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out.body)
	}
	return out
}

func fieldErr(r resp, field string) string {
	e, _ := r.body["error"].(map[string]any)
	f, _ := e["fields"].(map[string]any)
	s, _ := f[field].(string)
	return s
}

func TestCRUD(t *testing.T) {
	srv, _ := newTestServer(t)
	r := do(t, "POST", srv.URL+"/api/expenses", `{"user_id":111,"amount":"350.50","category":"food","spent_at":"2026-10-14","comment":" кофе "}`, nil)
	if r.status != 201 {
		t.Fatalf("create status %d %v", r.status, r.body)
	}
	if r.body["amount"] != "350.50" || r.body["comment"] != "кофе" || r.body["member_name"] != "Мама" {
		t.Fatalf("unexpected body %v", r.body)
	}
	id := int(r.body["id"].(float64))
	url := srv.URL + "/api/expenses/" + itoa(id)

	if g := do(t, "GET", url, "", nil); g.status != 200 || g.body["category"] != "food" {
		t.Fatalf("get %d %v", g.status, g.body)
	}
	u := do(t, "PUT", url, `{"user_id":222,"amount":450,"category":"transport","spent_at":"2026-10-15","comment":null}`, nil)
	if u.status != 200 || u.body["amount"] != "450.00" || u.body["user_id"] != float64(dad) || u.body["comment"] != nil {
		t.Fatalf("update %d %v", u.status, u.body)
	}
	if d := do(t, "DELETE", url, "", nil); d.status != 204 {
		t.Fatalf("delete %d", d.status)
	}
	if g := do(t, "GET", url, "", nil); g.status != 404 {
		t.Fatalf("get after delete %d", g.status)
	}
	if d := do(t, "DELETE", url, "", nil); d.status != 404 {
		t.Fatalf("second delete %d", d.status)
	}
}

func TestCreateValidation(t *testing.T) {
	srv, store := newTestServer(t)
	cases := []struct {
		body, field string
	}{
		{`{"user_id":999,"amount":"10","category":"food","spent_at":"2026-10-14"}`, "user_id"},
		{`{"amount":"10","category":"food","spent_at":"2026-10-14"}`, "user_id"},
		{`{"user_id":111,"amount":"0","category":"food","spent_at":"2026-10-14"}`, "amount"},
		{`{"user_id":111,"amount":"-1","category":"food","spent_at":"2026-10-14"}`, "amount"},
		{`{"user_id":111,"amount":"1.234","category":"food","spent_at":"2026-10-14"}`, "amount"},
		{`{"user_id":111,"amount":"abc","category":"food","spent_at":"2026-10-14"}`, "amount"},
		{`{"user_id":111,"amount":"10","category":"еда","spent_at":"2026-10-14"}`, "category"},
		{`{"user_id":111,"amount":"10","category":"food","spent_at":"14.10.2026"}`, "spent_at"},
		{`{"user_id":111,"amount":"10","category":"food","spent_at":"2026-10-16"}`, "spent_at"},
		{`{"user_id":111,"amount":"10","category":"food","spent_at":"1999-12-31"}`, "spent_at"},
		{`{"user_id":111,"amount":"10","category":"food","spent_at":"2026-10-14","comment":"` + strings.Repeat("x", 501) + `"}`, "comment"},
	}
	for _, c := range cases {
		r := do(t, "POST", srv.URL+"/api/expenses", c.body, nil)
		if r.status != 400 || fieldErr(r, c.field) == "" {
			t.Errorf("body %s: status %d, body %v; want 400 with field %s", c.body[:min(60, len(c.body))], r.status, r.body, c.field)
		}
	}
	if n := len(store.All()); n != 0 {
		t.Fatalf("invalid requests stored %d rows", n)
	}
}

func TestMalformedRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	if r := do(t, "POST", srv.URL+"/api/expenses", `{"user_id":111,`, nil); r.status != 400 {
		t.Errorf("broken json: %d", r.status)
	}
	if r := do(t, "POST", srv.URL+"/api/expenses", `{"user_id":111,"amount":"1","category":"food","spent_at":"2026-10-14","owner":1}`, nil); r.status != 400 {
		t.Errorf("unknown field: %d", r.status)
	}
	if r := do(t, "POST", srv.URL+"/api/expenses", `{}`, map[string]string{"Content-Type": "text/plain"}); r.status != 415 {
		t.Errorf("text/plain: %d", r.status)
	}
	if r := do(t, "GET", srv.URL+"/api/expenses/abc", "", nil); r.status != 400 {
		t.Errorf("bad id: %d", r.status)
	}
	if r := do(t, "GET", srv.URL+"/api/expenses?limit=1000", "", nil); r.status != 400 || fieldErr(r, "limit") == "" {
		t.Errorf("limit: %d %v", r.status, r.body)
	}
	if r := do(t, "GET", srv.URL+"/api/expenses?from=2026-10-10&to=2026-10-10", "", nil); r.status != 400 {
		t.Errorf("empty range: %d", r.status)
	}
	if r := do(t, "GET", srv.URL+"/api/expenses?category=fun", "", nil); r.status != 400 {
		t.Errorf("bad category: %d", r.status)
	}
	if r := do(t, "GET", srv.URL+"/api/expenses?user_id=999", "", nil); r.status != 400 {
		t.Errorf("unknown member: %d", r.status)
	}
	if r := do(t, "GET", srv.URL+"/api/nope", "", nil); r.status != 404 {
		t.Errorf("unknown route: %d", r.status)
	}
}

func TestFiltersAndHalfOpenRange(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, b := range []string{
		`{"user_id":111,"amount":"100","category":"food","spent_at":"2026-10-11"}`,    // Sunday, previous week
		`{"user_id":111,"amount":"200.10","category":"food","spent_at":"2026-10-12"}`, // Monday
		`{"user_id":222,"amount":"300.20","category":"transport","spent_at":"2026-10-15"}`,
		`{"user_id":111,"amount":"0.70","category":"food","spent_at":"2026-10-15"}`,
	} {
		if r := do(t, "POST", srv.URL+"/api/expenses", b, nil); r.status != 201 {
			t.Fatalf("seed: %d %v", r.status, r.body)
		}
	}
	r := do(t, "GET", srv.URL+"/api/expenses?from=2026-10-12&to=2026-10-19", "", nil)
	if r.body["total"] != float64(3) || r.body["sum"] != "501.00" {
		t.Fatalf("week: %v", r.body)
	}
	r = do(t, "GET", srv.URL+"/api/expenses?from=2026-10-11&to=2026-10-12", "", nil)
	if r.body["total"] != float64(1) || r.body["sum"] != "100.00" {
		t.Fatalf("exclusive end: %v", r.body)
	}
	r = do(t, "GET", srv.URL+"/api/expenses?user_id=111&category=food", "", nil)
	if r.body["total"] != float64(3) || r.body["sum"] != "300.80" {
		t.Fatalf("member+category: %v", r.body)
	}
	r = do(t, "GET", srv.URL+"/api/expenses?limit=2&offset=0", "", nil)
	items := r.body["items"].([]any)
	if len(items) != 2 || r.body["total"] != float64(4) {
		t.Fatalf("pagination: %v", r.body)
	}
	first := items[0].(map[string]any)
	if first["spent_at"] != "2026-10-15" {
		t.Fatalf("ordering: newest date first, got %v", first)
	}
}

func TestOriginPolicy(t *testing.T) {
	srv, store := newTestServer(t)
	body := `{"user_id":111,"amount":"1","category":"food","spent_at":"2026-10-14"}`
	if r := do(t, "POST", srv.URL+"/api/expenses", body, map[string]string{"Origin": "https://evil.example"}); r.status != 403 {
		t.Fatalf("foreign origin: %d", r.status)
	}
	if len(store.All()) != 0 {
		t.Fatal("foreign origin must not mutate")
	}
	r := do(t, "OPTIONS", srv.URL+"/api/expenses", "", map[string]string{"Origin": origin, "Access-Control-Request-Method": "POST"})
	if r.status != 204 || r.header.Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("preflight: %d %v", r.status, r.header)
	}
	r = do(t, "GET", srv.URL+"/api/expenses", "", map[string]string{"Origin": "https://evil.example"})
	if r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("must not grant CORS to unknown origins")
	}
	if r := do(t, "POST", srv.URL+"/api/expenses", body, map[string]string{"Origin": origin}); r.status != 201 {
		t.Fatalf("allowed origin: %d", r.status)
	}
}

func TestStorageFailureIsReported(t *testing.T) {
	srv, store := newTestServer(t)
	store.FailNext = errors.New("connection refused")
	r := do(t, "POST", srv.URL+"/api/expenses", `{"user_id":111,"amount":"1","category":"food","spent_at":"2026-10-14"}`, nil)
	if r.status != 503 {
		t.Fatalf("status %d", r.status)
	}
	if strings.Contains(r.body["error"].(map[string]any)["message"].(string), "refused") {
		t.Fatal("internal error details must not leak")
	}
}

func TestConfigEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	r := do(t, "GET", srv.URL+"/api/config", "", nil)
	if r.status != 200 || r.body["currency"] != "RUB" || r.body["today"] != "2026-10-15" || len(r.body["members"].([]any)) != 2 {
		t.Fatalf("config %v", r.body)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
