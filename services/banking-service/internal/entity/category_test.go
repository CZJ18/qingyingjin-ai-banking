package entity

import "testing"

func TestCanonicalCategoryDictionary(t *testing.T) {
	for raw, want := range map[string]string{" FOOD ": "dining", "餐饮": "dining", "transportation": "transport", "subscription": "subscriptions", "salary": "uncategorized", "": "uncategorized"} {
		got, _ := CanonicalCategory(raw)
		if got != want {
			t.Fatalf("%q: %s != %s", raw, got, want)
		}
	}
	aliases := CategoryAliases("dining")
	aliases[0] = "mutated"
	if CategoryAliases("dining")[0] != "dining" {
		t.Fatal("dictionary escaped")
	}
}
