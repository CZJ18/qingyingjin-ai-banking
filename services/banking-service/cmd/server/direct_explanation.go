package main

import (
	"errors"
	"github.com/qingyingjin/banking-service/internal/coze"
)

// A local test adapter opt-in, never an approval or a published Coze ID.
func directBillingWorkflow(value string) (string, error) {
	switch value {
	case "", "false":
		return "", nil
	case "true":
		return coze.DirectBillingExplanationWorkflow, nil
	default:
		return "", errors.New("BANKING_DIRECT_BILLING_EXPLANATION must be explicitly true or false")
	}
}

func directLifestyleWorkflow(value string) (string, error) {
	switch value {
	case "", "false":
		return "", nil
	case "true":
		return coze.DirectLifestyleExplanationWorkflow, nil
	default:
		return "", errors.New("BANKING_DIRECT_LIFESTYLE_EXPLANATION must be explicitly true or false")
	}
}
