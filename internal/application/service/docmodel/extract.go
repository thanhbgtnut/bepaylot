package docmodel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// ExtractTools are the only tools of an extraction turn (§6.9.3): arithmetic
// and JSON checking. The case tools (kb_*) and MCP tools are for chat.
var ExtractTools = []string{"calculate", "json_validate"}

// extractMaxChars caps the text of one document sent to the agent.
const extractMaxChars = 60000

// ExtractRunner runs one extraction turn of the agent with ExtractTools, in a
// hidden session of the case, and returns the agent's reply.
type ExtractRunner interface {
	RunExtract(ctx context.Context, user types.User, caseID, doc uuid.UUID, text string) (string, error)
}

// Extractor fills the fields of the documents of a file (§6.9.3) when a sheet
// is built: for every document (segment) of one of the given types, the agent
// reads the text of its pages and returns the values, which are stored as
// proposals of the case and reused by every later sheet.
type Extractor struct {
	st     *postgres.Store
	runner ExtractRunner
	log    *slog.Logger
}

// NewExtractor builds the extractor.
func NewExtractor(st *postgres.Store, runner ExtractRunner, log *slog.Logger) *Extractor {
	if log == nil {
		log = slog.Default()
	}
	return &Extractor{st: st, runner: runner, log: log}
}

