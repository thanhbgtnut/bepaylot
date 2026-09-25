package agent

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/tools"
	"github.com/thanhenti/bepaylot/internal/types"
)

// sessionKBScope reads the session's knowledge scope from its metadata:
// "kb_ids" (list of ids) and an optional pinned "kb_filter" (§8.1).
func sessionKBScope(sess types.Session, owner uuid.UUID) tools.KBScope {
	sc := tools.KBScope{Owner: owner}
	switch v := sess.Metadata["kb_ids"].(type) {
	case []any:
		for _, x := range v {
			if id, err := uuid.Parse(fmt.Sprint(x)); err == nil {
				sc.KBIDs = append(sc.KBIDs, id)
			}
		}
	case []string:
		for _, x := range v {
			if id, err := uuid.Parse(x); err == nil {
				sc.KBIDs = append(sc.KBIDs, id)
			}
		}
	}
	if f, ok := sess.Metadata["kb_filter"].(map[string]any); ok {
		sc.Filter = types.MetadataFilter(f)
	}
	return sc
}
