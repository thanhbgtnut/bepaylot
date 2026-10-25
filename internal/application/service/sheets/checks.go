package sheets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/checkexpr"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Errors of the cross-checks (§6.9.9).
var (
	ErrCheckNotFound   = errors.New("check run not found")
	ErrRulesetNotFound = errors.New("rule set not found")
	ErrNoChecks        = errors.New("bộ quy tắc không dùng được cho hồ sơ này: thêm bộ vào thư viện và thêm ít nhất một quy tắc ở Cài đặt → Quy tắc đối chiếu")
	ErrBadChecks       = errors.New("quy tắc đối chiếu không hợp lệ")
)

// typeInfos describes the doc types of a KB for formulas.
func typeInfos(kb types.KBConfig) map[string]checkexpr.TypeInfo {
	out := map[string]checkexpr.TypeInfo{}
	for _, t := range kb.DocTypes {
		ti := checkexpr.TypeInfo{Title: t.Title, Fields: map[string]checkexpr.FieldInfo{}}
		for _, f := range t.Fields {
			ti.Fields[f.Key] = checkexpr.FieldInfo{Label: f.Label, ValueType: f.ValueType}
		}
		out[t.Name] = ti
	}
	return out
}

func typeTitle(kb types.KBConfig, name string) string {
	for _, t := range kb.DocTypes {
		if t.Name == name {
			return t.Title
		}
	}
	return name
}

// linkScope is what a step formula sees: the document (by its type code)
// and one contract (by the anchor code).
func linkScope(kb types.KBConfig, docType, anchor string) checkexpr.Scope {
	return checkexpr.Scope{Types: typeInfos(kb), SingleType: map[string]string{docType: docType, anchor: anchor}, Single: map[string]checkexpr.Doc{}}
}

// ruleScope is what a rule sees: the contract and the documents of every
// other type in its group.
func ruleScope(kb types.KBConfig, anchor string) checkexpr.Scope {
	return checkexpr.Scope{Types: typeInfos(kb), SingleType: map[string]string{anchor: anchor}, Single: map[string]checkexpr.Doc{}, Groups: map[string][]checkexpr.Doc{}}
}

// Comparisons of a rule chosen field by field (U104), by the kind of field A.
var checkOps = map[string]map[string]string{
	"text":  {"same": "giống với", "contains": "có chứa", "diff": "khác với"},
	"money": {"eq": "bằng", "le": "không lớn hơn", "ge": "không nhỏ hơn", "sum_le": "cộng lại không vượt quá"},
	"date":  {"before": "trước hoặc cùng ngày", "after": "sau hoặc cùng ngày", "same_day": "cùng ngày với"},
}

// fieldKind groups value types the way rules compare them.
func fieldKind(vt string) string {
	switch vt {
	case "money", "number":
		return "money"
	case "date":
		return "date"
	}
	return "text"
}

func fieldOf(kb types.KBConfig, ref types.FieldRef) (types.SheetField, bool) {
	for _, t := range kb.DocTypes {
		if t.Name != ref.Type {
			continue
		}
		for _, f := range t.Fields {
			if f.Key == ref.Key {
				return f, true
			}
		}
	}
	return types.SheetField{}, false
}

// ruleFormula builds the formula of a rule chosen field by field. A field of
// a type other than the anchor is the list of its documents in the group:
// each item is compared (sum_le adds them up first).
func ruleFormula(r types.CheckRule, anchor string) string {
	a, b := r.A.Type+"."+r.A.Key, r.B.Type+"."+r.B.Key
	switch r.Op {
	case "same", "same_day":
		if r.Op == "same_day" {
			return a + " = " + b
		}
		return "SAME(" + a + ", " + b + ")"
	case "contains":
		return "CONTAINS(" + a + ", " + b + ")"
	case "diff":
		return "NOT(SAME(" + a + ", " + b + "))"
	case "eq":
		return a + " = " + b
	case "le", "before":
		return a + " <= " + b
	case "ge", "after":
		return a + " >= " + b
	case "sum_le":
		if r.B.Type != anchor {
			b = "SUM(" + b + ")"
		}
		return "SUM(" + a + ") <= " + b
	}
	return ""
}

// ruleName is a rule in plain words: "Tài khoản nhận của Uỷ nhiệm chi giống
// với Tài khoản bên bán của Hợp đồng".
func ruleName(kb types.KBConfig, r types.CheckRule, anchor string) string {
	if r.A == nil || r.B == nil {
		return r.Text
	}
	fa, _ := fieldOf(kb, *r.A)
	fb, _ := fieldOf(kb, *r.B)
	a := fa.Label + " của " + typeTitle(kb, r.A.Type)
	b := fb.Label + " của " + typeTitle(kb, r.B.Type)
	op := checkOps[fieldKind(fa.ValueType)][r.Op]
	if r.Op == "sum_le" {
		if r.B.Type != anchor {
			b = "tổng " + b
		}
		return "Tổng " + a + " " + op + " " + b
	}
	return a + " " + op + " " + b
}

