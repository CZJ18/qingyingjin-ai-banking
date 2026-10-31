package planning

import (
	"strings"
	"testing"
)

func TestAdviceExplainsProtectedCashAndUnknownIncome(t *testing.T) {
	s := sample()
	s.Settings.FutureIncomeMinor = nil
	r, err := Calculate(s)
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(r.ReviewNotes, " ")
	if !strings.Contains(notes, "future income unknown") {
		t.Fatal("unknown income silently presented as known zero", r)
	}
	for _, option := range r.Options {
		if !strings.Contains(option.Explanation, "55000") || !strings.Contains(option.Explanation, "45000") {
			t.Fatal("option does not explain its computed safe cap and protected reserves", option)
		}
	}
}
