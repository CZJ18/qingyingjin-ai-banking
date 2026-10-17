/*
 * Copyright 2025 coze-dev Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package model

import (
	"reflect"
	"testing"
)

func TestFoodOrderItemsAreIgnoredByGORM(t *testing.T) {
	field, ok := reflect.TypeOf(FoodOrder{}).FieldByName("Items")
	if !ok {
		t.Fatal("FoodOrder.Items field is missing")
	}
	if got := field.Tag.Get("gorm"); got != "-" {
		t.Fatalf("FoodOrder.Items gorm tag = %q, want %q", got, "-")
	}
}