// checkRuleset validates a rule set against the document types it may meet
// (those of its knowledge bases) and gives every step and rule an id, a name
// and, for a rule chosen field by field, its formula.
func checkRuleset(kb types.KBConfig, rs types.CheckRuleset) (types.CheckRuleset, error) {
	if strings.TrimSpace(rs.Name) == "" {
		return rs, fmt.Errorf("đặt tên cho bộ quy tắc")
	}
	out := rs
	out.Name, out.Description = strings.TrimSpace(rs.Name), strings.TrimSpace(rs.Description)
	out.Links, out.Rules = []types.CheckLink{}, []types.CheckRule{}
	if rs.Anchor == "" {
		if len(rs.Rules) > 0 || len(rs.Links) > 0 {
			return rs, fmt.Errorf("chọn loại giấy tờ gốc (ví dụ Hợp đồng)")
		}
		return out, nil
	}
	if !hasType(kb, rs.Anchor) {
		return rs, fmt.Errorf("thư viện dùng bộ này không có loại giấy tờ gốc %q", rs.Anchor)
	}
	for _, l := range rs.Links {
		if l.DocType == rs.Anchor || !hasType(kb, l.DocType) {
			continue
		}
		cl := types.CheckLink{DocType: l.DocType, Steps: []types.CheckStep{}}
		for _, st := range l.Steps {
			st.Text = strings.TrimSpace(st.Text)
			if st.Text == "" && st.Formula == "" {
				continue
			}
			if st.ID == "" {
				st.ID = uuid.NewString()[:8]
			}
			if !st.AI {
				if strings.TrimSpace(st.Formula) == "" {
					return rs, fmt.Errorf("%s, bước “%s”: chưa có công thức (bấm Dịch hoặc chọn AI đánh giá)", typeTitle(kb, l.DocType), st.Text)
				}
				if _, err := checkexpr.Check(st.Formula, linkScope(kb, l.DocType, rs.Anchor)); err != nil {
					return rs, fmt.Errorf("%s, bước “%s”: %v", typeTitle(kb, l.DocType), st.Text, err)
				}
			}
			cl.Steps = append(cl.Steps, st)
		}
		if len(cl.Steps) > 0 {
			out.Links = append(out.Links, cl)
		}
	}
	for _, r := range rs.Rules {
		if r.ID == "" {
			r.ID = uuid.NewString()[:8]
		}
		r.Text = strings.TrimSpace(r.Text)
		switch {
		case r.A != nil && r.B != nil && r.Text == "":
			fa, okA := fieldOf(kb, *r.A)
			fb, okB := fieldOf(kb, *r.B)
			if !okA || !okB {
				return rs, fmt.Errorf("quy tắc dùng trường không có trong loại giấy tờ của thư viện")
			}
			ka := fieldKind(fa.ValueType)
			if ka != fieldKind(fb.ValueType) {
				return rs, fmt.Errorf("“%s” và “%s” khác kiểu, không so được", fa.Label, fb.Label)
			}
			if _, ok := checkOps[ka][r.Op]; !ok {
				return rs, fmt.Errorf("cách so %q không dùng được cho %s", r.Op, fa.Label)
			}
			r.Formula, r.AI = ruleFormula(r, rs.Anchor), false
			if _, err := checkexpr.Check(r.Formula, ruleScope(kb, rs.Anchor)); err != nil {
				return rs, fmt.Errorf("quy tắc “%s”: %v", ruleName(kb, r, rs.Anchor), err)
			}
			r.Name = ruleName(kb, r, rs.Anchor)
		case r.Text != "":
			// A sentence that is not one comparison of two fields: judged by the
			// AI at each run (U105). A formula written by the agent before 0.33
			// is kept and still checked.
			r.A, r.B, r.Op = nil, nil, ""
			if strings.TrimSpace(r.Formula) == "" {
				r.AI = true
			} else if _, err := checkexpr.Check(r.Formula, ruleScope(kb, rs.Anchor)); err != nil {
				r.Formula, r.AI = "", true
			}
			if strings.TrimSpace(r.Name) == "" {
				r.Name = r.Text
			}
		default:
			continue
		}
		out.Rules = append(out.Rules, r)
	}
	return out, nil
}

// ---- rule sets

// rulesetTypes are the document types a rule set may meet: those of the
// knowledge bases that use it (the user's own when it is not used yet).
func (s *Service) rulesetTypes(ctx context.Context, owner uuid.UUID, kbIDs []uuid.UUID) (types.KBConfig, error) {
	var kbs []types.KnowledgeBase
	if len(kbIDs) == 0 {
		list, err := s.st.KBs.List(ctx, owner, false)
		if err != nil {
			return types.KBConfig{}, err
		}
		kbs = list
	}
	for _, id := range kbIDs {
		if kb, err := s.st.KBs.Get(ctx, id); err == nil {
			kbs = append(kbs, kb)
		}
	}
	out := types.KBConfig{}
	seen := map[string]int{}
	for _, kb := range kbs {
		for _, t := range kb.Config.DocTypes {
			i, ok := seen[t.Name]
			if !ok {
				seen[t.Name] = len(out.DocTypes)
				t.Fields = append([]types.SheetField(nil), t.Fields...)
				out.DocTypes = append(out.DocTypes, t)
				continue
			}
			have := map[string]bool{}
			for _, f := range out.DocTypes[i].Fields {
				have[f.Key] = true
			}
			for _, f := range t.Fields {
				if !have[f.Key] {
					out.DocTypes[i].Fields = append(out.DocTypes[i].Fields, f)
				}
			}
		}
	}
	return out, nil
}

// ListRulesets returns the user's rule sets, or those one knowledge base uses.
func (s *Service) ListRulesets(ctx context.Context, owner uuid.UUID, kb *uuid.UUID) ([]types.CheckRuleset, error) {
	return s.st.Rulesets.List(ctx, owner, kb)
}

// GetRuleset returns one rule set of the user.
func (s *Service) GetRuleset(ctx context.Context, owner, id uuid.UUID) (types.CheckRuleset, error) {
	rs, err := s.st.Rulesets.Get(ctx, owner, id)
	if err != nil {
		return rs, ErrRulesetNotFound
	}
	return rs, nil
}

// ownKBs keeps the knowledge bases of a set the user may attach: their own.
func (s *Service) ownKBs(ctx context.Context, owner uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := s.st.KBs.GetOwned(ctx, id, owner); err != nil {
			return nil, fmt.Errorf("%w: không có thư viện %s", ErrBadChecks, id)
		}
		out = append(out, id)
	}
	return out, nil
}

