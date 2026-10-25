// Package doctypes is the catalogue of document types of each owner (§6.9.10,
// U97): every knowledge base of the owner picks the types it uses from it.
// Each type has versioned AI instructions (U98), sample documents read once
// by the OCR engine (U94), test runs of a version on the samples, scores
// against the values a user reviewed, and AI proposals that improve a version.
package doctypes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound   = errors.New("document type not found")
	ErrBad        = errors.New("invalid request")
	ErrCurrent    = errors.New("the version in use cannot be deleted: use another version first")
	ErrNoReviewed = errors.New("review at least one sample first")
	ErrNotReady   = errors.New("sample processing is not configured on this server")
)

// InUseError is returned when a type still used by knowledge bases is deleted.
type InUseError struct{ KBs []types.KnowledgeBase }

func (e *InUseError) Error() string {
	names := make([]string, len(e.KBs))
	for i, k := range e.KBs {
		names[i] = k.Name
	}
	return "the document type is used by " + strings.Join(names, ", ")
}

// Deps are the service's collaborators. Renderer and Engines read samples
// and are set on servers that run workers.
type Deps struct {
	Store    *postgres.Store
	Objects  storage.ObjectStore
	Queue    queue.Enqueuer
	Renderer Renderer
	Engines  *parser.Registry
	LLM      interfaces.Completer
	Config   *config.Config
	Log      *slog.Logger
}

// Service implements the catalogue use cases.
type Service struct {
	st       *postgres.Store
	objects  storage.ObjectStore
	q        queue.Enqueuer
	renderer Renderer
	engines  *parser.Registry
	llm      interfaces.Completer
	cfg      *config.Config
	log      *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: d.Store, objects: d.Objects, q: d.Queue, renderer: d.Renderer, engines: d.Engines, llm: d.LLM, cfg: d.Config, log: log}
}

// Handlers returns the task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{types.TaskDocTypeSample: s.handleSample, types.TaskDocTypeJob: s.handleJob}
}

func notFound(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// ListFilter narrows the catalogue: the types a KB uses, the types no KB
// uses, a search in title and code.
type ListFilter struct {
	KB     *uuid.UUID
	Unused bool
	Q      string
}

// List returns the owner's catalogue with usage, samples and accuracy.
func (s *Service) List(ctx context.Context, owner uuid.UUID, f ListFilter) ([]types.DocTypeView, error) {
	all, err := s.st.DocTypes.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	usage, err := s.st.DocTypes.Usage(ctx, owner)
	if err != nil {
		return nil, err
	}
	rows, err := s.st.DocTypes.ScoreRows(ctx, owner)
	if err != nil {
		return nil, err
	}
	q := textutil.ValueKey(f.Q)
	out := []types.DocTypeView{}
	for _, t := range all {
		kbs := usage[t.Name]
		switch {
		case f.Unused && len(kbs) > 0,
			f.KB != nil && !slices.Contains(kbs, *f.KB),
			q != "" && !strings.Contains(textutil.ValueKey(t.Title+" "+t.Name), q):
			continue
		}
		out = append(out, view(t, kbs, rows))
	}
	return out, nil
}

func view(t types.CatalogDocType, kbs []uuid.UUID, rows []postgres.ScoreRow) types.DocTypeView {
	v := types.DocTypeView{CatalogDocType: t, KBIDs: kbs}
	if v.KBIDs == nil {
		v.KBIDs = []uuid.UUID{}
	}
	if v.Fields == nil {
		v.Fields = []types.SheetField{}
	}
	var cells []cell
	for _, r := range rows {
		if r.Code != t.Name {
			continue
		}
		v.Samples++
		if r.Truth != nil {
			v.Reviewed++
			if r.Output != nil {
				cells = append(cells, cellsOf(t.Fields, r.Truth, r.Output, "")...)
			}
		}
	}
	v.Accuracy = accuracy(cells)
	return v
}

// Get returns one type of the catalogue.
func (s *Service) Get(ctx context.Context, owner uuid.UUID, code string) (types.DocTypeView, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return types.DocTypeView{}, notFound(err)
	}
	usage, err := s.st.DocTypes.KBsUsing(ctx, owner, code)
	if err != nil {
		return types.DocTypeView{}, err
	}
	rows, err := s.st.DocTypes.ScoreRows(ctx, owner)
	if err != nil {
		return types.DocTypeView{}, err
	}
	return view(t, usage, rows), nil
}

