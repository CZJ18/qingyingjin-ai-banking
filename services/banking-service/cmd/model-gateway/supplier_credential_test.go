package main

import (
	"errors"
	"testing"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

func TestRealCozeAndDirectUseProtectedSupplierInjection(t *testing.T) {
	for _, mode := range []string{"coze", "direct_provider"} {
		reads := 0
		key, err := supplierCredential(modelspend.Approval{IntegrationMode: mode}, map[string]string{"BANKING_MODEL_SUPPLIER_SECRET_FILE": "/synthetic/secret"},
			func(string) string { return "" }, func(path string, max int64) ([]byte, error) {
				reads++
				if path != "/synthetic/secret" || max != 513 {
					t.Fatal("unexpected secret boundary")
				}
				return []byte("SYNTHETIC-ONLY-KEY\r\n"), nil
			})
		if err != nil || key != "SYNTHETIC-ONLY-KEY" || reads != 1 {
			t.Fatal("protected injection not shared", mode, err, reads)
		}
	}
}

func TestRealCredentialAmbiguityAndPlainConfigRejected(t *testing.T) {
	for _, config := range []map[string]string{
		{"BANKING_MODEL_SUPPLIER_API_KEY": "SYNTHETIC-PLAIN-KEY"},
		{"BANKING_MODEL_SUPPLIER_SECRET_FILE": "/synthetic/secret"},
	} {
		reads := 0
		_, err := supplierCredential(modelspend.Approval{IntegrationMode: "coze"}, config,
			func(string) string { return "SYNTHETIC-ENV-KEY" }, func(string, int64) ([]byte, error) { reads++; return nil, errors.New("must not read") })
		if err == nil || reads != 0 {
			t.Fatal("plain or ambiguous credential admitted")
		}
	}
}

func TestSimulationCredentialNeverReadsExternalSecret(t *testing.T) {
	key, err := supplierCredential(modelspend.Approval{Simulation: true}, map[string]string{"BANKING_MODEL_SUPPLIER_API_KEY": "synthetic-only"},
		func(string) string { t.Fatal("simulation inspected external env"); return "" }, func(string, int64) ([]byte, error) { t.Fatal("simulation inspected secret file"); return nil, nil })
	if err != nil || key != "synthetic-only" {
		t.Fatal("simulation contract changed")
	}
}