// SaveRuleset creates (zero id) or replaces a rule set after checking it.
func (s *Service) SaveRuleset(ctx context.Context, user types.User, rs types.CheckRuleset) (types.CheckRuleset, error) {
	kbIDs, err := s.ownKBs(ctx, user.ID, rs.KBIDs)
	if err != nil {
		return rs, err
	}
	rs.KBIDs = kbIDs
	kb, err := s.rulesetTypes(ctx, user.ID, rs.KBIDs)
	if err != nil {
		return rs, err
	}
	clean, err := checkRuleset(kb, rs)
	if err != nil {
		return rs, fmt.Errorf("%w: %v", ErrBadChecks, err)
	}
	if rs.ID == uuid.Nil {
		clean.CreatedBy = &user.ID
		return s.st.Rulesets.Create(ctx, clean)
	}
	if _, err := s.GetRuleset(ctx, user.ID, rs.ID); err != nil {
		return rs, err
	}
	return s.st.Rulesets.Update(ctx, clean)
}

// CopyRuleset duplicates a rule set, used by no knowledge base yet.
func (s *Service) CopyRuleset(ctx context.Context, user types.User, id uuid.UUID) (types.CheckRuleset, error) {
	rs, err := s.GetRuleset(ctx, user.ID, id)
	if err != nil {
		return rs, err
	}
	rs.ID, rs.Name, rs.KBIDs, rs.CreatedBy = uuid.Nil, rs.Name+" (bản sao)", []uuid.UUID{}, &user.ID
	return s.st.Rulesets.Create(ctx, rs)
}

// DeleteRuleset removes a rule set; earlier runs keep their result.
func (s *Service) DeleteRuleset(ctx context.Context, owner, id uuid.UUID) error {
	if _, err := s.GetRuleset(ctx, owner, id); err != nil {
		return err
	}
	return s.st.Rulesets.Delete(ctx, id)
}

// CompileResult is a sentence turned into a formula, or why it could not.
type CompileResult struct {
	Name        string `json:"name"`
	Formula     string `json:"formula,omitempty"`
	AI          bool   `json:"ai"`
	Explanation string `json:"explanation"`
	Missing     string `json:"missing,omitempty"` // fields the KB lacks for it
}

const compileSystem = `Bạn chuyển một câu mô tả kiểm tra chứng từ (tiếng Việt, người không biết kỹ thuật viết) thành một công thức đúng/sai.
Công thức giống Excel. Gọi trường theo dạng loai_giay_to.key, chỉ dùng mã loại và key có trong danh sách. Chuỗi trong ngoặc kép. Ngày viết "YYYY-MM-DD".
Phép: + - * / = != < <= > >= AND OR NOT. Hàm:
SUM(x) MIN(x) MAX(x) AVERAGE(x) COUNT(x) — gộp giá trị của mọi giấy tờ cùng loại; COUNT(loai) đếm số giấy tờ của loại
EXISTS(x) — có giấy tờ / giá trị
BETWEEN(x, thấp, cao) — x trong khoảng (áp cho từng giấy tờ)
SAME(a, b, ...) — các giá trị giống nhau (so sau khi chuẩn hoá)
MATCH(a, b, "số"|"tên"|"chứa"|"gần đúng") — so khớp: "số" chỉ so chữ số (tài khoản, mã số thuế), "tên" so tên công ty bỏ "Công ty TNHH/CP…", "chứa" a có chứa b, "gần đúng" giống nhau gần đủ
CONTAINS(văn_bản, đoạn) — văn bản có chứa đoạn
IF(điều_kiện, khi_đúng[, khi_sai]) — không có khi_sai thì điều kiện sai coi là đạt
DATE_ADD(ngày, số, "ngày"|"tháng"|"năm"), ROUND(x, n), ABS(x), TODAY()
Trường của loại có nhiều giấy tờ là danh sách: so sánh áp cho từng giấy tờ; muốn tổng thì dùng SUM.
Nếu câu cần thông tin mà thư viện không có trường nào phù hợp, hoặc cần đọc hiểu nội dung (so mặt hàng, đánh giá ý nghĩa) mà công thức không làm được, đặt "ai": true, bỏ "formula", và ghi trường còn thiếu vào "missing" nếu có.
Chỉ trả lời một JSON: {"name": "<tên ngắn của kiểm tra>", "formula": "<công thức>", "ai": false, "explanation": "<giải thích công thức bằng lời thường, một câu>", "missing": "<trường còn thiếu, nếu có>"}`