// CreateInput is a new type: its name, description and the KBs using it.
type CreateInput struct {
	Title       string      `json:"title" example:"Uỷ nhiệm chi"`
	Description string      `json:"description,omitempty"`
	KBIDs       []uuid.UUID `json:"kb_ids,omitempty"`
}

// Create adds a type to the catalogue; its code is made from its name (U65),
// unique in the catalogue, and version 1 of its instructions is the default.
func (s *Service) Create(ctx context.Context, owner uuid.UUID, in CreateInput) (types.DocTypeView, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return types.DocTypeView{}, fmt.Errorf("%w: title is required", ErrBad)
	}
	code, err := s.freeCode(ctx, owner, title)
	if err != nil {
		return types.DocTypeView{}, err
	}
	t := types.DocType{ClassLabel: types.ClassLabel{Name: code, Title: title, Description: strings.TrimSpace(in.Description)}, Fields: []types.SheetField{}}
	if err := s.st.DocTypes.Save(ctx, owner, t, true); err != nil {
		return types.DocTypeView{}, err
	}
	if len(in.KBIDs) > 0 {
		if err := s.SetKBs(ctx, owner, code, in.KBIDs); err != nil {
			return types.DocTypeView{}, err
		}
	}
	return s.Get(ctx, owner, code)
}

// freeCode is the code made from a name, with _2, _3… when it is taken.
func (s *Service) freeCode(ctx context.Context, owner uuid.UUID, title string) (string, error) {
	all, err := s.st.DocTypes.List(ctx, owner)
	if err != nil {
		return "", err
	}
	used := map[string]bool{types.LabelOther: true, "unknown": true}
	for _, t := range all {
		used[t.Name] = true
	}
	return keyOf(title, "loai", used), nil
}

// keyOf makes a code from a name (U65): accent-folded, lower-case, runs of
// other characters as one "_", at most 40 characters, made unique against
// used (and added to it).
func keyOf(name, empty string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(textutil.Unaccent(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
	}
	k := strings.Trim(b.String(), "_")
	if len(k) > 40 {
		k = strings.Trim(k[:40], "_")
	}
	if k == "" {
		k = empty
	}
	if k[0] >= '0' && k[0] <= '9' {
		k = empty + "_" + k
	}
	base := k
	for n := 2; used[k]; n++ {
		k = fmt.Sprintf("%s_%d", base, n)
	}
	used[k] = true
	return k
}

// Patch changes the name, description or fields of a type. A field keeps
// its key; a new field without one gets a key made from its name.
type Patch struct {
	Title       *string             `json:"title,omitempty"`
	Description *string             `json:"description,omitempty"`
	Fields      *[]types.SheetField `json:"fields,omitempty"`
}

// Update applies a patch; new fields become the fields of the version in use.
func (s *Service) Update(ctx context.Context, owner uuid.UUID, code string, p Patch) (types.DocTypeView, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return types.DocTypeView{}, notFound(err)
	}
	dt := t.DocType
	if p.Title != nil {
		if strings.TrimSpace(*p.Title) == "" {
			return types.DocTypeView{}, fmt.Errorf("%w: title is required", ErrBad)
		}
		dt.Title = strings.TrimSpace(*p.Title)
	}
	if p.Description != nil {
		dt.Description = strings.TrimSpace(*p.Description)
	}
	if p.Fields != nil {
		fields, err := cleanFields(*p.Fields)
		if err != nil {
			return types.DocTypeView{}, err
		}
		dt.Fields = fields
	}
	if err := s.st.DocTypes.Save(ctx, owner, dt, p.Fields != nil); err != nil {
		return types.DocTypeView{}, err
	}
	if p.Description != nil && dt.Description == "" {
		// Save keeps a description when given an empty one; clearing it is asked here.
		if err := s.st.DocTypes.ClearDescription(ctx, owner, code); err != nil {
			return types.DocTypeView{}, err
		}
	}
	return s.Get(ctx, owner, code)
}

