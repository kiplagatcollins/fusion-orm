package clause

import (
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
)

type goldenBuilder struct {
	buf  []byte
	vars []any
	d    dialect.Dialector
}

func (g *goldenBuilder) WriteString(s string)         { g.buf = append(g.buf, s...) }
func (g *goldenBuilder) AppendByte(c byte)            { g.buf = append(g.buf, c) }
func (g *goldenBuilder) WriteQuoted(s string)         { g.buf = g.d.QuoteTo(g.buf, s) }
func (g *goldenBuilder) Dialector() dialect.Dialector { return g.d }
func (g *goldenBuilder) AddVar(v any) {
	g.buf = g.d.BindVarTo(g.buf, len(g.vars)+1)
	g.vars = append(g.vars, v)
}

func TestGoldenClauses(t *testing.T) {
	tests := []struct {
		name   string
		clause Interface
		sqlite string
		pg     string
		vars   []any
	}{
		{
			"select", Select{Columns: []string{"id", "users.name"}},
			`SELECT "id", "users"."name"`, `SELECT "id", "users"."name"`, nil,
		},
		{
			"select distinct", Select{Columns: []string{"id"}, Distinct: true},
			`SELECT DISTINCT "id"`, `SELECT DISTINCT "id"`, nil,
		},
		{
			"from", From{Table: "users"},
			`FROM "users"`, `FROM "users"`, nil,
		},
		{
			"where", Where{Conds: []Condition{
				{Exprs: []Expression{Expr(`"age" > ?`, 18)}},
				Or(Expr(`"name" = ?`, "bob")),
			}},
			`WHERE "age" > ? OR "name" = ?`, `WHERE "age" > $1 OR "name" = $2`, []any{18, "bob"},
		},
		{
			"order", OrderBy{Columns: []string{"name DESC", "id"}},
			`ORDER BY name DESC, id`, `ORDER BY name DESC, id`, nil,
		},
		{
			"limit offset", Limit{N: 10},
			`LIMIT ?`, `LIMIT $1`, []any{10},
		},
		{
			"offset", Offset{N: 5},
			`OFFSET ?`, `OFFSET $1`, []any{5},
		},
		{
			"locking", Locking{Enabled: true},
			`FOR UPDATE`, `FOR UPDATE`, nil,
		},
		{
			"with", With{CTEName: "active", Subquery: "SELECT 1"},
			`WITH "active" AS (SELECT 1) `, `WITH "active" AS (SELECT 1) `, nil,
		},
		{
			"update", Update{Table: "users"},
			`UPDATE "users"`, `UPDATE "users"`, nil,
		},
		{
			"delete", Delete{Table: "users"},
			`DELETE FROM "users"`, `DELETE FROM "users"`, nil,
		},
		{
			"insert", Insert{Table: "users", Columns: []string{"id", "name"}},
			`INSERT INTO "users" ("id", "name")`, `INSERT INTO "users" ("id", "name")`, nil,
		},
		{
			"values", Values{Rows: []Expression{Expr("?, ?", 1, "a"), Expr("?, ?", 2, "b")}},
			`VALUES (?, ?), (?, ?)`, `VALUES ($1, $2), ($3, $4)`, []any{1, "a", 2, "b"},
		},
		{
			"set", Set{Assignments: []Assignment{
				{Column: "name", Value: Expr("?", "z")},
				{Column: "age", Value: Expr("? + 1", 41)},
			}},
			`SET "name" = ?, "age" = ? + 1`, `SET "name" = $1, "age" = $2 + 1`, []any{"z", 41},
		},
		{
			"on conflict", OnConflict{Columns: []string{"email"}, DoNothing: true},
			`ON CONFLICT ("email") DO NOTHING`, `ON CONFLICT ("email") DO NOTHING`, nil,
		},
		{
			"on conflict update", OnConflict{Columns: []string{"email"}, UpdateColumns: []string{"name"}},
			`ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name"`,
			`ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name"`, nil,
		},
		{
			"join relation", Join{
				Type: "LEFT", Table: "profiles", Alias: "profile",
				On: []Expression{Expr(`"users"."id" = "profile"."user_id"`)},
			},
			`LEFT JOIN "profiles" "profile" ON "users"."id" = "profile"."user_id"`,
			`LEFT JOIN "profiles" "profile" ON "users"."id" = "profile"."user_id"`, nil,
		},
		{
			"join raw", Join{HasRaw: true, Raw: Expr(`LEFT JOIN "profiles" ON "profiles"."user_id" = ?`, 7)},
			`LEFT JOIN "profiles" ON "profiles"."user_id" = ?`,
			`LEFT JOIN "profiles" ON "profiles"."user_id" = $1`, []any{7},
		},
		{
			"join set", Joins{Items: []Join{
				{Type: "LEFT", Table: "a", Alias: "x"},
				{HasRaw: true, Raw: Expr(`INNER JOIN "b" ON "b"."id" = ?`, 7)},
			}},
			`LEFT JOIN "a" "x" INNER JOIN "b" ON "b"."id" = ?`,
			`LEFT JOIN "a" "x" INNER JOIN "b" ON "b"."id" = $1`, []any{7},
		},
		{
			"returning", Returning{Columns: []string{"id"}},
			`RETURNING "id"`, `RETURNING "id"`, nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, d := range []struct {
				d    dialect.Dialector
				want string
			}{
				{sqlite.Open(""), tt.sqlite},
				{postgres.Open(""), tt.pg},
			} {
				g := &goldenBuilder{d: d.d}
				tt.clause.Build(g)
				if got := string(g.buf); got != d.want {
					t.Errorf("%s: got %q, want %q", d.d.Name(), got, d.want)
				}
				if len(g.vars) != len(tt.vars) {
					t.Errorf("%s: vars = %v, want %v", d.d.Name(), g.vars, tt.vars)
					continue
				}
				for i := range g.vars {
					if g.vars[i] != tt.vars[i] {
						t.Errorf("%s: vars[%d] = %v, want %v", d.d.Name(), i, g.vars[i], tt.vars[i])
					}
				}
			}
		})
	}
}

func TestExpressionExtraVarsIgnored(t *testing.T) {
	g := &goldenBuilder{d: sqlite.Open("")}
	Expr("?", 1, 2).Build(g)
	if got := string(g.buf); got != "?" || len(g.vars) != 1 {
		t.Errorf("got %q / %v", got, g.vars)
	}
}