// Compile asks the agent once to turn a sentence into a formula (§6.9.9),
// checks it against the document types the rule set meets, and asks once
// more with the error when it does not pass; a sentence that still has no
// valid formula is marked AI.
func (s *Service) Compile(ctx context.Context, user types.User, rulesetID uuid.UUID, kind, docType, anchor, text string) (CompileResult, error) {
	rs, err := s.GetRuleset(ctx, user.ID, rulesetID)
	if err != nil {
		return CompileResult{}, err
	}
	cfg, err := s.rulesetTypes(ctx, user.ID, rs.KBIDs)
	if err != nil {
		return CompileResult{}, err
	}
	kb := types.KnowledgeBase{Config: cfg}
	text = strings.TrimSpace(text)
	if text == "" {
		return CompileResult{}, fmt.Errorf("%w: câu trống", ErrBadChecks)
	}
	if !hasType(kb.Config, anchor) {
		return CompileResult{}, fmt.Errorf("%w: chọn loại giấy tờ gốc trước", ErrBadChecks)
	}
	sc := ruleScope(kb.Config, anchor)
	ctxText := fmt.Sprintf("Đây là một quy tắc kiểm tra cho cả bộ chứng từ của một %s (mã %s, chỉ có một giấy tờ trong bộ); các loại khác là danh sách giấy tờ của bộ.",
		typeTitle(kb.Config, anchor), anchor)
	if kind == "link" {
		if !hasType(kb.Config, docType) || docType == anchor {
			return CompileResult{}, fmt.Errorf("%w: loại giấy tờ không hợp lệ", ErrBadChecks)
		}
		sc = linkScope(kb.Config, docType, anchor)
		ctxText = fmt.Sprintf("Đây là một bước nhận biết một %s (mã %s, một giấy tờ) có thuộc một %s (mã %s, một giấy tờ ứng viên) hay không: công thức đúng khi giấy tờ thuộc hợp đồng đó.",
			typeTitle(kb.Config, docType), docType, typeTitle(kb.Config, anchor), anchor)
	}
	type field struct {
		Key, Label, Type, Description string `json:",omitempty"`
	}
	lib := map[string]any{}
	for _, t := range kb.Config.DocTypes {
		var fs []field
		for _, f := range t.Fields {
			vt := f.ValueType
			if vt == "" {
				vt = "string"
			}
			fs = append(fs, field{f.Key, f.Label, vt, f.Description})
		}
		lib[t.Name] = map[string]any{"title": t.Title, "fields": fs}
	}
	libJSON, _ := json.Marshal(lib)
	msg := ctxText + "\nThư viện (mã loại → tên, trường):\n" + string(libJSON) + "\n\nCâu cần chuyển: " + text
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: &user.ID, Kind: types.UsageSheet})
	var out CompileResult
	if err := s.formLLM.CompleteJSON(ctx, compileSystem, msg, &out); err != nil {
		return CompileResult{}, err
	}
	if !out.AI {
		if _, cerr := checkexpr.Check(out.Formula, sc); cerr != nil {
			retry := msg + "\n\nCông thức bạn đưa: " + out.Formula + "\nLỗi: " + cerr.Error() + "\nSửa lại."
			var again CompileResult
			if err := s.formLLM.CompleteJSON(ctx, compileSystem, retry, &again); err == nil {
				out = again
			}
			if _, cerr := checkexpr.Check(out.Formula, sc); !out.AI && cerr != nil {
				out = CompileResult{Name: out.Name, AI: true, Explanation: "Không chuyển được thành công thức (" + cerr.Error() + "); AI sẽ tự đánh giá câu này mỗi lần đối chiếu."}
			}
		}
	}
	if out.AI {
		out.Formula = ""
		if out.Explanation == "" {
			out.Explanation = "AI sẽ đọc thông tin các giấy tờ và tự đánh giá câu này mỗi lần đối chiếu."
		}
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = text
	}
	return out, nil
}

// ---- runs

// CheckDoc is one document of a run.
type CheckDoc struct {
	ID         string    `json:"id"` // segment id
	DocumentID uuid.UUID `json:"document_id"`
	Type       string    `json:"type"`
	TypeTitle  string    `json:"type_title"`
	Label      string    `json:"label"`
	FileName   string    `json:"file_name,omitempty"`
	PageStart  int       `json:"page_start,omitempty"`
	PageEnd    int       `json:"page_end,omitempty"`
}

// CheckMember is a document of a group, or one no group took.
type CheckMember struct {
	CheckDoc
	Source     string   `json:"source"` // rule | ai | user | single
	Reason     string   `json:"reason"`
	Candidates []string `json:"candidates,omitempty"` // contracts it may belong to
}

// CheckSide is one side of a rule chosen field by field, named for the page.
type CheckSide struct {
	Type      string `json:"type"`
	TypeTitle string `json:"type_title"`
	Key       string `json:"key"`
	Label     string `json:"label"`
}

// CheckRow is one comparison of a rule on one document (U108): the value of
// side A on that document, the value of side B it was compared with and the
// outcome, so the page can say which document is wrong.
type CheckRow struct {
	Segment    string    `json:"segment"`
	DocumentID uuid.UUID `json:"document_id"`
	Label      string    `json:"label"`
	Page       int       `json:"page"`
	ValueA     string    `json:"value_a"`
	ValueB     string    `json:"value_b,omitempty"`
	Status     string    `json:"status,omitempty"` // pass | fail | unknown; empty = part of a total
	Unsure     bool      `json:"unsure,omitempty"` // a value the AI read with low confidence
}

// CheckTotal is the sum a sum_le rule compared.
type CheckTotal struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Note string `json:"note,omitempty"`
}

// CheckOutcome is the outcome of one rule on one group.
type CheckOutcome struct {
	RuleID string      `json:"rule_id"`
	Name   string      `json:"name"`
	Text   string      `json:"text,omitempty"`
	AI     bool        `json:"ai,omitempty"`
	Op     string      `json:"op,omitempty"`
	OpText string      `json:"op_text,omitempty"`
	A      *CheckSide  `json:"a,omitempty"`
	B      *CheckSide  `json:"b,omitempty"`
	Rows   []CheckRow  `json:"rows,omitempty"`
	Total  *CheckTotal `json:"total,omitempty"`
	Hint   string      `json:"hint,omitempty"`
	checkexpr.Result
}

// CheckGroup is a contract and its documents.
type CheckGroup struct {
	Anchor  CheckDoc       `json:"anchor"`
	Members []CheckMember  `json:"members"`
	Results []CheckOutcome `json:"results"`
}

// CheckResult is the result of a run.
type CheckResult struct {
	RulesetID   uuid.UUID     `json:"ruleset_id"`
	RulesetName string        `json:"ruleset_name"`
	Anchor      string        `json:"anchor"`
	AnchorTitle string        `json:"anchor_title"`
	Groups      []CheckGroup  `json:"groups"`
	Unlinked    []CheckMember `json:"unlinked"`
	Counts      struct {
		Groups   int `json:"groups"`
		Pass     int `json:"pass"`
		Fail     int `json:"fail"`
		Unknown  int `json:"unknown"`
		Unlinked int `json:"unlinked"`
	} `json:"counts"`
	RanAt time.Time `json:"ran_at"`
}

