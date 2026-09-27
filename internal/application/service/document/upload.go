package document

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Upload is one (possibly multi-file) upload request into one case. Files
// are streamed to S3 as they arrive; the case, metadata and callback fields
// may arrive before or after the files. Finish resolves the case, creates
// the document rows (dedup per case), validates metadata and enqueues the
// pipeline.
type Upload struct {
	s           *Service
	owner       uuid.UUID
	kb          types.KnowledgeBase
	interactive bool
	caseRef     interfaces.CaseRef

	shared  map[string]any
	perFile map[string]map[string]any
	// callbackURL is the optional completion callback for every file.
	callbackURL string
	callbackErr error
	files       []pendingFile
	rejected    []Rejected
	index       int
}

// pendingFile is a file already in S3, waiting for Finish.
type pendingFile struct {
	docID       uuid.UUID
	index       int
	name, mime  string
	size        int64
	sha, key    string
	pdfaPart    int
	pdfaConform string
}

// Rejected describes a file that was not accepted.
type Rejected struct {
	FileName string   `json:"file_name"`
	Index    int      `json:"index"`
	Errors   []string `json:"errors"`
}

// Accepted describes an accepted file.
type Accepted struct {
	DocumentID uuid.UUID      `json:"document_id"`
	FileName   string         `json:"file_name"`
	Status     string         `json:"status"`
	Duplicate  bool           `json:"duplicate,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	// CallbackURL echoes the completion callback, when one was given.
	CallbackURL string `json:"callback_url,omitempty"`
}

// UploadCase is the case the files went into.
type UploadCase struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	CaseType string    `json:"case_type"`
	Created  bool      `json:"created"`
}

// UploadResult is returned by Finish.
type UploadResult struct {
	BatchID   uuid.UUID  `json:"batch_id"`
	Case      UploadCase `json:"case"`
	Documents []Accepted `json:"documents"`
	Rejected  []Rejected `json:"rejected"`
}

// BeginUpload starts an upload into a KB the owner owns. The case is set with
// SetCase before Finish.
func (s *Service) BeginUpload(ctx context.Context, owner, kbID uuid.UUID, interactive bool) (*Upload, error) {
	kb, err := s.st.KBs.GetOwned(ctx, kbID, owner)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Upload{s: s, owner: owner, kb: kb, interactive: interactive, perFile: map[string]map[string]any{}}, nil
}

// SetCase names the case of every file: an id, or a code (created when
// missing, with the given case type).
func (u *Upload) SetCase(ref interfaces.CaseRef) {
	if ref.ID != uuid.Nil {
		u.caseRef.ID = ref.ID
	}
	if ref.Code != "" {
		u.caseRef.Code = ref.Code
	}
	if ref.CaseType != "" {
		u.caseRef.CaseType = ref.CaseType
	}
	u.caseRef.Create = u.caseRef.Create || ref.Create
}

// SetSharedMetadata sets metadata applied to every file of the batch.
func (u *Upload) SetSharedMetadata(m map[string]any) { u.shared = m }

// SetCallbackURL sets the optional completion callback URL of every file of
// the upload. An invalid URL fails Finish (after removing the files), since
// the form field may arrive after the files were streamed.
func (u *Upload) SetCallbackURL(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	u.callbackURL, u.callbackErr = raw, u.s.ValidateCallbackURL(raw)
}

// SetFilesMetadata sets per-file metadata keyed by file name or 0-based index.
func (u *Upload) SetFilesMetadata(m map[string]map[string]any) {
	for k, v := range m {
		u.perFile[k] = v
	}
}

// AddFile streams one file to S3. Per-file problems (type, size) are
// recorded as rejections, not returned.
func (u *Upload) AddFile(ctx context.Context, name, declaredType string, body io.Reader) error {
	idx := u.index
	u.index++
	if u.index > u.s.cfg.Upload.MaxFiles {
		u.reject(name, idx, fmt.Sprintf("too many files (max %d)", u.s.cfg.Upload.MaxFiles))
		_, _ = io.Copy(io.Discard, body)
		return nil
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		name = fmt.Sprintf("file-%d", idx+1)
	}

	br := bufio.NewReaderSize(body, 4096)
	head, _ := br.Peek(512)
	mime := sniff(head, declaredType)
	if !u.s.allowed(mime) {
		u.reject(name, idx, fmt.Sprintf("unsupported file type %q", mime))
		_, _ = io.Copy(io.Discard, br)
		return nil
	}

	docID := uuid.New()
	key := u.s.keys.Source(u.kb.ID, docID, extFor(mime, name))
	hash := sha256.New()
	var det pdf.PDFADetector
	limited := &limitReader{r: io.TeeReader(br, io.MultiWriter(hash, &det)), max: u.s.cfg.Upload.MaxBytes}
	info, err := u.s.objects.Put(ctx, key, limited, -1, mime)
	if limited.exceeded {
		_ = u.s.objects.Delete(context.WithoutCancel(ctx), key)
		u.reject(name, idx, fmt.Sprintf("file exceeds %d bytes", u.s.cfg.Upload.MaxBytes))
		_, _ = io.Copy(io.Discard, br)
		return nil
	}
	if err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	f := pendingFile{docID: docID, index: idx, name: name, mime: mime, size: limited.n, sha: hex.EncodeToString(hash.Sum(nil)), key: info.Key}
	if det.IsPDFA() {
		f.pdfaPart, f.pdfaConform = det.Part, det.Conform
	}
	u.files = append(u.files, f)
	return nil
}

// abort removes every streamed object (the request failed as a whole).
func (u *Upload) abort(ctx context.Context) {
	for _, f := range u.files {
		_ = u.s.objects.Delete(context.WithoutCancel(ctx), f.key)
	}
}

// Finish resolves the case, creates the documents, validates metadata per
// file and enqueues the pipeline. Files whose metadata is invalid are
// rejected and removed; a case or callback error fails the whole request.
func (u *Upload) Finish(ctx context.Context) (*UploadResult, error) {
	if u.callbackErr != nil {
		u.abort(ctx)
		return nil, u.callbackErr
	}
	c, created, err := u.s.cases.ResolveCase(ctx, u.owner, u.kb.ID, u.caseRef)
	if err != nil {
		u.abort(ctx)
		return nil, err
	}
	ct := u.s.cases.CaseType(c.CaseType)
	schema := ct.MetadataSchema
	if schema == nil {
		schema = u.kb.MetadataSchema
	}
	res := &UploadResult{Rejected: append([]Rejected{}, u.rejected...), Documents: []Accepted{},
		Case: UploadCase{ID: c.ID, Code: c.Code, CaseType: c.CaseType, Created: created}}
	if len(u.files) > 0 {
		b, err := u.s.st.KBs.CreateBatch(ctx, types.UploadBatch{KBID: u.kb.ID, CreatedBy: u.owner, Metadata: u.shared, FileCount: u.index})
		if err != nil {
			u.abort(ctx)
			return nil, err
		}
		res.BatchID = b.ID
	}
	engine := firstNonEmpty(ct.Parser.Engine, u.kb.Config.ParserEngine, u.s.cfg.Parser.DefaultEngine)
	for i, f := range u.files {
		own := u.perFile[f.name]
		if own == nil {
			own = u.perFile[strconv.Itoa(f.index)]
		}
		d := types.Document{
			ID: f.docID, KBID: u.kb.ID, CaseID: c.ID, BatchID: &res.BatchID, CreatedBy: u.owner, FileName: f.name, MimeType: f.mime,
			SizeBytes: f.size, SHA256: f.sha, StorageKey: f.key, Engine: engine, CallbackURL: u.callbackURL,
		}
		if f.pdfaPart > 0 {
			part := f.pdfaPart
			d.PDFAPart, d.PDFAConformance = &part, f.pdfaConform
		}
		doc, err := u.s.st.Documents.Create(ctx, d, u.interactive)
		if errors.Is(err, postgres.ErrDuplicate) {
			_ = u.s.objects.Delete(context.WithoutCancel(ctx), f.key)
			if u.callbackURL != "" {
				// The file is already known: the new URL replaces the old
				// one, and a document that already finished is reported now.
				if err := u.s.st.Documents.SetCallbackURL(ctx, doc.ID, u.callbackURL); err != nil {
					return nil, err
				}
				if err := u.s.scheduleCallback(ctx, doc.ID); err != nil {
					u.s.log.Warn("callback: schedule for duplicate failed", "doc", doc.ID, "err", err)
				}
			}
			res.Documents = append(res.Documents, Accepted{DocumentID: doc.ID, FileName: doc.FileName, Status: doc.Status, Duplicate: true, Metadata: doc.Metadata, CallbackURL: u.callbackURL})
			continue
		}
		if err != nil {
			for _, rest := range u.files[i:] {
				_ = u.s.objects.Delete(context.WithoutCancel(ctx), rest.key)
			}
			return nil, err
		}
		meta, errs := metadata.Validate(schema, metadata.Merge(u.shared, own))
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			res.Rejected = append(res.Rejected, Rejected{FileName: f.name, Index: f.index, Errors: msgs})
			_ = u.s.objects.Delete(context.WithoutCancel(ctx), f.key)
			_ = u.s.st.Documents.Purge(context.WithoutCancel(ctx), doc.ID)
			continue
		}
		doc, err = u.s.st.Documents.SetMetadata(ctx, doc.ID, meta)
		if err != nil {
			return nil, err
		}
		if err := u.s.enqueue(ctx, types.TaskDocumentSplit, doc, nil, fmt.Sprintf("split:%s:%d", doc.ID, doc.Gen)); err != nil {
			return nil, err
		}
		res.Documents = append(res.Documents, Accepted{DocumentID: doc.ID, FileName: doc.FileName, Status: doc.Status, Metadata: doc.Metadata, CallbackURL: u.callbackURL})
	}
	if res.BatchID != uuid.Nil {
		_ = u.s.st.KBs.FinishBatch(ctx, res.BatchID, len(res.Documents), len(res.Rejected))
	}
	return res, nil
}

func (u *Upload) reject(name string, idx int, msg string) {
	u.rejected = append(u.rejected, Rejected{FileName: name, Index: idx, Errors: []string{msg}})
}

func (s *Service) allowed(mime string) bool {
	for _, t := range s.cfg.Upload.AllowedTypes {
		if strings.EqualFold(t, mime) {
			return true
		}
	}
	return false
}

// enqueue sends a per-document task, choosing the interactive lane when the
// document came from chat.
func (s *Service) enqueue(ctx context.Context, taskType string, d types.Document, pages []int, taskID string) error {
	return s.q.Enqueue(ctx, taskType, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: d.Gen, Interactive: d.Interactive, Pages: pages},
		queue.Opts{TaskID: taskID, Interactive: d.Interactive})
}

func sniff(head []byte, declared string) string {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf"
	case bytes.HasPrefix(head, []byte("II*\x00")), bytes.HasPrefix(head, []byte("MM\x00*")):
		return "image/tiff"
	}
	if ct := http.DetectContentType(head); ct != "application/octet-stream" {
		return strings.Split(ct, ";")[0]
	}
	if declared != "" {
		return strings.Split(declared, ";")[0]
	}
	return "application/octet-stream"
}

func extFor(mime, name string) string {
	switch mime {
	case "application/pdf":
		return "pdf"
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/tiff":
		return "tif"
	}
	return strings.TrimPrefix(filepath.Ext(name), ".")
}

type limitReader struct {
	r        io.Reader
	n, max   int64
	exceeded bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.max > 0 && l.n > l.max {
		l.exceeded = true
		return n, ErrTooLarge
	}
	return n, err
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
