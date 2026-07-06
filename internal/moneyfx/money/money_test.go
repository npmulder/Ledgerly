package money

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
)

func TestAddSubCmpAndNegate(t *testing.T) {
	t.Parallel()

	base := Money{Amount: 123, Currency: "GBP"}
	other := Money{Amount: 77, Currency: "GBP"}

	sum, err := base.Add(other)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if sum != (Money{Amount: 200, Currency: "GBP"}) {
		t.Fatalf("Add = %+v, want 200 GBP", sum)
	}

	diff, err := sum.Sub(other)
	if err != nil {
		t.Fatalf("Sub returned error: %v", err)
	}
	if diff != base {
		t.Fatalf("Sub round trip = %+v, want %+v", diff, base)
	}

	negated, err := other.Negate()
	if err != nil {
		t.Fatalf("Negate returned error: %v", err)
	}
	if negated != (Money{Amount: -77, Currency: "GBP"}) {
		t.Fatalf("Negate = %+v, want -77 GBP", negated)
	}

	for name, tc := range map[string]struct {
		left  Money
		right Money
		want  int
	}{
		"less":    {Money{Amount: 1, Currency: "GBP"}, Money{Amount: 2, Currency: "GBP"}, -1},
		"equal":   {Money{Amount: 2, Currency: "GBP"}, Money{Amount: 2, Currency: "GBP"}, 0},
		"greater": {Money{Amount: 3, Currency: "GBP"}, Money{Amount: 2, Currency: "GBP"}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tc.left.Cmp(tc.right)
			if err != nil {
				t.Fatalf("Cmp returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Cmp = %d, want %d", got, tc.want)
			}
		})
	}

	if !Zero("EUR").IsZero() {
		t.Fatal("Zero(EUR).IsZero() = false, want true")
	}
	if (Money{Amount: 1, Currency: "EUR"}).IsZero() {
		t.Fatal("non-zero money reported zero")
	}
}

func TestCurrencyMismatch(t *testing.T) {
	t.Parallel()

	left := Money{Amount: 1, Currency: "GBP"}
	right := Money{Amount: 1, Currency: "EUR"}

	if _, err := left.Add(right); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add mismatch error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := left.Sub(right); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Sub mismatch error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := left.Cmp(right); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Cmp mismatch error = %v, want ErrCurrencyMismatch", err)
	}
}

func TestOverflow(t *testing.T) {
	t.Parallel()

	for name, fn := range map[string]func() error{
		"add positive": func() error {
			_, err := (Money{Amount: maxInt64, Currency: "GBP"}).Add(Money{Amount: 1, Currency: "GBP"})
			return err
		},
		"add negative": func() error {
			_, err := (Money{Amount: minInt64, Currency: "GBP"}).Add(Money{Amount: -1, Currency: "GBP"})
			return err
		},
		"sub positive": func() error {
			_, err := (Money{Amount: minInt64, Currency: "GBP"}).Sub(Money{Amount: 1, Currency: "GBP"})
			return err
		},
		"sub negative": func() error {
			_, err := (Money{Amount: maxInt64, Currency: "GBP"}).Sub(Money{Amount: -1, Currency: "GBP"})
			return err
		},
		"negate min": func() error {
			_, err := (Money{Amount: minInt64, Currency: "GBP"}).Negate()
			return err
		},
		"parse": func() error {
			_, err := ParseAmount("92,233,720,368,547,758.08", "GBP")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, ErrOverflow) {
				t.Fatalf("error = %v, want ErrOverflow", err)
			}
		})
	}

	defer func() {
		if recovered := recover(); !errors.Is(recoveredAsError(recovered), ErrOverflow) {
			t.Fatalf("MulRat panic = %v, want ErrOverflow", recovered)
		}
	}()
	_ = (Money{Amount: maxInt64, Currency: "GBP"}).MulRat(big.NewRat(2, 1))
}

func TestMulRatHalfEvenRounding(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		amount int64
		rate   *big.Rat
		want   int64
	}{
		"zero":                {0, big.NewRat(1, 2), 0},
		"less than half":      {1, big.NewRat(2, 5), 0},
		"greater than half":   {1, big.NewRat(3, 5), 1},
		"half to even down":   {5, big.NewRat(1, 2), 2},
		"half to even up":     {7, big.NewRat(1, 2), 4},
		"negative half down":  {-5, big.NewRat(1, 2), -2},
		"negative half up":    {-7, big.NewRat(1, 2), -4},
		"negative above half": {-1, big.NewRat(3, 5), -1},
	} {
		t.Run(name, func(t *testing.T) {
			got := (Money{Amount: tc.amount, Currency: "GBP"}).MulRat(tc.rate)
			if got != (Money{Amount: tc.want, Currency: "GBP"}) {
				t.Fatalf("MulRat = %+v, want %d GBP", got, tc.want)
			}
		})
	}
}

