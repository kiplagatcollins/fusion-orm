package schema

import (
	"reflect"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
)

var benchSink []byte

func BenchmarkParseCached(b *testing.B) {
	tables := NewTables(nil, false, nil)
	if _, err := tables.Parse(&closureModel{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tables.Parse(&closureModel{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFieldAppend(b *testing.B) {
	table, err := NewTables(nil, false, nil).Parse(&closureModel{})
	if err != nil {
		b.Fatal(err)
	}
	field := table.Field("S")
	src := reflect.ValueOf(closureModel{S: "bench"})
	d := sqlite.Open("")
	b.ReportAllocs()
	for b.Loop() {
		benchSink = field.Append(benchSink[:0], src, d)
	}
}

func BenchmarkFieldScan(b *testing.B) {
	table, err := NewTables(nil, false, nil).Parse(&closureModel{})
	if err != nil {
		b.Fatal(err)
	}
	field := table.Field("I")
	dst := reflect.ValueOf(new(closureModel)).Elem()
	b.ReportAllocs()
	for b.Loop() {
		if err := field.Scan(dst, int64(42)); err != nil {
			b.Fatal(err)
		}
	}
}
