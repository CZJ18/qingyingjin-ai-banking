package entity

import "strings"

// The Unicode White_Space set used by strings.TrimSpace. SQL readers must use
// this same set rather than database TRIM's default ASCII-space semantics.
const CategoryWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

// CategoryAliases is the shared read-side classification dictionary. Original
// transaction categories remain immutable evidence.
var categoryAliases = map[string][]string{
	"dining":        {"dining", "food", "餐饮", "饮食", "groceries", "食品"},
	"transport":     {"transport", "transportation", "交通"},
	"shopping":      {"shopping", "购物"},
	"sports":        {"sports", "exercise", "fitness", "运动"},
	"subscriptions": {"subscription", "subscriptions", "订阅"},
	"housing":       {"housing", "住房", "rent"},
	"medical":       {"medical", "healthcare", "医疗"},
	"education":     {"education", "教育"},
	"other":         {"other", "其他"},
}

func CanonicalCategory(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	for canonical, aliases := range categoryAliases {
		for _, alias := range aliases {
			if alias == value {
				return canonical, true
			}
		}
	}
	return "uncategorized", false
}

func CategoryAliases(canonical string) []string {
	return append([]string{}, categoryAliases[canonical]...)
}
func KnownCategoryAliases() []string {
	result := []string{}
	for _, aliases := range categoryAliases {
		result = append(result, aliases...)
	}
	return result
}
