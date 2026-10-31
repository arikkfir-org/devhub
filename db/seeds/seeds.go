// Package seeds holds Cobranch's demo data: the users, organizations, repositories and the rest a demo installation
// starts with, so a visitor tries it at once, with nothing to register or set up. Each seed is a Go migration,
// numbered on its own and recorded in table cobranch_seed_version, apart from the schema's (db/migrations), so an
// installation that never seeds keeps every one of its schema's versions.
//
// Every image holds the seeds; the migrate Job applies them after the schema's migrations, and only when the chart's
// seed.enabled is on, as a demo installation does. A demo installation upgraded in place applies only the seeds it
// lacks. Each slice adds the seeds its feature needs, in Go, numbered after the last.
package seeds

import (
	"github.com/pressly/goose/v3"
)

// Go holds the seeds, in the order they apply.
var Go = []*goose.Migration{
	goose.NewGoMigration(1, &goose.GoFunc{RunTx: baseline}, nil),
	goose.NewGoMigration(2, &goose.GoFunc{RunTx: users}, nil),
	goose.NewGoMigration(3, &goose.GoFunc{RunTx: organizations}, nil),
	goose.NewGoMigration(4, &goose.GoFunc{RunTx: roles}, nil),
}
