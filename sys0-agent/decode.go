package main

import (
	"encoding/json"

	"github.com/fakecrowd/sys0/internal/rpc"
)

// decode unmarshals JSON params into v. Shared by every module's handlers, so it
// has no build tag — it compiles into every binary (monolith and each module).
func decode(params json.RawMessage, v any) *rpc.Error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return rpc.Errorf(rpc.CodeBadParams, "%v", err)
	}
	return nil
}
