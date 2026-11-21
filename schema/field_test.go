package schema

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	_ "modernc.org/sqlite"
)

type closureModel struct {
	Bool    bool
	I       int
	I8      int8
	U       uint
	F32     float32
	F64     float64
	S       string
	B       []byte
	T       time.Time
	M       map[string]any
	Slice   []int
	Null    sql.NullString
	Ptr     *string
	Ignored string `fusion:"-"`
}

func TestFieldClosureRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 20, 30, 0, time.UTC)
	ptr := "ptr"
	source := closureModel{
		Bool: true, I: 42, I8: -7, U: 9, F32: 1.5, F64: -2.25,
		S: "O'Brien", B: []byte{0xDE, 0xAD}, T: now,
		M: map[string]any{"a": 1.0}, Slice: []int{1, 2, 3},
		Null: sql.NullString{String: "nullable", Valid: true}, Ptr: &ptr,
	}

	tables := NewTables(nil, false, nil)
	table, err := tables.Parse(&closureModel{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if table.Column("ignored") != nil {
		t.Error("fusion:\"-\" field was parsed")
	}

	pool, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "roundtrip.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer pool.Close()

	d := sqlite.Open("")
	src := reflect.ValueOf(source)
	for _, field := range table.Fields {
		t.Run(field.Name, func(t *testing.T) {
			lit := string(field.Append(nil, src, d))
			var raw any
			if err := pool.QueryRow("SELECT " + lit).Scan(&raw); err != nil {
				t.Fatalf("SELECT %s: %v", lit, err)
			}
			var dst closureModel
			if err := field.Scan(reflect.ValueOf(&dst).Elem(), raw); err != nil {
				t.Fatalf("Scan(%T): %v", raw, err)
			}
			got := field.Value(reflect.ValueOf(&dst).Elem()).Interface()
			want := field.Value(src).Interface()
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip = %#v, want %#v (literal %s)", got, want, lit)
			}
		})
	}
}

func TestFieldMetadata(t *testing.T) {
	type metaModel struct {
		ID        int64  `fusion:"pk,autoincrement"`
		Email     string `fusion:"user_email,notnull,index"`
		Nick      string `fusion:"unique,nickname"`
		Blob      []byte `fusion:"json"`
		Custom    string `fusion:"serializer:json,type:varchar(9),default:'x'"`
		CreatedAt time.Time
		UpdatedAt time.Time
		RO        string `fusion:"->"`
		WO        string `fusion:"<-"`
	}
	table, err := NewTables(nil, false, nil).Parse(&metaModel{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tests := []struct {
		name, column, dataType             string
		pk, creatable, updatable, readable bool
	}{
		{"ID", "id", "int", true, true, true, true},
		{"Email", "user_email", "string", false, true, true, true},
		{"Nick", "nickname", "string", false, true, true, true},
		{"Blob", "blob", "json", false, true, true, true},
		{"Custom", "custom", "json", false, true, true, true},
		{"CreatedAt", "created_at", "time", false, true, true, true},
		{"RO", "ro", "string", false, false, false, true},
		{"WO", "wo", "string", false, true, false, false},
	}
	for _, tt := range tests {
		f := table.Field(tt.name)
		if f == nil {
			t.Errorf("missing field %s", tt.name)
			continue
		}
		if f.DBName != tt.column || string(f.DataType) != tt.dataType ||
			f.PrimaryKey != tt.pk || f.Creatable != tt.creatable ||
			f.Updatable != tt.updatable || f.Readable != tt.readable {
			t.Errorf("%s = %+v", tt.name, f)
		}
	}
	if len(table.PKs) != 1 || !table.Field("CreatedAt").AutoCreateTime || !table.Field("UpdatedAt").AutoUpdateTime {
		t.Error("PK/timestamp metadata missing")
	}
	if got := table.Field("Custom").SQLType; got != "varchar(9)" {
		t.Errorf("SQLType = %q", got)
	}
	if table.Field("Email").IndexName == "" || table.Field("Nick").UniqueName == "" {
		t.Error("index/unique names not derived")
	}
}

func TestIsZeroClosures(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want bool
	}{
		{"string zero", "", true},
		{"string set", "x", false},
		{"int zero", 0, true},
		{"time zero", time.Time{}, true},
		{"time set", time.Now(), false},
		{"nil pointer", (*int)(nil), true},
	}
	for _, tt := range tests {
		fn := IsZero(reflect.TypeOf(tt.v))
		if got := fn(reflect.ValueOf(tt.v)); got != tt.want {
			t.Errorf("IsZero(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

type uuidPKModel struct {
	ID   uuid.UUID `fusion:"pk,type:uuid"`
	Name string
}

func TestPKGeneratorGeneratesUUIDv7(t *testing.T) {
	table, err := NewTables(nil, false, nil).Parse(&uuidPKModel{})
	if err != nil {
		t.Fatal(err)
	}
	field := table.Field("ID")
	if field.PKGenerator == nil {
		t.Fatal("PKGenerator not resolved for uuid pk")
	}
	v := reflect.New(reflect.TypeFor[uuid.UUID]()).Elem()
	if err := field.PKGenerator(v); err != nil {
		t.Fatalf("PKGenerator: %v", err)
	}
	id := v.Interface().(uuid.UUID)
	if id == (uuid.UUID{}) {
		t.Fatal("id still zero")
	}
	if id[6]>>4 != 7 {
		t.Errorf("version nibble = %d, want 7", id[6]>>4)
	}
	if id[8]>>6 != 2 {
		t.Errorf("variant nibble = %d, want 2 (RFC 9562)", id[8]>>6)
	}
}

func TestPKGeneratorNotSetForAutoincrement(t *testing.T) {
	type autoPK struct {
		ID   uint64 `fusion:"pk,autoincrement"`
		Name string
	}
	table, err := NewTables(nil, false, nil).Parse(&autoPK{})
	if err != nil {
		t.Fatal(err)
	}
	if field := table.Field("ID"); field.PKGenerator != nil {
		t.Error("PKGenerator set on autoincrement pk")
	}
}
