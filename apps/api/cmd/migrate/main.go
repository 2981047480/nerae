// nerae-migrate applies a checksum-verified release manifest to an explicitly
// configured database. It does not create, drop or repair a database.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"nerae/api/internal/migrations"
)

func run(args []string) error {
	if len(args) != 1 || (args[0] != "check" && args[0] != "up") {
		return errors.New("usage: nerae-migrate check|up")
	}
	entries, err := migrations.Load(os.Getenv("NERAE_MIGRATION_ROOT"))
	if err != nil {
		return err
	}
	if args[0] == "check" {
		fmt.Printf("migration manifest valid: %d entries\n", len(entries))
		return nil
	}
	database := os.Getenv("NERAE_MIGRATION_DATABASE")
	db, err := migrations.Open(os.Getenv("NERAE_MYSQL_DSN"), database)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := migrations.Apply(ctx, db, database, entries); err != nil {
		return err
	}
	fmt.Printf("migration history current: %d entries\n", len(entries))
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migration:", err)
		os.Exit(1)
	}
}