// CreateCheck starts a cross-check of a case by one rule set the case's KB
// uses (U105): a sheet of the document types the set compares (extracting
// only what the case lacks, U80), then the groups and rules.
func (s *Service) CreateCheck(ctx context.Context, user types.User, caseID, rulesetID uuid.UUID) (types.CheckRun, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return types.CheckRun{}, err
	}
	rs, err := s.GetRuleset(ctx, user.ID, rulesetID)
	if err != nil {
		return types.CheckRun{}, err
	}
	if rs.Anchor == "" || len(rs.Rules) == 0 || !slices.Contains(rs.KBIDs, c.KBID) {
		return types.CheckRun{}, ErrNoChecks
	}
	sh, err := s.create(ctx, user, c, rulesetTables(rs), "Đối chiếu chứng từ", nil)
	if err != nil {
		return types.CheckRun{}, err
	}
	return s.st.Checks.Create(ctx, types.CheckRun{CaseID: c.ID, SheetID: sh.ID, RulesetID: &rs.ID, CreatedBy: user.ID})
}

// rulesetTables are the document types a rule set reads: the anchor, the
// linked types and both sides of every rule; a set with sentences only reads
// every type with fields (nil).
func rulesetTables(rs types.CheckRuleset) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	add(rs.Anchor)
	for _, l := range rs.Links {
		add(l.DocType)
	}
	for _, r := range rs.Rules {
		if r.Off {
			continue
		}
		if r.A == nil {
			return nil
		}
		add(r.A.Type)
		add(r.B.Type)
	}
	return out
}

// afterBuild runs the cross-checks that waited for a sheet.
func (s *Service) afterBuild(ctx context.Context, sheetID uuid.UUID, failed string) {
	runs, err := s.st.Checks.BySheet(ctx, sheetID)
	if err != nil {
		return
	}
	for _, r := range runs {
		if failed != "" {
			_ = s.st.Checks.SetResult(ctx, r.ID, types.SheetFailed, nil, failed)
			continue
		}
		if _, err := s.runCheck(ctx, r); err != nil {
			s.log.Warn("check: run failed", "run", r.ID, "err", err)
			_ = s.st.Checks.SetResult(context.WithoutCancel(ctx), r.ID, types.SheetFailed, nil, err.Error())
		}
	}
}

func (s *Service) ownedCheck(ctx context.Context, owner, id uuid.UUID) (types.CheckRun, error) {
	r, err := s.st.Checks.Get(ctx, id)
	if err != nil {
		return r, ErrCheckNotFound
	}
	if _, err := s.cases.GetCaseOwned(ctx, owner, r.CaseID); err != nil {
		return r, ErrCheckNotFound
	}
	return r, nil
}

// ListChecks returns the runs of a case.
func (s *Service) ListChecks(ctx context.Context, owner, caseID uuid.UUID) ([]types.CheckRun, error) {
	if _, err := s.cases.GetCaseOwned(ctx, owner, caseID); err != nil {
		return nil, err
	}
	return s.st.Checks.ListByCase(ctx, caseID)
}

// GetCheck returns a run.
func (s *Service) GetCheck(ctx context.Context, owner, id uuid.UUID) (types.CheckRun, error) {
	return s.ownedCheck(ctx, owner, id)
}

// DeleteCheck removes a run; its sheet stays.
func (s *Service) DeleteCheck(ctx context.Context, owner, id uuid.UUID) error {
	r, err := s.ownedCheck(ctx, owner, id)
	if err != nil {
		return err
	}
	return s.st.Checks.Delete(ctx, r.ID)
}

// RerunCheck evaluates a run again on the current values of its sheet (what
// the user corrected there) and the current rules; answers of the agent for
// unchanged inputs come from the cache.
func (s *Service) RerunCheck(ctx context.Context, user types.User, id uuid.UUID) (types.CheckRun, error) {
	r, err := s.ownedCheck(ctx, user.ID, id)
	if err != nil {
		return r, err
	}
	if r.Status != types.SheetDone && r.Status != types.SheetFailed {
		return r, ErrNotFinished
	}
	return s.runCheck(ctx, r)
}

// SetCheckLink records the contract the user chose for a document (anchor
// nil = none; auto = let the steps decide again) and evaluates the run again.
func (s *Service) SetCheckLink(ctx context.Context, user types.User, id, seg uuid.UUID, anchor *uuid.UUID, auto bool) (types.CheckRun, error) {
	r, err := s.ownedCheck(ctx, user.ID, id)
	if err != nil {
		return r, err
	}
	if err := s.st.Checks.SetLink(ctx, r.CaseID, seg, anchor, auto, user.ID); err != nil {
		return r, err
	}
	return s.runCheck(ctx, r)
}

// docsOf lists the documents of a sheet as formula documents, by type, with
// the fields the AI read with low confidence (segment → key).
func docsOf(v View, kb types.KBConfig) ([]checkexpr.Doc, map[string]CheckDoc, map[string]map[string]bool) {
	var docs []checkexpr.Doc
	info := map[string]CheckDoc{}
	low := map[string]map[string]bool{}
	for _, tv := range v.TablesView {
		for _, r := range tv.Rows {
			if r.SegmentID == nil {
				continue
			}
			d := checkexpr.Doc{ID: r.SegmentID.String(), Type: tv.Label, Fields: map[string]checkexpr.FieldValue{}}
			first := ""
			for _, f := range tv.Fields {
				cv := r.CellsView[f.Key]
				fv := checkexpr.FieldValue{Text: cv.Value}
				if cv.FieldID != nil {
					fv.FieldID = cv.FieldID.String()
				}
				d.Fields[f.Key] = fv
				if cv.Low && !cv.Edited {
					if low[d.ID] == nil {
						low[d.ID] = map[string]bool{}
					}
					low[d.ID][f.Key] = true
				}
				if first == "" && strings.TrimSpace(cv.Value) != "" {
					first = strings.TrimSpace(cv.Value)
				}
			}
			title := typeTitle(kb, tv.Label)
			d.Label = title
			if first != "" {
				d.Label += " " + first
			} else {
				d.Label += fmt.Sprintf(" (tr.%d–%d)", r.PageStart, r.PageEnd)
			}
			docs = append(docs, d)
			info[d.ID] = CheckDoc{ID: d.ID, DocumentID: r.DocumentID, Type: tv.Label, TypeTitle: title, Label: d.Label,
				FileName: r.FileName, PageStart: r.PageStart, PageEnd: r.PageEnd}
		}
	}
	return docs, info, low
}