func TestAllocateLargestRemainder(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		money  Money
		ratios []int
		want   []Money
	}{
		"empty zero": {
			money:  Money{Currency: "GBP"},
			ratios: nil,
			want:   []Money{},
		},
		"empty nonzero preserves amount": {
			money:  Money{Amount: 99, Currency: "GBP"},
			ratios: nil,
			want:   []Money{{Amount: 99, Currency: "GBP"}},
		},
		"zero amount keeps currencies": {
			money:  Money{Currency: "EUR"},
			ratios: []int{1, 0, -1},
			want:   []Money{{Currency: "EUR"}, {Currency: "EUR"}, {Currency: "EUR"}},
		},
		"single ratio no leftover": {
			money:  Money{Amount: 5, Currency: "GBP"},
			ratios: []int{1},
			want:   []Money{{Amount: 5, Currency: "GBP"}},
		},
		"largest remainder tie uses input order": {
			money:  Money{Amount: 2, Currency: "GBP"},
			ratios: []int{1, 1, 1},
			want: []Money{
				{Amount: 1, Currency: "GBP"},
				{Amount: 1, Currency: "GBP"},
				{Amount: 0, Currency: "GBP"},
			},
		},
		"positive amount": {
			money:  Money{Amount: 5, Currency: "GBP"},
			ratios: []int{1, 1},
			want: []Money{
				{Amount: 3, Currency: "GBP"},
				{Amount: 2, Currency: "GBP"},
			},
		},
		"largest remainder non-tie": {
			money:  Money{Amount: 5, Currency: "GBP"},
			ratios: []int{2, 1, 1},
			want: []Money{
				{Amount: 3, Currency: "GBP"},
				{Amount: 1, Currency: "GBP"},
				{Amount: 1, Currency: "GBP"},
			},
		},
		"negative amount": {
			money:  Money{Amount: -5, Currency: "GBP"},
			ratios: []int{1, 1},
			want: []Money{
				{Amount: -3, Currency: "GBP"},
				{Amount: -2, Currency: "GBP"},
			},
		},
		"zero and negative ratios": {
			money:  Money{Amount: 5, Currency: "GBP"},
			ratios: []int{0, 2, -1},
			want: []Money{
				{Amount: 0, Currency: "GBP"},
				{Amount: 5, Currency: "GBP"},
				{Amount: 0, Currency: "GBP"},
			},
		},
		"all non-positive ratios": {
			money:  Money{Amount: 5, Currency: "GBP"},
			ratios: []int{0, -2},
			want: []Money{
				{Amount: 5, Currency: "GBP"},
				{Amount: 0, Currency: "GBP"},
			},
		},
		"min int64 negative amount": {
			money:  Money{Amount: minInt64, Currency: "GBP"},
			ratios: []int{1},
			want:   []Money{{Amount: minInt64, Currency: "GBP"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := tc.money.Allocate(tc.ratios)
			if len(got) != len(tc.want) {
				t.Fatalf("len(Allocate) = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Allocate[%d] = %+v, want %+v; full result %+v", i, got[i], tc.want[i], got)
				}
			}
			assertAllocationSums(t, tc.money, got)
		})
	}
}

