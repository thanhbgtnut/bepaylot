package sheets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/checkexpr"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Errors of the cross-checks (§6.9.9).
var (
	ErrCheckNotFound = errors.New("check run not found")
	ErrNoChecks      = errors.New("thư viện chưa có quy tắc đối chiếu: cài đặt ở Cài đặt → Quy tắc đối chiếu")
	ErrBadChecks     = errors.New("quy tắc đối chiếu không hợp lệ")
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

// checkChecks validates a config against the KB and gives every step and
// rule an id and a name.
func checkChecks(kb types.KBConfig, cfg types.CheckConfig) (types.CheckConfig, error) {
	if !hasType(kb, cfg.Anchor) {
		return cfg, fmt.Errorf("chọn loại giấy tờ gốc (ví dụ Hợp đồng)")
	}
	out := types.CheckConfig{Anchor: cfg.Anchor, Links: []types.CheckLink{}, Rules: []types.CheckRule{}}
	for _, l := range cfg.Links {
		if l.DocType == cfg.Anchor || !hasType(kb, l.DocType) {
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
					return cfg, fmt.Errorf("%s, bước “%s”: chưa có công thức (bấm Dịch hoặc chọn AI đánh giá)", typeTitle(kb, l.DocType), st.Text)
				}
				if _, err := checkexpr.Check(st.Formula, linkScope(kb, l.DocType, cfg.Anchor)); err != nil {
					return cfg, fmt.Errorf("%s, bước “%s”: %v", typeTitle(kb, l.DocType), st.Text, err)
				}
			}
			cl.Steps = append(cl.Steps, st)
		}
		if len(cl.Steps) > 0 {
			out.Links = append(out.Links, cl)
		}
	}
	for _, r := range cfg.Rules {
		r.Text = strings.TrimSpace(r.Text)
		if r.Text == "" && r.Formula == "" {
			continue
		}
		if r.ID == "" {
			r.ID = uuid.NewString()[:8]
		}
		if strings.TrimSpace(r.Name) == "" {
			r.Name = r.Text
		}
		if !r.AI {
			if strings.TrimSpace(r.Formula) == "" {
				return cfg, fmt.Errorf("quy tắc “%s”: chưa có công thức (bấm Dịch hoặc chọn AI đánh giá)", r.Name)
			}
			if _, err := checkexpr.Check(r.Formula, ruleScope(kb, cfg.Anchor)); err != nil {
				return cfg, fmt.Errorf("quy tắc “%s”: %v", r.Name, err)
			}
		}
		out.Rules = append(out.Rules, r)
	}
	return out, nil
}

// SaveChecks stores the cross-checks of a KB the user owns.
func (s *Service) SaveChecks(ctx context.Context, user types.User, kbID uuid.UUID, cfg types.CheckConfig) (types.KnowledgeBase, error) {
	kb, err := s.formKB(ctx, user.ID, kbID)
	if err != nil {
		return kb, err
	}
	clean, err := checkChecks(kb.Config, cfg)
	if err != nil {
		return kb, fmt.Errorf("%w: %v", ErrBadChecks, err)
	}
	c := kb.Config
	c.Checks = &clean
	return s.st.KBs.Update(ctx, kb.ID, user.ID, postgres.KBPatch{Config: &c})
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
// checks it against the KB, and asks once more with the error when it does
// not pass; a sentence that still has no valid formula is marked AI.
func (s *Service) Compile(ctx context.Context, user types.User, kbID uuid.UUID, kind, docType, anchor, text string) (CompileResult, error) {
	kb, err := s.formKB(ctx, user.ID, kbID)
	if err != nil {
		return CompileResult{}, err
	}
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

// CheckOutcome is the outcome of one rule on one group.
type CheckOutcome struct {
	RuleID string `json:"rule_id"`
	Name   string `json:"name"`
	Text   string `json:"text"`
	AI     bool   `json:"ai,omitempty"`
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

// CreateCheck starts a cross-check of a case: a sheet of every document type
// with fields (extracting only what the case lacks, U80), then the groups
// and rules.
func (s *Service) CreateCheck(ctx context.Context, user types.User, caseID uuid.UUID) (types.CheckRun, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return types.CheckRun{}, err
	}
	kb, err := s.st.KBs.Get(ctx, c.KBID)
	if err != nil {
		return types.CheckRun{}, err
	}
	if kb.Config.Checks == nil || kb.Config.Checks.Anchor == "" || len(kb.Config.Checks.Rules) == 0 {
		return types.CheckRun{}, ErrNoChecks
	}
	sh, err := s.create(ctx, user, c, nil, "Đối chiếu chứng từ", nil)
	if err != nil {
		return types.CheckRun{}, err
	}
	return s.st.Checks.Create(ctx, types.CheckRun{CaseID: c.ID, SheetID: sh.ID, CreatedBy: user.ID})
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

// docsOf lists the documents of a sheet as formula documents, by type.
func docsOf(v View, kb types.KBConfig) ([]checkexpr.Doc, map[string]CheckDoc) {
	var docs []checkexpr.Doc
	info := map[string]CheckDoc{}
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
	return docs, info
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
	if kb.Config.Checks == nil || kb.Config.Checks.Anchor == "" {
		return r, ErrNoChecks
	}
	cfg := *kb.Config.Checks
	_ = s.st.Checks.SetResult(ctx, r.ID, types.SheetRunning, nil, "")
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: &r.CreatedBy, CaseID: &r.CaseID, Kind: types.UsageSheet})
	v, err := s.view(ctx, sh, c)
	if err != nil {
		return r, err
	}
	docs, info := docsOf(v, kb.Config)
	overrides, err := s.st.Checks.Links(ctx, r.CaseID)
	if err != nil {
		return r, err
	}
	res := CheckResult{Anchor: cfg.Anchor, AnchorTitle: typeTitle(kb.Config, cfg.Anchor), Groups: []CheckGroup{}, Unlinked: []CheckMember{}, RanAt: time.Now()}
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
			if rule.AI {
				o.Result = s.aiJudge(ctx, rule.Text, sc, cfg.Anchor)
			} else {
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
func (s *Service) link(ctx context.Context, kb types.KBConfig, cfg types.CheckConfig, d checkexpr.Doc, anchors []checkexpr.Doc, steps []types.CheckStep, m CheckMember) (string, CheckMember) {
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