// runCheck groups the documents of the run's sheet around the contracts and
// evaluates the rules of the KB on each group; it stores and returns the run.
func (s *Service) runCheck(ctx context.Context, r types.CheckRun) (types.CheckRun, error) {
	sh, err := s.st.Sheets.Get(ctx, r.SheetID)
	if err != nil {
		return r, err
	}
	c, err := s.cases.GetCase(ctx, r.CaseID)
	if err != nil {
		return r, err
	}
	kb, err := s.st.KBs.Get(ctx, c.KBID)
	if err != nil {
		return r, err
	}
	if r.RulesetID == nil {
		return r, ErrNoChecks
	}
	cfg, err := s.GetRuleset(ctx, r.CreatedBy, *r.RulesetID)
	if err != nil || cfg.Anchor == "" {
		return r, ErrNoChecks
	}
	_ = s.st.Checks.SetResult(ctx, r.ID, types.SheetRunning, nil, "")
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: &r.CreatedBy, CaseID: &r.CaseID, Kind: types.UsageSheet})
	v, err := s.view(ctx, sh, c)
	if err != nil {
		return r, err
	}
	docs, info, low := docsOf(v, kb.Config)
	overrides, err := s.st.Checks.Links(ctx, r.CaseID)
	if err != nil {
		return r, err
	}
	res := CheckResult{RulesetID: cfg.ID, RulesetName: cfg.Name, Anchor: cfg.Anchor, AnchorTitle: typeTitle(kb.Config, cfg.Anchor), Groups: []CheckGroup{}, Unlinked: []CheckMember{}, RanAt: time.Now()}
	var anchors []checkexpr.Doc
	for _, d := range docs {
		if d.Type == cfg.Anchor {
			anchors = append(anchors, d)
		}
	}
	steps := map[string][]types.CheckStep{}
	for _, l := range cfg.Links {
		steps[l.DocType] = l.Steps
	}
	byAnchor := map[string][]CheckMember{}
	for _, d := range docs {
		if d.Type == cfg.Anchor {
			continue
		}
		m := CheckMember{CheckDoc: info[d.ID]}
		anchor := ""
		if seg, err := uuid.Parse(d.ID); err == nil {
			if o, ok := overrides[seg]; ok {
				m.Source = "user"
				if o == nil {
					m.Reason = "Bạn chọn: không thuộc " + res.AnchorTitle + " nào"
				} else if _, ok := info[o.String()]; ok {
					anchor, m.Reason = o.String(), "Bạn chọn"
				} else {
					m.Reason = "Bạn đã chọn một " + res.AnchorTitle + " không còn trong hồ sơ"
				}
				if anchor == "" {
					res.Unlinked = append(res.Unlinked, m)
				} else {
					byAnchor[anchor] = append(byAnchor[anchor], m)
				}
				continue
			}
		}
		anchor, m = s.link(ctx, kb.Config, cfg, d, anchors, steps[d.Type], m)
		if anchor == "" {
			res.Unlinked = append(res.Unlinked, m)
		} else {
			byAnchor[anchor] = append(byAnchor[anchor], m)
		}
	}
	docByID := map[string]checkexpr.Doc{}
	for _, d := range docs {
		docByID[d.ID] = d
	}
	for _, a := range anchors {
		g := CheckGroup{Anchor: info[a.ID], Members: byAnchor[a.ID], Results: []CheckOutcome{}}
		if g.Members == nil {
			g.Members = []CheckMember{}
		}
		sc := ruleScope(kb.Config, cfg.Anchor)
		sc.Single[cfg.Anchor] = a
		for t := range sc.Types {
			if t != cfg.Anchor {
				sc.Groups[t] = []checkexpr.Doc{}
			}
		}
		for _, m := range g.Members {
			sc.Groups[m.Type] = append(sc.Groups[m.Type], docByID[m.ID])
		}
		for _, rule := range cfg.Rules {
			if rule.Off {
				continue
			}
			o := CheckOutcome{RuleID: rule.ID, Name: rule.Name, Text: rule.Text, AI: rule.AI}
			switch {
			case rule.AI:
				o.Result = s.aiJudge(ctx, rule.Text, sc, cfg.Anchor)
				if o.Status == checkexpr.Fail {
					o.Hint = "AI nhận định: " + o.Detail
				}
			case rule.A != nil && rule.B != nil:
				o.Result = checkexpr.Eval(rule.Formula, sc)
				comparison(&o, kb.Config, rule, cfg.Anchor, sc, info, low)
			default:
				o.Result = checkexpr.Eval(rule.Formula, sc)
			}
			switch o.Status {
			case checkexpr.Pass:
				res.Counts.Pass++
			case checkexpr.Fail:
				res.Counts.Fail++
			default:
				res.Counts.Unknown++
			}
			g.Results = append(g.Results, o)
		}
		res.Groups = append(res.Groups, g)
	}
	res.Counts.Groups = len(res.Groups)
	res.Counts.Unlinked = len(res.Unlinked)
	if err := s.st.Checks.SetResult(ctx, r.ID, types.SheetDone, res, ""); err != nil {
		return r, err
	}
	return s.st.Checks.Get(ctx, r.ID)
}

