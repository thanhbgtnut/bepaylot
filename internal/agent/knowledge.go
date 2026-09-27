package agent

import (
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/tools"
	"github.com/thanhenti/bepaylot/internal/types"
)

// sessionCaseScope reads the case bound to the session (sessions.case_id,
// §8.1). The scope never comes from the model or the message: ok is false
// for a session without a case, which gets no document tools.
func sessionCaseScope(sess types.Session, owner uuid.UUID) (tools.CaseScope, bool) {
	if sess.CaseID == nil || *sess.CaseID == uuid.Nil {
		return tools.CaseScope{}, false
	}
	return tools.CaseScope{Owner: owner, CaseID: *sess.CaseID}, true
}
