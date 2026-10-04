package agent

import "regexp"

// citeRefs rewrites citation ids the model wrote with case refs
// (doc:d2:p1:l3, doc:d1.n4:p2:l7) to the document id (doc:<uuid>:p…), so
// every citation that leaves the server resolves (§6.5 step 8, §8.2). It runs
// after the textGuard on streamed text; text is held only while it may still
// be the start of such an id. Refs not in the session's case are left as is.
type citeRefs struct {
	refs map[string]string // d<n> → document id
	held string
}

var (
	refCite = regexp.MustCompile(`doc:(d\d+)(?:\.n\d+)?:`)
	// refTail matches the end of text that may still become a ref citation.
	refTail = regexp.MustCompile(`(?:doc:(?:d(?:\d+(?:\.(?:n\d*)?)?)?)?|doc|do|d)$`)
)

// feed returns the text that is safe to send.
func (c *citeRefs) feed(s string) string {
	if len(c.refs) == 0 {
		return s
	}
	s = c.held + s
	cut := len(s)
	if loc := refTail.FindStringIndex(s); loc != nil {
		cut = loc[0]
	}
	c.held = s[cut:]
	return c.rewrite(s[:cut])
}

// flush releases what is still held; call it when the text block ends.
func (c *citeRefs) flush() string {
	s := c.held
	c.held = ""
	return c.rewrite(s)
}

func (c *citeRefs) rewrite(s string) string {
	return refCite.ReplaceAllStringFunc(s, func(m string) string {
		if id, ok := c.refs[refCite.FindStringSubmatch(m)[1]]; ok {
			return "doc:" + id + ":"
		}
		return m
	})
}
