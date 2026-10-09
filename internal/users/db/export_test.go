package db

// Path exposes the path to the database file for testing.
func (m *Manager) Path() string {
	return m.path
}

// GetCreateSchemaQuery exposes the query to create the schema for testing.
func GetCreateSchemaQuery() string {
	return createSchemaQuery
}

// SetCreateSchemaQuery sets the query to create the schema for testing.
func SetCreateSchemaQuery(query string) {
	createSchemaQuery = query
}

// ApplyFullUsernameMigration applies the full username schema migration for testing.
func ApplyFullUsernameMigration(m *Manager) error {
	for _, migration := range schemaMigrations {
		if migration.description == "Add column 'full_username' to users table" {
			return migration.migrate(m)
		}
	}
	panic("full username migration not found")
}
