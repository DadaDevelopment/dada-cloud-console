package solutions

// ManagedResource is a platform resource the resolver can offer alongside
// applications: something the customer asks for by name and the platform runs
// for them, rather than an image or a repository we deploy.
//
// Engine is the value the databases API expects. Aliases are the other words
// people type for the same thing — the point of the whole list is that "post"
// finds Postgres before the third keystroke, which is the OSS-user scenario in
// tasks/ready-made-projects-unified-design.md.
type ManagedResource struct {
	Slug    string
	Name    string
	Tagline string
	Engine  string
	Aliases []string
}

// ManagedResources is what the resolver can offer today.
//
// Postgres only, and deliberately so: the k8s track creates managed databases
// through ServiceDatabase, which is Postgres, and offering Redis here would
// mean a card that resolves on both runtimes but installs on one. A short
// honest list beats a long one that fails in the second half.
var ManagedResources = []ManagedResource{
	{
		Slug:    "postgres",
		Name:    "PostgreSQL",
		Tagline: "Управляемая база данных: бэкапы, восстановление и строка подключения из консоли",
		Engine:  "postgres",
		Aliases: []string{"postgresql", "postgre", "pg", "постгрес", "база", "база данных", "бд", "database", "db", "sql"},
	},
}
