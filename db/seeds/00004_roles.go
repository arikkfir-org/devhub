package seeds

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
)

// releaseManager is Acme's own role, which Max holds beside Member, so a reviewer sees a custom role defined,
// granted and listed on the roles page.
var releaseManager = struct {
	id                uuid.UUID
	name, description string
	permissions       []string
}{
	id: uuid.MustParse("00000000-0000-7000-8000-00000000d1c1"), name: "Release manager",
	description: "Merges and bypasses the rules of Acme's repositories.",
	permissions: []string{"org.read", "repo.read", "repo.merge", "repo.bypass"},
}

// roles seeds Acme's custom role and grants it to Max.
func roles(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	acme := demoOrganizations[0]
	if err := q.InsertSeedRole(ctx, queries.InsertSeedRoleParams{
		ID: releaseManager.id, Name: releaseManager.name, Description: releaseManager.description,
		Permissions: strings.Join(releaseManager.permissions, ","), OrgID: uuid.NullUUID{UUID: acme.id, Valid: true},
	}); err != nil {
		return fmt.Errorf("seeding %s's role %s: %w", acme.slug, releaseManager.name, err)
	}
	if err := q.InsertSeedGrant(ctx, queries.InsertSeedGrantParams{
		PrincipalID: demoUsers[1].id, RoleID: releaseManager.id, Scope: "organization", ScopeID: uuid.NullUUID{UUID: acme.id, Valid: true},
	}); err != nil {
		return fmt.Errorf("granting Max %s at %s: %w", releaseManager.name, acme.slug, err)
	}
	return nil
}
