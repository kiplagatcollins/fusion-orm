package feature_test

import (
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
)

func TestDialectCapabilitiesAreNamedFlags(t *testing.T) {
	tests := []struct {
		name    string
		flags   feature.Feature
		want    []feature.Feature
		missing []feature.Feature
	}{
		{
			name:  "postgres",
			flags: (&postgres.Dialector{}).Features(),
			want: []feature.Feature{
				feature.PostgreSQLTypes, feature.InformationSchema,
				feature.DollarPlaceholder, feature.SerialColumn,
				feature.AlterColumnType,
			},
			missing: []feature.Feature{feature.PragmaTableInfo, feature.AutoIncrementClause},
		},
		{
			name:  "sqlite",
			flags: (&sqlite.Dialector{}).Features(),
			want: []feature.Feature{
				feature.PragmaTableInfo, feature.AutoIncrementClause,
			},
			missing: []feature.Feature{
				feature.InformationSchema, feature.DollarPlaceholder,
				feature.SerialColumn, feature.AlterColumnType,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, flag := range tt.want {
				if !tt.flags.Has(flag) {
					t.Errorf("missing flag %s", flag.Name())
				}
			}
			for _, flag := range tt.missing {
				if tt.flags.Has(flag) {
					t.Errorf("unexpected flag %s", flag.Name())
				}
			}
		})
	}
}
