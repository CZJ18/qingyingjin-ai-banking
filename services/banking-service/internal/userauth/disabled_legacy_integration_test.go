//go:build integration

package userauth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
)

func TestLegacyUserTokenRejectsInactiveExistingUser(t *testing.T) {
	s, auth, conn := authHarness(t)
	ctx := context.Background()
	id := time.Now().UnixNano() / 1000
	user := entity.User{ID: id, DisplayName: "Synthetic disabled legacy", Status: "active"}
	if err := conn.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	_ = s
	token, err := auth.IssueUser(fmt.Sprint(id), "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.UserContext(ctx, token); err != nil {
		t.Fatal("active control rejected", err)
	}
	for _, status := range []string{"inactive", "frozen", "disabled"} {
		if err = conn.DB.Model(&entity.User{}).Where("id=?", id).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		if _, err = auth.UserContext(ctx, token); err == nil {
			t.Errorf("existing %s user kept financial access with a legitimate legacy JWT", status)
		}
	}
}

func TestDelegatedAgentTokenCannotReadForInactiveOwner(t *testing.T) {
	_, auth, conn := authHarness(t)
	id := time.Now().UnixNano() / 1000
	if e := conn.DB.Create(&entity.User{ID: id, DisplayName: "Synthetic delegated owner", Status: "active"}).Error; e != nil {
		t.Fatal(e)
	}
	token, e := auth.IssueAgent(fmt.Sprint(id), "billing", "synthetic-task", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = auth.UserContext(context.Background(), token); e != nil {
		t.Fatal("active delegated control rejected", e)
	}
	if e = conn.DB.Model(&entity.User{}).Where("id=?", id).Update("status", "inactive").Error; e != nil {
		t.Fatal(e)
	}
	if _, e = auth.UserContext(context.Background(), token); e == nil {
		t.Fatal("agent retained financial reads after owner disabled")
	}
}
