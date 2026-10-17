package httpapi

import "testing"

func TestOrchestrationTransportIdentifiers(t *testing.T) {
	for _, raw := range []string{"", "1", "00000000-0000-0000-0000-00000000000G", "E95B4060-62C7-4B06-944A-2A52AC8BB6B4"} {
		if _, e := orchestrationID(raw); e == nil {
			t.Fatalf("accepted noncanonical UUID %q", raw)
		}
	}
	if _, e := orchestrationID("e95b4060-62c7-4b06-944a-2a52ac8bb6b4"); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"-1", "01", "+1", " 1", "9223372036854775808"} {
		if _, e := orchestrationCursor(raw); e == nil {
			t.Fatalf("accepted cursor %q", raw)
		}
	}
	for _, raw := range []string{"", "0", "1", "9223372036854775807"} {
		if _, e := orchestrationCursor(raw); e != nil {
			t.Fatal(e)
		}
	}
}
