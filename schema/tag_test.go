package schema

import (
	"errors"
	"testing"
)

func TestParseTag(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantName string
		want     map[string]string
	}{
		{"bare column", "email", "email", nil},
		{"full", "user_email,type:varchar(255),pk,autoincrement,notnull,default:now(),index,unique", "user_email", map[string]string{
			"type": "varchar(255)", "pk": "", "autoincrement": "", "notnull": "",
			"default": "now()", "index": "", "unique": "",
		}},
		{"gorm semicolon migration", "column:user_email;primaryKey;autoIncrement;not null;uniqueIndex;foreignKey:UserID", "", map[string]string{
			"column": "user_email", "pk": "", "autoincrement": "", "notnull": "",
			"unique": "", "foreignkey": "UserID",
		}},
		{"quoted value", `type:"decimal(10,2)"`, "", map[string]string{"type": "decimal(10,2)"}},
		{"relation", "rel:has-many,foreignKey:UserID,many2many:user_groups,polymorphic:owner", "", map[string]string{
			"rel": "has-many", "foreignkey": "UserID", "many2many": "user_groups", "polymorphic": "owner",
		}},
		{"ignored", "-", "", map[string]string{"-": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag, err := ParseTag(tt.raw, false, nil)
			if err != nil {
				t.Fatalf("ParseTag: %v", err)
			}
			if tag.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", tag.Name, tt.wantName)
			}
			for key, want := range tt.want {
				if got := tag.Get(key); got != want {
					t.Errorf("Get(%q) = %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestParseTagUnknownKeys(t *testing.T) {
	var warned []string
	tag, err := ParseTag("email,bogus,other:value", false, func(key string) { warned = append(warned, key) })
	if err != nil {
		t.Fatalf("lenient ParseTag: %v", err)
	}
	if tag.Name != "email" {
		t.Errorf("Name = %q", tag.Name)
	}
	if len(warned) != 2 || warned[0] != "bogus" || warned[1] != "other" {
		t.Errorf("warned = %v", warned)
	}

	_, err = ParseTag("email,bogus:x", true, nil)
	if !errors.Is(err, ErrInvalidTag) {
		t.Errorf("strict ParseTag = %v, want ErrInvalidTag", err)
	}
}

func TestTagHasGet(t *testing.T) {
	tag, _ := ParseTag("pk,index:idx_custom,->", false, nil)
	for _, key := range []string{"pk", "index", "->"} {
		if !tag.Has(key) {
			t.Errorf("Has(%q) = false", key)
		}
	}
	if tag.Has("unique") {
		t.Error("Has(unique) = true")
	}
	if got := tag.Get("index"); got != "idx_custom" {
		t.Errorf("Get(index) = %q", got)
	}
	if got := tag.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q", got)
	}
}