// link finds the contract of a document (§6.9.9): every step keeps the
// contracts its formula holds for (a step that cannot tell keeps them all);
// it stops at one contract. An AI step runs only while several remain.
func (s *Service) link(ctx context.Context, kb types.KBConfig, cfg types.CheckRuleset, d checkexpr.Doc, anchors []checkexpr.Doc, steps []types.CheckStep, m CheckMember) (string, CheckMember) {
	title := typeTitle(kb, cfg.Anchor)
	m.Source = "rule"
	if len(anchors) == 0 {
		m.Reason = "Hồ sơ chưa có " + title
		return "", m
	}
	if len(steps) == 0 {
		if len(anchors) == 1 {
			m.Source, m.Reason = "single", "Hồ sơ chỉ có một "+title
			return anchors[0].ID, m
		}
		m.Reason = "Chưa cài đặt cách nhận biết " + typeTitle(kb, d.Type) + " thuộc " + title + " nào"
		m.Candidates = ids(anchors)
		return "", m
	}
	cands := anchors
	var why []string
	for _, st := range steps {
		if st.AI {
			if len(cands) < 2 {
				continue
			}
			pick, reason := s.aiPick(ctx, kb, cfg.Anchor, st.Text, d, cands)
			switch {
			case pick == "none":
				m.Source, m.Reason = "ai", "AI: "+reason
				return "", m
			case pick != "":
				for _, a := range cands {
					if a.ID == pick {
						cands = []checkexpr.Doc{a}
					}
				}
				if len(cands) == 1 {
					m.Source = "ai"
					why = append(why, "AI ("+st.Text+"): "+reason)
				}
			default:
				why = append(why, "AI chưa chắc ("+st.Text+"): "+reason)
			}
		} else {
			var pass, unknown []checkexpr.Doc
			var notes []string
			for _, a := range cands {
				sc := linkScope(kb, d.Type, cfg.Anchor)
				sc.Single[d.Type], sc.Single[cfg.Anchor] = d, a
				res := checkexpr.Eval(st.Formula, sc)
				switch res.Status {
				case checkexpr.Pass:
					pass = append(pass, a)
					if res.Detail != "" {
						notes = append(notes, res.Detail)
					}
				case checkexpr.Fail:
					for _, f := range res.Failing {
						notes = append(notes, f.Note)
					}
				default:
					unknown = append(unknown, a)
					notes = append(notes, res.Missing...)
				}
			}
			switch {
			case len(pass) > 0:
				cands = pass
				why = append(why, st.Text+" ✓ "+firstNote(notes))
			case len(unknown) == len(cands):
				why = append(why, st.Text+": chưa đủ thông tin ("+firstNote(notes)+")")
			default:
				cands = unknown
				if len(cands) == 0 {
					m.Reason = st.Text + " ✗ " + firstNote(notes)
					return "", m
				}
			}
		}
		if len(cands) == 1 && len(why) > 0 && strings.Contains(why[len(why)-1], "✓") {
			break
		}
	}
	m.Reason = strings.Join(why, " · ")
	if len(cands) == 1 {
		if m.Reason == "" {
			m.Reason = "Chỉ còn một " + title + " phù hợp"
		}
		return cands[0].ID, m
	}
	m.Candidates = ids(cands)
	labels := make([]string, 0, len(cands))
	for _, a := range cands {
		labels = append(labels, a.Label)
	}
	m.Reason = "Khớp nhiều " + title + ": " + strings.Join(labels, ", ") + ". " + m.Reason
	return "", m
}

func firstNote(xs []string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func ids(ds []checkexpr.Doc) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}

// cached answers a model question once per input (U56).
func (s *Service) cached(ctx context.Context, kind, system, user string, out any) error {
	sum := sha256.Sum256([]byte(kind + "\n" + system + "\n" + user))
	key := "check:" + hex.EncodeToString(sum[:])
	if b, ok, err := s.st.ModelCache.Get(ctx, key); err == nil && ok {
		if json.Unmarshal(b, out) == nil {
			return nil
		}
	}
	if s.formLLM == nil {
		return errors.New("no model configured")
	}
	if err := s.formLLM.CompleteJSON(ctx, system, user, out); err != nil {
		return err
	}
	if b, err := json.Marshal(out); err == nil {
		_ = s.st.ModelCache.Put(ctx, key, "check", b)
	}
	return nil
}

func docText(kb types.KBConfig, d checkexpr.Doc) map[string]string {
	out := map[string]string{"_id": d.ID, "_loai": typeTitle(kb, d.Type)}
	for _, t := range kb.DocTypes {
		if t.Name != d.Type {
			continue
		}
		for _, f := range t.Fields {
			if v := strings.TrimSpace(d.Fields[f.Key].Text); v != "" {
				out[f.Label] = v
			}
		}
	}
	return out
}

const pickSystem = `Bạn xác định một chứng từ thuộc hợp đồng nào trong các ứng viên, theo tiêu chí người dùng đưa. Chỉ dựa vào thông tin được cho.
Trả lời một JSON: {"anchor": "<_id của ứng viên phù hợp>" | "none" (không thuộc ứng viên nào) | "" (không chắc), "reason": "<lý do ngắn bằng tiếng Việt>"}`

func (s *Service) aiPick(ctx context.Context, kb types.KBConfig, anchor, text string, d checkexpr.Doc, cands []checkexpr.Doc) (string, string) {
	var cs []map[string]string
	for _, a := range cands {
		cs = append(cs, docText(kb, a))
	}
	in, _ := json.Marshal(map[string]any{"tieu_chi": text, "chung_tu": docText(kb, d), "ung_vien": cs})
	var out struct {
		Anchor string `json:"anchor"`
		Reason string `json:"reason"`
	}
	if err := s.cached(ctx, "pick", pickSystem, string(in), &out); err != nil {
		return "", "không gọi được AI: " + err.Error()
	}
	return out.Anchor, out.Reason
}