// Extract asks the agent for the fields of tables that the documents of one
// file still need, and returns how many fields it stored. A field that has a
// value (proposed or confirmed) is never asked again. A field the agent did
// not find is asked once more, the next time a sheet is built, and then left
// alone (U82). Two sheets built at once do not ask twice: the file is locked.
func (e *Extractor) Extract(ctx context.Context, docID uuid.UUID, tables []types.SheetTable) (int, error) {
	d, err := e.st.Documents.Get(ctx, docID)
	if errors.Is(err, postgres.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	byLabel := map[string]types.SheetTable{}
	for _, t := range tables {
		byLabel[t.Label] = t
	}
	segs, err := e.st.Segments.ByDocument(ctx, d.ID)
	if err != nil {
		return 0, err
	}
	conn, err := e.st.Pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	const lockKey = `hashtextextended('extract:' || $1::text, 0)`
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+lockKey+`)`, d.ID.String()); err != nil {
		return 0, err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(`+lockKey+`)`, d.ID.String()) //nolint:errcheck

	have, err := e.st.Fields.ListByCase(ctx, d.CaseID, &d.ID, "")
	if err != nil {
		return 0, err
	}
	// The list has the field a cell shows first (confirmed, else the newest
	// proposal), so the first one of a (segment, key) decides.
	done := map[string]bool{} // segment id + key
	seen := map[string]bool{}
	for _, f := range have {
		if f.SegmentID == nil || f.Ord != 0 || seen[f.SegmentID.String()+f.Key] {
			continue
		}
		seen[f.SegmentID.String()+f.Key] = true
		notFoundOnce := f.Source == types.FieldSourceAgent && f.ValueText == "" && f.Supersedes == nil
		done[f.SegmentID.String()+f.Key] = !notFoundOnce
	}
	var user *types.User
	saved := 0
	for _, g := range segs {
		t, ok := byLabel[g.Label]
		if !ok {
			continue
		}
		var missing []types.SheetField
		for _, f := range t.Fields {
			if !done[g.ID.String()+f.Key] {
				missing = append(missing, f)
			}
		}
		if len(missing) == 0 {
			continue
		}
		lines, err := e.st.Pages.Lines(ctx, d.ID, g.PageStart, g.PageEnd)
		if err != nil {
			return saved, err
		}
		if len(lines) == 0 {
			continue // no text to read: asking would only record "not found"
		}
		if user == nil {
			u, err := e.st.Users.Get(ctx, d.CreatedBy)
			if err != nil {
				return saved, fmt.Errorf("%w: uploader: %v", queue.ErrSkipRetry, err)
			}
			user = &u
		}
		reply, err := e.runner.RunExtract(ctx, *user, d.CaseID, d.ID, extractPrompt(t, missing, lines))
		if err != nil {
			return saved, err
		}
		for _, w := range extractedFields(d, g, missing, lines, reply) {
			if _, err := e.st.Fields.Save(ctx, w); err != nil {
				e.log.Warn("extract: save field", "doc", d.ID, "key", w.Key, "err", err)
				continue
			}
			saved++
		}
	}
	return saved, nil
}

// extractPrompt is the message of one extraction turn: the document type, the
// fields to find, each with the description that guides the agent (U81), and
// the text of the document as numbered lines.
func extractPrompt(t types.SheetTable, fields []types.SheetField, lines []postgres.PageLine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bóc tách thông tin từ một giấy tờ thuộc loại: %s", t.Title)
	if t.Description != "" {
		fmt.Fprintf(&b, " (%s)", t.Description)
	}
	b.WriteString(".\n\nCác trường cần lấy (key · tên trường · kiểu · mô tả). Mô tả, nếu có, là hướng dẫn cách tìm và cách ghi giá trị của trường đó:\n")
	for _, f := range fields {
		vt := f.ValueType
		if vt == "" {
			vt = "string"
		}
		fmt.Fprintf(&b, "- %s · %s · %s", f.Key, f.Label, vt)
		if desc := strings.Join(strings.Fields(f.Description), " "); desc != "" {
			fmt.Fprintf(&b, " · %s", desc)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\nNội dung giấy tờ, theo trang; mỗi dòng mở đầu bằng số dòng [L<n>] của trang đó:\n")
	cur, size := -1, 0
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		if size += len(l.Text); size > extractMaxChars {
			b.WriteString("[… phần sau của giấy tờ bị lược vì quá dài]\n")
			break
		}
		if l.PageNo != cur {
			if cur >= 0 {
				b.WriteString("</page>\n")
			}
			cur = l.PageNo
			fmt.Fprintf(&b, "<page n=\"%d\">\n", cur)
		}
		fmt.Fprintf(&b, "[L%d] %s\n", l.LineNo, l.Text)
	}
	if cur >= 0 {
		b.WriteString("</page>\n")
	}
	b.WriteString("\nChỉ dùng nội dung ở trên, không suy đoán. Giá trị phải tính (tổng, hiệu, tích, thương) thì gọi tool calculate " +
		"và ghi cách tính vào note. Kiểu date viết YYYY-MM-DD; kiểu number và money viết số, không có dấu ngăn cách hàng nghìn.\n" +
		"Trả lời đúng một JSON, không kèm lời giải thích:\n" +
		`{"fields":[{"key":"<key>","value":<giá trị, hoặc null khi giấy tờ không có>,"confidence":<0-1>,` +
		`"page":<số trang>,"line":<số dòng [L] chứa giá trị>,"note":"<cách tính, nếu có>"}]}`)
	return b.String()
}

// extractedFields reads the agent's JSON reply into the fields to store: each
// asked key with its value and the cited line as evidence when the line
// exists. A key the reply has no value for is stored without a value: the
// document does not have it. A reply that is not JSON gives no field, so the
// document is asked again by the next sheet.
func extractedFields(d types.Document, g types.Segment, asked []types.SheetField, lines []postgres.PageLine, reply string) []postgres.FieldWrite {
	type item struct {
		Key        string  `json:"key"`
		Value      any     `json:"value"`
		Confidence float64 `json:"confidence"`
		Page       int     `json:"page"`
		Line       int     `json:"line"`
		Note       string  `json:"note"`
	}
	var out struct {
		Fields []item `json:"fields"`
	}
	if err := llm.DecodeJSON(reply, &out); err != nil {
		return nil
	}
	got := map[string]item{}
	for _, f := range out.Fields {
		if _, dup := got[f.Key]; !dup { // the first value of a key wins
			got[f.Key] = f
		}
	}
	seg := g.ID
	var res []postgres.FieldWrite
	for _, a := range asked {
		vt := a.ValueType
		if !valueTypes[vt] {
			vt = "string"
		}
		f := got[a.Key]
		text := strings.TrimSpace(textutil.ValueText(f.Value))
		w := postgres.FieldWrite{CaseID: d.CaseID, DocumentID: d.ID, SegmentID: &seg, Key: a.Key, Value: f.Value, ValueType: vt, ValueText: text,
			Confidence: math.Max(0, math.Min(1, f.Confidence)), Status: types.FieldProposed, Source: types.FieldSourceAgent,
			CreatedBy: &d.CreatedBy, Note: f.Note}
		if f.Value == nil || text == "" {
			w.Value, w.ValueText, w.Confidence = nil, "", 0
			if w.Note == "" {
				w.Note = "Không tìm thấy trong giấy tờ"
			}
			res = append(res, w)
			continue
		}
		for _, l := range lines {
			if l.PageNo == f.Page && l.LineNo == f.Line {
				w.Evidence = []types.Evidence{{DocumentID: d.ID, FileName: d.FileName, PageNo: l.PageNo, LineFrom: l.LineNo, LineTo: l.LineNo,
					BBox: []float64{l.BBox.X0, l.BBox.Y0, l.BBox.X1, l.BBox.Y1}, Quote: l.Text,
					CitationID: fmt.Sprintf("doc:%s:p%d:l%d-%d", d.ID, l.PageNo, l.LineNo, l.LineNo), Status: "valid"}}
				w.ValueMatched = textutil.ValueIn(text, l.Text)
			}
		}
		res = append(res, w)
	}
	return res
}