var valueTypes = map[string]bool{"string": true, "number": true, "money": true, "date": true, "bool": true}

// cleanFields drops fields without a name, keeps the keys given and makes
// one for the others.
func cleanFields(in []types.SheetField) ([]types.SheetField, error) {
	used := map[string]bool{}
	for _, f := range in {
		if f.Key != "" {
			if used[f.Key] {
				return nil, fmt.Errorf("%w: two fields have the key %q", ErrBad, f.Key)
			}
			used[f.Key] = true
		}
	}
	out := []types.SheetField{}
	for _, f := range in {
		f.Label = strings.TrimSpace(f.Label)
		if f.Label == "" {
			continue
		}
		if f.Key == "" {
			f.Key = keyOf(f.Label, "truong", used)
		}
		if !valueTypes[f.ValueType] {
			f.ValueType = "string"
		}
		f.Description = strings.TrimSpace(f.Description)
		out = append(out, f)
	}
	return out, nil
}

// Delete removes a type with its instructions and samples. A type used by
// knowledge bases is removed from them first when detach is set, else the
// call fails with an InUseError.
func (s *Service) Delete(ctx context.Context, owner uuid.UUID, code string, detach bool) error {
	if _, err := s.st.DocTypes.Get(ctx, owner, code); err != nil {
		return notFound(err)
	}
	ids, err := s.st.DocTypes.KBsUsing(ctx, owner, code)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if !detach {
			e := &InUseError{}
			for _, id := range ids {
				if kb, err := s.st.KBs.Get(ctx, id); err == nil {
					e.KBs = append(e.KBs, kb)
				}
			}
			return e
		}
		if err := s.st.DocTypes.SetKBs(ctx, owner, code, nil); err != nil {
			return err
		}
	}
	if _, err := s.objects.DeletePrefix(ctx, samplePrefix(owner, code)); err != nil {
		s.log.Warn("doctypes: delete sample files", "code", code, "err", err)
	}
	return notFound(s.st.DocTypes.Delete(ctx, owner, code))
}

// Copy makes a new type with the name "<name> (bản sao)", the fields and the
// version in use of the instructions (as its version 1); samples are not
// copied, so examples are dropped.
func (s *Service) Copy(ctx context.Context, owner uuid.UUID, code string, by uuid.UUID) (types.DocTypeView, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return types.DocTypeView{}, notFound(err)
	}
	p, err := s.st.DocTypes.Prompt(ctx, owner, code, 0)
	if err != nil {
		return types.DocTypeView{}, notFound(err)
	}
	title := t.Title + " (bản sao)"
	nc, err := s.freeCode(ctx, owner, title)
	if err != nil {
		return types.DocTypeView{}, err
	}
	dt := t.DocType
	dt.Name, dt.Title = nc, title
	if err := s.st.DocTypes.Save(ctx, owner, dt, true); err != nil {
		return types.DocTypeView{}, err
	}
	p.Code, p.Shots, p.Note, p.CreatedBy = nc, []types.PromptShot{}, "Nhân bản từ "+t.Title+" v"+fmt.Sprint(p.Version), &by
	if err := s.st.DocTypes.SetFirstPrompt(ctx, owner, p); err != nil {
		return types.DocTypeView{}, err
	}
	return s.Get(ctx, owner, nc)
}

