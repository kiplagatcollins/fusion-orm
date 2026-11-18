package callbacks

import (
	"reflect"
	"testing"
)

func TestChainOrder(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"create", Create(), []string{"fusion:begin_tx", "fusion:before_create", "fusion:save_before_associations", "fusion:create", "fusion:save_after_associations", "fusion:after_create", "fusion:commit_or_rollback"}},
		{"query", Query(), []string{"fusion:build_query", "fusion:before_query", "fusion:query", "fusion:preload", "fusion:after_query"}},
		{"update", Update(), []string{"fusion:begin_tx", "fusion:before_update", "fusion:save_before_associations", "fusion:update", "fusion:save_after_associations", "fusion:after_update", "fusion:commit_or_rollback"}},
		{"delete", Delete(), []string{"fusion:begin_tx", "fusion:before_delete", "fusion:delete_before_associations", "fusion:delete", "fusion:after_delete", "fusion:commit_or_rollback"}},
		{"row", Row(), []string{"fusion:build_query", "fusion:row"}},
		{"raw", Raw(), []string{"fusion:raw"}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s chain = %v, want %v", tt.name, tt.got, tt.want)
		}
		tt.got[0] = "mutated"
		if tt.got[0] == Create()[0] && tt.name == "create" {
			t.Error("chain slice leaked to caller")
		}
	}
}
