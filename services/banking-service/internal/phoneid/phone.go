// Package phoneid shares the existing browser-auth phone normalization contract.
package phoneid

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"strings"
)

func Normalize(raw string) (string, error) {
	phone := strings.TrimPrefix(strings.TrimSpace(raw), "+86")
	if len(phone) != 11 || phone[0] != '1' || phone[1] < '3' || phone[1] > '9' {
		return "", entity.Fail("INVALID_ARGUMENT", "a valid mainland China phone number is required")
	}
	for _, c := range phone {
		if c < '0' || c > '9' {
			return "", entity.Fail("INVALID_ARGUMENT", "a valid mainland China phone number is required")
		}
	}
	return "+86" + phone, nil
}
func Mask(phone string) string {
	if len(phone) != 14 || !strings.HasPrefix(phone, "+86") {
		return ""
	}
	return phone[3:6] + "****" + phone[10:]
}