// SetKBs makes kbs the owner's knowledge bases that use a type.
func (s *Service) SetKBs(ctx context.Context, owner uuid.UUID, code string, kbs []uuid.UUID) error {
	if _, err := s.st.DocTypes.Get(ctx, owner, code); err != nil {
		return notFound(err)
	}
	for _, id := range kbs {
		if _, err := s.st.KBs.GetOwned(ctx, id, owner); err != nil {
			return fmt.Errorf("%w: knowledge base %s not found", ErrBad, id)
		}
	}
	return s.st.DocTypes.SetKBs(ctx, owner, code, kbs)
}

// ---- instructions

// PromptView is a version of the instructions with its accuracy on the
// reviewed samples (nil when none was run with it).
type PromptView struct {
	types.DocTypePrompt
	Current  bool     `json:"current"`
	Accuracy *float64 `json:"accuracy"`
}

// Prompts returns the versions of a type, newest first, with their accuracy.
func (s *Service) Prompts(ctx context.Context, owner uuid.UUID, code string) ([]PromptView, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return nil, notFound(err)
	}
	ps, err := s.st.DocTypes.Prompts(ctx, owner, code)
	if err != nil {
		return nil, err
	}
	samples, runs, err := s.scoring(ctx, owner, code)
	if err != nil {
		return nil, err
	}
	out := make([]PromptView, len(ps))
	for i, p := range ps {
		out[i] = PromptView{DocTypePrompt: p, Current: p.Version == t.PromptVersion,
			Accuracy: accuracy(versionCells(p, samples, runs, ""))}
	}
	return out, nil
}

// scoring returns the samples of a type and their runs by sample and version.
func (s *Service) scoring(ctx context.Context, owner uuid.UUID, code string) ([]types.DocTypeSample, map[uuid.UUID]map[int]types.SampleRun, error) {
	samples, err := s.st.DocTypes.Samples(ctx, owner, code)
	if err != nil {
		return nil, nil, err
	}
	all, err := s.st.DocTypes.Runs(ctx, owner, code)
	if err != nil {
		return nil, nil, err
	}
	runs := map[uuid.UUID]map[int]types.SampleRun{}
	for _, r := range all {
		if runs[r.SampleID] == nil {
			runs[r.SampleID] = map[int]types.SampleRun{}
		}
		runs[r.SampleID][r.Version] = r
	}
	return samples, runs, nil
}

// PromptInput is a new version of the instructions.
type PromptInput struct {
	Instruction string              `json:"instruction"`
	Rules       []string            `json:"rules"`
	Shots       []types.PromptShot  `json:"shots"`
	Fields      *[]types.SheetField `json:"fields,omitempty"`
	Note        string              `json:"note,omitempty"`
	// Use makes the new version the one in use.
	Use bool `json:"use,omitempty"`
}

// AddPrompt saves a new version (the largest number plus one). Fields left
// out are those of the version in use; examples must be reviewed samples.
func (s *Service) AddPrompt(ctx context.Context, owner uuid.UUID, code string, in PromptInput, by uuid.UUID) (types.DocTypePrompt, error) {
	cur, err := s.st.DocTypes.Prompt(ctx, owner, code, 0)
	if err != nil {
		return types.DocTypePrompt{}, notFound(err)
	}
	p := types.DocTypePrompt{Code: code, Instruction: strings.TrimSpace(in.Instruction), Fields: cur.Fields, Note: strings.TrimSpace(in.Note), CreatedBy: &by,
		Rules: []string{}, Shots: []types.PromptShot{}}
	if in.Fields != nil {
		if p.Fields, err = cleanFields(*in.Fields); err != nil {
			return types.DocTypePrompt{}, err
		}
	}
	for _, r := range in.Rules {
		if r = strings.TrimSpace(r); r != "" && !slices.Contains(p.Rules, r) {
			p.Rules = append(p.Rules, r)
		}
	}
	for _, sh := range in.Shots {
		smp, err := s.st.DocTypes.Sample(ctx, owner, sh.SampleID)
		if err != nil || smp.Code != code || smp.Truth == nil {
			return types.DocTypePrompt{}, fmt.Errorf("%w: example %s is not a reviewed sample of this type", ErrBad, sh.SampleID)
		}
		p.Shots = append(p.Shots, sh)
	}
	if p.Note == "" {
		p.Note = "Sửa tay"
	}
	p, err = s.st.DocTypes.AddPrompt(ctx, owner, p)
	if err != nil {
		return p, err
	}
	if in.Use {
		err = s.st.DocTypes.SetCurrent(ctx, owner, code, p.Version)
	}
	return p, err
}

