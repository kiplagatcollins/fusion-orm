package schema

import (
	"strings"
	"testing"
)

func TestNamingStrategy(t *testing.T) {
	ns := NamingStrategy{}
	tests := []struct {
		in, want string
	}{
		{"User", "user"},
		{"Person", "person"},
		{"Category", "category"},
		{"UserID", "user_id"},
		{"HTTPServer", "http_server"},
		{"ID", "id"},
		{"URLValue", "url_value"},
		{"CreatedAt", "created_at"},
	}
	for _, tt := range tests {
		if got := ns.ColumnName("users", tt.in); got != tt.want {
			t.Errorf("ColumnName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for in, want := range map[string]string{"User": "users", "Person": "people", "Category": "categories", "UserID": "user_ids", "ID": "ids"} {
		if got := ns.TableName(in); got != want {
			t.Errorf("TableName(%q) = %q, want %q", in, got, want)
		}
	}

	if got := (NamingStrategy{SingularTable: true, TablePrefix: "app_"}).TableName("User"); got != "app_user" {
		t.Errorf("singular/prefixed = %q", got)
	}
	if got := (NamingStrategy{NoLowerCase: true}).ColumnName("", "UserID"); got != "UserID" {
		t.Errorf("NoLowerCase = %q", got)
	}
	if got := (NamingStrategy{NameReplacer: replacer{"User": "Person"}}).TableName("User"); got != "people" {
		t.Errorf("NameReplacer = %q", got)
	}
}

type replacer map[string]string

func (r replacer) Replace(s string) string {
	if v, ok := r[s]; ok {
		return v
	}
	return s
}

func TestJoinAndConstraintNames(t *testing.T) {
	ns := NamingStrategy{}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"JoinTableName lowercase", ns.JoinTableName("user_post"), "user_post"},
		{"JoinTableName camel", ns.JoinTableName("UserPost"), "user_posts"},
		{"FKName", ns.FKName("users", "account_id"), "fk_users_account_id"},
		{"IndexName", ns.IndexName("users", "email"), "idx_users_email"},
		{"UniqueName", ns.UniqueName("users", "email"), "uni_users_email"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	long := (NamingStrategy{IdentifierMaxLength: 20}).IndexName("very_long_table_name", "very_long_column_name")
	if len(long) != 20 || strings.HasPrefix(long, "idx_very_long_table_") {
		t.Errorf("long IndexName = %q (len %d)", long, len(long))
	}
}
