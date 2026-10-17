package migration

import (
	"context"
	"database/sql"
	"github.com/qingyingjin/banking-service/internal/storegate"
	"strings"
)

const CurrentVersion = 18

// Apply retains the existing caller contract; detect the actual connected
// database rather than trusting a mismatched environment label.
func Apply(ctx context.Context, db *sql.DB) error {
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return err
	}
	dialect := "mysql"
	if strings.HasPrefix(version, "PostgreSQL ") {
		dialect = "postgres"
	}
	if dialect == "postgres" {
		if err := ApplyPostgres(ctx, db); err != nil {
			return err
		}
	} else if err := applyMySQL(ctx, db); err != nil {
		return err
	}
	if err := storegate.Install(ctx, db, dialect, strings.Split(PlanningSQL, ";")); err != nil {
		return err
	}
	if err := applyPhoneAuth(ctx, db, dialect); err != nil {
		return err
	}
	if err := applySMSAuth(ctx, db, dialect); err != nil {
		return err
	}
	if err := applyCardPortfolio(ctx, db, dialect); err != nil {
		return err
	}
	if err := applyRenewals(ctx, db, dialect); err != nil {
		return err
	}
	if err := applyAAPortfolio(ctx, db, dialect); err != nil {
		return err
	}
	if err := applyRecipientDirectory(ctx, db, dialect); err != nil {
		return err
	}
	return applyModelBudget(ctx, db, dialect)
}