const judgeSystem = `Bạn kiểm tra một bộ chứng từ theo một quy tắc người dùng viết. Chỉ dựa vào thông tin được cho; thiếu thông tin để kết luận thì trả "unknown".
Trả lời một JSON: {"status": "pass" | "fail" | "unknown", "detail": "<giải thích ngắn bằng tiếng Việt, nêu giá trị đã so>", "missing": "<thông tin còn thiếu, nếu unknown>"}`

func (s *Service) aiJudge(ctx context.Context, text string, sc checkexpr.Scope, anchor string) checkexpr.Result {
	group := map[string]any{}
	kbTypes := sc.Types
	for name, d := range sc.Single {
		group[kbTypes[name].Title] = fieldsOf(kbTypes[d.Type], d)
	}
	names := make([]string, 0, len(sc.Groups))
	for t := range sc.Groups {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		var list []map[string]string
		for _, d := range sc.Groups[t] {
			list = append(list, fieldsOf(kbTypes[t], d))
		}
		group[kbTypes[t].Title] = list
	}
	in, _ := json.Marshal(map[string]any{"quy_tac": text, "bo_chung_tu": group})
	var out struct {
		Status  string `json:"status"`
		Detail  string `json:"detail"`
		Missing string `json:"missing"`
	}
	if err := s.cached(ctx, "judge", judgeSystem, string(in), &out); err != nil {
		return checkexpr.Result{Status: checkexpr.Error, Detail: "không gọi được AI: " + err.Error()}
	}
	r := checkexpr.Result{Status: out.Status, Detail: out.Detail}
	switch out.Status {
	case checkexpr.Fail:
		r.Failing = []checkexpr.Item{{Note: out.Detail}}
	case checkexpr.Pass:
	default:
		r.Status = checkexpr.Unknown
		if out.Missing != "" {
			r.Missing = []string{out.Missing}
		} else if out.Detail != "" {
			r.Missing = []string{out.Detail}
		}
	}
	return r
}

func fieldsOf(ti checkexpr.TypeInfo, d checkexpr.Doc) map[string]string {
	out := map[string]string{"_giay_to": d.Label}
	for k, f := range ti.Fields {
		if v := strings.TrimSpace(d.Fields[k].Text); v != "" {
			out[f.Label] = v
		}
	}
	return out
}

// comparison fills the rows of a rule chosen field by field (U108): one row
// per document of side A (the contract when A is the anchor type) with the
// value it was compared with and its own outcome, the sums of a sum_le rule,
// and a hint when the rule fails.
func comparison(o *CheckOutcome, kb types.KBConfig, rule types.CheckRule, anchor string, sc checkexpr.Scope, info map[string]CheckDoc, low map[string]map[string]bool) {
	fa, _ := fieldOf(kb, *rule.A)
	fb, _ := fieldOf(kb, *rule.B)
	kind := fieldKind(fa.ValueType)
	o.Op, o.OpText = rule.Op, checkOps[kind][rule.Op]
	o.A = &CheckSide{Type: rule.A.Type, TypeTitle: typeTitle(kb, rule.A.Type), Key: rule.A.Key, Label: fa.Label}
	o.B = &CheckSide{Type: rule.B.Type, TypeTitle: typeTitle(kb, rule.B.Type), Key: rule.B.Key, Label: fb.Label}
	docs := func(t string) []checkexpr.Doc {
		if t == anchor {
			return []checkexpr.Doc{sc.Single[anchor]}
		}
		return sc.Groups[t]
	}
	var bVals []string
	for _, d := range docs(rule.B.Type) {
		if v := strings.TrimSpace(d.Fields[rule.B.Key].Text); v != "" {
			bVals = append(bVals, v)
		}
	}
	row := func(d checkexpr.Doc, status string) CheckRow {
		ci := info[d.ID]
		return CheckRow{Segment: d.ID, DocumentID: ci.DocumentID, Label: d.Label, Page: ci.PageStart,
			ValueA: strings.TrimSpace(d.Fields[rule.A.Key].Text), Status: status, Unsure: low[d.ID][rule.A.Key]}
	}
	aDocs := docs(rule.A.Type)
	if rule.Op == "sum_le" {
		for _, d := range aDocs {
			o.Rows = append(o.Rows, row(d, ""))
		}
		t := &CheckTotal{A: sumOf(aDocs, rule.A.Key), B: strings.Join(bVals, ", ")}
		if rule.B.Type != anchor {
			t.B = sumOf(docs(rule.B.Type), rule.B.Key)
			t.Note = fmt.Sprintf("Tổng %d %s", len(docs(rule.B.Type)), strings.ToLower(o.B.TypeTitle))
		}
		o.Total = t
	} else {
		for _, d := range aDocs {
			status := o.Status
			if rule.A.Type != anchor {
				sub := sc
				sub.Groups = maps.Clone(sc.Groups)
				sub.Groups[rule.A.Type] = []checkexpr.Doc{d}
				status = checkexpr.Eval(rule.Formula, sub).Status
			}
			r := row(d, status)
			r.ValueB = strings.Join(bVals, ", ")
			o.Rows = append(o.Rows, r)
		}
	}
	if o.Status != checkexpr.Fail {
		return
	}
	what := map[string]string{"text": "Giá trị", "money": "Số tiền", "date": "Ngày"}[kind]
	o.Hint = fmt.Sprintf("%s ở giấy tờ tô đỏ không thoả “%s %s”. Mở trang gốc để xem: nếu AI đọc sai thì sửa ở bảng số liệu rồi đối chiếu lại; nếu đúng là sai lệch thì cần làm rõ với khách hàng.",
		what, o.OpText, fb.Label+" của "+o.B.TypeTitle)
}

// sumOf adds up a money field of documents, written like 8.626.500.000.
func sumOf(docs []checkexpr.Doc, key string) string {
	total := 0.0
	for _, d := range docs {
		if n, ok := textutil.ParseNumber(d.Fields[key].Text); ok {
			total += n
		}
	}
	s := fmt.Sprintf("%.0f", total)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 && r != '-' && s[i-1] != '-' {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}
