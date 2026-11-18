// Package callbacks defines the fixed CRUD callback chains. The processors
// themselves live in the fusion root package because callbacks operate on
// *DB; keeping the chain order here makes the extension points explicit.
package callbacks

// Create is the default create chain.
func Create() []string {
	return []string{
		"fusion:begin_tx", "fusion:before_create",
		"fusion:save_before_associations", "fusion:create",
		"fusion:save_after_associations", "fusion:after_create",
		"fusion:commit_or_rollback",
	}
}

// Query is the default query chain.
func Query() []string {
	return []string{
		"fusion:build_query", "fusion:before_query", "fusion:query",
		"fusion:preload", "fusion:after_query",
	}
}

// Update is the default update chain.
func Update() []string {
	return []string{
		"fusion:begin_tx", "fusion:before_update",
		"fusion:save_before_associations", "fusion:update",
		"fusion:save_after_associations", "fusion:after_update",
		"fusion:commit_or_rollback",
	}
}

// Delete is the default delete chain.
func Delete() []string {
	return []string{
		"fusion:begin_tx", "fusion:before_delete",
		"fusion:delete_before_associations", "fusion:delete",
		"fusion:after_delete", "fusion:commit_or_rollback",
	}
}

// Row is the default chain for Row/Rows.
func Row() []string { return []string{"fusion:build_query", "fusion:row"} }

// Raw is the default chain for raw Exec.
func Raw() []string { return []string{"fusion:raw"} }