// SetCurrent makes a version the one in use, from the next extraction on.
func (s *Service) SetCurrent(ctx context.Context, owner uuid.UUID, code string, version int) error {
	if _, err := s.st.DocTypes.Prompt(ctx, owner, code, version); err != nil {
		return notFound(err)
	}
	return s.st.DocTypes.SetCurrent(ctx, owner, code, version)
}

// DeletePrompt removes a version that is not in use.
func (s *Service) DeletePrompt(ctx context.Context, owner uuid.UUID, code string, version int) error {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return notFound(err)
	}
	if t.PromptVersion == version {
		return ErrCurrent
	}
	return notFound(s.st.DocTypes.DeletePrompt(ctx, owner, code, version))
}

// PromptText is the whole text a version sends the model, with the place of
// the document to read marked.
func (s *Service) PromptText(ctx context.Context, owner uuid.UUID, code string, version int) (string, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return "", notFound(err)
	}
	p, err := s.st.DocTypes.Prompt(ctx, owner, code, version)
	if err != nil {
		return "", notFound(err)
	}
	return promptText(t, p, docmodel.GuideOf(ctx, s.st, owner, p, uuid.Nil), s.minConf()), nil
}

func (s *Service) minConf() float64 {
	if s.cfg == nil {
		return 0
	}
	return s.cfg.Parser.LowConfThreshold
}

// promptText is the text of a version around a placeholder document.
func promptText(t types.CatalogDocType, p types.DocTypePrompt, g *types.Guide, minConf float64) string {
	return docmodel.ExtractPrompt(tableOf(t, p), nil, map[int]string{1: "{{nội dung của tài liệu}}"}, nil, minConf, g)
}

func tableOf(t types.CatalogDocType, p types.DocTypePrompt) types.SheetTable {
	return types.SheetTable{Label: t.Name, Title: t.Title, Description: t.Description, Fields: p.Fields}
}

// ---- scores

// cell is one (sample, field) compared with its reviewed value.
type cell struct {
	sample uuid.UUID
	key    string
	ok     bool
}

// cellsOf compares the output of a run with the reviewed values, for every
// field (or only the field only).
func cellsOf(fields []types.SheetField, truth map[string]string, out map[string]types.RunValue, only string) []cell {
	var cells []cell
	for _, f := range fields {
		if only != "" && f.Key != only {
			continue
		}
		cells = append(cells, cell{key: f.Key, ok: textutil.ValueKey(out[f.Key].Value) == textutil.ValueKey(truth[f.Key])})
	}
	return cells
}

// versionCells are the cells of a version on the reviewed samples it ran on.
func versionCells(p types.DocTypePrompt, samples []types.DocTypeSample, runs map[uuid.UUID]map[int]types.SampleRun, only string) []cell {
	var cells []cell
	for _, smp := range samples {
		r, ok := runs[smp.ID][p.Version]
		if smp.Truth == nil || !ok {
			continue
		}
		for _, c := range cellsOf(p.Fields, smp.Truth, r.Output, only) {
			c.sample = smp.ID
			cells = append(cells, c)
		}
	}
	return cells
}

// accuracy is the share of right cells; nil without cells.
func accuracy(cells []cell) *float64 {
	if len(cells) == 0 {
		return nil
	}
	ok := 0
	for _, c := range cells {
		if c.ok {
			ok++
		}
	}
	a := float64(ok) / float64(len(cells))
	return &a
}
