package main

import (
	"errors"
	"strings"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

// Called only after approval, independently confirmed plan, local scope and
// policy validation. Both real transports share the same protected injection;
// Coze must not require a key inside an ordinary config file.
func supplierCredential(a modelspend.Approval, config map[string]string, lookup func(string) string, read func(string, int64) ([]byte, error)) (string, error) {
	if a.Simulation {
		return config["BANKING_MODEL_SUPPLIER_API_KEY"], nil
	}
	if config["BANKING_MODEL_SUPPLIER_API_KEY"] != "" {
		return "", errors.New("real supplier key requires protected secret injection")
	}
	key := lookup("BANKING_MODEL_SUPPLIER_API_KEY")
	if path := config["BANKING_MODEL_SUPPLIER_SECRET_FILE"]; path != "" {
		if key != "" {
			return "", errors.New("ambiguous supplier secret injection")
		}
		raw, err := read(path, 513)
		if err != nil {
			return "", errors.New("supplier secret unavailable")
		}
		key = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	}
	return key, nil
}
