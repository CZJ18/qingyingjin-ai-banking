package main

import (
	"github.com/qingyingjin/banking-service/internal/coze"
	"testing"
)

func TestDirectBillingExplanationIsOptInAndDoesNotReinterpretCozeIDs(t *testing.T) {
	for _, value := range []string{"", "false"} {
		id, err := directBillingWorkflow(value)
		if err != nil || id != "" {
			t.Fatal(value, id, err)
		}
	}
	id, err := directBillingWorkflow("true")
	if err != nil || id != coze.DirectBillingExplanationWorkflow {
		t.Fatal(id, err)
	}
	for _, value := range []string{"1", "TRUE", "123", " true ", "direct_provider"} {
		if _, err := directBillingWorkflow(value); err == nil {
			t.Fatal("invalid opt-in", value)
		}
	}
}

func TestDirectLifestyleExplanationIsOptInAndDoesNotReinterpretCozeIDs(t *testing.T) {
	for _, value := range []string{"", "false"} {
		id, err := directLifestyleWorkflow(value)
		if err != nil || id != "" {
			t.Fatal(value, id, err)
		}
	}
	id, err := directLifestyleWorkflow("true")
	if err != nil || id != coze.DirectLifestyleExplanationWorkflow {
		t.Fatal(id, err)
	}
	for _, value := range []string{"1", "TRUE", "123", " true ", "direct_provider"} {
		if _, err := directLifestyleWorkflow(value); err == nil {
			t.Fatal("invalid opt-in", value)
		}
	}
}