func TestFormatAndParseAmount(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		money Money
		want  string
	}{
		"gbp grouped":          {Money{Amount: 123456, Currency: "GBP"}, "£1,234.56"},
		"gbp triple group":     {Money{Amount: 12345600, Currency: "GBP"}, "£123,456.00"},
		"eur negative padded":  {Money{Amount: -7, Currency: "EUR"}, "-€0.07"},
		"unknown currency":     {Money{Amount: 123, Currency: "usd"}, "USD 1.23"},
		"blank currency":       {Money{Amount: 123, Currency: ""}, "1.23"},
		"small amount padding": {Money{Amount: 5, Currency: "GBP"}, "£0.05"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.money.Format(); got != tc.want {
				t.Fatalf("Format = %q, want %q", got, tc.want)
			}
		})
	}

	for name, tc := range map[string]struct {
		input    string
		currency string
		want     Money
	}{
		"plain grouped":        {"1,234.56", "GBP", Money{Amount: 123456, Currency: "GBP"}},
		"symbol negative":      {"-£1,234.50", "GBP", Money{Amount: -123450, Currency: "GBP"}},
		"symbol before sign":   {"£-1.23", "GBP", Money{Amount: -123, Currency: "GBP"}},
		"currency code prefix": {"gbp 1.20", "gbp", Money{Amount: 120, Currency: "GBP"}},
		"plus sign":            {"+1.23", "GBP", Money{Amount: 123, Currency: "GBP"}},
		"one decimal":          {"1.2", "GBP", Money{Amount: 120, Currency: "GBP"}},
		"whole units":          {"1", "GBP", Money{Amount: 100, Currency: "GBP"}},
		"eur symbol":           {"€0.07", "EUR", Money{Amount: 7, Currency: "EUR"}},
		"blank currency":       {"1.23", "", Money{Amount: 123, Currency: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseAmount(tc.input, tc.currency)
			if err != nil {
				t.Fatalf("ParseAmount returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ParseAmount = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseAmountRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"",
		"--1.00",
		"-+1.00",
		"+-1.00",
		"++1.00",
		"+£-1.00",
		"-£+1.00",
		".50",
		"12,34.00",
		"1234,567.00",
		"1.",
		"1.234",
		"1.a",
		"1 2.00",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseAmount(input, "GBP"); err == nil {
				t.Fatal("ParseAmount returned nil error, want failure")
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	m := Money{Amount: 123456, Currency: "GBP"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if string(data) != `{"amount":123456,"currency":"GBP"}` {
		t.Fatalf("Marshal = %s, want integer amount object", data)
	}

	var roundTrip Money
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if roundTrip != m {
		t.Fatalf("round trip = %+v, want %+v", roundTrip, m)
	}

	if err := json.Unmarshal([]byte(`{"amount":1.5,"currency":"GBP"}`), &roundTrip); err == nil {
		t.Fatal("Unmarshal accepted float amount, want error")
	}
}

func FuzzAddSubRoundTrip(f *testing.F) {
	for _, seed := range []struct {
		a int64
		b int64
	}{
		{0, 0},
		{1, -1},
		{123456, 654321},
		{maxInt64, 0},
		{minInt64, 0},
		{maxInt64, 1},
	} {
		f.Add(seed.a, seed.b)
	}

	f.Fuzz(func(t *testing.T, a, b int64) {
		left := Money{Amount: a, Currency: "GBP"}
		right := Money{Amount: b, Currency: "GBP"}
		sum, err := left.Add(right)
		if errors.Is(err, ErrOverflow) {
			return
		}
		if err != nil {
			t.Fatalf("Add returned unexpected error: %v", err)
		}
		got, err := sum.Sub(right)
		if err != nil {
			t.Fatalf("Sub returned error after successful Add: %v", err)
		}
		if got != left {
			t.Fatalf("Add/Sub round trip = %+v, want %+v", got, left)
		}
	})
}

func FuzzAllocateSumsExactly(f *testing.F) {
	f.Add(int64(1), []byte{1, 1})
	f.Add(int64(-1), []byte{1, 1, 1})
	f.Add(int64(5), []byte{0, 255, 2})
	f.Add(minInt64, []byte{1})

	f.Fuzz(func(t *testing.T, amount int64, data []byte) {
		ratios := ratiosFromBytes(data)
		original := Money{Amount: amount, Currency: "GBP"}
		parts := original.Allocate(ratios)
		if len(parts) != len(ratios) {
			t.Fatalf("Allocate returned %d parts, want %d", len(parts), len(ratios))
		}
		assertAllocationSums(t, original, parts)
		for i, part := range parts {
			if part.Currency != original.Currency {
				t.Fatalf("part %d currency = %q, want %q", i, part.Currency, original.Currency)
			}
		}
	})
}

func FuzzMulRatInverseWithinOneMinorUnit(f *testing.F) {
	for _, seed := range []struct {
		amount int64
		num    int64
		den    int64
	}{
		{1, 1, 2},
		{3, 2, 1},
		{123456, 7, 5},
		{-123456, 5, 7},
	} {
		f.Add(seed.amount, seed.num, seed.den)
	}

	f.Fuzz(func(t *testing.T, amount, num, den int64) {
		amount %= 1_000_000_000
		num = boundedPositive(num, 1000)
		den = boundedPositive(den, 1000)
		if num > 2*den || den > 2*num {
			return
		}

		original := Money{Amount: amount, Currency: "GBP"}
		rate := big.NewRat(num, den)
		inverse := big.NewRat(den, num)
		roundTrip := original.MulRat(rate).MulRat(inverse)
		if diff := absDiff(original.Amount, roundTrip.Amount); diff > 1 {
			t.Fatalf("rate/inverse round trip diff = %d minor units; original=%+v roundTrip=%+v rate=%s", diff, original, roundTrip, rate.RatString())
		}
	})
}

func assertAllocationSums(t *testing.T, original Money, parts []Money) {
	t.Helper()

	total := Zero(original.Currency)
	for _, part := range parts {
		var err error
		total, err = total.Add(part)
		if err != nil {
			t.Fatalf("summing allocation parts: %v", err)
		}
	}
	if total != original {
		t.Fatalf("allocation sum = %+v, want %+v; parts=%+v", total, original, parts)
	}
}

func ratiosFromBytes(data []byte) []int {
	if len(data) == 0 {
		return []int{0}
	}
	n := int(data[0]%8) + 1
	ratios := make([]int, n)
	for i := range ratios {
		b := data[i%len(data)]
		ratios[i] = int(int8(b)) % 7
	}
	return ratios
}

func boundedPositive(n int64, max int64) int64 {
	n %= max
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return 1
	}
	return n%max + 1
}

func absDiff(a, b int64) int64 {
	if a >= b {
		return a - b
	}
	return b - a
}

func recoveredAsError(recovered any) error {
	err, _ := recovered.(error)
	return err
}
