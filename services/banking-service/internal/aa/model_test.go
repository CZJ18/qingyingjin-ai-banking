package aa

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestEqualSplitRemainder(t *testing.T) {
	v, e := Allocate(Input{Title: "聚餐", TotalMinor: 100, Mode: "equal", AlreadyPaid: true, AccountID: 1, Participants: []ShareInput{{Name: "甲"}, {Name: "乙"}}})
	if e != nil || len(v) != 3 || v[0] != 34 || v[1] != 33 || v[2] != 33 {
		t.Fatal(v, e)
	}
}
func TestDinnerExample(t *testing.T) {
	v, e := Allocate(Input{Title: "聚餐", TotalMinor: 36000, Mode: "equal", AlreadyPaid: true, AccountID: 1, Participants: []ShareInput{{Name: "甲"}, {Name: "乙"}, {Name: "丙"}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range v {
		if n != 9000 {
			t.Fatal(v)
		}
	}
}
func TestCustomSum(t *testing.T) {
	in := Input{Title: "出行", TotalMinor: 10000, OwnerShareMinor: 2000, Mode: "custom", AlreadyPaid: true, AccountID: 1, Participants: []ShareInput{{Name: "甲", AmountMinor: 3000}, {Name: "乙", AmountMinor: 5000}}}
	if _, e := Allocate(in); e != nil {
		t.Fatal(e)
	}
	in.TotalMinor++
	if _, e := Allocate(in); entity.Code(e) != "INVALID_ARGUMENT" {
		t.Fatal(e)
	}
}
func TestInvalidSplits(t *testing.T) {
	base := Input{Title: "聚餐", TotalMinor: 100, Mode: "equal", AlreadyPaid: true, AccountID: 1, Participants: []ShareInput{{Name: "甲"}}}
	cases := []Input{}
	v := base
	v.TotalMinor = 1
	cases = append(cases, v)
	v = base
	v.TotalMinor = entity.MaxBalance + 1
	cases = append(cases, v)
	v = base
	v.AlreadyPaid = false
	cases = append(cases, v)
	v = base
	v.Mode = "weighted"
	cases = append(cases, v)
	v = base
	v.Participants = []ShareInput{}
	cases = append(cases, v)
	v = base
	v.Participants = []ShareInput{{Name: "甲"}, {Name: "甲"}}
	cases = append(cases, v)
	v = base
	v.Participants = []ShareInput{{Name: "甲", AmountMinor: 2}}
	cases = append(cases, v)
	for _, in := range cases {
		if _, e := Allocate(in); entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatal(in, e)
		}
	}
}
