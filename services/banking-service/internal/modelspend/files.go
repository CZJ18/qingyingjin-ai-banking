package modelspend

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

// StrictJSON rejects duplicate keys at every nesting level as well as unknown
// struct fields and trailing values. It is shared by operator file contracts.
func StrictJSON(raw []byte, out any, maxBytes int) error {
	if len(raw) == 0 || len(raw) > maxBytes {
		return ErrPolicy
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := uniqueValue(d, 0); e != nil {
		return ErrPolicy
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrPolicy
	}
	if e := exactFields(raw, reflect.TypeOf(out), 0); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return ErrPolicy
	}
	return nil
}

func exactFields(raw []byte, typ reflect.Type, depth int) error {
	if typ == nil || depth > 40 {
		return ErrPolicy
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return ErrPolicy
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for key, value := range values {
			field, ok := fields[key]
			if !ok {
				return ErrPolicy
			}
			if e := exactFields(value, field, depth+1); e != nil {
				return e
			}
		}
	case reflect.Slice, reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return ErrPolicy
		}
		for _, value := range values {
			if e := exactFields(value, typ.Elem(), depth+1); e != nil {
				return e
			}
		}
	}
	return nil
}
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 40 {
		return ErrPolicy
	}
	token, e := d.Token()
	if e != nil {
		return ErrPolicy
	}
	marker, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch marker {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			name, ok := key.(string)
			if e != nil || !ok || keys[name] {
				return ErrPolicy
			}
			keys[name] = true
			if e := uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return ErrPolicy
		}
	case '[':
		for d.More() {
			if e := uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return ErrPolicy
		}
	default:
		return ErrPolicy
	}
	return nil
}
func ParsePolicy(raw []byte) (Policy, error) {
	var p Policy
	if e := StrictJSON(raw, &p, 16*1024); e != nil {
		return p, e
	}
	return p, p.Check()
}

type billEnvelope struct {
	SchemaVersion  int        `json:"schema_version"`
	ProjectID      string     `json:"project_id"`
	Currency       string     `json:"currency"`
	BillID         string     `json:"bill_id"`
	EvidenceDigest string     `json:"evidence_sha256"`
	Rows           []billLine `json:"rows"`
}
type billLine struct {
	LineID             string `json:"line_id"`
	AttemptID          string `json:"attempt_id"`
	ProviderID         string `json:"provider_id"`
	ActualCost         string `json:"actual_cost"`
	ConfirmNoActiveRun bool   `json:"confirm_no_active_run"`
}

func ParseBill(raw []byte, evidenceDigest string) ([]Settlement, error) {
	var b billEnvelope
	if e := StrictJSON(raw, &b, 512*1024); e != nil {
		return nil, e
	}
	if b.SchemaVersion != 1 || !identifier.MatchString(b.ProjectID) || len(b.Currency) != 3 || !modelName.MatchString(b.BillID) || len(b.EvidenceDigest) != 64 || b.EvidenceDigest != evidenceDigest || len(b.Rows) == 0 || len(b.Rows) > 100 {
		return nil, ErrPolicy
	}
	if _, e := hex.DecodeString(b.EvidenceDigest); e != nil {
		return nil, ErrPolicy
	}
	seen := map[string]bool{}
	out := make([]Settlement, 0, len(b.Rows))
	for _, r := range b.Rows {
		if !modelName.MatchString(r.LineID) || seen[r.LineID] || r.AttemptID == "" || r.ProviderID == "" {
			return nil, ErrPolicy
		}
		seen[r.LineID] = true
		if _, e := ParseMoney(r.ActualCost); e != nil {
			return nil, e
		}
		out = append(out, Settlement{AttemptID: r.AttemptID, ProjectID: b.ProjectID, Currency: b.Currency, BillID: b.BillID, LineID: r.LineID, ProviderID: r.ProviderID, ActualCost: r.ActualCost, EvidenceDigest: b.EvidenceDigest, ConfirmNoActiveRun: r.ConfirmNoActiveRun})
	}
	return out, nil
}
