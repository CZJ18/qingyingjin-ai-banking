package subscription

import (
	"bytes"
	"embed"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"io"
	"strings"
	"sync"
)

//go:embed schemas/*.json
var Schemas embed.FS
var once sync.Once
var schemas map[string]*jsonschema.Schema
var initError error

func validate(name string, raw []byte) error {
	once.Do(func() {
		schemas = map[string]*jsonschema.Schema{}
		for _, n := range []string{"subscription-report", "subscription-detail", "subscription-intent", "subscription"} {
			raw, e := Schemas.ReadFile("schemas/" + n + ".schema.json")
			if e != nil {
				initError = e
				return
			}
			var doc any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if e = d.Decode(&doc); e != nil {
				initError = e
				return
			}
			c := jsonschema.NewCompiler()
			url := "urn:qingyingjin:" + n + ":1"
			if e = c.AddResource(url, doc); e != nil {
				initError = e
				return
			}
			schemas[n], e = c.Compile(url)
			if e != nil {
				initError = e
				return
			}
		}
	})
	if initError != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "subscription schema unavailable")
	}
	var doc any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return entity.Fail("MODEL_OUTPUT_INVALID", "invalid structured subscription JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || schemas[name].Validate(doc) != nil {
		return entity.Fail("MODEL_OUTPUT_INVALID", "subscription JSON Schema rejected output")
	}
	return nil
}
func ValidateReport(r Report) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return validate("subscription-report", raw)
}

func normalizeMerchant(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
